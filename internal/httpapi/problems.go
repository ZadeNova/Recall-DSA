package httpapi

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
	"github.com/ZadeNova/recall-dsa/internal/service"
)

// problemFormData feeds both add.html and edit.html. AllTopics/Selected
// drive the shared topic-picker partial: every known topic renders as a
// checkbox, Selected marking which ones should start checked (empty for
// a new problem, the problem's current tags for an edit or a failed
// resubmission).
type problemFormData struct {
	Problem   service.Problem // zero value for the "new problem" form
	AllTopics []string
	Selected  map[string]bool
	Error     string
	Notice    string
}

func splitTopics(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// topicsFromForm merges the topic-picker's checked boxes ("topic",
// multi-value) with the free-text field for brand-new tags
// ("new_topics", comma-separated), deduping as it goes.
func topicsFromForm(r *http.Request) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, t := range r.PostForm["topic"] {
		add(t)
	}
	for _, t := range splitTopics(r.PostForm.Get("new_topics")) {
		add(t)
	}
	return out
}

func selectedSet(topics []string) map[string]bool {
	set := make(map[string]bool, len(topics))
	for _, t := range topics {
		set[t] = true
	}
	return set
}

// topicNames fetches every known topic as a plain []string, for feeding
// the topic-picker partial's checkbox list.
func (s *Server) topicNames(r *http.Request) ([]string, error) {
	topics, err := s.svc.ListTopics(r.Context())
	if err != nil {
		return nil, err
	}
	names := make([]string, len(topics))
	for i, t := range topics {
		names[i] = t.Name
	}
	return names, nil
}

func (s *Server) handleNewProblemForm(w http.ResponseWriter, r *http.Request) {
	names, err := s.topicNames(r)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	s.render(w, r, s.tpl.addForm, problemFormData{AllTopics: names, Selected: map[string]bool{}})
}

// handleCreateProblem is the "brand-new problem" half of SPEC.md §2's
// grading action: AddProblem creates the row and logs the first grade
// atomically, so there's nothing further to compose here.
func (s *Server) handleCreateProblem(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, err)
		return
	}

	topics := topicsFromForm(r)
	input := service.AddProblemInput{
		Title:      r.PostForm.Get("title"),
		URL:        r.PostForm.Get("url"),
		Difficulty: service.Difficulty(r.PostForm.Get("difficulty")),
		Topics:     topics,
		Grade:      scheduler.Grade(r.PostForm.Get("grade")),
		At:         s.svc.Now(),
	}

	result, err := s.svc.AddProblem(r.Context(), input)
	if err != nil {
		names, nameErr := s.topicNames(r)
		if nameErr != nil {
			s.renderError(w, r, http.StatusInternalServerError, nameErr)
			return
		}
		s.renderFormError(w, r, s.tpl.addForm, problemFormData{
			Problem:   service.Problem{Title: input.Title, URL: input.URL, Difficulty: input.Difficulty, Topics: input.Topics},
			AllTopics: names,
			Selected:  selectedSet(topics),
			Error:     err.Error(),
		})
		return
	}

	// A re-add is a materially different outcome from a create: the grade
	// landed on an existing problem and everything else typed into the
	// form was discarded (SPEC.md §9 keeps the existing row's fields).
	// Redirecting to /due the same way a real create does would leave no
	// trace of either fact, so say so on the form instead of bouncing
	// away from it.
	if !result.Created {
		names, nameErr := s.topicNames(r)
		if nameErr != nil {
			s.renderError(w, r, http.StatusInternalServerError, nameErr)
			return
		}
		notice := fmt.Sprintf(
			"%q was already tracked — your %s grade was recorded against the existing entry. Its title, difficulty, and topics were left unchanged.",
			result.Title, input.Grade,
		)
		if result.Paused {
			notice += " It is currently paused, so it stays out of your review queue until you unpause it in the Library."
		}
		s.render(w, r, s.tpl.addForm, problemFormData{
			AllTopics: names,
			Selected:  map[string]bool{},
			Notice:    notice,
		})
		return
	}

	http.Redirect(w, r, "/due", http.StatusSeeOther)
}

func (s *Server) handleEditProblemForm(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.renderError(w, r, http.StatusBadRequest, err)
		return
	}

	problem, err := s.svc.GetProblem(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		s.renderError(w, r, http.StatusNotFound, fmt.Errorf("problem %d not found", id))
		return
	}
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}

	names, err := s.topicNames(r)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	s.render(w, r, s.tpl.editForm, problemFormData{Problem: problem, AllTopics: names, Selected: selectedSet(problem.Topics)})
}

func (s *Server) handleUpdateProblem(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.renderError(w, r, http.StatusBadRequest, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, err)
		return
	}

	topics := topicsFromForm(r)
	input := service.UpdateProblemInput{
		Title:      r.PostForm.Get("title"),
		URL:        r.PostForm.Get("url"),
		Difficulty: service.Difficulty(r.PostForm.Get("difficulty")),
		Topics:     topics,
	}

	if err := s.svc.UpdateProblem(r.Context(), id, input); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.renderError(w, r, http.StatusNotFound, fmt.Errorf("problem %d not found", id))
			return
		}
		names, nameErr := s.topicNames(r)
		if nameErr != nil {
			s.renderError(w, r, http.StatusInternalServerError, nameErr)
			return
		}
		s.renderFormError(w, r, s.tpl.editForm, problemFormData{
			Problem:   service.Problem{ID: id, Title: input.Title, URL: input.URL, Difficulty: input.Difficulty, Topics: input.Topics},
			AllTopics: names,
			Selected:  selectedSet(topics),
			Error:     err.Error(),
		})
		return
	}

	http.Redirect(w, r, "/library", http.StatusSeeOther)
}

// handleDeleteProblem cascades to attempts/review_state at the DB level
// (SPEC.md §9) — nothing extra to do here beyond calling the service.
func (s *Server) handleDeleteProblem(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.renderError(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.svc.DeleteProblem(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.renderError(w, r, http.StatusNotFound, fmt.Errorf("problem %d not found", id))
			return
		}
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/library", http.StatusSeeOther)
}
