package httpapi

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
	"github.com/ZadeNova/recall-dsa/internal/service"
)

type problemFormData struct {
	Problem service.Problem // zero value for the "new problem" form
	Error   string
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

func (s *Server) handleNewProblemForm(w http.ResponseWriter, r *http.Request) {
	if err := s.tpl.addForm.ExecuteTemplate(w, "layout", problemFormData{}); err != nil {
		s.renderError(w, http.StatusInternalServerError, err)
	}
}

// handleCreateProblem is the "brand-new problem" half of SPEC.md §2's
// grading action: AddProblem creates the row and logs the first grade
// atomically, so there's nothing further to compose here.
func (s *Server) handleCreateProblem(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}

	input := service.AddProblemInput{
		Title:      r.PostForm.Get("title"),
		URL:        r.PostForm.Get("url"),
		Difficulty: service.Difficulty(r.PostForm.Get("difficulty")),
		Topics:     splitTopics(r.PostForm.Get("topics")),
		Grade:      scheduler.Grade(r.PostForm.Get("grade")),
		At:         time.Now(),
	}

	if _, err := s.svc.AddProblem(r.Context(), input); err != nil {
		s.renderFormError(w, s.tpl.addForm, problemFormData{
			Problem: service.Problem{Title: input.Title, URL: input.URL, Difficulty: input.Difficulty, Topics: input.Topics},
			Error:   err.Error(),
		})
		return
	}

	http.Redirect(w, r, "/due", http.StatusSeeOther)
}

func (s *Server) handleEditProblemForm(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}

	problem, err := s.svc.GetProblem(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		s.renderError(w, http.StatusNotFound, fmt.Errorf("problem %d not found", id))
		return
	}
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err)
		return
	}

	if err := s.tpl.editForm.ExecuteTemplate(w, "layout", problemFormData{Problem: problem}); err != nil {
		s.renderError(w, http.StatusInternalServerError, err)
	}
}

func (s *Server) handleUpdateProblem(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}

	input := service.UpdateProblemInput{
		Title:      r.PostForm.Get("title"),
		URL:        r.PostForm.Get("url"),
		Difficulty: service.Difficulty(r.PostForm.Get("difficulty")),
		Topics:     splitTopics(r.PostForm.Get("topics")),
	}

	if err := s.svc.UpdateProblem(r.Context(), id, input); err != nil {
		s.renderFormError(w, s.tpl.editForm, problemFormData{
			Problem: service.Problem{ID: id, Title: input.Title, URL: input.URL, Difficulty: input.Difficulty, Topics: input.Topics},
			Error:   err.Error(),
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
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.svc.DeleteProblem(r.Context(), id); err != nil {
		s.renderError(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/library", http.StatusSeeOther)
}
