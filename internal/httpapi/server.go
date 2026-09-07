// Package httpapi is the thin HTTP layer described in SPEC.md §6: it
// decodes requests, calls internal/service, and renders server-rendered
// HTML via html/template. Pages here are plain HTML forms (no htmx, no
// JS beyond a couple of inline confirm() guards on destructive actions)
// — the htmx interactivity and visual design pass is SPEC.md §7's own
// separate, later step, deliberately not built here.
package httpapi

import (
	"html/template"
	"net/http"
	"strconv"

	"github.com/ZadeNova/recall-dsa/internal/service"
)

type Server struct {
	svc *service.Service
	tpl *pageTemplates
}

// NewServer builds a Server, parsing the embedded templates once up
// front so a malformed template fails at startup, not on first request.
func NewServer(svc *service.Service) (*Server, error) {
	tpl, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	return &Server{svc: svc, tpl: tpl}, nil
}

// Routes returns the full application handler.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.handleHome)

	mux.Handle("GET /assets/", http.FileServerFS(assetsFS))

	mux.HandleFunc("GET /due", s.handleDue)
	mux.HandleFunc("POST /problems/{id}/grade", s.handleGrade)

	mux.HandleFunc("GET /problems/new", s.handleNewProblemForm)
	mux.HandleFunc("POST /problems", s.handleCreateProblem)
	mux.HandleFunc("GET /problems/{id}/edit", s.handleEditProblemForm)
	mux.HandleFunc("POST /problems/{id}/update", s.handleUpdateProblem)
	mux.HandleFunc("POST /problems/{id}/delete", s.handleDeleteProblem)

	mux.HandleFunc("GET /library", s.handleLibrary)

	mux.HandleFunc("GET /topics", s.handleTopicsPage)
	mux.HandleFunc("POST /topics", s.handleCreateTopic)
	mux.HandleFunc("POST /topics/{id}/rename", s.handleRenameTopic)
	mux.HandleFunc("POST /topics/{id}/delete", s.handleDeleteTopic)

	return mux
}

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

// errorViewData feeds the shared error page, covering 400/404/500
// (FRONTEND.md, Decided UX facts #9). It shows the underlying error text
// verbatim — reasonable for a single-user, self-hosted tool where the
// user is also the one who'd have to debug it (SPEC.md §10). Form
// validation failures (422) don't go through here — see
// renderFormError, which re-renders the actual form instead so input
// isn't lost.
type errorViewData struct {
	Status     int
	StatusText string
	Message    string
}

func (s *Server) renderError(w http.ResponseWriter, status int, err error) {
	w.WriteHeader(status)
	data := errorViewData{Status: status, StatusText: http.StatusText(status), Message: err.Error()}
	if tplErr := s.tpl.errorPage.ExecuteTemplate(w, "layout", data); tplErr != nil {
		// The error template itself failed — fall back to plain text
		// rather than masking the original error with a template bug.
		w.Write([]byte(err.Error()))
	}
}

// renderFormError re-renders a form page with a validation/service error
// and a 422 status, instead of redirecting — so the user's input isn't
// lost on a failed submission.
func (s *Server) renderFormError(w http.ResponseWriter, tpl *template.Template, data any) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	tpl.ExecuteTemplate(w, "layout", data)
}
