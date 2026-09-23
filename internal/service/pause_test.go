package service

import (
	"context"
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
