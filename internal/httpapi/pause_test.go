package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/service"
)

// pauseTestServer pins the clock to 2026-01-10 (Asia/Singapore's "today").
func pauseTestServer(t *testing.T) (http.Handler, *service.Service) {
	t.Helper()
	pinned := time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC)
	return newTestServerAt(t, service.WithClock(func() time.Time { return pinned }))
}

func addSeedProblem(t *testing.T, svc *service.Service, title, slug string) int64 {
	t.Helper()
	res, err := svc.AddProblem(t.Context(), service.AddProblemInput{
		Title: title, URL: slug, Difficulty: service.DifficultyEasy, Grade: "Good", At: svc.Now(),
	})
	if err != nil {
		t.Fatalf("AddProblem(%s): unexpected err: %v", title, err)
	}
	return res.ID
}

func idsForm(action string, ids ...int64) url.Values {
	form := url.Values{"action": {action}}
	for _, id := range ids {
		form.Add("id", strconv.FormatInt(id, 10))
	}
	return form
}

func redirectLocation(t *testing.T, rec *httptest.ResponseRecorder, wantCode int) *url.URL {
	t.Helper()
	if rec.Code != wantCode {
		t.Fatalf("status = %d, want %d", rec.Code, wantCode)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("bad Location header: %v", err)
	}
	return loc
}

func countStatus(t *testing.T, svc *service.Service, status string) int {
	t.Helper()
	_, total, err := svc.ListLibrary(t.Context(), service.ListProblemsFilter{Status: status})
	if err != nil {
		t.Fatalf("ListLibrary(%s): unexpected err: %v", status, err)
	}
	return total
}

func TestBulkStatus_PauseRedirectsBackWithFiltersAndResult(t *testing.T) {
	h, svc := pauseTestServer(t)
	a := addSeedProblem(t, svc, "A", "a")
	b := addSeedProblem(t, svc, "B", "b")
	addSeedProblem(t, svc, "C", "c")

	form := idsForm("pause", a, b)
	for k, v := range map[string]string{
		"q": "sum", "topic": "Graphs", "difficulty": "Easy", "sort": "title",
		"status": "all", "page": "2", "page_size": "25",
	} {
		form.Set(k, v)
	}
	rec := doForm(t, h, http.MethodPost, "/problems/bulk-status", form)

	loc := redirectLocation(t, rec, http.StatusSeeOther)
	if loc.Path != "/library" || loc.Host != "" {
		t.Errorf("redirect target = %q, want a local /library path", loc.String())
	}
	q := loc.Query()
	want := map[string]string{
		"q": "sum", "topic": "Graphs", "difficulty": "Easy", "sort": "title",
		"status": "all", "page": "2", "page_size": "25", "done": "paused", "n": "2",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("redirect param %s = %q, want %q", k, got, v)
		}
	}
	if got := countStatus(t, svc, service.StatusPaused); got != 2 {
		t.Errorf("paused problems = %d, want 2", got)
	}
}

func TestBulkStatus_UnpauseStaggersUsingTargetPerDay(t *testing.T) {
	h, svc := pauseTestServer(t)
	a := addSeedProblem(t, svc, "A", "a")
	b := addSeedProblem(t, svc, "B", "b")
	// Make both overdue, so their slots (not their old dates) decide the
	// result — a still-future date is deliberately kept by the service.
	for _, id := range []int64{a, b} {
		if _, err := svc.RecordReview(t.Context(), id, "Good", svc.Now().AddDate(0, 0, -30)); err != nil {
			t.Fatalf("RecordReview: unexpected err: %v", err)
		}
	}
	if _, err := svc.PauseProblems(t.Context(), []int64{a, b}); err != nil {
		t.Fatalf("PauseProblems: unexpected err: %v", err)
	}

	form := idsForm("unpause", a, b)
	form.Set("target_per_day", "1")
	rec := doForm(t, h, http.MethodPost, "/problems/bulk-status", form)

	loc := redirectLocation(t, rec, http.StatusSeeOther)
	if loc.Query().Get("done") != "unpaused" || loc.Query().Get("n") != "2" {
		t.Errorf("redirect = %q, want done=unpaused&n=2", loc.String())
	}
	items, _, err := svc.ListLibrary(t.Context(), service.ListProblemsFilter{Sort: "next_review"})
	if err != nil {
		t.Fatalf("ListLibrary: unexpected err: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("active problems = %d, want 2", len(items))
	}
	// "Today" is 2026-01-10; a target of 1/day puts one problem on each of +1 and +2.
	got := []string{items[0].NextReviewDate.Format("2006-01-02"), items[1].NextReviewDate.Format("2006-01-02")}
	if got[0] != "2026-01-11" || got[1] != "2026-01-12" {
		t.Errorf("next review dates = %v, want [2026-01-11 2026-01-12]", got)
	}
}

func TestBulkStatus_BadTargetPerDayFallsBackToDefault(t *testing.T) {
	h, svc := pauseTestServer(t)
	a := addSeedProblem(t, svc, "A", "a")
	if _, err := svc.PauseProblems(t.Context(), []int64{a}); err != nil {
		t.Fatalf("PauseProblems: unexpected err: %v", err)
	}
	for _, bad := range []string{"", "abc", "-4", "0"} {
		form := idsForm("unpause", a)
		form.Set("target_per_day", bad)
		rec := doForm(t, h, http.MethodPost, "/problems/bulk-status", form)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("target_per_day=%q: status = %d, want 303", bad, rec.Code)
		}
		// re-pause for the next iteration
		if _, err := svc.PauseProblems(t.Context(), []int64{a}); err != nil {
			t.Fatalf("PauseProblems: unexpected err: %v", err)
		}
	}
}

func TestBulkStatus_NothingSelectedIsANoticeNotAnError(t *testing.T) {
	h, svc := pauseTestServer(t)
	addSeedProblem(t, svc, "A", "a")

	rec := doForm(t, h, http.MethodPost, "/problems/bulk-status", url.Values{"action": {"pause"}, "status": {"all"}})

	loc := redirectLocation(t, rec, http.StatusSeeOther)
	if loc.Query().Get("done") != "none" {
		t.Errorf("redirect = %q, want done=none", loc.String())
	}
	if loc.Query().Get("status") != "all" {
		t.Errorf("filters not preserved: %q", loc.String())
	}
	if got := countStatus(t, svc, service.StatusPaused); got != 0 {
		t.Errorf("paused = %d, want 0", got)
	}
}

func TestBulkStatus_BadInputIs400AndChangesNothing(t *testing.T) {
	h, svc := pauseTestServer(t)
	a := addSeedProblem(t, svc, "A", "a")

	cases := []struct {
		name string
		form url.Values
	}{
		{"unknown action", idsForm("delete", a)},
		{"missing action", url.Values{"id": {strconv.FormatInt(a, 10)}}},
		{"non-numeric id", url.Values{"action": {"pause"}, "id": {strconv.FormatInt(a, 10), "abc"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := doForm(t, h, http.MethodPost, "/problems/bulk-status", c.form)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
	if got := countStatus(t, svc, service.StatusPaused); got != 0 {
		t.Errorf("paused = %d after bad requests, want 0", got)
	}
}

func TestBulkStatus_UnknownIDsAreIgnored(t *testing.T) {
	h, svc := pauseTestServer(t)
	a := addSeedProblem(t, svc, "A", "a")

	rec := doForm(t, h, http.MethodPost, "/problems/bulk-status", idsForm("pause", a, 999999))

	loc := redirectLocation(t, rec, http.StatusSeeOther)
	if loc.Query().Get("n") != "1" {
		t.Errorf("redirect = %q, want n=1 (the unknown id doesn't count)", loc.String())
	}
}

// The redirect is rebuilt from a whitelist of params, never echoed, so a
// crafted form can't smuggle a foreign URL or extra params into it.
func TestBulkStatus_RedirectIgnoresUnknownParams(t *testing.T) {
	h, svc := pauseTestServer(t)
	a := addSeedProblem(t, svc, "A", "a")

	form := idsForm("pause", a)
	form.Set("next", "https://evil.example/")
	form.Set("redirect", "//evil.example")
	form.Set("evil", "1")
	rec := doForm(t, h, http.MethodPost, "/problems/bulk-status", form)

	loc := redirectLocation(t, rec, http.StatusSeeOther)
	if loc.Host != "" || !strings.HasPrefix(loc.String(), "/library?") {
		t.Errorf("Location = %q, want a local /library URL", loc.String())
	}
	for _, k := range []string{"next", "redirect", "evil"} {
		if loc.Query().Has(k) {
			t.Errorf("unexpected param %q echoed into the redirect: %q", k, loc.String())
		}
	}
}

func TestBulkStatus_GETIsNotAllowed(t *testing.T) {
	h, _ := pauseTestServer(t)
	rec := doGet(t, h, "/problems/bulk-status")
	if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Errorf("GET status = %d, want 405 (state changes are POST-only)", rec.Code)
	}
}

func TestBulkNotice(t *testing.T) {
	cases := []struct {
		done, n, want string
	}{
		{"paused", "1", "Paused 1 problem."},
		{"paused", "10", "Paused 10 problems."},
		{"paused", "0", "Nothing to pause — the selected problems were already paused."},
		{"unpaused", "3", "Unpaused 3 problems. They return to your queue over the next few days."},
		{"unpaused", "0", "Nothing to unpause — the selected problems weren't paused."},
		{"none", "", "No problems selected."},
		{"none", "junk", "No problems selected."},
		{"", "", ""},
		{"bogus", "5", ""},
		{"paused", "abc", ""},
		{"paused", "-2", ""},
		{"paused", "", ""},
	}
	for _, c := range cases {
		t.Run(c.done+"/"+c.n, func(t *testing.T) {
			if got := bulkNotice(c.done, c.n); got != c.want {
				t.Errorf("bulkNotice(%q, %q) = %q, want %q", c.done, c.n, got, c.want)
			}
		})
	}
}

// --- Library status filter ---

func titlesIn(body string, titles ...string) (present, absent []string) {
	for _, title := range titles {
		if strings.Contains(body, ">"+title+"</a>") {
			present = append(present, title)
		} else {
			absent = append(absent, title)
		}
	}
	return present, absent
}

func TestLibrary_StatusFilterControlsWhichRowsAreListed(t *testing.T) {
	h, svc := pauseTestServer(t)
	addSeedProblem(t, svc, "Active Problem", "active-problem")
	paused := addSeedProblem(t, svc, "Paused Problem", "paused-problem")
	if _, err := svc.PauseProblems(t.Context(), []int64{paused}); err != nil {
		t.Fatalf("PauseProblems: unexpected err: %v", err)
	}

	cases := []struct {
		query       string
		wantPresent []string
		wantAbsent  []string
	}{
		{"", []string{"Active Problem"}, []string{"Paused Problem"}},
		{"?status=active", []string{"Active Problem"}, []string{"Paused Problem"}},
		{"?status=paused", []string{"Paused Problem"}, []string{"Active Problem"}},
		{"?status=all", []string{"Active Problem", "Paused Problem"}, nil},
		{"?status=bogus", []string{"Active Problem"}, []string{"Paused Problem"}},
	}
	for _, c := range cases {
		t.Run("status query "+c.query, func(t *testing.T) {
			rec := doGet(t, h, "/library"+c.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			present, absent := titlesIn(rec.Body.String(), "Active Problem", "Paused Problem")
			if fmt.Sprint(present) != fmt.Sprint(c.wantPresent) || (len(c.wantAbsent) > 0 && fmt.Sprint(absent) != fmt.Sprint(c.wantAbsent)) {
				t.Errorf("listed = %v (absent %v), want listed %v absent %v", present, absent, c.wantPresent, c.wantAbsent)
			}
		})
	}
}

// Prev/Next links and the page-size form are built from an explicit list
// of params; if status weren't in it, clicking Next would silently drop
// back to the Active view.
func TestLibrary_StatusSurvivesPagination(t *testing.T) {
	h, svc := pauseTestServer(t)
	var ids []int64
	for i := 0; i < 12; i++ {
		ids = append(ids, addSeedProblem(t, svc, fmt.Sprintf("Problem %02d", i), fmt.Sprintf("problem-%02d", i)))
	}
	if _, err := svc.PauseProblems(t.Context(), ids); err != nil {
		t.Fatalf("PauseProblems: unexpected err: %v", err)
	}

	for _, status := range []string{"paused", "all"} {
		t.Run(status, func(t *testing.T) {
			body := doGet(t, h, "/library?status="+status+"&page_size=10").Body.String()
			if !strings.Contains(body, "status="+status) {
				t.Errorf("Next link / page-size form lost status=%s:\n%s", status, body)
			}
			if !strings.Contains(body, `name="status" value="`+status+`"`) {
				t.Errorf("page-size form has no hidden status=%s field", status)
			}
		})
	}

	// The default view doesn't add a status param at all.
	if body := doGet(t, h, "/library").Body.String(); strings.Contains(body, "status=") {
		t.Errorf("default Active view leaked a status param into its links")
	}
}

// --- empty-state messages when everything due is paused ---

func TestHomeAndDue_EmptyStateMentionsPausedProblems(t *testing.T) {
	h, svc := pauseTestServer(t)
	a := addSeedProblem(t, svc, "A", "a")
	b := addSeedProblem(t, svc, "B", "b")
	// Both are overdue, then paused: nothing is due, but two are paused.
	for _, id := range []int64{a, b} {
		if _, err := svc.RecordReview(t.Context(), id, "Good", svc.Now().AddDate(0, 0, -30)); err != nil {
			t.Fatalf("RecordReview: unexpected err: %v", err)
		}
	}
	if _, err := svc.PauseProblems(t.Context(), []int64{a, b}); err != nil {
		t.Fatalf("PauseProblems: unexpected err: %v", err)
	}

	for _, path := range []string{"/", "/due"} {
		t.Run(path, func(t *testing.T) {
			body := doGet(t, h, path).Body.String()
			if !strings.Contains(body, "Nothing due right now") {
				t.Fatalf("expected the nothing-due message:\n%s", body)
			}
			if !strings.Contains(body, "2 problems currently paused") {
				t.Errorf("empty state doesn't mention the 2 paused problems:\n%s", body)
			}
			if !strings.Contains(body, "/library?status=paused") {
				t.Errorf("empty state doesn't link to the paused view")
			}
		})
	}
}

func TestHomeAndDue_EmptyStateHasNoPausedNoteWhenNothingIsPaused(t *testing.T) {
	h, _ := pauseTestServer(t)
	for _, path := range []string{"/", "/due"} {
		if body := doGet(t, h, path).Body.String(); strings.Contains(body, "currently paused") {
			t.Errorf("%s mentions paused problems when none exist", path)
		}
	}
}

func TestNothingDueMessageFor_Pluralizes(t *testing.T) {
	if got := string(nothingDueMessageFor(0)); got != nothingDueMessage {
		t.Errorf("0 paused = %q, want the plain message", got)
	}
	if got := string(nothingDueMessageFor(1)); !strings.Contains(got, "1 problem currently paused") {
		t.Errorf("1 paused = %q, want singular wording", got)
	}
	if got := string(nothingDueMessageFor(5)); !strings.Contains(got, "5 problems currently paused") {
		t.Errorf("5 paused = %q, want plural wording", got)
	}
}

// --- templates (Step 6) ---

// bulkFormHTML returns the markup of the Library's bulk pause/unpause form.
func bulkFormHTML(t *testing.T, body string) string {
	t.Helper()
	const open = `<form method="post" action="/problems/bulk-status"`
	start := strings.Index(body, open)
	if start < 0 {
		t.Fatalf("Library has no bulk pause/unpause form:\n%s", body)
	}
	end := strings.Index(body[start:], "</form>")
	if end < 0 {
		t.Fatal("bulk form is never closed")
	}
	return body[start : start+end]
}

func TestLibrary_BulkFormHasCheckboxesAndButtons(t *testing.T) {
	h, svc := pauseTestServer(t)
	a := addSeedProblem(t, svc, "Two Sum", "two-sum")
	b := addSeedProblem(t, svc, "Valid Anagram", "valid-anagram")

	form := bulkFormHTML(t, doGet(t, h, "/library?sort=title&page_size=25").Body.String())

	for _, id := range []int64{a, b} {
		want := fmt.Sprintf(`name="id" value="%d"`, id)
		if !strings.Contains(form, want) {
			t.Errorf("no row checkbox %s in the bulk form", want)
		}
	}
	for _, want := range []string{
		`aria-label="Select Two Sum"`,
		`aria-label="Select all on this page"`,
		`name="action" value="pause"`,
		`name="action" value="unpause"`,
		`name="target_per_day"`,
		`value="5"`,
		`name="sort" value="title"`,
		`name="page_size" value="25"`,
	} {
		if !strings.Contains(form, want) {
			t.Errorf("bulk form is missing %s:\n%s", want, form)
		}
	}
	// HTML forbids nested forms; a nested <form> would be silently dropped.
	if strings.Contains(form[len("<form"):], "<form") {
		t.Error("the bulk form contains another <form>")
	}
}

// Enter in the number box would submit the form using its FIRST submit
// button (Pause) when the user meant Unpause.
func TestLibrary_TargetPerDayBlocksEnterKey(t *testing.T) {
	h, svc := pauseTestServer(t)
	addSeedProblem(t, svc, "Two Sum", "two-sum")

	form := bulkFormHTML(t, doGet(t, h, "/library").Body.String())
	i := strings.Index(form, `name="target_per_day"`)
	if i < 0 {
		t.Fatal("no target_per_day input")
	}
	tag := form[strings.LastIndex(form[:i], "<input"):]
	tag = tag[:strings.Index(tag, ">")]
	if !strings.Contains(tag, "onkeydown") || !strings.Contains(tag, "preventDefault") {
		t.Errorf("target_per_day input doesn't block Enter: %s", tag)
	}
}

func TestLibrary_BulkFormCarriesCurrentFilters(t *testing.T) {
	h, svc := pauseTestServer(t)
	addSeedProblem(t, svc, "Two Sum", "two-sum")

	form := bulkFormHTML(t, doGet(t, h, "/library?status=all&q=two&difficulty=Easy&page_size=25").Body.String())
	for _, want := range []string{
		`name="status" value="all"`, `name="q" value="two"`, `name="difficulty" value="Easy"`,
		`name="page_size" value="25"`, `name="page" value="1"`,
	} {
		if !strings.Contains(form, want) {
			t.Errorf("bulk form doesn't carry %s, so the redirect would lose the filter", want)
		}
	}
}

func TestLibrary_StatusSelectReflectsCurrentFilter(t *testing.T) {
	h, svc := pauseTestServer(t)
	addSeedProblem(t, svc, "Two Sum", "two-sum")

	for _, c := range []struct{ query, selected string }{
		{"", "active"}, {"?status=paused", "paused"}, {"?status=all", "all"}, {"?status=bogus", "active"},
	} {
		body := doGet(t, h, "/library"+c.query).Body.String()
		if !strings.Contains(body, `<option value="`+c.selected+`" selected>`) {
			t.Errorf("query %q: status option %q not selected:\n%s", c.query, c.selected, body)
		}
		for _, v := range []string{"active", "paused", "all"} {
			if !strings.Contains(body, `<option value="`+v+`"`) {
				t.Errorf("status select is missing option %q", v)
			}
		}
	}
}

func TestLibrary_PausedRowsAreMarkedUnderAll(t *testing.T) {
	h, svc := pauseTestServer(t)
	addSeedProblem(t, svc, "Active Problem", "active-problem")
	paused := addSeedProblem(t, svc, "Paused Problem", "paused-problem")
	if _, err := svc.PauseProblems(t.Context(), []int64{paused}); err != nil {
		t.Fatalf("PauseProblems: unexpected err: %v", err)
	}

	body := doGet(t, h, "/library?status=all").Body.String()
	if !strings.Contains(body, `class="row-paused"`) {
		t.Errorf("paused row isn't styled:\n%s", body)
	}
	if got := strings.Count(body, `class="badge-paused"`); got != 1 {
		t.Errorf("Paused badges = %d, want 1 (only the paused problem)", got)
	}
	if !strings.Contains(body, `class="status-paused">Paused</span>`) {
		t.Errorf("Next Review column doesn't say Paused:\n%s", body)
	}
	if !strings.Contains(body, "since Jan 10") {
		t.Errorf("Paused row doesn't say when it was paused:\n%s", body)
	}
	// The paused row must not show a stale overdue/due status.
	if strings.Count(body, `class="status-upcoming"`)+strings.Count(body, `class="status-overdue"`)+strings.Count(body, `class="status-due-today"`) != 1 {
		t.Errorf("expected exactly one dated status (the active problem's)")
	}
	// Paused rows sort after active ones.
	if strings.Index(body, "Active Problem") > strings.Index(body, "Paused Problem") {
		t.Error("paused row is listed before the active one")
	}
}

func TestLibrary_ShowsNoticeAfterBulkAction(t *testing.T) {
	h, svc := pauseTestServer(t)
	addSeedProblem(t, svc, "Two Sum", "two-sum")

	body := doGet(t, h, "/library?done=paused&n=3").Body.String()
	if !strings.Contains(body, `role="status"`) || !strings.Contains(body, "Paused 3 problems.") {
		t.Errorf("notice missing:\n%s", body)
	}
	if body := doGet(t, h, "/library").Body.String(); strings.Contains(body, `role="status"`) {
		t.Error("a notice is shown when there is no bulk action to report")
	}
	if body := doGet(t, h, "/library?done=<script>&n=1").Body.String(); strings.Contains(body, "<script>&") {
		t.Error("unrecognised done value leaked into the page")
	}
}

func TestPausedStatCardOnLibraryAndHome(t *testing.T) {
	h, svc := pauseTestServer(t)
	a := addSeedProblem(t, svc, "A", "a")
	b := addSeedProblem(t, svc, "B", "b")
	addSeedProblem(t, svc, "C", "c")
	if _, err := svc.PauseProblems(t.Context(), []int64{a, b}); err != nil {
		t.Fatalf("PauseProblems: unexpected err: %v", err)
	}

	for _, path := range []string{"/library", "/"} {
		body := doGet(t, h, path).Body.String()
		i := strings.Index(body, `<span class="meta">Paused</span>`)
		if i < 0 {
			t.Errorf("%s has no Paused stat card", path)
			continue
		}
		if !strings.Contains(body[i:i+120], `>2</div>`) {
			t.Errorf("%s Paused card doesn't show 2: %s", path, body[i:i+120])
		}
		// Total Tracked is solve history and ignores pause state.
		if !strings.Contains(body, `<div class="stat-number">3</div>`) {
			t.Errorf("%s Total Tracked should still be 3", path)
		}
	}
}

func TestTopicsHeaderSaysProblems(t *testing.T) {
	h, _ := pauseTestServer(t)
	body := doGet(t, h, "/topics").Body.String()
	if strings.Contains(body, "Active Problems") {
		t.Error("Topics still says \"Active Problems\", but its counts include paused problems")
	}
	if !strings.Contains(body, "<th>Problems</th>") {
		t.Error("Topics header \"Problems\" missing")
	}
}

// The Filter button submits a plain GET form. If the form doesn't carry the
// current page size, clicking Filter silently resets it to the default.
func TestLibrary_FilterFormCarriesPageSize(t *testing.T) {
	h, svc := pauseTestServer(t)
	addSeedProblem(t, svc, "Two Sum", "two-sum")

	for _, size := range []string{"10", "25", "50"} {
		body := doGet(t, h, "/library?page_size="+size).Body.String()
		start := strings.Index(body, `<form method="get" action="/library" class="library-toolbar">`)
		if start < 0 {
			t.Fatal("Library has no filter toolbar form")
		}
		form := body[start : start+strings.Index(body[start:], "</form>")]
		if !strings.Contains(form, `name="page_size" value="`+size+`"`) {
			t.Errorf("page_size=%s: the Filter form drops the page size:\n%s", size, form)
		}
	}
}
