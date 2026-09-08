package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
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

const defaultLibrarySort = "next_review"

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

	Page pageInfo
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

	page := pageFromQuery(q.Get("page"))
	pageSize := pageSizeFromQuery(q.Get("page_size"))
	filter.Limit = pageSize
	filter.Offset = (page - 1) * pageSize

	items, total, err := s.svc.ListLibrary(ctx, filter)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	// A requested page beyond the last one (e.g. a stale bookmarked/Next
	// link after items were deleted) would otherwise produce a
	// nonsensical "Showing 26-3 of 3" — clamp and re-query once rather
	// than displaying that.
	if totalPages := totalPagesFor(total, pageSize); page > totalPages {
		page = totalPages
		filter.Offset = (page - 1) * pageSize
		if items, total, err = s.svc.ListLibrary(ctx, filter); err != nil {
			s.renderError(w, r, http.StatusInternalServerError, err)
			return
		}
	}

	extra := url.Values{}
	if data.Search != "" {
		extra.Set("q", data.Search)
	}
	if data.SelectedTopic != "" {
		extra.Set("topic", data.SelectedTopic)
	}
	if data.SelectedDifficulty != "" {
		extra.Set("difficulty", string(data.SelectedDifficulty))
	}
	extra.Set("sort", data.Sort)
	data.Page = buildPageInfo("/library", extra, "page", "page_size", page, pageSize, total)

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
	days := daysBetween(nextReview, today)
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
	days := daysBetween(nextReview, today)
	switch {
	case days < 0:
		return "status-overdue"
	case days == 0:
		return "status-due-today"
	default:
		return "status-upcoming"
	}
}

// daysBetween computes the calendar-day difference between two
// same-location midnight timestamps by stripping location entirely
// (comparing each side's Y/M/D as plain UTC dates) rather than dividing
// their wall-clock duration by 24 hours. A duration-based diff breaks
// across a DST transition: e.g. in America/New_York, midnight Mar 10
// 2024 to midnight Mar 11 2024 is only a 23-hour wall-clock span (the
// spring-forward transition falls inside it), so
// int(23.0/24) truncates to 0 — a problem due tomorrow would render as
// "Today (Due)". Comparing calendar dates directly is exact regardless
// of the offset either timestamp happens to carry.
func daysBetween(a, b time.Time) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	utcA := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
	utcB := time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC)
	return int(utcA.Sub(utcB).Hours() / 24)
}
