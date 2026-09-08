package service

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestBulkImportProblems_CreatesGradesHardAndStaggersReviewDates(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	now := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	fixedClock(s, now)

	rows := []BulkImportRow{
		{Title: "Two Sum", URL: "two-sum", Difficulty: DifficultyEasy, Topics: []string{"Arrays"}},
		{Title: "Valid Anagram", URL: "valid-anagram", Difficulty: DifficultyEasy, Topics: []string{"Hash Table"}},
		{Title: "Group Anagrams", URL: "group-anagrams", Difficulty: DifficultyMedium, Topics: []string{"Arrays", "Hash Table"}},
	}

	result, err := s.BulkImportProblems(ctx, rows, 0)
	if err != nil {
		t.Fatalf("BulkImportProblems: unexpected err: %v", err)
	}
	if result.Created != 3 || result.Merged != 0 {
		t.Fatalf("result = %+v, want Created=3 Merged=0", result)
	}

	problems, err := s.ListProblems(ctx, ListProblemsFilter{})
	if err != nil {
		t.Fatalf("ListProblems: unexpected err: %v", err)
	}
	if len(problems) != 3 {
		t.Fatalf("len(problems) = %d, want 3", len(problems))
	}

	seen := map[string]bool{}
	for _, p := range problems {
		var id int64 = p.ID
		var easeFactor float64
		var intervalDays, repetitions int
		var nextReviewDate, lastGrade string
		row := s.db.QueryRowContext(ctx, `SELECT ease_factor, interval_days, repetitions, next_review_date, last_grade FROM review_state WHERE problem_id = ?`, id)
		if err := row.Scan(&easeFactor, &intervalDays, &repetitions, &nextReviewDate, &lastGrade); err != nil {
			t.Fatalf("scan review_state for %q: %v", p.Slug, err)
		}
		if lastGrade != "Hard" {
			t.Errorf("problem %q graded %q, want Hard (SPEC.md §9 baseline)", p.Slug, lastGrade)
		}
		if seen[nextReviewDate] {
			t.Errorf("next_review_date %q reused across rows — dates should be staggered, not identical", nextReviewDate)
		}
		seen[nextReviewDate] = true
	}
}

func TestBulkImportProblems_ReimportMergesRatherThanDuplicates(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	fixedClock(s, time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))

	rows := []BulkImportRow{
		{Title: "Two Sum", URL: "two-sum", Difficulty: DifficultyEasy, Topics: []string{"Arrays"}},
	}
	if _, err := s.BulkImportProblems(ctx, rows, 0); err != nil {
		t.Fatalf("first import: unexpected err: %v", err)
	}

	result, err := s.BulkImportProblems(ctx, rows, 0)
	if err != nil {
		t.Fatalf("second import: unexpected err: %v", err)
	}
	if result.Created != 0 || result.Merged != 1 {
		t.Fatalf("result = %+v, want Created=0 Merged=1 (dedupe on slug)", result)
	}

	problems, err := s.ListProblems(ctx, ListProblemsFilter{})
	if err != nil {
		t.Fatalf("ListProblems: unexpected err: %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("len(problems) = %d, want 1 (re-import should not duplicate)", len(problems))
	}
}

// TestBulkImportProblems_ProtectsProgressOnThirdImport asserts the
// overwrite-protection guard: a problem re-imported a second time (still
// only its own original attempt behind it) is safely refreshed, but a
// *third* import — now that it has real history beyond its creation —
// is left completely untouched rather than reset back to a fresh Hard
// grade.
func TestBulkImportProblems_ProtectsProgressOnThirdImport(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	fixedClock(s, time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))

	rows := []BulkImportRow{
		{Title: "Two Sum", URL: "two-sum", Difficulty: DifficultyEasy, Topics: []string{"Arrays"}},
	}

	if _, err := s.BulkImportProblems(ctx, rows, 0); err != nil {
		t.Fatalf("first import: unexpected err: %v", err)
	}
	second, err := s.BulkImportProblems(ctx, rows, 0)
	if err != nil {
		t.Fatalf("second import: unexpected err: %v", err)
	}
	if second.Merged != 1 || second.Skipped != 0 {
		t.Fatalf("second import result = %+v, want Merged=1 Skipped=0 (only its own original attempt so far)", second)
	}

	problems, err := s.ListProblems(ctx, ListProblemsFilter{})
	if err != nil {
		t.Fatalf("ListProblems: unexpected err: %v", err)
	}
	var problemID int64
	for _, p := range problems {
		problemID = p.ID
	}
	var beforeInterval int
	var beforeNextDate string
	if err := s.db.QueryRowContext(ctx, `SELECT interval_days, next_review_date FROM review_state WHERE problem_id = ?`, problemID).
		Scan(&beforeInterval, &beforeNextDate); err != nil {
		t.Fatalf("scan review_state after second import: %v", err)
	}

	third, err := s.BulkImportProblems(ctx, rows, 0)
	if err != nil {
		t.Fatalf("third import: unexpected err: %v", err)
	}
	if third.Merged != 0 || third.Skipped != 1 {
		t.Fatalf("third import result = %+v, want Merged=0 Skipped=1 (has real progress now, must be protected)", third)
	}

	var afterInterval int
	var afterNextDate string
	if err := s.db.QueryRowContext(ctx, `SELECT interval_days, next_review_date FROM review_state WHERE problem_id = ?`, problemID).
		Scan(&afterInterval, &afterNextDate); err != nil {
		t.Fatalf("scan review_state after third import: %v", err)
	}
	if afterInterval != beforeInterval || afterNextDate != beforeNextDate {
		t.Errorf("review_state changed after a protected import: before (interval=%d, next=%q), after (interval=%d, next=%q)",
			beforeInterval, beforeNextDate, afterInterval, afterNextDate)
	}
}

// TestBulkImportProblems_ProtectsManuallyGradedProblem asserts that a
// problem graded again through the normal UI flow (RecordReview, as the
// due page's grade buttons do) after its original import is protected
// from a later bulk re-import — the guard isn't specific to repeated
// bulk imports, it protects any real review history.
func TestBulkImportProblems_ProtectsManuallyGradedProblem(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	fixedClock(s, time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))

	rows := []BulkImportRow{
		{Title: "Two Sum", URL: "two-sum", Difficulty: DifficultyEasy, Topics: []string{"Arrays"}},
	}
	if _, err := s.BulkImportProblems(ctx, rows, 0); err != nil {
		t.Fatalf("import: unexpected err: %v", err)
	}

	problems, err := s.ListProblems(ctx, ListProblemsFilter{})
	if err != nil {
		t.Fatalf("ListProblems: unexpected err: %v", err)
	}
	problemID := problems[0].ID

	// Simulate the user honestly re-grading it to Good via the due page.
	if _, err := s.RecordReview(ctx, problemID, "Good", s.now()); err != nil {
		t.Fatalf("RecordReview: unexpected err: %v", err)
	}
	var goodInterval int
	if err := s.db.QueryRowContext(ctx, `SELECT interval_days FROM review_state WHERE problem_id = ?`, problemID).Scan(&goodInterval); err != nil {
		t.Fatalf("scan interval after manual grade: %v", err)
	}

	result, err := s.BulkImportProblems(ctx, rows, 0)
	if err != nil {
		t.Fatalf("re-import: unexpected err: %v", err)
	}
	if result.Skipped != 1 || result.Merged != 0 {
		t.Fatalf("re-import result = %+v, want Skipped=1 Merged=0", result)
	}

	var intervalAfter int
	if err := s.db.QueryRowContext(ctx, `SELECT interval_days FROM review_state WHERE problem_id = ?`, problemID).Scan(&intervalAfter); err != nil {
		t.Fatalf("scan interval after re-import: %v", err)
	}
	if intervalAfter != goodInterval {
		t.Errorf("interval_days = %d after re-import, want unchanged %d (Good-grade progress must not be reset to Hard)", intervalAfter, goodInterval)
	}
}

func makeStaggerRows(n int) []BulkImportRow {
	rows := make([]BulkImportRow, n)
	for i := range rows {
		rows[i] = BulkImportRow{
			Title:      "Problem",
			URL:        fmt.Sprintf("problem-%d", i),
			Difficulty: DifficultyEasy,
			Topics:     []string{"Arrays"},
		}
	}
	return rows
}

func spreadDays(t *testing.T, ctx context.Context, s *Service) float64 {
	t.Helper()
	var minDate, maxDate string
	row := s.db.QueryRowContext(ctx, `SELECT MIN(next_review_date), MAX(next_review_date) FROM review_state`)
	if err := row.Scan(&minDate, &maxDate); err != nil {
		t.Fatalf("scan min/max: %v", err)
	}
	min, _ := time.Parse("2006-01-02", minDate)
	max, _ := time.Parse("2006-01-02", maxDate)
	return max.Sub(min).Hours() / 24
}

// TestBulkImportProblems_StaggerScalesWithBatchSizeAboveFloor asserts the
// fix for the real-world case that motivated it: a batch large enough
// that the old hardcoded 14-day window packed too many reviews onto each
// day (192 problems / 14 days ≈ 14/day). With the default target of 5/day,
// 100 rows should spread across ceil(100/5)=20 days, not be capped at 14.
func TestBulkImportProblems_StaggerScalesWithBatchSizeAboveFloor(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	fixedClock(s, time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))

	rows := makeStaggerRows(100)
	if _, err := s.BulkImportProblems(ctx, rows, 0); err != nil {
		t.Fatalf("BulkImportProblems: unexpected err: %v", err)
	}

	spread := spreadDays(t, ctx, s)
	if spread < 18 || spread > 20 {
		t.Errorf("spread = %v days, want ~19-20 (ceil(100/5)=20 days at the default 5/day target)", spread)
	}
}

// TestBulkImportProblems_StaggerRespectsExplicitTarget asserts a caller
// can tune pacing per import: a tighter target (8/day) should produce a
// visibly narrower spread than a gentler one (3/day) for the same batch.
func TestBulkImportProblems_StaggerRespectsExplicitTarget(t *testing.T) {
	tight := newTestService(t)
	gentle := newTestService(t)
	fixedClock(tight, time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))
	fixedClock(gentle, time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))
	ctx := context.Background()

	const n = 100
	if _, err := tight.BulkImportProblems(ctx, makeStaggerRows(n), 8); err != nil {
		t.Fatalf("tight import: unexpected err: %v", err)
	}
	if _, err := gentle.BulkImportProblems(ctx, makeStaggerRows(n), 3); err != nil {
		t.Fatalf("gentle import: unexpected err: %v", err)
	}

	tightSpread := spreadDays(t, ctx, tight)
	gentleSpread := spreadDays(t, ctx, gentle)

	wantTight := float64(staggerWindowDays(n, 8) - 1)
	wantGentle := float64(staggerWindowDays(n, 3) - 1)
	if tightSpread != wantTight {
		t.Errorf("tight (target=8) spread = %v, want %v", tightSpread, wantTight)
	}
	if gentleSpread != wantGentle {
		t.Errorf("gentle (target=3) spread = %v, want %v", gentleSpread, wantGentle)
	}
	if gentleSpread <= tightSpread {
		t.Errorf("gentle spread (%v) should be wider than tight spread (%v)", gentleSpread, tightSpread)
	}
}

func TestBulkImportProblems_StaggerSpreadsAcrossTwoWeeks(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	fixedClock(s, time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))

	const n = 30
	rows := make([]BulkImportRow, n)
	for i := range rows {
		rows[i] = BulkImportRow{
			Title:      "Problem",
			URL:        "problem-" + string(rune('a'+i)),
			Difficulty: DifficultyEasy,
			Topics:     []string{"Arrays"},
		}
	}

	if _, err := s.BulkImportProblems(ctx, rows, 0); err != nil {
		t.Fatalf("BulkImportProblems: unexpected err: %v", err)
	}

	var minDate, maxDate string
	row := s.db.QueryRowContext(ctx, `SELECT MIN(next_review_date), MAX(next_review_date) FROM review_state`)
	if err := row.Scan(&minDate, &maxDate); err != nil {
		t.Fatalf("scan min/max: %v", err)
	}
	min, _ := time.Parse("2006-01-02", minDate)
	max, _ := time.Parse("2006-01-02", maxDate)
	spread := max.Sub(min).Hours() / 24
	if spread < 10 || spread > 14 {
		t.Errorf("spread = %v days, want roughly 10-14 (SPEC.md §9: stagger across ~2 weeks)", spread)
	}
}
