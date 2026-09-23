package service

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
)

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestRecordReview_MatchesSchedulerTrace re-runs SPEC.md §5's worked trace
// (Good, Good, Good, Hard, Good) through the service layer, proving the
// DB round-trip (load current state, Apply, upsert) doesn't disturb the
// scheduler's own verified behavior.
func TestRecordReview_MatchesSchedulerTrace(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	problem, err := s.AddProblem(ctx, AddProblemInput{
		Title: "Two Sum", URL: "two-sum", Difficulty: DifficultyEasy,
		Grade: scheduler.Good, At: at, // review 1
	})
	if err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	steps := []struct {
		grade        scheduler.Grade
		wantInterval int
		wantEase     float64
	}{
		{scheduler.Good, 13, 2.50}, // review 2
		{scheduler.Good, 33, 2.50}, // review 3
		{scheduler.Hard, 40, 2.35}, // review 4
		{scheduler.Good, 45, 2.35}, // review 5
	}
	for i, step := range steps {
		rs, err := s.RecordReview(ctx, problem.ID, step.grade, at)
		if err != nil {
			t.Fatalf("review %d: RecordReview unexpected err: %v", i+2, err)
		}
		if rs.IntervalDays != step.wantInterval || !almostEqual(rs.EaseFactor, step.wantEase) {
			t.Fatalf("review %d: state = %+v, want {interval:%d ease:%v}", i+2, rs, step.wantInterval, step.wantEase)
		}
	}

	var attemptCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE problem_id = ?`, problem.ID).Scan(&attemptCount); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if attemptCount != 5 {
		t.Errorf("attempt count = %d, want 5 (one per grade)", attemptCount)
	}
}

// TestRecordReview_NextReviewDateUsesConfiguredTimezone picks a moment
// that falls on different calendar dates in UTC vs. the service's
// configured Asia/Singapore timezone (23:00 UTC on Jan 1 is already Jan 2
// in SGT, UTC+8), to prove next_review_date is computed against the
// configured location (SPEC.md §4), not UTC or the host clock.
func TestRecordReview_NextReviewDateUsesConfiguredTimezone(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	at := time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC) // 2026-01-02 07:00 SGT

	problem, err := s.AddProblem(ctx, AddProblemInput{
		Title: "Two Sum", URL: "two-sum", Difficulty: DifficultyEasy,
		Grade: scheduler.Good, At: at, // first grade: +5 days
	})
	if err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	var nextReviewDate string
	if err := s.db.QueryRowContext(ctx, `SELECT next_review_date FROM review_state WHERE problem_id = ?`, problem.ID).Scan(&nextReviewDate); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	want := "2026-01-07" // 2026-01-02 (SGT date) + 5 days
	if nextReviewDate != want {
		t.Errorf("next_review_date = %q, want %q (SGT-dated, not UTC-dated 2026-01-06)", nextReviewDate, want)
	}
}

func TestRecommendDue_FiltersOverdueAndOrdersByMostOverdueFirst(t *testing.T) {
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

	overdueFar := add("Overdue Far", "overdue-far")
	overdueNear := add("Overdue Near", "overdue-near")
	dueToday := add("Due Today", "due-today")
	notYetDue := add("Not Yet Due", "not-yet-due")

	setDue(overdueFar, "2026-01-01")
	setDue(overdueNear, "2026-01-05")
	setDue(dueToday, "2026-01-10")
	setDue(notYetDue, "2026-01-15")

	fixedClock(s, time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC)) // 2026-01-10 in SGT too

	due, total, err := s.RecommendDue(ctx, nil, 100, 0)
	if err != nil {
		t.Fatalf("RecommendDue: unexpected err: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}

	var gotIDs []int64
	for _, d := range due {
		gotIDs = append(gotIDs, d.ID)
	}
	wantIDs := []int64{overdueFar, overdueNear, dueToday}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Errorf("due IDs = %v, want %v (overdue+today, most overdue first, not-yet-due excluded)", gotIDs, wantIDs)
	}
}

// TestRecommendDue_NoConnectionPoolDeadlockUnderSingleConnection guards
// against a real regression: scanReviewItems once called
// loadProblemTopics per-row while the outer *sql.Rows was still open, so
// the inner query blocked forever waiting for the one connection the
// outer, unfinished iteration was holding. db.Open pins the pool to one
// connection, so this would hang every Due and Library page. Topics must
// still come back attached, not just "didn't hang": the fix (batch-loading
// after the outer rows are drained) changes when the topics query runs,
// not what's returned.
func TestRecommendDue_NoConnectionPoolDeadlockUnderSingleConnection(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	// Graded 10 days ago with Good's 5-day first interval lands 5 days in
	// the past — overdue, so RecommendDue actually returns these rows
	// rather than 0 (the point of this test is proving the query
	// completes with topics attached, not just that it doesn't hang).
	at := time.Now().AddDate(0, 0, -10)

	for i, title := range []string{"Two Sum", "Valid Anagram", "Group Anagrams"} {
		if _, err := s.AddProblem(ctx, AddProblemInput{
			Title: title, URL: fmt.Sprintf("problem-%d", i), Difficulty: DifficultyEasy,
			Topics: []string{"Arrays", "Hash Table"}, Grade: scheduler.Good, At: at,
		}); err != nil {
			t.Fatalf("AddProblem(%s): unexpected err: %v", title, err)
		}
	}

	done := make(chan struct {
		items []DueItem
		err   error
	}, 1)
	go func() {
		items, _, err := s.RecommendDue(ctx, nil, 10, 0)
		done <- struct {
			items []DueItem
			err   error
		}{items, err}
	}()

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("RecommendDue: unexpected err: %v", result.err)
		}
		if len(result.items) != 3 {
			t.Fatalf("len(items) = %d, want 3", len(result.items))
		}
		for _, item := range result.items {
			if len(item.Topics) != 2 {
				t.Errorf("problem %q topics = %v, want 2 topics attached", item.Title, item.Topics)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RecommendDue deadlocked on the single connection — a per-row query is running while the outer cursor is still open")
	}
}

func TestRecommendDue_Pagination(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Now()

	var ids []int64
	for i := 0; i < 5; i++ {
		p, err := s.AddProblem(ctx, AddProblemInput{
			Title: fmt.Sprintf("Problem %d", i), URL: fmt.Sprintf("problem-%d", i),
			Difficulty: DifficultyEasy, Grade: scheduler.Good, At: at,
		})
		if err != nil {
			t.Fatalf("AddProblem: unexpected err: %v", err)
		}
		ids = append(ids, p.ID)
	}
	// All 5 are due (Good = 5-day interval; fast-forward past it).
	fixedClock(s, at.AddDate(0, 0, 10))

	page1, total, err := s.RecommendDue(ctx, nil, 2, 0)
	if err != nil {
		t.Fatalf("RecommendDue page1: unexpected err: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5 (independent of limit)", total)
	}
	if len(page1) != 2 {
		t.Fatalf("len(page1) = %d, want 2", len(page1))
	}

	page3, total, err := s.RecommendDue(ctx, nil, 2, 4)
	if err != nil {
		t.Fatalf("RecommendDue page3: unexpected err: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(page3) != 1 {
		t.Errorf("len(page3) = %d, want 1 (5 items, page size 2, offset 4 -> 1 remaining)", len(page3))
	}

	assertPagesCoverEachItemExactlyOnce(t, s, ids)
}

// assertPagesCoverEachItemExactlyOnce walks every page of RecommendDue at
// page size 2 and checks the pages together contain each wantIDs exactly
// once.
//
// This is the assertion the page-length checks above cannot make. All the
// seeded problems share one next_review_date — the normal case, since
// next_review_date is a date and a bulk import deliberately clusters
// rows onto each day — and paging is separate queries at different
// offsets. Rows tied on every ORDER BY term have no guaranteed order, so
// without a total sort the pages can disagree about which tied row comes
// first: one problem appears on two pages and another appears on none,
// silently vanishing from the due list. See dueOrderBy.
func assertPagesCoverEachItemExactlyOnce(t *testing.T, s *Service, wantIDs []int64) {
	t.Helper()

	const pageSize = 2
	seen := map[int64]int{}
	for offset := 0; offset < len(wantIDs); offset += pageSize {
		page, _, err := s.RecommendDue(context.Background(), nil, pageSize, offset)
		if err != nil {
			t.Fatalf("RecommendDue(offset=%d): unexpected err: %v", offset, err)
		}
		for _, item := range page {
			seen[item.ID]++
		}
	}

	for _, id := range wantIDs {
		switch seen[id] {
		case 1: // present exactly once, as required
		case 0:
			t.Errorf("problem %d never appeared on any page — a due problem is unreachable through pagination", id)
		default:
			t.Errorf("problem %d appeared on %d pages — a duplicate here means another problem was skipped", id, seen[id])
		}
	}
	if len(seen) != len(wantIDs) {
		t.Errorf("pages covered %d distinct problems, want %d", len(seen), len(wantIDs))
	}
}

func TestRecommendUpcoming_WindowBoundaryAndOrdering(t *testing.T) {
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

	dueToday := add("Due Today", "due-today")               // excluded — that's RecommendDue's job
	tomorrow := add("Tomorrow", "tomorrow")                 // included
	inFiveDays := add("In Five Days", "in-five-days")       // included
	exactlySeven := add("Exactly Seven", "exactly-seven")   // included — boundary
	eightDaysOut := add("Eight Days Out", "eight-days-out") // excluded — just past the window

	setDue(dueToday, "2026-01-10")
	setDue(tomorrow, "2026-01-11")
	setDue(inFiveDays, "2026-01-15")
	setDue(exactlySeven, "2026-01-17")
	setDue(eightDaysOut, "2026-01-18")

	fixedClock(s, time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC)) // "today" = 2026-01-10 in SGT

	upcoming, total, err := s.RecommendUpcoming(ctx, nil, 7, 100, 0)
	if err != nil {
		t.Fatalf("RecommendUpcoming: unexpected err: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}

	var gotIDs []int64
	for _, u := range upcoming {
		gotIDs = append(gotIDs, u.ID)
	}
	wantIDs := []int64{tomorrow, inFiveDays, exactlySeven}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Errorf("upcoming IDs = %v, want %v (today excluded, 7-day boundary included, day 8 excluded)", gotIDs, wantIDs)
	}
}

// TestRecommendDueAndUpcoming_FilterByTopic covers RecommendDue and
// RecommendUpcoming's topic filter, both built on the same
// appendTopicJoin (reviews.go) — one shared setup, one subtest per
// method, so their join semantics are proven not to have drifted apart
// without duplicating the seed data twice.
func TestRecommendDueAndUpcoming_FilterByTopic(t *testing.T) {
	seedTwoTopics := func(t *testing.T, s *Service, ctx context.Context, at time.Time) (graphsID int64) {
		t.Helper()
		graphs, err := s.AddProblem(ctx, AddProblemInput{
			Title: "Number of Islands", URL: "number-of-islands", Difficulty: DifficultyMedium,
			Topics: []string{"Graphs"}, Grade: scheduler.Good, At: at,
		})
		if err != nil {
			t.Fatalf("AddProblem: unexpected err: %v", err)
		}
		if _, err := s.AddProblem(ctx, AddProblemInput{
			Title: "Two Sum", URL: "two-sum", Difficulty: DifficultyEasy,
			Topics: []string{"Arrays & Hashing"}, Grade: scheduler.Good, At: at,
		}); err != nil {
			t.Fatalf("AddProblem: unexpected err: %v", err)
		}
		return graphs.ID
	}

	t.Run("RecommendDue", func(t *testing.T) {
		s := newTestService(t)
		ctx := context.Background()
		at := time.Now()
		graphsID := seedTwoTopics(t, s, ctx, at)

		// Both problems' next_review_date is already overdue relative to a
		// far-future fixed "today", so the topic filter is the only thing
		// distinguishing the results.
		fixedClock(s, at.AddDate(1, 0, 0))

		topic := "Graphs"
		due, _, err := s.RecommendDue(ctx, &topic, 100, 0)
		if err != nil {
			t.Fatalf("RecommendDue: unexpected err: %v", err)
		}
		if len(due) != 1 || due[0].ID != graphsID {
			t.Errorf("RecommendDue(topic=Graphs) = %+v, want just Number of Islands", due)
		}
	})

	t.Run("RecommendUpcoming", func(t *testing.T) {
		s := newTestService(t)
		ctx := context.Background()
		at := time.Now()
		graphsID := seedTwoTopics(t, s, ctx, at)

		// Both problems were just graded Good (5-day interval), so both fall
		// inside a 7-day upcoming window relative to "now" — the topic
		// filter is the only thing distinguishing the results.
		topic := "Graphs"
		upcoming, _, err := s.RecommendUpcoming(ctx, &topic, 7, 100, 0)
		if err != nil {
			t.Fatalf("RecommendUpcoming: unexpected err: %v", err)
		}
		if len(upcoming) != 1 || upcoming[0].ID != graphsID {
			t.Errorf("RecommendUpcoming(topic=Graphs) = %+v, want just Number of Islands", upcoming)
		}
	})
}
