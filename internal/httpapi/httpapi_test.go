package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
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

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: unexpected err: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	loc, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Fatalf("LoadLocation: unexpected err: %v", err)
	}
	svc := service.New(conn, loc)

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

func TestHome_ShowsDueCountAndEmptyState(t *testing.T) {
	h, _ := newTestServer(t)
	rec := doGet(t, h, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<strong>0</strong> due for review") {
		t.Errorf("home missing zero due count:\n%s", body)
	}
	if !strings.Contains(body, "Nothing due right now") {
		t.Errorf("home missing empty-due message:\n%s", body)
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
		Title: "Upcoming Problem", URL: "upcoming-problem", Difficulty: service.DifficultyEasy,
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
		"topics": {"Arrays & Hashing, Two Pointers"}, "grade": {"Good"},
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
		"title": {"Two Sum (renamed)"}, "url": {"two-sum"}, "difficulty": {"Medium"}, "topics": {"Two Pointers"},
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
}

func TestEditProblem_MissingIDReturns404(t *testing.T) {
	h, _ := newTestServer(t)
	rec := doGet(t, h, "/problems/999/edit")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
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
	due, err := svc.RecommendDue(ctx, nil)
	if err != nil {
		t.Fatalf("RecommendDue: unexpected err: %v", err)
	}
	for _, d := range due {
		if d.ID == problem.ID {
			t.Fatalf("problem still shows as due right after being graded: %+v", d)
		}
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

	rec := doForm(t, h, http.MethodPost, "/topics", url.Values{"name": {"Monotonic Stack"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}

	rec = doGet(t, h, "/topics")
	if !strings.Contains(rec.Body.String(), "Monotonic Stack") {
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
