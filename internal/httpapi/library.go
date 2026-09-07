package httpapi

import (
	"net/http"

	"github.com/ZadeNova/recall-dsa/internal/service"
)

type libraryViewData struct {
	Topics             []string
	SelectedTopic      string
	Difficulties       []service.Difficulty
	SelectedDifficulty service.Difficulty
	Problems           []service.Problem
}

// handleLibrary is SPEC.md §7's library view: every logged problem,
// filterable by topic and/or difficulty — a distinct browsing page from
// the due-queue, not just a filter on top of it.
func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	data := libraryViewData{
		Difficulties: []service.Difficulty{service.DifficultyEasy, service.DifficultyMedium, service.DifficultyHard},
	}
	filter := service.ListProblemsFilter{}

	if v := q.Get("topic"); v != "" {
		filter.Topic = &v
		data.SelectedTopic = v
	}
	if v := q.Get("difficulty"); v != "" {
		d := service.Difficulty(v)
		filter.Difficulty = &d
		data.SelectedDifficulty = d
	}

	problems, err := s.svc.ListProblems(ctx, filter)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	data.Problems = problems

	topics, err := s.svc.ListTopics(ctx)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	for _, t := range topics {
		data.Topics = append(data.Topics, t.Name)
	}

	s.render(w, r, s.tpl.library, data)
}
