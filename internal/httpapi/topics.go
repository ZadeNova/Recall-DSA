package httpapi

import (
	"context"
	"net/http"
	"sort"

	"github.com/ZadeNova/recall-dsa/internal/service"
)

// topicRow is one row of the Topics page's main table: a topic's count
// plus how wide its bar should render relative to the busiest topic —
// pre-computed here so the template stays dumb (no arithmetic in
// html/template).
type topicRow struct {
	ID         int64
	Name       string
	Count      int
	BarPercent float64
}

// distributionRow is one bar in the Topics page's Topic Distribution
// section: a topic's share of all tagged problems.
type distributionRow struct {
	Name    string
	Count   int
	Percent float64
}

type topicsViewData struct {
	Topics        []topicRow
	TopicsByCount []distributionRow
	TotalTopics   int
	TotalMapped   int
	Error         string
}

// newTopicsViewData fetches topic counts once and derives everything the
// page needs from it: the alphabetical table (with bar widths relative
// to the busiest topic), the count-descending distribution rows (with
// bar widths relative to the total), and the header stats.
func (s *Server) newTopicsViewData(ctx context.Context) (topicsViewData, error) {
	counts, err := s.svc.TopicCounts(ctx)
	if err != nil {
		return topicsViewData{}, err
	}

	data := topicsViewData{TotalTopics: len(counts)}
	var maxCount, totalMapped int
	for _, tc := range counts {
		totalMapped += tc.Count
		if tc.Count > maxCount {
			maxCount = tc.Count
		}
	}
	data.TotalMapped = totalMapped

	for _, tc := range counts {
		var barPct float64
		if maxCount > 0 {
			barPct = float64(tc.Count) / float64(maxCount) * 100
		}
		data.Topics = append(data.Topics, topicRow{ID: tc.ID, Name: tc.Name, Count: tc.Count, BarPercent: barPct})
	}

	byCount := append([]service.TopicCount(nil), counts...)
	sort.Slice(byCount, func(i, j int) bool { return byCount[i].Count > byCount[j].Count })
	for _, tc := range byCount {
		var pct float64
		if totalMapped > 0 {
			pct = float64(tc.Count) / float64(totalMapped) * 100
		}
		data.TopicsByCount = append(data.TopicsByCount, distributionRow{Name: tc.Name, Count: tc.Count, Percent: pct})
	}

	return data, nil
}

// handleTopicsPage is SPEC.md §8's topic management surface: full CRUD
// on topics, deliberately separate from the due-queue/library pages.
func (s *Server) handleTopicsPage(w http.ResponseWriter, r *http.Request) {
	data, err := s.newTopicsViewData(r.Context())
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	s.render(w, r, s.tpl.topics, data)
}

func (s *Server) handleCreateTopic(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, err)
		return
	}
	if _, err := s.svc.CreateTopic(r.Context(), r.PostForm.Get("name")); err != nil {
		s.renderTopicsError(w, r, err)
		return
	}
	http.Redirect(w, r, "/topics", http.StatusSeeOther)
}

func (s *Server) handleRenameTopic(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.renderError(w, r, http.StatusBadRequest, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.svc.RenameTopic(r.Context(), id, r.PostForm.Get("name")); err != nil {
		s.renderTopicsError(w, r, err)
		return
	}
	http.Redirect(w, r, "/topics", http.StatusSeeOther)
}

// DeleteTopic only drops the tag association (SPEC.md §8) — the problems
// that carried it are untouched.
func (s *Server) handleDeleteTopic(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.renderError(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.svc.DeleteTopic(r.Context(), id); err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/topics", http.StatusSeeOther)
}

// renderTopicsError re-renders the topics page with an error message
// (e.g. a duplicate name) instead of redirecting, so the failed input
// isn't just silently lost.
func (s *Server) renderTopicsError(w http.ResponseWriter, r *http.Request, formErr error) {
	data, err := s.newTopicsViewData(r.Context())
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	data.Error = formErr.Error()
	s.renderFormError(w, r, s.tpl.topics, data)
}
