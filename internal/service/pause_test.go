package service

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
)

// pauseFixture seeds problems and pauses them with direct SQL, since the
// pause/unpause service methods are tested separately.
type pauseFixture struct {
	t   *testing.T
	s   *Service
	ctx context.Context
}

func newPauseFixture(t *testing.T) *pauseFixture {
	t.Helper()
	s := newTestService(t)
	// "today" = 2026-01-10 in Asia/Singapore.
	fixedClock(s, time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC))
	return &pauseFixture{t: t, s: s, ctx: context.Background()}
}

func (f *pauseFixture) add(title, slug string, d Difficulty, nextReview string, topics ...string) int64 {
	f.t.Helper()
	p, err := f.s.AddProblem(f.ctx, AddProblemInput{
		Title: title, URL: slug, Difficulty: d, Topics: topics, Grade: scheduler.Good, At: time.Now(),
	})
	if err != nil {
		f.t.Fatalf("AddProblem(%s): unexpected err: %v", title, err)
	}
	if _, err := f.s.db.ExecContext(f.ctx, `UPDATE review_state SET next_review_date = ? WHERE problem_id = ?`, nextReview, p.ID); err != nil {
		f.t.Fatalf("set next_review_date: %v", err)
	}
	return p.ID
}

func (f *pauseFixture) pause(id int64) {
	f.t.Helper()
	pausedAt := time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	if _, err := f.s.db.ExecContext(f.ctx, `UPDATE review_state SET paused_at = ? WHERE problem_id = ?`, pausedAt, id); err != nil {
		f.t.Fatalf("pause: %v", err)
	}
}

// TestPausedProblemsExcludedEverywhereDueWorkIsShownOrCounted guards the
// bug class from FRONTEND.md #23: if any one of these queries misses the
// paused filter, a count disagrees with the list next to it.
func TestPausedProblemsExcludedEverywhereDueWorkIsShownOrCounted(t *testing.T) {
	f := newPauseFixture(t)

	f.add("Overdue Active", "overdue-active", DifficultyEasy, "2026-01-05", "Graphs")
	pausedOverdue := f.add("Overdue Paused", "overdue-paused", DifficultyEasy, "2026-01-06", "Graphs")
	f.add("Today Active", "today-active", DifficultyEasy, "2026-01-10")
	pausedToday := f.add("Today Paused", "today-paused", DifficultyEasy, "2026-01-10")
	f.add("Upcoming Active", "upcoming-active", DifficultyEasy, "2026-01-12")
	pausedUpcoming := f.add("Upcoming Paused", "upcoming-paused", DifficultyEasy, "2026-01-12")
	f.pause(pausedOverdue)
	f.pause(pausedToday)
	f.pause(pausedUpcoming)

	t.Run("RecommendDue", func(t *testing.T) {
		items, total, err := f.s.RecommendDue(f.ctx, nil, 10, 0)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if total != 2 || len(items) != 2 {
			t.Errorf("total=%d len=%d, want 2 and 2 (active overdue + active due today)", total, len(items))
		}
		for _, it := range items {
			if it.Title == "Overdue Paused" || it.Title == "Today Paused" {
				t.Errorf("paused problem %q appeared in the due queue", it.Title)
			}
		}
	})

	t.Run("RecommendDue with topic filter", func(t *testing.T) {
		topic := "Graphs"
		_, total, err := f.s.RecommendDue(f.ctx, &topic, 10, 0)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if total != 1 {
			t.Errorf("total = %d, want 1 (paused Graphs problem excluded)", total)
		}
	})

	t.Run("RecommendUpcoming", func(t *testing.T) {
		items, total, err := f.s.RecommendUpcoming(f.ctx, nil, 7, 10, 0)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if total != 1 || len(items) != 1 || items[0].Title != "Upcoming Active" {
			t.Errorf("got total=%d items=%v, want only \"Upcoming Active\"", total, items)
		}
	})

	t.Run("CountDue", func(t *testing.T) {
		n, err := f.s.CountDue(f.ctx)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if n != 2 {
			t.Errorf("CountDue = %d, want 2", n)
		}
	})

	t.Run("DueStats", func(t *testing.T) {
		due, overdue, err := f.s.DueStats(f.ctx, nil)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if due != 2 || overdue != 1 {
			t.Errorf("DueStats = (due=%d, overdue=%d), want (2, 1)", due, overdue)
		}
	})

	t.Run("UpcomingByDay", func(t *testing.T) {
		days, err := f.s.UpcomingByDay(f.ctx, 5)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		// 2026-01-12 is two days out; only the active problem counts.
		if got := days[1]; got.DaysFromNow != 2 || got.Count != 1 {
			t.Errorf("day +2 = %+v, want Count 1 (paused upcoming excluded)", got)
		}
	})
}

func TestListLibrary_StatusFilter(t *testing.T) {
	f := newPauseFixture(t)

	f.add("Active One", "active-one", DifficultyEasy, "2026-01-11")
	f.add("Active Two", "active-two", DifficultyEasy, "2026-01-12")
	f.pause(f.add("Paused One", "paused-one", DifficultyEasy, "2026-01-13"))

	cases := []struct {
		status    string
		wantTotal int
	}{
		{"", 2}, // default is active
		{"active", 2},
		{"paused", 1},
		{"all", 3},
		{"nonsense", 2}, // unknown values fall back to active
	}
	for _, c := range cases {
		t.Run("status="+c.status, func(t *testing.T) {
			items, total, err := f.s.ListLibrary(f.ctx, ListProblemsFilter{Status: c.status, Limit: 10})
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if total != c.wantTotal || len(items) != c.wantTotal {
				t.Errorf("total=%d len=%d, want %d", total, len(items), c.wantTotal)
			}
		})
	}
}

func TestListLibrary_PausedAtPopulatedOnlyForPausedRows(t *testing.T) {
	f := newPauseFixture(t)
	f.add("Active", "active", DifficultyEasy, "2026-01-11")
	f.pause(f.add("Paused", "paused", DifficultyEasy, "2026-01-12"))

	items, _, err := f.s.ListLibrary(f.ctx, ListProblemsFilter{Status: "all", Limit: 10})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	for _, it := range items {
		switch it.Title {
		case "Active":
			if it.PausedAt != nil {
				t.Errorf("active problem has PausedAt = %v, want nil", it.PausedAt)
			}
		case "Paused":
			want := time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC)
			if it.PausedAt == nil || !it.PausedAt.Equal(want) {
				t.Errorf("paused problem PausedAt = %v, want %v", it.PausedAt, want)
			}
		}
	}
}

// TestListLibrary_PausedRowsSortLastUnderEverySort: the paused problem
// is constructed to come FIRST under each sort on its own merits
// (earliest date, alphabetically first title, easiest difficulty), so it
// only ends up last if the paused-last rule actually applies.
func TestListLibrary_PausedRowsSortLastUnderEverySort(t *testing.T) {
	f := newPauseFixture(t)

	f.pause(f.add("AAA Paused", "aaa-paused", DifficultyEasy, "2026-01-01"))
	f.add("BBB Active", "bbb-active", DifficultyMedium, "2026-01-11")
	f.add("CCC Active", "ccc-active", DifficultyHard, "2026-01-12")

	for _, sort := range []string{"next_review", "title", "difficulty"} {
		t.Run(sort, func(t *testing.T) {
			items, _, err := f.s.ListLibrary(f.ctx, ListProblemsFilter{Status: "all", Sort: sort, Limit: 10})
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(items) != 3 {
				t.Fatalf("len(items) = %d, want 3", len(items))
			}
			if last := items[2]; last.Title != "AAA Paused" {
				t.Errorf("last row = %q, want \"AAA Paused\" (paused rows sort last)", last.Title)
			}
		})
	}
}

func TestCountPaused(t *testing.T) {
	f := newPauseFixture(t)

	if n, err := f.s.CountPaused(f.ctx); err != nil || n != 0 {
		t.Fatalf("CountPaused on empty library = (%d, %v), want (0, nil)", n, err)
	}

	f.add("Active", "active", DifficultyEasy, "2026-01-11")
	f.pause(f.add("Paused A", "paused-a", DifficultyEasy, "2026-01-12"))
	f.pause(f.add("Paused B", "paused-b", DifficultyEasy, "2026-01-13"))

	n, err := f.s.CountPaused(f.ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if n != 2 {
		t.Errorf("CountPaused = %d, want 2", n)
	}
}

// --- write side: PauseProblems / UnpauseProblems ---

func (f *pauseFixture) pausedAt(id int64) *string {
	f.t.Helper()
	var v sql.NullString
	if err := f.s.db.QueryRowContext(f.ctx, `SELECT paused_at FROM review_state WHERE problem_id = ?`, id).Scan(&v); err != nil {
		f.t.Fatalf("read paused_at: %v", err)
	}
	if !v.Valid {
		return nil
	}
	return &v.String
}

func (f *pauseFixture) nextReview(id int64) string {
	f.t.Helper()
	var v string
	if err := f.s.db.QueryRowContext(f.ctx, `SELECT next_review_date FROM review_state WHERE problem_id = ?`, id).Scan(&v); err != nil {
		f.t.Fatalf("read next_review_date: %v", err)
	}
	return v
}

func (f *pauseFixture) mustPause(ids ...int64) int {
	f.t.Helper()
	n, err := f.s.PauseProblems(f.ctx, ids)
	if err != nil {
		f.t.Fatalf("PauseProblems: unexpected err: %v", err)
	}
	return n
}

func (f *pauseFixture) mustUnpause(target int, ids ...int64) int {
	f.t.Helper()
	n, err := f.s.UnpauseProblems(f.ctx, ids, target)
	if err != nil {
		f.t.Fatalf("UnpauseProblems: unexpected err: %v", err)
	}
	return n
}

func TestPauseProblems_PausesAndCountsOnlyNewlyPaused(t *testing.T) {
	f := newPauseFixture(t)
	a := f.add("A", "a", DifficultyEasy, "2026-01-11")
	b := f.add("B", "b", DifficultyEasy, "2026-01-11")
	c := f.add("C", "c", DifficultyEasy, "2026-01-11")

	if n := f.mustPause(a, b); n != 2 {
		t.Errorf("first pause = %d, want 2", n)
	}
	// b is already paused, c is new, 999999 doesn't exist, a is duplicated.
	if n := f.mustPause(b, c, 999999, a, a); n != 1 {
		t.Errorf("mixed pause = %d, want 1 (only c is newly paused)", n)
	}
	for _, id := range []int64{a, b, c} {
		if f.pausedAt(id) == nil {
			t.Errorf("problem %d not paused", id)
		}
	}
}

func TestPauseProblems_EmptySelectionIsNoOp(t *testing.T) {
	f := newPauseFixture(t)
	f.add("A", "a", DifficultyEasy, "2026-01-11")

	n, err := f.s.PauseProblems(f.ctx, nil)
	if err != nil || n != 0 {
		t.Errorf("PauseProblems(nil) = (%d, %v), want (0, nil)", n, err)
	}
	n, err = f.s.UnpauseProblems(f.ctx, []int64{}, 5)
	if err != nil || n != 0 {
		t.Errorf("UnpauseProblems(empty) = (%d, %v), want (0, nil)", n, err)
	}
}

func TestPauseProblems_RepausingKeepsOriginalTimestamp(t *testing.T) {
	f := newPauseFixture(t)
	id := f.add("A", "a", DifficultyEasy, "2026-01-11")

	f.mustPause(id)
	first := *f.pausedAt(id)

	fixedClock(f.s, time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC))
	if n := f.mustPause(id); n != 0 {
		t.Errorf("re-pause counted %d, want 0", n)
	}
	if got := *f.pausedAt(id); got != first {
		t.Errorf("paused_at changed on re-pause: %q -> %q", first, got)
	}
}

func TestUnpauseProblems_ActiveProblemIsUntouched(t *testing.T) {
	f := newPauseFixture(t)
	active := f.add("Active", "active", DifficultyEasy, "2026-02-20")

	if n := f.mustUnpause(5, active); n != 0 {
		t.Errorf("unpausing an active problem counted %d, want 0", n)
	}
	if got := f.nextReview(active); got != "2026-02-20" {
		t.Errorf("active problem's next_review_date = %q, want unchanged 2026-02-20", got)
	}
}

func TestUnpauseProblems_MixedSelectionOnlyTouchesPaused(t *testing.T) {
	f := newPauseFixture(t)
	active := f.add("Active", "active", DifficultyEasy, "2026-02-20")
	paused := f.add("Paused", "paused", DifficultyEasy, "2026-01-05")
	f.mustPause(paused)

	if n := f.mustUnpause(5, active, paused); n != 1 {
		t.Errorf("unpaused = %d, want 1", n)
	}
	if got := f.nextReview(active); got != "2026-02-20" {
		t.Errorf("active problem's date changed to %q", got)
	}
	if f.pausedAt(paused) != nil {
		t.Error("paused problem is still paused")
	}
}

// dateCounts unpauses n problems (all overdue, distinct dates so the
// order is deterministic) and returns how many landed on each date.
func dateCountsAfterUnpause(t *testing.T, n, target int) map[string]int {
	t.Helper()
	f := newPauseFixture(t)
	var ids []int64
	for i := 0; i < n; i++ {
		date := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i).Format("2006-01-02")
		ids = append(ids, f.add(fmt.Sprintf("P%02d", i), fmt.Sprintf("p%02d", i), DifficultyEasy, date))
	}
	f.mustPause(ids...)
	if got := f.mustUnpause(target, ids...); got != n {
		t.Fatalf("unpaused = %d, want %d", got, n)
	}
	counts := map[string]int{}
	for _, id := range ids {
		counts[f.nextReview(id)]++
	}
	return counts
}

func TestUnpauseProblems_StaggersByTarget(t *testing.T) {
	// "today" is 2026-01-10, so the first slot is 2026-01-11.
	cases := []struct {
		name      string
		n, target int
		want      map[string]int
	}{
		{"20 at 5/day -> five on each of +1..+4", 20, 5,
			map[string]int{"2026-01-11": 5, "2026-01-12": 5, "2026-01-13": 5, "2026-01-14": 5}},
		{"3 at 5/day -> all tomorrow (no 14-day floor)", 3, 5,
			map[string]int{"2026-01-11": 3}},
		{"7 at 5/day -> window 2: four then three", 7, 5,
			map[string]int{"2026-01-11": 4, "2026-01-12": 3}},
		{"zero target falls back to the default of 5", 20, 0,
			map[string]int{"2026-01-11": 5, "2026-01-12": 5, "2026-01-13": 5, "2026-01-14": 5}},
		{"negative target falls back to the default of 5", 10, -3,
			map[string]int{"2026-01-11": 5, "2026-01-12": 5}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dateCountsAfterUnpause(t, c.n, c.target)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("dates = %v, want %v", got, c.want)
			}
		})
	}
}

func TestUnpauseProblems_MostOverdueComesBackFirst(t *testing.T) {
	f := newPauseFixture(t)
	older := f.add("Older", "older", DifficultyEasy, "2025-11-01")
	newer := f.add("Newer", "newer", DifficultyEasy, "2025-12-15")
	f.mustPause(older, newer)

	// Pass them in the "wrong" order; the service must sort by overdue-ness.
	f.mustUnpause(1, newer, older)

	if got := f.nextReview(older); got != "2026-01-11" {
		t.Errorf("older = %q, want 2026-01-11 (+1)", got)
	}
	if got := f.nextReview(newer); got != "2026-01-12" {
		t.Errorf("newer = %q, want 2026-01-12 (+2)", got)
	}
}

// TestUnpauseProblems_NeverPullsAReviewEarlier: a short pause must not
// bring a review forward — grading early inflates ease on a false signal.
func TestUnpauseProblems_NeverPullsAReviewEarlier(t *testing.T) {
	f := newPauseFixture(t)
	farFuture := f.add("Short pause", "short-pause", DifficultyEasy, "2026-02-09") // 30 days out
	longAgo := f.add("Long pause", "long-pause", DifficultyEasy, "2026-01-05")     // 5 days overdue
	f.mustPause(farFuture, longAgo)

	f.mustUnpause(5, farFuture, longAgo)

	if got := f.nextReview(farFuture); got != "2026-02-09" {
		t.Errorf("short pause: next_review_date = %q, want original 2026-02-09 kept", got)
	}
	if got := f.nextReview(longAgo); got != "2026-01-11" {
		t.Errorf("long pause: next_review_date = %q, want slot 2026-01-11", got)
	}
}

func TestUnpauseProblems_KeepsSchedulerState(t *testing.T) {
	f := newPauseFixture(t)
	id := f.add("A", "a", DifficultyEasy, "2026-01-05")
	if _, err := f.s.db.ExecContext(f.ctx,
		`UPDATE review_state SET ease_factor = 1.85, interval_days = 27, repetitions = 4, last_grade = 'Hard' WHERE problem_id = ?`, id); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	f.mustPause(id)
	f.mustUnpause(5, id)

	var ease float64
	var interval, reps int
	var grade string
	if err := f.s.db.QueryRowContext(f.ctx,
		`SELECT ease_factor, interval_days, repetitions, last_grade FROM review_state WHERE problem_id = ?`, id).
		Scan(&ease, &interval, &reps, &grade); err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !almostEqual(ease, 1.85) || interval != 27 || reps != 4 || grade != "Hard" {
		t.Errorf("state = (ease %v, interval %d, reps %d, grade %s), want (1.85, 27, 4, Hard) unchanged", ease, interval, reps, grade)
	}
}

// --- grading and re-adding never change pause state ---

func TestRecordReview_OnPausedProblemLogsGradeButStaysPaused(t *testing.T) {
	f := newPauseFixture(t)
	id := f.add("A", "a", DifficultyEasy, "2026-01-05")
	f.mustPause(id)
	before := *f.pausedAt(id)

	if _, err := f.s.RecordReview(f.ctx, id, scheduler.Good, f.s.Now()); err != nil {
		t.Fatalf("RecordReview: unexpected err: %v", err)
	}

	after := f.pausedAt(id)
	if after == nil || *after != before {
		t.Errorf("paused_at = %v after grading, want unchanged %q", after, before)
	}
	var attempts int
	if err := f.s.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM attempts WHERE problem_id = ?`, id).Scan(&attempts); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2 (the grade is still logged)", attempts)
	}
	if got := f.nextReview(id); got == "2026-01-05" {
		t.Error("next_review_date was not updated by the grade")
	}
}

func TestAddProblem_ExistingPausedSlugStaysPausedAndIsNotDuplicated(t *testing.T) {
	f := newPauseFixture(t)
	id := f.add("A", "a", DifficultyEasy, "2026-01-05")
	f.mustPause(id)

	res, err := f.s.AddProblem(f.ctx, AddProblemInput{
		Title: "A again", URL: "https://leetcode.com/problems/a/", Difficulty: DifficultyEasy,
		Grade: scheduler.Easy, At: f.s.Now(),
	})
	if err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}
	if res.Created || res.ID != id {
		t.Errorf("result = %+v, want the existing problem %d, not a new one", res, id)
	}
	if !res.Paused {
		t.Error("result.Paused = false, want true so the caller can tell the user")
	}
	if f.pausedAt(id) == nil {
		t.Error("re-adding a paused problem unpaused it")
	}
	if n, _ := f.s.CountProblems(f.ctx); n != 1 {
		t.Errorf("problem count = %d, want 1 (no duplicate)", n)
	}
}

func TestBulkImport_RefreshingAPausedProblemLeavesItPaused(t *testing.T) {
	f := newPauseFixture(t)
	id := f.add("A", "a", DifficultyEasy, "2026-01-05")
	f.mustPause(id)

	res, err := f.s.BulkImportProblems(f.ctx, []BulkImportRow{
		{Title: "A", URL: "https://leetcode.com/problems/a/", Difficulty: DifficultyEasy},
	}, 5)
	if err != nil {
		t.Fatalf("BulkImportProblems: unexpected err: %v", err)
	}
	if res.Merged != 1 {
		t.Fatalf("result = %+v, want the row merged into the existing problem", res)
	}
	if f.pausedAt(id) == nil {
		t.Error("bulk import refresh unpaused the problem")
	}
}

func TestAddProblem_PausedFlagIsFalseForNewAndActiveProblems(t *testing.T) {
	f := newPauseFixture(t)
	in := AddProblemInput{
		Title: "A", URL: "a", Difficulty: DifficultyEasy, Grade: scheduler.Good, At: f.s.Now(),
	}

	created, err := f.s.AddProblem(f.ctx, in)
	if err != nil || created.Paused {
		t.Fatalf("new problem: (Paused=%v, err=%v), want Paused=false", created.Paused, err)
	}
	readded, err := f.s.AddProblem(f.ctx, in)
	if err != nil || readded.Created || readded.Paused {
		t.Fatalf("re-add of active problem: (Created=%v, Paused=%v, err=%v), want Created=false Paused=false",
			readded.Created, readded.Paused, err)
	}
}
