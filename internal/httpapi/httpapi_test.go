package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/db"
	"github.com/ZadeNova/recall-dsa/internal/service"
)

// newTestServer returns both the routed handler (for exercising HTTP
// behavior) and the underlying service (for setting up/inspecting state
// directly, without scraping HTML — the HTTP tests here focus on routing
// and rendering, not re-verifying business rules already covered by
// internal/service's own tests).
func newTestServer(t *testing.T) (http.Handler, *service.Service) {
	t.Helper()
	return newTestServerAt(t)
}

// newTestServerAt is newTestServer with the clock optionally pinned, so a
// test can assert on behavior that depends on what day it is without
// depending on what day it actually is.
func newTestServerAt(t *testing.T, opts ...service.Option) (http.Handler, *service.Service) {
	t.Helper()

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: unexpected err: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	loc, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Fatalf("LoadLocation: unexpected err: %v", err)
	}
	svc := service.New(conn, loc, opts...)

	srv, err := NewServer(svc)
	if err != nil {
		t.Fatalf("NewServer: unexpected err: %v", err)
	}
	return srv.Routes(), svc
}

func doForm(t *testing.T, h http.Handler, method, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func doGet(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// TestHome_EmptyLibraryShowsGetStartedCard asserts the first-run
// experience: a brand-new self-hoster with an empty library sees a
// "Get started" card pointing at Import/Add Problem instead of an
// all-zero dashboard that gives no guidance on what to do next.
func TestHome_EmptyLibraryShowsGetStartedCard(t *testing.T) {
	h, _ := newTestServer(t)
	rec := doGet(t, h, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Get started") {
		t.Errorf("home missing get-started empty state:\n%s", body)
	}
	if !strings.Contains(body, `href="/problems/import"`) {
		t.Errorf("home empty state missing link to /problems/import:\n%s", body)
	}
	if strings.Contains(body, "<strong>0</strong> due for review") {
		t.Errorf("home should not show the normal dashboard when empty:\n%s", body)
	}
}

// TestHome_IsPureGlanceNoGradeButtonsAndNoUpcoming asserts the whole
// point of the redesign: Home shows due items for orientation only (no
// grade-form anywhere on the page, for any item), and doesn't show
// Upcoming at all — that view lives exclusively on /due now
// (FRONTEND.md, Decided UX facts #1).
func TestHome_IsPureGlanceNoGradeButtonsAndNoUpcoming(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	// Graded 10 days ago with a 5-day interval lands 5 days in the past:
	// overdue, so it shows on Home's glance list.
	if _, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Due Now", URL: "due-now", Difficulty: service.DifficultyEasy,
		Grade: "Good", At: time.Now().AddDate(0, 0, -10),
	}); err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}
	// Graded just now with a 5-day interval lands 5 days out: would be in
	// the Upcoming window, which Home no longer shows at all.
	if _, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Upcoming Problem", URL: "upcoming-problem", Difficulty: service.DifficultyEasy,
		Grade: "Good", At: time.Now(),
	}); err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	rec := doGet(t, h, "/")
	body := rec.Body.String()

	if !strings.Contains(body, "Due Now") {
		t.Errorf("home missing the due item entirely:\n%s", body)
	}
	if strings.Contains(body, "/grade") {
		t.Errorf("home must have no grade-form anywhere (pure glance), but found one:\n%s", body)
	}
	if strings.Contains(body, "Upcoming Problem") {
		t.Errorf("home must not show upcoming items at all — that's /due's job now:\n%s", body)
	}
	if !strings.Contains(body, `href="/due"`) {
		t.Errorf("home missing its link through to /due:\n%s", body)
	}
}

func TestHome_ShowsStatsAndUpcomingByDay(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	if _, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Two Sum", URL: "two-sum", Difficulty: service.DifficultyEasy,
		Grade: "Good", At: time.Now(), // +5 days -> "In 5 days" on the sidebar
	}); err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}
	if _, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "3Sum", URL: "3sum", Difficulty: service.DifficultyMedium,
		Grade: "Good", At: time.Now(),
	}); err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}
	// Overdue, so it appears in the glance table itself (the two problems
	// above are 5 days out — not yet due — so they never reach the
	// glance table, only the sidebar). Graded Hard for a 3-day interval,
	// a string that can't collide with the sidebar's own "In 5 days" text.
	if _, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Climbing Stairs", URL: "climbing-stairs", Difficulty: service.DifficultyEasy,
		Grade: "Hard", At: time.Now().AddDate(0, 0, -10),
	}); err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	rec := doGet(t, h, "/")
	body := rec.Body.String()

	if !strings.Contains(body, `<div class="stat-number">3</div>`) {
		t.Errorf("home missing Total Tracked = 3:\n%s", body)
	}
	if !strings.Contains(body, "2 Easy") || !strings.Contains(body, "1 Medium") || !strings.Contains(body, "0 Hard") {
		t.Errorf("home missing correct difficulty split:\n%s", body)
	}
	if !strings.Contains(body, "In 5 days") || !strings.Contains(body, "2 problems") {
		t.Errorf("home missing the upcoming-by-day sidebar entry:\n%s", body)
	}
	if !strings.Contains(body, "3 days") {
		t.Errorf("home glance table missing the Interval column:\n%s", body)
	}
}

func TestDue_EmptyShowsNothingDueMessage(t *testing.T) {
	h, _ := newTestServer(t)
	rec := doGet(t, h, "/due")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Nothing due") {
		t.Errorf("body missing 'Nothing due' message:\n%s", rec.Body.String())
	}
}

// TestDue_ShowsUpcomingSectionViewOnly also carries what was previously
// a separate CSS-source-assertion test (Upcoming's rows must render
// their difficulty class, since .muted-section — the wrapper around
// Upcoming — once set `color` at higher specificity than
// .difficulty-Easy/Medium/Hard and silently flattened every difficulty
// to muted-gray). That regression was in the stylesheet's cascade, which
// Go can't verify by inspecting rendered HTML — the class attribute
// alone was already present even while the bug was live — so the
// remaining, testable half is just that the class is still emitted.
func TestDue_ShowsUpcomingSectionViewOnly(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	overdue, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Overdue Problem", URL: "overdue-problem", Difficulty: service.DifficultyEasy,
		Grade: "Good", At: time.Now().AddDate(0, 0, -10),
	})
	if err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}
	upcoming, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Upcoming Problem", URL: "upcoming-problem", Difficulty: service.DifficultyHard,
		Grade: "Good", At: time.Now(),
	})
	if err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	rec := doGet(t, h, "/due")
	body := rec.Body.String()

	overdueGradeForm := "/problems/" + strconv.FormatInt(overdue.ID, 10) + "/grade"
	if !strings.Contains(body, overdueGradeForm) {
		t.Errorf("due section missing its grade form:\n%s", body)
	}
	if !strings.Contains(body, "Upcoming Problem") {
		t.Errorf("/due missing the upcoming item entirely:\n%s", body)
	}
	upcomingGradeForm := "/problems/" + strconv.FormatInt(upcoming.ID, 10) + "/grade"
	if strings.Contains(body, upcomingGradeForm) {
		t.Errorf("upcoming item must not be gradable, but found its grade form:\n%s", body)
	}
	if !strings.Contains(body, `class="difficulty-Hard"`) {
		t.Errorf("upcoming row missing its difficulty class:\n%s", body)
	}
}

func TestDue_TopicFilterAppliesToBothDueAndUpcoming(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	// Overdue + upcoming, both tagged Graphs.
	if _, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Graphs Overdue", URL: "graphs-overdue", Difficulty: service.DifficultyEasy,
		Topics: []string{"Graphs"}, Grade: "Good", At: time.Now().AddDate(0, 0, -10),
	}); err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}
	if _, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Graphs Upcoming", URL: "graphs-upcoming", Difficulty: service.DifficultyEasy,
		Topics: []string{"Graphs"}, Grade: "Good", At: time.Now(),
	}); err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}
	// Overdue + upcoming, both tagged Arrays & Hashing — should be
	// excluded entirely once filtered to Graphs.
	if _, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Arrays Overdue", URL: "arrays-overdue", Difficulty: service.DifficultyEasy,
		Topics: []string{"Arrays & Hashing"}, Grade: "Good", At: time.Now().AddDate(0, 0, -10),
	}); err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}
	if _, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Arrays Upcoming", URL: "arrays-upcoming", Difficulty: service.DifficultyEasy,
		Topics: []string{"Arrays & Hashing"}, Grade: "Good", At: time.Now(),
	}); err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	rec := doGet(t, h, "/due?topic="+url.QueryEscape("Graphs"))
	body := rec.Body.String()

	for _, want := range []string{"Graphs Overdue", "Graphs Upcoming"} {
		if !strings.Contains(body, want) {
			t.Errorf("filtered /due missing %q:\n%s", want, body)
		}
	}
	for _, notWant := range []string{"Arrays Overdue", "Arrays Upcoming"} {
		if strings.Contains(body, notWant) {
			t.Errorf("filtered /due should not show %q:\n%s", notWant, body)
		}
	}
}

func TestErrorPage_404OnMissingProblem(t *testing.T) {
	h, _ := newTestServer(t)
	rec := doGet(t, h, "/problems/999/edit")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "404") || !strings.Contains(body, "problem 999 not found") {
		t.Errorf("error page missing status/message:\n%s", body)
	}
	if !strings.Contains(body, `href="/"`) {
		t.Errorf("error page missing a way back home:\n%s", body)
	}
}

func TestErrorPage_400OnMalformedID(t *testing.T) {
	h, _ := newTestServer(t)
	rec := doGet(t, h, "/problems/not-a-number/edit")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "400") || !strings.Contains(body, "invalid syntax") {
		t.Errorf("error page missing status/message:\n%s", body)
	}
}

func TestCreateProblem_AppearsInLibraryNotYetDue(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doForm(t, h, http.MethodPost, "/problems", url.Values{
		"title": {"Two Sum"}, "url": {"two-sum"}, "difficulty": {"Easy"},
		"new_topics": {"Arrays & Hashing, Two Pointers"}, "grade": {"Good"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}

	rec = doGet(t, h, "/library")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Two Sum") {
		t.Errorf("library missing added problem (status %d):\n%s", rec.Code, rec.Body.String())
	}

	// A fresh Good grade is due in 5 days, so the due-queue should still
	// be empty right after creation.
	rec = doGet(t, h, "/due")
	if !strings.Contains(rec.Body.String(), "Nothing due") {
		t.Errorf("expected nothing due right after a fresh Good grade:\n%s", rec.Body.String())
	}
}

func TestCreateProblem_InvalidInputReRendersFormWithError(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doForm(t, h, http.MethodPost, "/problems", url.Values{
		"title": {""}, "url": {"two-sum"}, "difficulty": {"Easy"}, "grade": {"Good"},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "class=\"error\"") {
		t.Errorf("body missing error message:\n%s", rec.Body.String())
	}
}

// TestCreateProblem_MergesCheckedTopicsAndFreeTextTopics covers
// topicsFromForm (problems.go), which had no real coverage at any level:
// two existing tests appeared to exercise it but both submitted a form
// key ("topics") that topicsFromForm never reads — it reads "topic"
// (checkbox, multi-value) and "new_topics" (comma-separated free text).
// This submits both, including a name present in both, and asserts the
// result is the union with no duplicate.
func TestCreateProblem_MergesCheckedTopicsAndFreeTextTopics(t *testing.T) {
	h, svc := newTestServer(t)

	rec := doForm(t, h, http.MethodPost, "/problems", url.Values{
		"title": {"Two Sum"}, "url": {"two-sum"}, "difficulty": {"Easy"}, "grade": {"Good"},
		"topic":      {"Arrays & Hashing", "Two Pointers"},
		"new_topics": {"Two Pointers, Custom Tag"}, // "Two Pointers" duplicated on purpose
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}

	problem, _, err := svc.ListLibrary(t.Context(), service.ListProblemsFilter{})
	if err != nil {
		t.Fatalf("ListLibrary: unexpected err: %v", err)
	}
	if len(problem) != 1 {
		t.Fatalf("len(problem) = %d, want 1", len(problem))
	}

	got := append([]string(nil), problem[0].Topics...)
	sort.Strings(got)
	want := []string{"Arrays & Hashing", "Custom Tag", "Two Pointers"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("topics = %v, want %v (checked boxes + free text merged, duplicate dropped)", got, want)
	}
}

func TestLibrary_SearchFiltersByTitle(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	mustAdd := func(title, slug string) {
		t.Helper()
		if _, err := svc.AddProblem(ctx, service.AddProblemInput{
			Title: title, URL: slug, Difficulty: service.DifficultyEasy,
			Grade: "Good", At: time.Now(),
		}); err != nil {
			t.Fatalf("AddProblem(%s): unexpected err: %v", title, err)
		}
	}
	mustAdd("Two Sum", "two-sum")
	mustAdd("Climbing Stairs", "climbing-stairs")

	rec := doGet(t, h, "/library?q=Sum")
	body := rec.Body.String()
	if !strings.Contains(body, "Two Sum") {
		t.Errorf("search for %q should match Two Sum:\n%s", "Sum", body)
	}
	if strings.Contains(body, "Climbing Stairs") {
		t.Errorf("search for %q should not match Climbing Stairs:\n%s", "Sum", body)
	}
}

// TestLibrary_PageSizeFormPreservesActiveFilters is the HTTP-level
// counterpart to TestHiddenFieldsFrom_SortedAndFlattened: it exercises
// the actual rendered page rather than the helper function directly,
// confirming q/topic/sort all survive into the page-size form's hidden
// fields when changing page size — the exact behavior only checked
// manually with curl when the #7 pagination consolidation landed.
func TestLibrary_PageSizeFormPreservesActiveFilters(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	if _, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Two Sum", URL: "two-sum", Difficulty: service.DifficultyEasy,
		Topics: []string{"Arrays & Hashing"}, Grade: "Good", At: time.Now(),
	}); err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	body := doGet(t, h, "/library?q=Sum&topic=Arrays+%26+Hashing&sort=title").Body.String()
	formStart := strings.Index(body, `class="page-size-form"`)
	if formStart == -1 {
		t.Fatalf("page-size-form not found in body:\n%s", body)
	}
	formEnd := strings.Index(body[formStart:], "</form>")
	form := body[formStart : formStart+formEnd]

	for _, want := range []string{
		`name="q" value="Sum"`,
		`name="topic" value="Arrays &amp; Hashing"`,
		`name="sort" value="title"`,
	} {
		if !strings.Contains(form, want) {
			t.Errorf("page-size form missing %q:\n%s", want, form)
		}
	}
}

func TestLibrary_ShowsIntervalAndStatusColumns(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	if _, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Two Sum", URL: "two-sum", Difficulty: service.DifficultyEasy,
		Grade: "Failed", At: time.Now(),
	}); err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	body := doGet(t, h, "/library").Body.String()
	if !strings.Contains(body, "1 days") {
		t.Errorf("library should show the Failed grade's 1-day interval:\n%s", body)
	}
	// A fresh Failed sets IntervalDays=1, so next_review_date is
	// tomorrow, not today (internal/service/reviews.go's recordReview:
	// next_review_date = at + IntervalDays).
	if !strings.Contains(body, "Tomorrow") {
		t.Errorf("a fresh Failed grade is due tomorrow, want a \"Tomorrow\" status:\n%s", body)
	}
}

// TestStatusLabel_SurvivesDSTTransition guards against a real
// regression: statusLabel/statusClass once computed the day-difference
// as int(nextReview.Sub(today).Hours() / 24), a plain duration divide.
// Across a DST transition that's wrong — reproduced manually with
// America/New_York's 2024 spring-forward (midnight Mar 10 to midnight
// Mar 11 is only a 23-hour wall-clock span), where int(23.0/24)
// truncates to 0: a problem due tomorrow rendered as "Today (Due)".
// main.go explicitly documents -tz as self-hoster-configurable, so any
// DST-observing zone hit this, not just an edge case.
func TestStatusLabel_SurvivesDSTTransition(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("America/New_York tzdata unavailable: %v", err)
	}

	today := time.Date(2024, 3, 10, 0, 0, 0, 0, loc)    // the spring-forward day itself
	tomorrow := time.Date(2024, 3, 11, 0, 0, 0, 0, loc) // only a 23h wall-clock span away

	if label := statusLabel(tomorrow, today); label != "Tomorrow" {
		t.Errorf("statusLabel across DST spring-forward = %q, want %q", label, "Tomorrow")
	}
	if class := statusClass(tomorrow, today); class != "status-upcoming" {
		t.Errorf("statusClass across DST spring-forward = %q, want %q", class, "status-upcoming")
	}

	// Fall-back (2024-11-03, a 25h wall-clock day) sanity check too.
	fallToday := time.Date(2024, 11, 3, 0, 0, 0, 0, loc)
	fallTomorrow := time.Date(2024, 11, 4, 0, 0, 0, 0, loc)
	if label := statusLabel(fallTomorrow, fallToday); label != "Tomorrow" {
		t.Errorf("statusLabel across DST fall-back = %q, want %q", label, "Tomorrow")
	}
}

// seedPaginatedProblems adds n problems, overdue (so they also populate
// Home's and Due's glance/due tables, not just Library) unless overdue is
// false.
func seedPaginatedProblems(t *testing.T, svc *service.Service, n int, overdue bool) {
	t.Helper()
	at := time.Now()
	if overdue {
		at = at.AddDate(0, 0, -10)
	}
	for i := 0; i < n; i++ {
		title := "Problem " + strconv.Itoa(i)
		if _, err := svc.AddProblem(t.Context(), service.AddProblemInput{
			Title: title, URL: "problem-" + strconv.Itoa(i), Difficulty: service.DifficultyEasy,
			Grade: "Good", At: at,
		}); err != nil {
			t.Fatalf("AddProblem(%s): unexpected err: %v", title, err)
		}
	}
}

// TestPagination_ShowingRangeAndClamping covers the "Showing X-Y of N" /
// Prev / Next / out-of-range-clamp behavior shared by all three
// paginated views (Library, Home's glance list, Due's due-now section) —
// they're all built on the same buildPageInfo/pagination partial, so one
// table proves the wiring is correct everywhere it's used instead of
// restating the same four assertions three times.
func TestPagination_ShowingRangeAndClamping(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		overdue   bool
		extraWant string // page-1-only assertion specific to this view
	}{
		{"Library", "/library?sort=title", false, ""},
		{"Home", "/", true, "<strong>15</strong> due for review"},
		{"Due", "/due", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, svc := newTestServer(t)
			seedPaginatedProblems(t, svc, 15, c.overdue)
			sep := "?"
			if strings.Contains(c.path, "?") {
				sep = "&"
			}

			body := doGet(t, h, c.path+sep+"page_size=10&page=1").Body.String()
			if !strings.Contains(body, "Showing 1-10 of 15 problems") {
				t.Errorf("page 1 of size 10 should show 1-10 of 15:\n%s", body)
			}
			if strings.Contains(body, `&laquo; Prev</a>`) {
				t.Errorf("page 1 should not show a Prev link:\n%s", body)
			}
			if !strings.Contains(body, `>Next &raquo;</a>`) {
				t.Errorf("page 1 of 2 should show a Next link:\n%s", body)
			}
			if c.extraWant != "" && !strings.Contains(body, c.extraWant) {
				t.Errorf("missing %q:\n%s", c.extraWant, body)
			}

			body = doGet(t, h, c.path+sep+"page_size=10&page=2").Body.String()
			if !strings.Contains(body, "Showing 11-15 of 15 problems") {
				t.Errorf("page 2 of size 10 should show 11-15 of 15:\n%s", body)
			}
			if !strings.Contains(body, `&laquo; Prev</a>`) {
				t.Errorf("page 2 should show a Prev link:\n%s", body)
			}
			if strings.Contains(body, `>Next &raquo;</a>`) {
				t.Errorf("last page should not show a Next link:\n%s", body)
			}

			// A page number beyond the last one (e.g. a stale link after
			// deletions) should clamp to the last page rather than showing
			// a nonsensical range.
			body = doGet(t, h, c.path+sep+"page_size=10&page=99").Body.String()
			if !strings.Contains(body, "Showing 11-15 of 15 problems") {
				t.Errorf("out-of-range page should clamp to the last page:\n%s", body)
			}
		})
	}
}

// TestLibrary_SinglePageHidesNextLink covers the one pagination edge the
// table above doesn't: everything fitting on a single page (page_size
// larger than the total) should show neither Prev nor Next.
func TestLibrary_SinglePageHidesNextLink(t *testing.T) {
	h, svc := newTestServer(t)
	seedPaginatedProblems(t, svc, 15, false)

	body := doGet(t, h, "/library?page_size=50&sort=title").Body.String()
	if !strings.Contains(body, "Showing 1-15 of 15 problems") {
		t.Errorf("expected all 15 problems on one page:\n%s", body)
	}
	if strings.Contains(body, `>Next &raquo;</a>`) {
		t.Errorf("only page should not show a Next link:\n%s", body)
	}
}

// TestDue_SectionsPaginateIndependently is the reason buildPageInfo takes
// separate pageParam/pageSizeParam per section: /due renders a due-now
// table and an upcoming table side by side, sharing one URL, and paging
// one must not disturb the other's page. This seeds both sections and
// pages them to different pages within a single request, then checks
// each section's actual item boundary (not just the "Showing X-Y" text,
// which both sections render independently and can't distinguish which
// section is on which page).
func TestDue_SectionsPaginateIndependently(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	// Zero-padded so no title is a substring of another (DueItem-1 would
	// otherwise match DueItem-10..19).
	for i := 0; i < 15; i++ {
		title := fmt.Sprintf("DueItem-%02d", i)
		if _, err := svc.AddProblem(ctx, service.AddProblemInput{
			Title: title, URL: fmt.Sprintf("due-item-%02d", i), Difficulty: service.DifficultyEasy,
			Grade: "Good", At: time.Now().AddDate(0, 0, -10),
		}); err != nil {
			t.Fatalf("AddProblem(%s): unexpected err: %v", title, err)
		}
	}
	for i := 0; i < 15; i++ {
		title := fmt.Sprintf("UpItem-%02d", i)
		if _, err := svc.AddProblem(ctx, service.AddProblemInput{
			Title: title, URL: fmt.Sprintf("up-item-%02d", i), Difficulty: service.DifficultyEasy,
			Grade: "Good", At: time.Now(), // 5-day interval, inside the 7-day Upcoming window
		}); err != nil {
			t.Fatalf("AddProblem(%s): unexpected err: %v", title, err)
		}
	}

	// Due-now paged to page 2 (items 10-14), Upcoming left on page 1
	// (items 0-9) — in the same request, so a shared-state bug would show
	// up as one section leaking the other's page.
	body := doGet(t, h, "/due?page=2&page_size=10&upcoming_page=1&upcoming_page_size=10").Body.String()

	if !strings.Contains(body, "DueItem-10") {
		t.Errorf("due-now section should be on page 2 (items 10-14):\n%s", body)
	}
	if strings.Contains(body, "DueItem-00") {
		t.Errorf("due-now section on page 2 should not show page 1's items:\n%s", body)
	}
	if !strings.Contains(body, "UpItem-00") {
		t.Errorf("upcoming section should still be on its own page 1 (items 0-9), unaffected by due-now paging to page 2:\n%s", body)
	}
	if strings.Contains(body, "UpItem-10") {
		t.Errorf("upcoming section should not have been pushed to page 2 by the due-now page param:\n%s", body)
	}
}

func TestEditProblem_PrefillsFormAndUpdatePersists(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	problem, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Two Sum", URL: "two-sum", Difficulty: service.DifficultyEasy,
		Topics: []string{"Arrays & Hashing"}, Grade: "Good", At: time.Now(),
	})
	if err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	editPath := "/problems/" + strconv.FormatInt(problem.ID, 10) + "/edit"
	rec := doGet(t, h, editPath)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `value="Two Sum"`) {
		t.Fatalf("edit form not prefilled (status %d):\n%s", rec.Code, rec.Body.String())
	}

	rec = doForm(t, h, http.MethodPost, "/problems/"+strconv.FormatInt(problem.ID, 10)+"/update", url.Values{
		"title": {"Two Sum (renamed)"}, "url": {"two-sum"}, "difficulty": {"Medium"}, "topic": {"Two Pointers"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("update status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}

	updated, err := svc.GetProblem(ctx, problem.ID)
	if err != nil {
		t.Fatalf("GetProblem: unexpected err: %v", err)
	}
	if updated.Title != "Two Sum (renamed)" || updated.Difficulty != service.DifficultyMedium {
		t.Errorf("updated = %+v, unexpected fields", updated)
	}
	if !reflect.DeepEqual(updated.Topics, []string{"Two Pointers"}) {
		t.Errorf("updated.Topics = %v, want [Two Pointers] (replaced wholesale, the old Arrays & Hashing tag dropped)", updated.Topics)
	}
}

// GET /problems/999/edit returning 404 is already covered by
// TestErrorPage_404OnMissingProblem, which issues the identical request
// and additionally asserts the message and the way back home.

// TestMissingID_Returns404 guards against a real regression across four
// actions that share the same shape: none of them checked whether
// anything actually matched the given ID, so POSTing to any of them with
// a nonexistent ID used to redirect exactly as if the action had
// succeeded, rather than surfacing that nothing happened. The
// service-level counterpart (TestMissingID_ReturnsNotFound in
// internal/service) proves each Service method returns sql.ErrNoRows;
// this proves each of the four handlers actually wires that into a 404
// rather than falling through to its other error paths.
func TestMissingID_Returns404(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		form   url.Values
	}{
		{"UpdateProblem", http.MethodPost, "/problems/999/update", url.Values{
			"title": {"Ghost"}, "url": {"ghost"}, "difficulty": {"Easy"},
		}},
		{"DeleteProblem", http.MethodPost, "/problems/999/delete", url.Values{}},
		{"RenameTopic", http.MethodPost, "/topics/999/rename", url.Values{"name": {"New Name"}}},
		{"DeleteTopic", http.MethodPost, "/topics/999/delete", url.Values{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, _ := newTestServer(t)
			rec := doForm(t, h, c.method, c.path, c.form)
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404:\n%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestGradeNonexistentProblem_ReturnsClientErrorNotServerError covers the
// one mutating route the table above doesn't: grading. RecordReview
// doesn't check the problem exists before writing — it hits
// review_state's foreign-key constraint on insert instead — so this
// pins the realistic invariant (a stale tab grading an already-deleted
// problem must not look like success, and must not be a 500) without
// asserting on the exact error text, which currently leaks the raw
// SQLite constraint message; that's a separate, known issue.
func TestGradeNonexistentProblem_ReturnsClientErrorNotServerError(t *testing.T) {
	h, _ := newTestServer(t)
	rec := doForm(t, h, http.MethodPost, "/problems/999/grade", url.Values{"grade": {"Good"}})
	if rec.Code < 400 || rec.Code >= 500 {
		t.Errorf("status = %d, want a 4xx client error", rec.Code)
	}
}

func TestGradeExistingProblem_UpdatesReviewStateAndRedirects(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	problem, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Two Sum", URL: "two-sum", Difficulty: service.DifficultyEasy,
		Grade: "Good", At: time.Now(),
	})
	if err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	rec := doForm(t, h, http.MethodPost, "/problems/"+strconv.FormatInt(problem.ID, 10)+"/grade", url.Values{"grade": {"Easy"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/due" {
		t.Fatalf("grade status/location = %d %q, want 303 to /due", rec.Code, rec.Header().Get("Location"))
	}

	// Exact scheduler arithmetic is already covered by internal/service's
	// own tests; here it's enough to prove the grade actually reached
	// RecordReview (not just a no-op redirect) — the problem should no
	// longer show as due immediately after being graded.
	due, _, err := svc.RecommendDue(ctx, nil, 100, 0)
	if err != nil {
		t.Fatalf("RecommendDue: unexpected err: %v", err)
	}
	for _, d := range due {
		if d.ID == problem.ID {
			t.Fatalf("problem still shows as due right after being graded: %+v", d)
		}
	}
}

func TestGrade_HtmxRefreshesStatsAndNavPillOutOfBand(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	overdueProblem := func(title, slug string) int64 {
		t.Helper()
		p, err := svc.AddProblem(ctx, service.AddProblemInput{
			Title: title, URL: slug, Difficulty: service.DifficultyEasy,
			Grade: "Good", At: time.Now().AddDate(0, 0, -10),
		})
		if err != nil {
			t.Fatalf("AddProblem(%s): unexpected err: %v", title, err)
		}
		return p.ID
	}

	toGrade := overdueProblem("Overdue A", "overdue-a")
	overdueProblem("Overdue B", "overdue-b")

	before := doGet(t, h, "/due").Body.String()
	if !strings.Contains(before, `id="due-today-count" class="stat-number">2<`) {
		t.Fatalf("expected 2 due before grading:\n%s", before)
	}

	req := httptest.NewRequest(http.MethodPost, "/problems/"+strconv.FormatInt(toGrade, 10)+"/grade",
		strings.NewReader(url.Values{"grade": {"Good"}, "topic": {""}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "✓ Graded") {
		t.Errorf("htmx grade response missing the graded-badge fragment:\n%s", body)
	}
	if !strings.Contains(body, `id="due-today-count" class="stat-number" hx-swap-oob="true">1<`) {
		t.Errorf("htmx grade response missing the OOB-updated Due Today count (want 1):\n%s", body)
	}
	if !strings.Contains(body, `id="overdue-count" class="stat-number" hx-swap-oob="true">1<`) {
		t.Errorf("htmx grade response missing the OOB-updated Overdue count (want 1):\n%s", body)
	}
	if !strings.Contains(body, `id="nav-due-count" href="/due" class="due-pill" hx-swap-oob="true">1 due today<`) {
		t.Errorf("htmx grade response missing the OOB-updated nav due-pill (want 1):\n%s", body)
	}
}

func TestGrade_HtmxOOBStatsRespectTopicFilter(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	graphs, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Number of Islands", URL: "number-of-islands", Difficulty: service.DifficultyMedium,
		Topics: []string{"Graphs"}, Grade: "Good", At: time.Now().AddDate(0, 0, -10),
	})
	if err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}
	if _, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Two Sum", URL: "two-sum", Difficulty: service.DifficultyEasy,
		Topics: []string{"Arrays & Hashing"}, Grade: "Good", At: time.Now().AddDate(0, 0, -10),
	}); err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	// Grading the sole Graphs-tagged problem, filtered to Graphs: the
	// page-scoped stats should drop to 0 (nothing else tagged Graphs is
	// due), but the global nav pill should still show 1 (the untouched
	// Arrays & Hashing problem is still due).
	req := httptest.NewRequest(http.MethodPost, "/problems/"+strconv.FormatInt(graphs.ID, 10)+"/grade",
		strings.NewReader(url.Values{"grade": {"Good"}, "topic": {"Graphs"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `id="due-today-count" class="stat-number" hx-swap-oob="true">0<`) {
		t.Errorf("topic-scoped Due Today should be 0 after grading the only Graphs problem:\n%s", body)
	}
	if !strings.Contains(body, `id="overdue-count" class="stat-number" hx-swap-oob="true">0<`) {
		t.Errorf("topic-scoped Overdue should be 0 after grading the only Graphs problem:\n%s", body)
	}
	if !strings.Contains(body, `id="nav-due-count" href="/due" class="due-pill" hx-swap-oob="true">1 due today<`) {
		t.Errorf("global nav pill should still show 1 (Arrays & Hashing problem untouched):\n%s", body)
	}
}

func TestDeleteProblem_RemovesFromLibrary(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	problem, err := svc.AddProblem(ctx, service.AddProblemInput{
		Title: "Two Sum", URL: "two-sum", Difficulty: service.DifficultyEasy,
		Grade: "Good", At: time.Now(),
	})
	if err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	rec := doForm(t, h, http.MethodPost, "/problems/"+strconv.FormatInt(problem.ID, 10)+"/delete", url.Values{})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/library" {
		t.Fatalf("delete status/location = %d %q, want 303 to /library", rec.Code, rec.Header().Get("Location"))
	}

	rec = doGet(t, h, "/library")
	if strings.Contains(rec.Body.String(), "Two Sum") {
		t.Errorf("deleted problem still present in library:\n%s", rec.Body.String())
	}
}

func TestTopicsPage_CreateAppearsInList(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doForm(t, h, http.MethodPost, "/topics", url.Values{"name": {"Segment Tree"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}

	rec = doGet(t, h, "/topics")
	if !strings.Contains(rec.Body.String(), "Segment Tree") {
		t.Errorf("topics page missing created topic:\n%s", rec.Body.String())
	}
}

func TestTopicsPage_DuplicateNameReRendersWithError(t *testing.T) {
	h, _ := newTestServer(t)

	// "Linked List" is already seeded (internal/db/schema.sql).
	rec := doForm(t, h, http.MethodPost, "/topics", url.Values{"name": {"linked list"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "class=\"error\"") {
		t.Errorf("body missing error message:\n%s", rec.Body.String())
	}
}

// TestCreateProblem_UsesServiceClockNotHostClock proves the add-problem
// handler timestamps the grade with the service's clock rather than
// calling time.Now itself. It used to do the latter, which meant the
// injectable clock could be pinned and the handler would ignore it —
// leaving the day-boundary behavior SPEC.md §4 calls correctness-critical
// with no way to test it at all.
//
// The pinned instant is late evening in Asia/Singapore, so a host clock
// running in UTC would still be on the previous calendar day: getting
// this wrong shifts every next_review_date by one.
func TestCreateProblem_UsesServiceClockNotHostClock(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Fatalf("LoadLocation: unexpected err: %v", err)
	}
	pinned := time.Date(2031, 3, 14, 23, 30, 0, 0, loc)
	h, svc := newTestServerAt(t, service.WithClock(func() time.Time { return pinned }))

	rec := doForm(t, h, http.MethodPost, "/problems", url.Values{
		"title":      {"Two Sum"},
		"url":        {"two-sum"},
		"difficulty": {"Easy"},
		"grade":      {"Good"}, // Good's first interval is 5 days
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303:\n%s", rec.Code, rec.Body.String())
	}

	items, _, err := svc.ListLibrary(t.Context(), service.ListProblemsFilter{})
	if err != nil {
		t.Fatalf("ListLibrary: unexpected err: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}

	const want = "2031-03-19" // pinned SGT date + Good's 5-day interval
	if got := items[0].NextReviewDate.Format("2006-01-02"); got != want {
		t.Errorf("next_review_date = %s, want %s (the handler is still reading the host clock, not the service's)", got, want)
	}
}

// TestGrade_UsesServiceClockNotHostClock is the same guarantee for the
// grading handler, which is the one that runs on every review.
func TestGrade_UsesServiceClockNotHostClock(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Fatalf("LoadLocation: unexpected err: %v", err)
	}
	pinned := time.Date(2031, 7, 2, 23, 45, 0, 0, loc)
	h, svc := newTestServerAt(t, service.WithClock(func() time.Time { return pinned }))

	added, err := svc.AddProblem(t.Context(), service.AddProblemInput{
		Title: "Valid Anagram", URL: "valid-anagram", Difficulty: service.DifficultyEasy,
		Grade: "Good", At: pinned.AddDate(0, 0, -10),
	})
	if err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	rec := doForm(t, h, http.MethodPost, "/problems/"+strconv.FormatInt(added.ID, 10)+"/grade",
		url.Values{"grade": {"Good"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303:\n%s", rec.Code, rec.Body.String())
	}

	items, _, err := svc.ListLibrary(t.Context(), service.ListProblemsFilter{})
	if err != nil {
		t.Fatalf("ListLibrary: unexpected err: %v", err)
	}
	// Second Good on a 5-day interval at ease 2.5: 5 * 2.5 = 12.5 -> 13 days.
	const want = "2031-07-15"
	if got := items[0].NextReviewDate.Format("2006-01-02"); got != want {
		t.Errorf("next_review_date = %s, want %s (grading is still reading the host clock)", got, want)
	}
}

// TestCreateProblem_ExistingSlugReportsMergeInsteadOfSilentRedirect covers
// the re-add path. Adding a URL that already exists records a grade
// against the existing row and discards the submitted title, difficulty,
// and topics (SPEC.md §9). That used to redirect to /due exactly like a
// real create, so the user saw no sign that nothing was created and that
// what they typed went nowhere.
func TestCreateProblem_ExistingSlugReportsMergeInsteadOfSilentRedirect(t *testing.T) {
	h, svc := newTestServer(t)

	if _, err := svc.AddProblem(t.Context(), service.AddProblemInput{
		Title: "Two Sum", URL: "two-sum", Difficulty: service.DifficultyEasy,
		Topics: []string{"Arrays & Hashing"}, Grade: "Good", At: time.Now(),
	}); err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	rec := doForm(t, h, http.MethodPost, "/problems", url.Values{
		"title":      {"Totally Different Title"},
		"url":        {"https://leetcode.com/problems/two-sum/description/"},
		"difficulty": {"Hard"},
		"new_topics": {"Graphs"},
		"grade":      {"Failed"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the form should re-render with a notice, not redirect):\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "already tracked") {
		t.Errorf("response missing the already-tracked notice:\n%s", body)
	}

	// The grade landed on the existing row, and nothing about it changed.
	items, total, err := svc.ListLibrary(t.Context(), service.ListProblemsFilter{})
	if err != nil {
		t.Fatalf("ListLibrary: unexpected err: %v", err)
	}
	if total != 1 {
		t.Fatalf("total problems = %d, want 1 (a re-add must not create a second row)", total)
	}
	if items[0].Title != "Two Sum" {
		t.Errorf("title = %q, want %q (the submitted title must not overwrite the existing one)", items[0].Title, "Two Sum")
	}
	if items[0].Difficulty != service.DifficultyEasy {
		t.Errorf("difficulty = %q, want Easy (unchanged)", items[0].Difficulty)
	}
	if items[0].LastGrade != "Failed" {
		t.Errorf("last_grade = %q, want Failed (the grade is the one thing a re-add does apply)", items[0].LastGrade)
	}
}
