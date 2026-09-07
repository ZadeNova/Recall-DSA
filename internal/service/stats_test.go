package service

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
)

func TestCountProblems(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	count, err := s.CountProblems(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0 on an empty DB", count)
	}

	if _, err := s.AddProblem(ctx, AddProblemInput{
		Title: "Two Sum", URL: "two-sum", Difficulty: DifficultyEasy, Grade: scheduler.Good, At: time.Now(),
	}); err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	count, err = s.CountProblems(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestDifficultyBreakdown(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Now()

	mustAdd := func(slug string, difficulty Difficulty) {
		t.Helper()
		if _, err := s.AddProblem(ctx, AddProblemInput{
			Title: slug, URL: slug, Difficulty: difficulty, Grade: scheduler.Good, At: at,
		}); err != nil {
			t.Fatalf("AddProblem(%s): unexpected err: %v", slug, err)
		}
	}
	mustAdd("a", DifficultyEasy)
	mustAdd("b", DifficultyEasy)
	mustAdd("c", DifficultyMedium)
	mustAdd("d", DifficultyHard)

	got, err := s.DifficultyBreakdown(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := DifficultyCounts{Easy: 2, Medium: 1, Hard: 1}
	if got != want {
		t.Errorf("DifficultyBreakdown = %+v, want %+v", got, want)
	}
}

func TestUpcomingByDay_ZeroFillsAndGroupsByExactOffset(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Now()

	add := func(title, slug string) int64 {
		t.Helper()
		p, err := s.AddProblem(ctx, AddProblemInput{
			Title: title, URL: slug, Difficulty: DifficultyEasy, Grade: scheduler.Good, At: at,
		})
		if err != nil {
			t.Fatalf("AddProblem(%s): unexpected err: %v", title, err)
		}
		return p.ID
	}
	setDue := func(id int64, date string) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, `UPDATE review_state SET next_review_date = ? WHERE problem_id = ?`, date, id); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	}

	tomorrowA := add("Tomorrow A", "tomorrow-a")
	tomorrowB := add("Tomorrow B", "tomorrow-b")
	inThreeDays := add("In Three Days", "in-three-days")
	tooFar := add("Too Far", "too-far")

	setDue(tomorrowA, "2026-01-11")
	setDue(tomorrowB, "2026-01-11")
	setDue(inThreeDays, "2026-01-13")
	setDue(tooFar, "2026-01-20") // beyond the 5-day window

	fixedClock(s, time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC)) // "today" = 2026-01-10 SGT

	got, err := s.UpcomingByDay(ctx, 5)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := []DayCount{
		{DaysFromNow: 1, Count: 2}, // 2026-01-11
		{DaysFromNow: 2, Count: 0}, // 2026-01-12
		{DaysFromNow: 3, Count: 1}, // 2026-01-13
		{DaysFromNow: 4, Count: 0}, // 2026-01-14
		{DaysFromNow: 5, Count: 0}, // 2026-01-15
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("UpcomingByDay(5) = %+v, want %+v", got, want)
	}
}
