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

	problems, _, err := s.ListLibrary(ctx, ListProblemsFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListLibrary: unexpected err: %v", err)
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

	problems, _, err := s.ListLibrary(ctx, ListProblemsFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListLibrary: unexpected err: %v", err)
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

// TestStaggerWindowDays is a direct, pure unit test of the window-sizing
// formula (ceiling division, floored at minStaggerDays) — previously the
// only test exercising this arithmetic computed its own expected value
// by calling staggerWindowDays itself, which can't fail no matter what
// the formula does. Hardcoded expectations here so a real regression
// (e.g. a floor/ceiling swap, or an off-by-one in the division) shows up.
func TestStaggerWindowDays(t *testing.T) {
	cases := []struct {
		name         string
		n            int
		targetPerDay int
		minDays      int
		want         int
	}{
		{"zero_target_falls_back_to_default_above_floor", 100, 0, minStaggerDays, 20}, // ceil(100/5)=20
		{"negative_target_falls_back_to_default", 100, -3, minStaggerDays, 20},        // same fallback as zero
		{"small_batch_floors_at_minStaggerDays", 3, 5, minStaggerDays, 14},            // ceil(3/5)=1, floored to 14
		{"computed_window_below_floor_gets_floored", 100, 8, minStaggerDays, 14},      // ceil(100/8)=13, floored to 14
		{"tighter_target_scales_above_floor", 100, 3, minStaggerDays, 34},             // ceil(100/3)=34
		{"exact_division_above_floor", 100, 5, minStaggerDays, 20},                    // ceil(100/5)=20 exactly
		// Unpause uses a 1-day floor: a small batch isn't spread over two weeks.
		{"unpause_small_batch_is_one_day", 3, 5, 1, 1},  // ceil(3/5)=1
		{"unpause_twenty_at_five_per_day", 20, 5, 1, 4}, // ceil(20/5)=4
		{"unpause_seven_at_five_per_day", 7, 5, 1, 2},   // ceil(7/5)=2
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := staggerWindowDays(c.n, c.targetPerDay, c.minDays); got != c.want {
				t.Errorf("staggerWindowDays(%d, %d, %d) = %d, want %d", c.n, c.targetPerDay, c.minDays, got, c.want)
			}
		})
	}
}

// TestBulkImportProblems_Stagger is the integration counterpart to
// TestStaggerWindowDays: it proves BulkImportProblems actually applies
// whatever staggerWindowDays computes to the rows it writes, across both
// regimes that formula produces — floored (a batch small enough that the
// target-driven window would be under two weeks) and scaled (a batch
// large enough to exceed the floor) — plus that a tighter explicit
// target measurably narrows the spread.
func TestBulkImportProblems_Stagger(t *testing.T) {
	t.Run("small batch floors at two weeks", func(t *testing.T) {
		s := newTestService(t)
		ctx := t.Context()
		fixedClock(s, time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))

		if _, err := s.BulkImportProblems(ctx, makeStaggerRows(30), 0); err != nil {
			t.Fatalf("BulkImportProblems: unexpected err: %v", err)
		}
		spread := spreadDays(t, ctx, s)
		if spread < 10 || spread > 14 {
			t.Errorf("spread = %v days, want roughly 10-14 (SPEC.md §9: stagger across ~2 weeks)", spread)
		}
	})

	t.Run("large batch scales with target", func(t *testing.T) {
		s := newTestService(t)
		ctx := t.Context()
		fixedClock(s, time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))

		if _, err := s.BulkImportProblems(ctx, makeStaggerRows(100), 0); err != nil {
			t.Fatalf("BulkImportProblems: unexpected err: %v", err)
		}
		spread := spreadDays(t, ctx, s)
		if spread < 18 || spread > 20 {
			t.Errorf("spread = %v days, want ~19-20 (ceil(100/5)=20 days at the default 5/day target)", spread)
		}
	})

	t.Run("explicit target narrows or widens spread", func(t *testing.T) {
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
		if gentleSpread <= tightSpread {
			t.Errorf("gentle spread (%v, target=3) should be wider than tight spread (%v, target=8) — a looser target_per_day should stagger further, not less", gentleSpread, tightSpread)
		}
	})
}

// TestCheckSlugs_BatchedMixOfNewSafeAndProtected asserts CheckSlugs
// correctly classifies a mix of statuses in a single call — the whole
// point of batching everything into one query is that it must still
// distinguish each slug individually, not just prove "the query didn't
// error."
func TestCheckSlugs_BatchedMixOfNewSafeAndProtected(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	fixedClock(s, time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))

	// "untouched" gets only its own single import attempt: safe to refresh.
	if _, err := s.BulkImportProblems(ctx, []BulkImportRow{
		{Title: "Untouched", URL: "untouched", Difficulty: DifficultyEasy, Topics: []string{"Arrays"}},
	}, 0); err != nil {
		t.Fatalf("import untouched: unexpected err: %v", err)
	}

	// "progressed" gets a second, real attempt beyond its import: protected.
	if _, err := s.BulkImportProblems(ctx, []BulkImportRow{
		{Title: "Progressed", URL: "progressed", Difficulty: DifficultyEasy, Topics: []string{"Arrays"}},
	}, 0); err != nil {
		t.Fatalf("import progressed: unexpected err: %v", err)
	}
	progressed, _, err := s.ListLibrary(ctx, ListProblemsFilter{Search: strPtr("Progressed"), Limit: 10})
	if err != nil {
		t.Fatalf("ListLibrary: unexpected err: %v", err)
	}
	if _, err := s.RecordReview(ctx, progressed[0].ID, "Good", s.now()); err != nil {
		t.Fatalf("RecordReview: unexpected err: %v", err)
	}

	statuses, err := s.CheckSlugs(ctx, []string{"untouched", "progressed", "brand-new"})
	if err != nil {
		t.Fatalf("CheckSlugs: unexpected err: %v", err)
	}
	if statuses["untouched"] != SlugSafeToRefresh {
		t.Errorf(`statuses["untouched"] = %v, want SlugSafeToRefresh`, statuses["untouched"])
	}
	if statuses["progressed"] != SlugProtected {
		t.Errorf(`statuses["progressed"] = %v, want SlugProtected`, statuses["progressed"])
	}
	if statuses["brand-new"] != SlugNew {
		t.Errorf(`statuses["brand-new"] = %v, want SlugNew`, statuses["brand-new"])
	}
}

func strPtr(s string) *string { return &s }
