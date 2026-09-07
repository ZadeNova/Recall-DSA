package httpapi

import (
	"net/http"

	"github.com/ZadeNova/recall-dsa/internal/service"
)

type topicsViewData struct {
	Topics []service.Topic
	Error  string
}

// handleTopicsPage is SPEC.md §8's topic management surface: full CRUD
// on topics, deliberately separate from the due-queue/library pages.
func (s *Server) handleTopicsPage(w http.ResponseWriter, r *http.Request) {
	topics, err := s.svc.ListTopics(r.Context())
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	s.render(w, r, s.tpl.topics, topicsViewData{Topics: topics})
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
	topics, err := s.svc.ListTopics(r.Context())
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	s.renderFormError(w, r, s.tpl.topics, topicsViewData{Topics: topics, Error: formErr.Error()})
}
