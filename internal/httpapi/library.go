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
	Paused      bool
	PausedSince string
}

const defaultLibrarySort = "next_review"

type libraryViewData struct {
	Topics             []string
	SelectedTopic      string
	Difficulties       []service.Difficulty
	SelectedDifficulty service.Difficulty
	Search             string
	Sort               string
	Status             string
	Rows               []libraryRow

	Summary service.Summary

	// Notice is the result of the last bulk pause/unpause, shown once
	// after the redirect back here. BulkFields are the current filters,
	// carried through the bulk form so the redirect lands on the same view.
	Notice     string
	BulkFields []hiddenField

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
		Status:       service.StatusActive,
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
	if v := q.Get("status"); v == service.StatusPaused || v == service.StatusAll {
		data.Status = v
	}
	filter.Status = data.Status

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
	// Active is the default and is left out of the URL; anything else has
	// to be carried on every Prev/Next link and the page-size form, or
	// paging would silently fall back to the Active view.
	if data.Status != service.StatusActive {
		extra.Set("status", data.Status)
	}
	data.Page = buildPageInfo("/library", extra, "page", "page_size", page, pageSize, total)

	bulk := url.Values{}
	for k, v := range extra {
		bulk[k] = v
	}
	bulk.Set("page", strconv.Itoa(page))
	bulk.Set("page_size", strconv.Itoa(pageSize))
	data.BulkFields = hiddenFieldsFrom(bulk)
	data.Notice = bulkNotice(q.Get("done"), q.Get("n"))

	today := s.svc.Today()
	for _, item := range items {
		row := libraryRow{DueItem: item, StatusLabel: statusLabel(item.NextReviewDate, today), StatusClass: statusClass(item.NextReviewDate, today)}
		if item.PausedAt != nil {
			// A paused problem's stored date is stale and no longer means
			// "due", so show when it was paused instead of an overdue label.
			row.Paused = true
			row.StatusLabel = "Paused"
			row.StatusClass = "status-paused"
			row.PausedSince = item.PausedAt.In(today.Location()).Format("Jan 2")
		}
		data.Rows = append(data.Rows, row)
	}

	if data.Summary, err = s.svc.Summary(ctx); err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}

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

// libraryReturnParams are the only params handleBulkStatus copies into
// its redirect back to /library. It's a whitelist rebuilt from scratch,
// never the raw request URL, so a crafted form can't turn the redirect
// into an open redirect or inject extra params.
var libraryReturnParams = []string{"q", "topic", "difficulty", "sort", "status", "page", "page_size"}

// maxBulkIDs caps one bulk request. The UI can only select one page (at
// most 50 rows), so this only stops a hand-crafted POST from exceeding
// SQLite's bound-parameter limit and failing with a 500.
const maxBulkIDs = 500

// handleBulkStatus pauses or unpauses the ticked Library rows, then
// redirects (post/redirect/get) back to the same filtered view with the
// outcome in ?done=&n= for handleLibrary to turn into a notice.
func (s *Server) handleBulkStatus(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, err)
		return
	}

	action := r.PostFormValue("action")
	if action != "pause" && action != "unpause" {
		s.renderError(w, r, http.StatusBadRequest, fmt.Errorf("unknown action %q", action))
		return
	}

	if n := len(r.PostForm["id"]); n > maxBulkIDs {
		s.renderError(w, r, http.StatusBadRequest, fmt.Errorf("too many problems selected (%d, max %d)", n, maxBulkIDs))
		return
	}
	var ids []int64
	for _, raw := range r.PostForm["id"] {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			s.renderError(w, r, http.StatusBadRequest, fmt.Errorf("invalid problem id %q", raw))
			return
		}
		ids = append(ids, id)
	}

	back := url.Values{}
	for _, k := range libraryReturnParams {
		if v := r.PostFormValue(k); v != "" {
			back.Set(k, v)
		}
	}

	if len(ids) == 0 {
		back.Set("done", "none")
		http.Redirect(w, r, "/library?"+back.Encode(), http.StatusSeeOther)
		return
	}

	var (
		n   int
		err error
	)
	if action == "pause" {
		n, err = s.svc.PauseProblems(r.Context(), ids)
		back.Set("done", "paused")
	} else {
		// A missing or invalid target parses to 0, which the service
		// replaces with its default.
		target, _ := strconv.Atoi(r.PostFormValue("target_per_day"))
		n, err = s.svc.UnpauseProblems(r.Context(), ids, target)
		back.Set("done", "unpaused")
	}
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	back.Set("n", strconv.Itoa(n))
	http.Redirect(w, r, "/library?"+back.Encode(), http.StatusSeeOther)
}

// bulkNotice turns the ?done=&n= a bulk action redirected with into the
// message shown on Library. Anything unrecognised or malformed yields no
// notice rather than echoing arbitrary query text into the page.
func bulkNotice(done, nRaw string) string {
	if done == "none" {
		return "No problems selected."
	}
	n, err := strconv.Atoi(nRaw)
	if err != nil || n < 0 {
		return ""
	}
	switch done {
	case "paused":
		if n == 0 {
			return "Nothing to pause — the selected problems were already paused."
		}
		return fmt.Sprintf("Paused %s.", problemCount(n))
	case "unpaused":
		if n == 0 {
			return "Nothing to unpause — the selected problems weren't paused."
		}
		return fmt.Sprintf("Unpaused %s. They return to your queue over the next few days.", problemCount(n))
	}
	return ""
}

func problemCount(n int) string {
	if n == 1 {
		return "1 problem"
	}
	return fmt.Sprintf("%d problems", n)
}
