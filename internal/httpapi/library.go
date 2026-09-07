package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/service"
)

// libraryRow adds display-only fields to a DueItem — status text/class
// derived from comparing NextReviewDate to today, computed here rather
// than in the template (mirrors topics.go's topicRow/distributionRow
// pattern: html/template stays dumb, arithmetic and date math live in Go).
type libraryRow struct {
	service.DueItem
	StatusLabel string
	StatusClass string
}

const (
	defaultLibraryPageSize = 25
	defaultLibrarySort     = "next_review"
)

var validLibraryPageSizes = map[int]bool{10: true, 25: true, 50: true}

type libraryViewData struct {
	Topics             []string
	SelectedTopic      string
	Difficulties       []service.Difficulty
	SelectedDifficulty service.Difficulty
	Search             string
	Sort               string
	Rows               []libraryRow

	TotalTracked int
	Difficulty   service.DifficultyCounts

	Page        int
	PageSize    int
	TotalCount  int
	TotalPages  int
	ShowingText string
	PrevURL     string
	NextURL     string
}

// handleLibrary is SPEC.md §7's library view: every logged problem,
// searchable/filterable/sortable and paginated, each row showing its
// current SRS interval and next-review status — a distinct browsing
// page from the due-queue, not just a filter on top of it.
func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	data := libraryViewData{
		Difficulties: []service.Difficulty{service.DifficultyEasy, service.DifficultyMedium, service.DifficultyHard},
		Sort:         defaultLibrarySort,
		PageSize:     defaultLibraryPageSize,
		Page:         1,
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
	if v := q.Get("q"); v != "" {
		filter.Search = &v
		data.Search = v
	}
	if v := q.Get("sort"); v == "title" || v == "difficulty" {
		data.Sort = v
	}
	filter.Sort = data.Sort

	if v, err := strconv.Atoi(q.Get("page_size")); err == nil && validLibraryPageSizes[v] {
		data.PageSize = v
	}
	if v, err := strconv.Atoi(q.Get("page")); err == nil && v > 0 {
		data.Page = v
	}
	filter.Limit = data.PageSize
	filter.Offset = (data.Page - 1) * data.PageSize

	items, total, err := s.svc.ListLibrary(ctx, filter)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	data.TotalPages = (total + data.PageSize - 1) / data.PageSize
	if data.TotalPages == 0 {
		data.TotalPages = 1
	}
	// A requested page beyond the last one (e.g. a stale bookmarked/Next
	// link after items were deleted) would otherwise produce a
	// nonsensical "Showing 26-3 of 3" — clamp and re-query once rather
	// than displaying that.
	if data.Page > data.TotalPages {
		data.Page = data.TotalPages
		filter.Offset = (data.Page - 1) * data.PageSize
		if items, total, err = s.svc.ListLibrary(ctx, filter); err != nil {
			s.renderError(w, r, http.StatusInternalServerError, err)
			return
		}
	}
	data.TotalCount = total
	data.ShowingText = showingText(data.Page, data.PageSize, total)
	libraryPageURL := func(page int) string {
		v := url.Values{}
		if data.Search != "" {
			v.Set("q", data.Search)
		}
		if data.SelectedTopic != "" {
			v.Set("topic", data.SelectedTopic)
		}
		if data.SelectedDifficulty != "" {
			v.Set("difficulty", string(data.SelectedDifficulty))
		}
		v.Set("sort", data.Sort)
		v.Set("page_size", strconv.Itoa(data.PageSize))
		v.Set("page", strconv.Itoa(page))
		return "/library?" + v.Encode()
	}
	if data.Page > 1 {
		data.PrevURL = libraryPageURL(data.Page - 1)
	}
	if data.Page < data.TotalPages {
		data.NextURL = libraryPageURL(data.Page + 1)
	}

	today := s.svc.Today()
	for _, item := range items {
		data.Rows = append(data.Rows, libraryRow{DueItem: item, StatusLabel: statusLabel(item.NextReviewDate, today), StatusClass: statusClass(item.NextReviewDate, today)})
	}

	total, err = s.svc.CountProblems(ctx)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	data.TotalTracked = total

	difficulty, err := s.svc.DifficultyBreakdown(ctx)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	data.Difficulty = difficulty

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

// statusLabel/statusClass classify a review's next-review date relative
// to today for the Library page's at-a-glance column — every problem
// always has one (AddProblem requires a first grade at creation), so
// there's no "unscheduled" case to handle.
func statusLabel(nextReview, today time.Time) string {
	days := int(nextReview.Sub(today).Hours() / 24)
	switch {
	case days < 0:
		return fmt.Sprintf("Overdue %dd", -days)
	case days == 0:
		return "Today (Due)"
	case days == 1:
		return "Tomorrow"
	default:
		return fmt.Sprintf("In %d days", days)
	}
}

func statusClass(nextReview, today time.Time) string {
	days := int(nextReview.Sub(today).Hours() / 24)
	switch {
	case days < 0:
		return "status-overdue"
	case days == 0:
		return "status-due-today"
	default:
		return "status-upcoming"
	}
}

// showingText renders the "Showing X-Y of N" range label for the
// current page — N=0 (no matches) is rendered distinctly since there's
// no meaningful X-Y range in that case.
func showingText(page, pageSize, total int) string {
	if total == 0 {
		return "No problems match"
	}
	from := (page-1)*pageSize + 1
	to := from + pageSize - 1
	if to > total {
		to = total
	}
	return fmt.Sprintf("Showing %d-%d of %d problems", from, to, total)
}
