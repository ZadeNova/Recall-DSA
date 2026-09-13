package service

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
)

func TestExtractSlug(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"trailing_slash", "https://leetcode.com/problems/two-sum/", "two-sum", false},
		{"description_suffix", "https://leetcode.com/problems/two-sum/description/", "two-sum", false},
		{"no_trailing_slash", "https://leetcode.com/problems/two-sum", "two-sum", false},
		{"query_params", "https://leetcode.com/problems/two-sum/?envType=study-plan", "two-sum", false},
		{"bare_slug", "two-sum", "two-sum", false},
		{"bare_slug_whitespace_case", "  Two-Sum  ", "two-sum", false},
		{"empty", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := extractSlug(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("extractSlug(%q) = %q, want error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("extractSlug(%q) unexpected err: %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("extractSlug(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestAddProblem_CreatesAndGradesAtomically(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	problem, err := s.AddProblem(ctx, AddProblemInput{
		Title:      "Two Sum",
		URL:        "https://leetcode.com/problems/two-sum/",
		Difficulty: DifficultyEasy,
		Topics:     []string{"Arrays & Hashing", "Two Pointers"},
		Grade:      scheduler.Good,
		At:         at,
	})
	if err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	if problem.Title != "Two Sum" || problem.Slug != "two-sum" || problem.Difficulty != DifficultyEasy {
		t.Errorf("problem = %+v, unexpected fields", problem)
	}
	wantURL := "https://leetcode.com/problems/two-sum/"
	if problem.URL != wantURL {
		t.Errorf("problem.URL = %q, want %q (canonical form)", problem.URL, wantURL)
	}

	sort.Strings(problem.Topics)
	wantTopics := []string{"Arrays & Hashing", "Two Pointers"}
	if !reflect.DeepEqual(problem.Topics, wantTopics) {
		t.Errorf("problem.Topics = %v, want %v", problem.Topics, wantTopics)
	}

	var attemptCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE problem_id = ?`, problem.ID).Scan(&attemptCount); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if attemptCount != 1 {
		t.Errorf("attempt count = %d, want 1 (AddProblem must log the first attempt)", attemptCount)
	}

	var easeFactor float64
	var intervalDays int
	if err := s.db.QueryRowContext(ctx, `SELECT ease_factor, interval_days FROM review_state WHERE problem_id = ?`, problem.ID).
		Scan(&easeFactor, &intervalDays); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// First Good grade per the scheduler: interval 5, ease unchanged 2.5.
	if intervalDays != 5 || easeFactor != 2.5 {
		t.Errorf("review_state = {ease:%v interval:%d}, want {ease:2.5 interval:5}", easeFactor, intervalDays)
	}
}

func TestAddProblem_DedupesOnSlugAndPreservesExistingTopics(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	first, err := s.AddProblem(ctx, AddProblemInput{
		Title: "Two Sum", URL: "https://leetcode.com/problems/two-sum/",
		Difficulty: DifficultyEasy, Topics: []string{"Arrays & Hashing"},
		Grade: scheduler.Good, At: at,
	})
	if err != nil {
		t.Fatalf("first AddProblem: unexpected err: %v", err)
	}

	// A different URL variant, same slug, and different topics — should
	// dedupe onto the same row rather than create a duplicate, and must
	// not silently overwrite the existing topic set (SPEC.md §9).
	second, err := s.AddProblem(ctx, AddProblemInput{
		Title: "Two Sum (dup submission)", URL: "https://leetcode.com/problems/two-sum/description/?envType=x",
		Difficulty: DifficultyEasy, Topics: []string{"Hash Table (should be ignored)"},
		Grade: scheduler.Hard, At: at.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("second AddProblem: unexpected err: %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("second.ID = %d, want %d (same problem, deduped by slug)", second.ID, first.ID)
	}

	var problemCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM problems WHERE slug = 'two-sum'`).Scan(&problemCount); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if problemCount != 1 {
		t.Errorf("problems with slug two-sum = %d, want 1 (no duplicate row)", problemCount)
	}

	if !reflect.DeepEqual(second.Topics, []string{"Arrays & Hashing"}) {
		t.Errorf("second.Topics = %v, want unchanged [Arrays & Hashing] (dedupe path must not alter existing topics)", second.Topics)
	}

	var attemptCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE problem_id = ?`, first.ID).Scan(&attemptCount); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if attemptCount != 2 {
		t.Errorf("attempt count = %d, want 2 (both calls logged)", attemptCount)
	}
}

func TestAddProblem_ValidatesInput(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Now()

	cases := []struct {
		name  string
		input AddProblemInput
	}{
		{"empty_title", AddProblemInput{Title: "", URL: "two-sum", Difficulty: DifficultyEasy, Grade: scheduler.Good, At: at}},
		{"empty_url", AddProblemInput{Title: "Two Sum", URL: "", Difficulty: DifficultyEasy, Grade: scheduler.Good, At: at}},
		{"invalid_difficulty", AddProblemInput{Title: "Two Sum", URL: "two-sum", Difficulty: Difficulty("Extreme"), Grade: scheduler.Good, At: at}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := s.AddProblem(ctx, c.input); err == nil {
				t.Fatalf("AddProblem(%+v) = nil error, want error", c.input)
			}
		})
	}
}

func TestGetProblem(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	created, err := s.AddProblem(ctx, AddProblemInput{
		Title: "Two Sum", URL: "two-sum", Difficulty: DifficultyEasy,
		Topics: []string{"Arrays & Hashing"}, Grade: scheduler.Good, At: time.Now(),
	})
	if err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	got, err := s.GetProblem(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetProblem: unexpected err: %v", err)
	}
	if got.Title != "Two Sum" || !reflect.DeepEqual(got.Topics, []string{"Arrays & Hashing"}) {
		t.Errorf("GetProblem = %+v, unexpected fields", got)
	}

	if _, err := s.GetProblem(ctx, created.ID+999); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetProblem(missing id) err = %v, want wrapped sql.ErrNoRows", err)
	}
}

func TestUpdateProblem(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Now()

	problem, err := s.AddProblem(ctx, AddProblemInput{
		Title: "Two Sum", URL: "two-sum", Difficulty: DifficultyEasy,
		Topics: []string{"Arrays & Hashing"}, Grade: scheduler.Good, At: at,
	})
	if err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	err = s.UpdateProblem(ctx, problem.ID, UpdateProblemInput{
		Title: "Two Sum (renamed)", URL: "two-sum", Difficulty: DifficultyMedium,
		Topics: []string{"Two Pointers"},
	})
	if err != nil {
		t.Fatalf("UpdateProblem: unexpected err: %v", err)
	}

	updated, err := loadProblem(ctx, s.db, problem.ID)
	if err != nil {
		t.Fatalf("loadProblem: unexpected err: %v", err)
	}
	if updated.Title != "Two Sum (renamed)" || updated.Difficulty != DifficultyMedium {
		t.Errorf("updated = %+v, unexpected fields", updated)
	}
	if !reflect.DeepEqual(updated.Topics, []string{"Two Pointers"}) {
		t.Errorf("updated.Topics = %v, want [Two Pointers] (replaced wholesale)", updated.Topics)
	}
}

// TestUpdateProblem_RejectsDuplicateSlug guards against the same
// raw-error-leak regression as CreateTopic/RenameTopic, for the other
// realistic path into it: editing a problem's URL to one that already
// belongs to a different problem hits the UNIQUE constraint on
// problems.slug, and must translate to ErrDuplicateSlug rather than the
// raw SQLite driver message.
func TestUpdateProblem_RejectsDuplicateSlug(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Now()

	if _, err := s.AddProblem(ctx, AddProblemInput{
		Title: "Two Sum", URL: "two-sum", Difficulty: DifficultyEasy,
		Grade: scheduler.Good, At: at,
	}); err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}
	other, err := s.AddProblem(ctx, AddProblemInput{
		Title: "Valid Anagram", URL: "valid-anagram", Difficulty: DifficultyEasy,
		Grade: scheduler.Good, At: at,
	})
	if err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	err = s.UpdateProblem(ctx, other.ID, UpdateProblemInput{
		Title: "Valid Anagram", URL: "two-sum", Difficulty: DifficultyEasy,
	})
	if err == nil {
		t.Fatal("expected error updating a problem's URL to one already used by another problem")
	}
	if !errors.Is(err, ErrDuplicateSlug) {
		t.Errorf("err = %v, want errors.Is(err, ErrDuplicateSlug)", err)
	}
	if strings.Contains(err.Error(), "constraint") || strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("err.Error() = %q leaks raw SQL vocabulary, want a plain-English message", err.Error())
	}
}

// Missing-ID coverage for UpdateProblem/DeleteProblem lives in
// TestMissingID_ReturnsNotFound (shared_test.go), alongside the same
// regression for RenameTopic/DeleteTopic.

func TestDeleteProblem_CascadesAtServiceLevel(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Now()

	problem, err := s.AddProblem(ctx, AddProblemInput{
		Title: "Two Sum", URL: "two-sum", Difficulty: DifficultyEasy,
		Grade: scheduler.Good, At: at,
	})
	if err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	if err := s.DeleteProblem(ctx, problem.ID); err != nil {
		t.Fatalf("DeleteProblem: unexpected err: %v", err)
	}

	problems, _, err := s.ListLibrary(ctx, ListProblemsFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListLibrary: unexpected err: %v", err)
	}
	for _, p := range problems {
		if p.ID == problem.ID {
			t.Fatalf("deleted problem %d still present in ListLibrary", problem.ID)
		}
	}

	var attemptCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE problem_id = ?`, problem.ID).Scan(&attemptCount); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if attemptCount != 0 {
		t.Errorf("attempts remaining after delete = %d, want 0", attemptCount)
	}
}

// TestWithTx_PanicRollsBackAndReleasesConnection covers the panic path in
// withTx. A panic used to escape with the transaction still open; since
// net/http recovers per-request the process would survive while never
// returning that connection to the pool. The pool holds exactly one
// connection (db.Open), so a single leak wedges every later request —
// which is what the follow-up query here detects. It also checks the
// aborted write left nothing behind.
func TestWithTx_PanicRollsBackAndReleasesConnection(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	func() {
		defer func() {
			if p := recover(); p == nil {
				t.Error("withTx swallowed the panic, want it to keep unwinding after rollback")
			}
		}()
		_ = s.withTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO problems (title, url, difficulty, slug) VALUES (?, ?, ?, ?)`,
				"Doomed", "https://leetcode.com/problems/doomed/", DifficultyEasy, "doomed",
			); err != nil {
				t.Errorf("seed insert: unexpected err: %v", err)
			}
			panic("boom")
		})
	}()

	// Would block until the context deadline if the connection leaked.
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var count int
	if err := s.db.QueryRowContext(queryCtx, `SELECT COUNT(*) FROM problems`).Scan(&count); err != nil {
		t.Fatalf("query after panicking tx: %v (the connection was never returned to the pool)", err)
	}
	if count != 0 {
		t.Errorf("problems after panicking tx = %d, want 0 (the insert must have rolled back)", count)
	}
}
