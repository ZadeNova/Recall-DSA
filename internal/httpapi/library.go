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

// libraryQuery is Library's view state (filters, sort and page), parsed
// and validated in one place. handleLibrary reads it from the URL and
// handleBulkStatus from the POSTed hidden fields, and both write it back
// out through params. So the Go side names each parameter exactly once,
// and a new filter can't be carried by Prev/Next but dropped by the bulk
// redirect (or the reverse).
type libraryQuery struct {
	Search     string
	Topic      string
	Difficulty service.Difficulty
	Sort       string
	Status     service.Status
	Page       int
	PageSize   int
}

// parseLibraryQuery keeps only known parameters, each validated or
// defaulted, so anything rebuilt from the result is safe to put in a
// URL: a crafted form can't smuggle extra params into the bulk redirect.
func parseLibraryQuery(v url.Values) libraryQuery {
	lq := libraryQuery{
		Search:   v.Get("q"),
		Topic:    v.Get("topic"),
		Sort:     defaultLibrarySort,
		Status:   service.ParseStatus(v.Get("status")),
		Page:     pageFromQuery(v.Get("page")),
		PageSize: pageSizeFromQuery(v.Get("page_size")),
	}
	if d := service.Difficulty(v.Get("difficulty")); d.Valid() {
		lq.Difficulty = d
	}
	if s := v.Get("sort"); s == "title" || s == "difficulty" {
		lq.Sort = s
	}
	return lq
}

// filterParams are the filters and sort, without paging: what Prev/Next
// and the page-size form carry (buildPageInfo adds the page params).
// Active is the default status and is left out of the URL; any other
// status must be carried, or paging would fall back to the Active view.
func (lq libraryQuery) filterParams() url.Values {
	v := url.Values{}
	if lq.Search != "" {
		v.Set("q", lq.Search)
	}
	if lq.Topic != "" {
		v.Set("topic", lq.Topic)
	}
	if lq.Difficulty != "" {
		v.Set("difficulty", string(lq.Difficulty))
	}
	v.Set("sort", lq.Sort)
	if lq.Status != service.StatusActive {
		v.Set("status", string(lq.Status))
	}
	return v
}

// params is filterParams plus the current page and page size: the full
// view, for the bulk form's hidden fields and its redirect back.
func (lq libraryQuery) params() url.Values {
	v := lq.filterParams()
	v.Set("page", strconv.Itoa(lq.Page))
	v.Set("page_size", strconv.Itoa(lq.PageSize))
	return v
}

func (lq libraryQuery) filter() service.ListProblemsFilter {
	f := service.ListProblemsFilter{
		Sort:   lq.Sort,
		Status: lq.Status,
		Limit:  lq.PageSize,
		Offset: (lq.Page - 1) * lq.PageSize,
	}
	if lq.Topic != "" {
		f.Topic = &lq.Topic
	}
	if lq.Difficulty != "" {
		f.Difficulty = &lq.Difficulty
	}
	if lq.Search != "" {
		f.Search = &lq.Search
	}
	return f
}

type libraryViewData struct {
	Topics       []string
	Difficulties []service.Difficulty
	Query        libraryQuery
	Rows         []libraryRow

	Summary service.Summary

	// Notice is the result of the last bulk pause/unpause, shown once
	// after the redirect back here. BulkFields are the current view,
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
	lq := parseLibraryQuery(q)

	items, total, err := s.svc.ListLibrary(ctx, lq.filter())
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	// A requested page beyond the last one (e.g. a stale bookmarked/Next
	// link after items were deleted) would otherwise produce a
	// nonsensical "Showing 26-3 of 3" — clamp and re-query once rather
	// than displaying that.
	if totalPages := totalPagesFor(total, lq.PageSize); lq.Page > totalPages {
		lq.Page = totalPages
		if items, total, err = s.svc.ListLibrary(ctx, lq.filter()); err != nil {
			s.renderError(w, r, http.StatusInternalServerError, err)
			return
		}
	}

	data := libraryViewData{
		Difficulties: []service.Difficulty{service.DifficultyEasy, service.DifficultyMedium, service.DifficultyHard},
		Query:        lq,
		Rows:         libraryRows(items, s.svc.Today()),
		Notice:       bulkNotice(q.Get("done"), q.Get("n")),
		BulkFields:   hiddenFieldsFrom(lq.params()),
		Page:         buildPageInfo("/library", lq.filterParams(), "page", "page_size", lq.Page, lq.PageSize, total),
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

// libraryRows adds each item's display fields. A paused problem's
// stored date is stale and no longer means "due", so it shows when it
// was paused instead of an overdue label.
func libraryRows(items []service.DueItem, today time.Time) []libraryRow {
	rows := make([]libraryRow, 0, len(items))
	for _, item := range items {
		row := libraryRow{DueItem: item, StatusLabel: statusLabel(item.NextReviewDate, today), StatusClass: statusClass(item.NextReviewDate, today)}
		if item.PausedAt != nil {
			row.Paused = true
			row.StatusLabel = "Paused"
			row.StatusClass = "status-paused"
			row.PausedSince = item.PausedAt.In(today.Location()).Format("Jan 2")
		}
		rows = append(rows, row)
	}
	return rows
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

	// Rebuilt from the parsed view, never the raw form or URL, so the
	// redirect always stays on /library with only known params.
	back := parseLibraryQuery(r.PostForm).params()

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
