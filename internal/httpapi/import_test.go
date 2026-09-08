package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/service"
)

func TestParseImportRows(t *testing.T) {
	csv := "title,url,difficulty,topics\n" +
		"Two Sum,https://leetcode.com/problems/two-sum/,Easy,Arrays;Hash Table\n" +
		"Bad Difficulty,https://leetcode.com/problems/bad-difficulty/,Extreme,Arrays\n" +
		"No Topics,https://leetcode.com/problems/no-topics/,Easy,\n" +
		",https://leetcode.com/problems/blank-title/,Easy,Arrays\n" +
		"Dup,https://leetcode.com/problems/two-sum/,Easy,Arrays\n"

	rows, err := parseImportRows(csv)
	if err != nil {
		t.Fatalf("parseImportRows: unexpected err: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("len(rows) = %d, want 5", len(rows))
	}

	if rows[0].Status != "new" {
		t.Errorf("row 0 status = %q, want new (issue: %s)", rows[0].Status, rows[0].Issue)
	}
	if len(rows[0].Topics) != 2 || rows[0].Topics[0] != "Arrays" || rows[0].Topics[1] != "Hash Table" {
		t.Errorf("row 0 topics = %v, want [Arrays, Hash Table]", rows[0].Topics)
	}
	if rows[1].Status != "error" {
		t.Errorf("row 1 (bad difficulty) status = %q, want error", rows[1].Status)
	}
	if rows[2].Status != "error" {
		t.Errorf("row 2 (no topics) status = %q, want error", rows[2].Status)
	}
	if rows[3].Status != "error" {
		t.Errorf("row 3 (blank title) status = %q, want error", rows[3].Status)
	}
	if rows[4].Status != "error" || !strings.Contains(rows[4].Issue, "duplicate") {
		t.Errorf("row 4 (duplicate slug) status = %q issue = %q, want error mentioning duplicate", rows[4].Status, rows[4].Issue)
	}
}

func TestParseImportRows_MalformedColumnCount(t *testing.T) {
	rows, err := parseImportRows("title,url,difficulty,topics\nToo,Few,Columns\n")
	if err != nil {
		t.Fatalf("parseImportRows: unexpected err: %v", err)
	}
	if len(rows) != 1 || rows[0].Status != "error" {
		t.Fatalf("rows = %+v, want a single error row for the malformed line", rows)
	}
}

func TestImportPreviewThenCommit_FullFlow(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	csv := "title,url,difficulty,topics\n" +
		"Two Sum,https://leetcode.com/problems/two-sum/,Easy,Arrays\n" +
		"Valid Anagram,https://leetcode.com/problems/valid-anagram/,Easy,Hash Table\n"

	previewRec := doForm(t, h, http.MethodPost, "/problems/import/preview", url.Values{"csv": {csv}})
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200:\n%s", previewRec.Code, previewRec.Body.String())
	}
	previewBody := previewRec.Body.String()
	if !strings.Contains(previewBody, "Confirm Import") {
		t.Fatalf("preview missing Confirm Import button (should have zero errors):\n%s", previewBody)
	}

	commitRec := doForm(t, h, http.MethodPost, "/problems/import/commit", url.Values{"csv": {csv}})
	if commitRec.Code != http.StatusOK {
		t.Fatalf("commit status = %d, want 200:\n%s", commitRec.Code, commitRec.Body.String())
	}
	commitBody := commitRec.Body.String()
	if !strings.Contains(commitBody, "2 problems added") {
		t.Errorf("commit result missing expected count:\n%s", commitBody)
	}

	problems, err := svc.ListProblems(ctx, service.ListProblemsFilter{})
	if err != nil {
		t.Fatalf("ListProblems: unexpected err: %v", err)
	}
	if len(problems) != 2 {
		t.Fatalf("len(problems) = %d, want 2", len(problems))
	}
}

// TestImportCommit_CustomTargetPerDayReachesService confirms a
// user-submitted target_per_day actually changes the resulting stagger,
// not just the default — i.e. it reaches Service.BulkImportProblems
// rather than being silently ignored between form and service call. 100
// rows at target=2 forces the window well past the 14-day floor
// (ceil(100/2)=50), so a wrong/ignored target would be caught by the
// narrower default-driven spread instead.
func TestImportCommit_CustomTargetPerDayReachesService(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	var csv strings.Builder
	csv.WriteString("title,url,difficulty,topics\n")
	for i := 0; i < 100; i++ {
		csv.WriteString(fmt.Sprintf("Problem %d,https://leetcode.com/problems/problem-%d/,Easy,Arrays\n", i, i))
	}

	commitRec := doForm(t, h, http.MethodPost, "/problems/import/commit", url.Values{
		"csv":            {csv.String()},
		"target_per_day": {"2"},
	})
	if commitRec.Code != http.StatusOK {
		t.Fatalf("commit status = %d, want 200:\n%s", commitRec.Code, commitRec.Body.String())
	}

	items, _, err := svc.ListLibrary(ctx, service.ListProblemsFilter{Limit: 200})
	if err != nil {
		t.Fatalf("ListLibrary: unexpected err: %v", err)
	}
	if len(items) != 100 {
		t.Fatalf("len(items) = %d, want 100", len(items))
	}
	min, max := items[0].NextReviewDate, items[0].NextReviewDate
	for _, item := range items {
		if item.NextReviewDate.Before(min) {
			min = item.NextReviewDate
		}
		if item.NextReviewDate.After(max) {
			max = item.NextReviewDate
		}
	}
	spread := max.Sub(min).Hours() / 24
	if spread < 45 {
		t.Errorf("spread = %v days, want >= 45 (ceil(100/2)=50 days at target_per_day=2 — a narrower spread suggests the custom target was ignored)", spread)
	}
}

// TestImportPreview_FlagsProtectedRow asserts the preview table warns
// about a row that would otherwise silently reset real review progress
// (internal/service.TestBulkImportProblems_ProtectsProgressOnThirdImport
// covers the actual protection; this covers that the preview correctly
// predicts it before commit).
func TestImportPreview_FlagsProtectedRow(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	csv := "title,url,difficulty,topics\n" +
		"Two Sum,https://leetcode.com/problems/two-sum/,Easy,Arrays\n"

	commitRec := doForm(t, h, http.MethodPost, "/problems/import/commit", url.Values{"csv": {csv}})
	if commitRec.Code != http.StatusOK {
		t.Fatalf("setup commit status = %d, want 200:\n%s", commitRec.Code, commitRec.Body.String())
	}

	problems, err := svc.ListProblems(ctx, service.ListProblemsFilter{})
	if err != nil {
		t.Fatalf("ListProblems: unexpected err: %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("setup: len(problems) = %d, want 1", len(problems))
	}
	problemID := problems[0].ID

	// Give it real progress beyond its own import (a manual re-grade).
	if _, err := svc.RecordReview(ctx, problemID, "Good", time.Now()); err != nil {
		t.Fatalf("RecordReview: unexpected err: %v", err)
	}

	previewRec := doForm(t, h, http.MethodPost, "/problems/import/preview", url.Values{"csv": {csv}})
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200:\n%s", previewRec.Code, previewRec.Body.String())
	}
	body := previewRec.Body.String()
	if !strings.Contains(body, "import-status-protected") {
		t.Errorf("preview missing protected-row warning:\n%s", body)
	}
	if !strings.Contains(body, "Confirm Import") {
		t.Errorf("preview should still allow confirming (protected rows aren't errors):\n%s", body)
	}
}

func TestImportCommit_RejectsBatchWithErrors(t *testing.T) {
	h, svc := newTestServer(t)
	ctx := t.Context()

	csv := "title,url,difficulty,topics\n" +
		"Bad Row,https://leetcode.com/problems/bad-row/,NotADifficulty,Arrays\n"

	rec := doForm(t, h, http.MethodPost, "/problems/import/commit", url.Values{"csv": {csv}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("commit status = %d, want 422:\n%s", rec.Code, rec.Body.String())
	}

	problems, err := svc.ListProblems(ctx, service.ListProblemsFilter{})
	if err != nil {
		t.Fatalf("ListProblems: unexpected err: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("len(problems) = %d, want 0 (a batch with errors must not commit anything)", len(problems))
	}
}
