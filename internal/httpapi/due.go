package httpapi

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
	"github.com/ZadeNova/recall-dsa/internal/service"
)

// glanceTableData feeds the shared "glance-table" partial: a read-only,
// paginated list (title/topics/next-review-date, no actions), used by
// both Home (for due items) and /due (for upcoming items) — same
// rendering, just different data and empty-state text. EmptyMessage is
// template.HTML (built in Go, never from user input) so an embedded
// link renders as a real link rather than escaped text. Independent
// paginated sections on the same page (Due's due-now vs upcoming
// tables) use distinct query param names, carried on Page itself
// (pageInfo.PageSizeParam) — see buildPageInfo.
type glanceTableData struct {
	Items        []service.DueItem
	EmptyMessage template.HTML
	Page         pageInfo
}

const nothingDueMessage = `Nothing due right now — go solve something new on your own and log it via <a href="/problems/new">Add Problem</a>.`

// nothingDueMessageFor appends how many problems are paused, so an empty
// queue caused by pausing isn't mistaken for a bug. Built in Go from an
// integer only, so it's safe as template.HTML.
func nothingDueMessageFor(paused int) template.HTML {
	if paused <= 0 {
		return nothingDueMessage
	}
	return nothingDueMessage + template.HTML(fmt.Sprintf(
		` %s currently paused — see them in the <a href="/library?status=paused">Library</a>.`, problemCount(paused)))
}

// dueTableData feeds the "due-table" partial. SelectedTopic travels with
// it (not read from an outer template scope) so each grade-form can
// carry the current topic filter as a hidden field — handleGrade needs
// to know it to recompute topic-scoped stats for its live htmx refresh.
type dueTableData struct {
	Items         []service.DueItem
	EmptyMessage  template.HTML
	SelectedTopic string
	Page          pageInfo
}

// dueStatsOOB feeds the "due-stats-oob" partial: htmx out-of-band swap
// targets refreshed after grading, so the Due page's stats-row and the
// nav's due-count pill update live instead of reflecting the page's last
// full load. Due/Overdue are scoped to whatever topic filter (if any)
// the grading request came from; NavDue is always global, matching the
// nav pill's own unfiltered semantics.
type dueStatsOOB struct {
	Due     int
	Overdue int
	NavDue  int
}

type dueViewData struct {
	Topics        []string
	SelectedTopic string
	DueTable      dueTableData
	Upcoming      glanceTableData

	DueCount     int
	OverdueCount int
}

type homeViewData struct {
	DueCount      int
	Due           glanceTableData
	TotalTracked  int
	PausedCount   int
	Difficulty    service.DifficultyCounts
	UpcomingByDay []service.DayCount
	Quote         string
}

// upcomingWindowDays is /due's Upcoming section lookahead: informational
// only, not a recommendation — RecommendDue remains the sole thing
// deciding what's actionable right now (SPEC.md §4). Upcoming items are
// deliberately not gradable (no grade-form is ever rendered for them):
// grading before the interval has actually elapsed would inflate
// ease/interval on a false signal.
const upcomingWindowDays = 7

// homeSidebarWindowDays is Home's compact "Review Load" sidebar — a
// shorter, day-by-day glance distinct from /due's full 7-day Upcoming
// list (FRONTEND.md, Home dashboard enrichment).
const homeSidebarWindowDays = 5

// handleHome is the dashboard landing page: a due count and a read-only
// glance at what's due. It is deliberately not an action surface — all
// grading happens on /due, which this page links to. Avoids having two
// pages that can both mutate review state (FRONTEND.md, Decided UX
// facts #1).
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	page := pageFromQuery(q.Get("page"))
	pageSize := pageSizeFromQuery(q.Get("page_size"))

	dueItems, total, err := s.svc.RecommendDue(ctx, nil, pageSize, (page-1)*pageSize)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	if totalPages := totalPagesFor(total, pageSize); page > totalPages {
		page = totalPages
		if dueItems, total, err = s.svc.RecommendDue(ctx, nil, pageSize, (page-1)*pageSize); err != nil {
			s.renderError(w, r, http.StatusInternalServerError, err)
			return
		}
	}

	countTotal, err := s.svc.CountProblems(ctx)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}

	pausedCount, err := s.svc.CountPaused(ctx, nil)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}

	difficulty, err := s.svc.DifficultyBreakdown(ctx)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}

	byDay, err := s.svc.UpcomingByDay(ctx, homeSidebarWindowDays)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}

	data := homeViewData{
		DueCount: total,
		Due: glanceTableData{
			Items:        dueItems,
			EmptyMessage: nothingDueMessageFor(pausedCount),
			Page:         buildPageInfo("/", url.Values{}, "page", "page_size", page, pageSize, total),
		},
		TotalTracked:  countTotal,
		PausedCount:   pausedCount,
		Difficulty:    difficulty,
		UpcomingByDay: byDay,
		Quote:         randomQuote(),
	}
	s.render(w, r, s.tpl.home, data)
}

// handleDue is the workflow's entry point (SPEC.md §2 step 1) and the
// only place grading happens: what's overdue for review now (gradable),
// plus what's coming up next (view-only) — both filtered by the same
// topic selection (FRONTEND.md, Decided UX facts #2), and both
// paginated independently of each other.
func (s *Server) handleDue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	var topic *string
	if v := q.Get("topic"); v != "" {
		topic = &v
	}

	duePage := pageFromQuery(q.Get("page"))
	duePageSize := pageSizeFromQuery(q.Get("page_size"))
	items, dueTotal, err := s.svc.RecommendDue(ctx, topic, duePageSize, (duePage-1)*duePageSize)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	if totalPages := totalPagesFor(dueTotal, duePageSize); duePage > totalPages {
		duePage = totalPages
		if items, dueTotal, err = s.svc.RecommendDue(ctx, topic, duePageSize, (duePage-1)*duePageSize); err != nil {
			s.renderError(w, r, http.StatusInternalServerError, err)
			return
		}
	}

	upcomingPage := pageFromQuery(q.Get("upcoming_page"))
	upcomingPageSize := pageSizeFromQuery(q.Get("upcoming_page_size"))
	upcoming, upcomingTotal, err := s.svc.RecommendUpcoming(ctx, topic, upcomingWindowDays, upcomingPageSize, (upcomingPage-1)*upcomingPageSize)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	if totalPages := totalPagesFor(upcomingTotal, upcomingPageSize); upcomingPage > totalPages {
		upcomingPage = totalPages
		if upcoming, upcomingTotal, err = s.svc.RecommendUpcoming(ctx, topic, upcomingWindowDays, upcomingPageSize, (upcomingPage-1)*upcomingPageSize); err != nil {
			s.renderError(w, r, http.StatusInternalServerError, err)
			return
		}
	}

	topics, err := s.svc.ListTopics(ctx)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}

	due, overdue, err := s.svc.DueStats(ctx, topic)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}

	// Scoped to the topic filter, like the list it annotates.
	pausedCount, err := s.svc.CountPaused(ctx, topic)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}

	selectedTopic := ""
	if topic != nil {
		selectedTopic = *topic
	}
	topicExtra := url.Values{}
	if selectedTopic != "" {
		topicExtra.Set("topic", selectedTopic)
	}

	data := dueViewData{
		SelectedTopic: selectedTopic,
		DueTable: dueTableData{
			Items:         items,
			EmptyMessage:  nothingDueMessageFor(pausedCount),
			SelectedTopic: selectedTopic,
			Page:          buildPageInfo("/due", topicExtra, "page", "page_size", duePage, duePageSize, dueTotal),
		},
		Upcoming: glanceTableData{
			Items:        upcoming,
			EmptyMessage: "Nothing scheduled in the next 7 days.",
			Page:         buildPageInfo("/due", topicExtra, "upcoming_page", "upcoming_page_size", upcomingPage, upcomingPageSize, upcomingTotal),
		},
		DueCount:     due,
		OverdueCount: overdue,
	}
	for _, t := range topics {
		data.Topics = append(data.Topics, t.Name)
	}

	s.render(w, r, s.tpl.due, data)
}

// handleGrade is the one-click grading action (SPEC.md §2 step 3): it
// calls RecordReview, which is itself the single unified attempt+review
// write — there's nothing else for this handler to compose. Progressive
// enhancement (FRONTEND.md, htmx grading): htmx requests get back a
// small confirmation fragment swapped into the row in place, instead of
// the full-page redirect a plain form submission (JS disabled, or htmx
// failed to load) still receives — plus two out-of-band swaps so the
// Due page's stats-row and the nav's due-count pill update live instead
// of going stale until the next full page load.
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
	result, err := s.svc.RecordReview(r.Context(), id, grade, s.svc.Now())
	if err != nil {
		s.renderError(w, r, http.StatusBadRequest, err)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		// Fetch everything needed for both fragments BEFORE writing
		// anything to w — once ExecuteTemplate below writes its first
		// byte, the response is committed at 200 and a later error can
		// no longer go through renderError (which calls WriteHeader)
		// without producing "superfluous WriteHeader" plus an error
		// page's HTML appended after the already-sent graded-badge
		// fragment, which htmx would then swap into the grade cell.
		var topic *string
		if v := r.PostForm.Get("topic"); v != "" {
			topic = &v
		}
		due, overdue, err := s.svc.DueStats(r.Context(), topic)
		if err != nil {
			s.renderError(w, r, http.StatusInternalServerError, err)
			return
		}
		navDue, err := s.svc.CountDue(r.Context())
		if err != nil {
			s.renderError(w, r, http.StatusInternalServerError, err)
			return
		}

		if err := s.tpl.due.ExecuteTemplate(w, "graded-badge", result); err != nil {
			s.renderError(w, r, http.StatusInternalServerError, err)
			return
		}
		// The response is committed now — a failure here can't be
		// recovered from, but it should still be visible in the logs
		// rather than silently dropped.
		if err := s.tpl.due.ExecuteTemplate(w, "due-stats-oob", dueStatsOOB{Due: due, Overdue: overdue, NavDue: navDue}); err != nil {
			log.Printf("handleGrade: due-stats-oob template failed after graded-badge was already written: %v", err)
		}
		return
	}

	http.Redirect(w, r, "/due", http.StatusSeeOther)
}
