package httpapi

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/ZadeNova/recall-dsa/internal/service"
)

// importRowView is one parsed+validated row of a pasted bulk-import
// batch, as shown on the preview table (SPEC.md §9: verify before
// committing).
type importRowView struct {
	Line       int
	Title      string
	URL        string
	Difficulty string
	Topics     []string
	Status     string // "new", "merge", or "error"
	Issue      string // populated when Status == "error"
}

// importViewData feeds templates/import.html's three states: a blank
// form (Submitted false), a preview after parsing (Submitted true,
// Result nil), and a result summary after commit (Result non-nil).
type importViewData struct {
	RawCSV       string
	TargetPerDay int
	Rows         []importRowView
	HasErrors    bool
	Submitted    bool
	Result       *service.BulkImportResult
	Error        string
}

// targetPerDayFromForm parses the "target_per_day" field, falling back
// to service.DefaultTargetPerDay on anything blank, non-numeric, or
// non-positive — never an error, since this only tunes stagger pacing,
// not correctness.
func targetPerDayFromForm(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return service.DefaultTargetPerDay
	}
	return n
}

// parseImportRows parses the pasted CSV text into rows, validating each
// one's shape (title/url/difficulty/topics all present, difficulty is
// one of the three recognized values, at least one topic, no duplicate
// slug within the pasted batch itself). It does not touch the database —
// see annotateExistingSlugs for the "will merge into an existing
// problem" check, which does.
func parseImportRows(raw string) ([]importRowView, error) {
	reader := csv.NewReader(strings.NewReader(raw))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	var rows []importRowView
	seenSlugs := make(map[string]int) // slug -> first row's Line

	line := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse CSV: %w", err)
		}
		line++

		// Skip a header row ("title,url,difficulty,topics"), if present —
		// the console-script generator always emits one.
		if line == 1 && len(record) > 0 && strings.EqualFold(strings.TrimSpace(record[0]), "title") {
			continue
		}
		if allBlank(record) {
			continue
		}

		row := importRowView{Line: line}
		if len(record) != 4 {
			row.Status = "error"
			row.Issue = fmt.Sprintf("expected 4 columns (title,url,difficulty,topics), got %d", len(record))
			rows = append(rows, row)
			continue
		}

		row.Title = strings.TrimSpace(record[0])
		row.URL = strings.TrimSpace(record[1])
		row.Difficulty = strings.TrimSpace(record[2])
		row.Topics = splitTopics(strings.ReplaceAll(record[3], ";", ","))

		switch {
		case row.Title == "":
			row.Status = "error"
			row.Issue = "title is empty"
		case !service.Difficulty(row.Difficulty).Valid():
			row.Status = "error"
			row.Issue = fmt.Sprintf("difficulty must be Easy, Medium, or Hard (got %q)", row.Difficulty)
		case len(row.Topics) == 0:
			row.Status = "error"
			row.Issue = "at least one topic is required"
		default:
			slug, err := service.ExtractSlug(row.URL)
			if err != nil {
				row.Status = "error"
				row.Issue = err.Error()
			} else if firstLine, dup := seenSlugs[slug]; dup {
				row.Status = "error"
				row.Issue = fmt.Sprintf("duplicate of line %d in this batch", firstLine)
			} else {
				seenSlugs[slug] = row.Line
				row.Status = "new"
			}
		}

		rows = append(rows, row)
	}

	return rows, nil
}

func allBlank(record []string) bool {
	for _, f := range record {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

// annotateExistingSlugs replaces every currently-"new" row's status with
// what bulk import will actually do with its slug: "new" stays as-is,
// "merge" means it'll safely refresh an existing-but-untouched problem,
// and "protected" means it already has real review progress that bulk
// import will leave alone (see Service.BulkImportProblems) — informational
// in all three cases, never an error.
func annotateExistingSlugs(ctx context.Context, svc *service.Service, rows []importRowView) error {
	for i := range rows {
		if rows[i].Status != "new" {
			continue
		}
		slug, err := service.ExtractSlug(rows[i].URL)
		if err != nil {
			return err
		}
		status, err := svc.CheckSlug(ctx, slug)
		if err != nil {
			return err
		}
		switch status {
		case service.SlugSafeToRefresh:
			rows[i].Status = "merge"
		case service.SlugProtected:
			rows[i].Status = "protected"
		}
	}
	return nil
}

func (s *Server) buildImportPreview(ctx context.Context, raw string, targetPerDayRaw string) (importViewData, error) {
	data := importViewData{RawCSV: raw, TargetPerDay: targetPerDayFromForm(targetPerDayRaw), Submitted: true}

	if strings.TrimSpace(raw) == "" {
		data.Error = "Paste some CSV text first."
		data.HasErrors = true
		return data, nil
	}

	rows, err := parseImportRows(raw)
	if err != nil {
		data.Error = err.Error()
		data.HasErrors = true
		return data, nil
	}
	if err := annotateExistingSlugs(ctx, s.svc, rows); err != nil {
		return importViewData{}, err
	}

	data.Rows = rows
	for _, row := range rows {
		if row.Status == "error" {
			data.HasErrors = true
			break
		}
	}
	if len(rows) == 0 {
		data.Error = "No rows found in the pasted text."
		data.HasErrors = true
	}
	return data, nil
}

func (s *Server) handleImportForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, s.tpl.importPage, importViewData{TargetPerDay: service.DefaultTargetPerDay})
}

func (s *Server) handleImportPreview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, err)
		return
	}
	data, err := s.buildImportPreview(r.Context(), r.PostForm.Get("csv"), r.PostForm.Get("target_per_day"))
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	s.render(w, r, s.tpl.importPage, data)
}

func (s *Server) handleImportCommit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, err)
		return
	}
	raw := r.PostForm.Get("csv")

	// Never trust the hidden field as pre-validated — re-parse and
	// re-check against the database before writing anything.
	data, err := s.buildImportPreview(r.Context(), raw, r.PostForm.Get("target_per_day"))
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	if data.HasErrors {
		s.renderFormError(w, r, s.tpl.importPage, data)
		return
	}

	rows := make([]service.BulkImportRow, len(data.Rows))
	for i, row := range data.Rows {
		rows[i] = service.BulkImportRow{
			Title:      row.Title,
			URL:        row.URL,
			Difficulty: service.Difficulty(row.Difficulty),
			Topics:     row.Topics,
		}
	}

	result, err := s.svc.BulkImportProblems(r.Context(), rows, data.TargetPerDay)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}

	data.Result = &result
	s.render(w, r, s.tpl.importPage, data)
}
