package httpapi

import (
	"html/template"
	"net/http"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
	"github.com/ZadeNova/recall-dsa/internal/service"
)

// glanceTableData feeds the shared "glance-table" partial: a read-only
// list (title/topics/next-review-date, no actions), used by both Home
// (for due items) and /due (for upcoming items) — same rendering, just
// different data and empty-state text. EmptyMessage is template.HTML
// (built in Go, never from user input) so an embedded link renders as a
// real link rather than escaped text.
type glanceTableData struct {
	Items        []service.DueItem
	EmptyMessage template.HTML
}

const nothingDueMessage = `Nothing due right now — go solve something new on your own and log it via <a href="/problems/new">Add Problem</a>.`

type dueViewData struct {
	Topics   []string
	Selected string
	Items    []service.DueItem
	Upcoming glanceTableData
}

type homeViewData struct {
	DueCount int
	Due      glanceTableData
}

// upcomingWindowDays is /due's Upcoming section lookahead: informational
// only, not a recommendation — RecommendDue remains the sole thing
// deciding what's actionable right now (SPEC.md §4). Upcoming items are
// deliberately not gradable (no grade-form is ever rendered for them):
// grading before the interval has actually elapsed would inflate
// ease/interval on a false signal.
const upcomingWindowDays = 7

// handleHome is the dashboard landing page: a due count and a read-only
// glance at what's due. It is deliberately not an action surface — all
// grading happens on /due, which this page links to. Avoids having two
// pages that can both mutate review state (FRONTEND.md, Decided UX
// facts #1).
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	dueItems, err := s.svc.RecommendDue(ctx, nil)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}

	data := homeViewData{
		DueCount: len(dueItems),
		Due:      glanceTableData{Items: dueItems, EmptyMessage: nothingDueMessage},
	}
	s.render(w, r, s.tpl.home, data)
}

// handleDue is the workflow's entry point (SPEC.md §2 step 1) and the
// only place grading happens: what's overdue for review now (gradable),
// plus what's coming up next (view-only) — both filtered by the same
// topic selection (FRONTEND.md, Decided UX facts #2).
func (s *Server) handleDue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var topic *string
	if v := r.URL.Query().Get("topic"); v != "" {
		topic = &v
	}

	items, err := s.svc.RecommendDue(ctx, topic)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}

	upcoming, err := s.svc.RecommendUpcoming(ctx, topic, upcomingWindowDays)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}

	topics, err := s.svc.ListTopics(ctx)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}

	data := dueViewData{
		Items:    items,
		Upcoming: glanceTableData{Items: upcoming, EmptyMessage: "Nothing scheduled in the next 7 days."},
	}
	for _, t := range topics {
		data.Topics = append(data.Topics, t.Name)
	}
	if topic != nil {
		data.Selected = *topic
	}

	s.render(w, r, s.tpl.due, data)
}

// handleGrade is the one-click grading action (SPEC.md §2 step 3): it
// calls RecordReview, which is itself the single unified attempt+review
// write — there's nothing else for this handler to compose. Progressive
// enhancement (FRONTEND.md, htmx grading): htmx requests get back a
// small confirmation fragment swapped into the row in place, instead of
// the full-page redirect a plain form submission (JS disabled, or htmx
// failed to load) still receives.
func (s *Server) handleGrade(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.renderError(w, r, http.StatusBadRequest, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, err)
		return
	}

	grade := scheduler.Grade(r.PostForm.Get("grade"))
	result, err := s.svc.RecordReview(r.Context(), id, grade, time.Now())
	if err != nil {
		s.renderError(w, r, http.StatusBadRequest, err)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		if err := s.tpl.due.ExecuteTemplate(w, "graded-badge", result); err != nil {
			s.renderError(w, r, http.StatusInternalServerError, err)
		}
		return
	}

	http.Redirect(w, r, "/due", http.StatusSeeOther)
}
