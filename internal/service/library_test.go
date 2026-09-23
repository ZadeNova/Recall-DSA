package service

import (
	"context"
	"testing"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
)

// TestListLibrary_ZeroLimitMeansUnlimited guards against a real
// regression: a zero-value ListProblemsFilter{} (no Limit set) used to
// pass LIMIT 0 straight into SQL, silently returning 0 items while total
// still reported every matching row — "0 items, total=3" reads as a bug,
// not as "no pagination requested." Limit <= 0 must mean "return
// everything," matching how a zero-value filter reads everywhere else
// (no filtering specified).
func TestListLibrary_ZeroLimitMeansUnlimited(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Now()

	for i, slug := range []string{"a", "b", "c"} {
		if _, err := s.AddProblem(ctx, AddProblemInput{
			Title: "Problem", URL: slug, Difficulty: DifficultyEasy,
			Grade: scheduler.Good, At: at,
		}); err != nil {
			t.Fatalf("AddProblem(%d): unexpected err: %v", i, err)
		}
	}

	items, total, err := s.ListLibrary(ctx, ListProblemsFilter{})
	if err != nil {
		t.Fatalf("ListLibrary: unexpected err: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if len(items) != 3 {
		t.Errorf("len(items) = %d, want 3 (zero Limit should mean unlimited, not zero)", len(items))
	}
}

func TestListLibrary_SearchSortAndPagination(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Now()

	mustAdd := func(title, slug string, difficulty Difficulty, grade scheduler.Grade) {
		t.Helper()
		if _, err := s.AddProblem(ctx, AddProblemInput{
			Title: title, URL: slug, Difficulty: difficulty,
			Grade: grade, At: at,
		}); err != nil {
			t.Fatalf("AddProblem(%s): unexpected err: %v", title, err)
		}
	}
	// Distinct grades give distinct intervals, so sorting by next_review_date
	// (the default) has a deterministic order to assert against: Failed=1,
	// Hard=3, Good=5, Easy=7 days (internal/scheduler/scheduler.go).
	mustAdd("Two Sum", "two-sum", DifficultyEasy, scheduler.Easy)
	mustAdd("3Sum", "3sum", DifficultyMedium, scheduler.Good)
	mustAdd("Climbing Stairs", "climbing-stairs", DifficultyEasy, scheduler.Hard)
	mustAdd("Trapping Rain Water", "trapping-rain-water", DifficultyHard, scheduler.Failed)

	t.Run("search matches title substring", func(t *testing.T) {
		q := "sum"
		items, total, err := s.ListLibrary(ctx, ListProblemsFilter{Search: &q, Limit: 10})
		if err != nil {
			t.Fatalf("ListLibrary: unexpected err: %v", err)
		}
		if total != 2 {
			t.Errorf("total = %d, want 2 (Two Sum, 3Sum)", total)
		}
		if len(items) != 2 {
			t.Errorf("len(items) = %d, want 2", len(items))
		}
	})

	t.Run("default sort is next_review_date ascending", func(t *testing.T) {
		items, total, err := s.ListLibrary(ctx, ListProblemsFilter{Limit: 10})
		if err != nil {
			t.Fatalf("ListLibrary: unexpected err: %v", err)
		}
		if total != 4 {
			t.Fatalf("total = %d, want 4", total)
		}
		want := []string{"trapping-rain-water", "climbing-stairs", "3sum", "two-sum"}
		for i, slug := range want {
			if items[i].Slug != slug {
				t.Errorf("items[%d].Slug = %q, want %q (order: %v)", i, items[i].Slug, slug, sluglist(items))
			}
		}
	})

	t.Run("sort by difficulty ascending", func(t *testing.T) {
		items, _, err := s.ListLibrary(ctx, ListProblemsFilter{Sort: "difficulty", Limit: 10})
		if err != nil {
			t.Fatalf("ListLibrary: unexpected err: %v", err)
		}
		if items[0].Difficulty != DifficultyEasy || items[len(items)-1].Difficulty != DifficultyHard {
			t.Errorf("difficulty order wrong: %v", sluglist(items))
		}
	})

	t.Run("sort by title, paginated: right slices and total", func(t *testing.T) {
		page1, total, err := s.ListLibrary(ctx, ListProblemsFilter{Sort: "title", Limit: 2, Offset: 0})
		if err != nil {
			t.Fatalf("ListLibrary page1: unexpected err: %v", err)
		}
		if total != 4 {
			t.Fatalf("total = %d, want 4", total)
		}
		if len(page1) != 2 || page1[0].Slug != "3sum" || page1[1].Slug != "climbing-stairs" {
			t.Errorf("page1 = %v, want [3sum climbing-stairs]", sluglist(page1))
		}

		page2, _, err := s.ListLibrary(ctx, ListProblemsFilter{Sort: "title", Limit: 2, Offset: 2})
		if err != nil {
			t.Fatalf("ListLibrary page2: unexpected err: %v", err)
		}
		if len(page2) != 2 || page2[0].Slug != "trapping-rain-water" || page2[1].Slug != "two-sum" {
			t.Errorf("page2 = %v, want [trapping-rain-water two-sum]", sluglist(page2))
		}
	})

	t.Run("difficulty filter still combines with pagination", func(t *testing.T) {
		easy := DifficultyEasy
		items, total, err := s.ListLibrary(ctx, ListProblemsFilter{Difficulty: &easy, Sort: "title", Limit: 10})
		if err != nil {
			t.Fatalf("ListLibrary: unexpected err: %v", err)
		}
		if total != 2 {
			t.Errorf("total = %d, want 2 (Two Sum, Climbing Stairs)", total)
		}
		for _, it := range items {
			if it.Difficulty != DifficultyEasy {
				t.Errorf("item %s has difficulty %s, want Easy", it.Slug, it.Difficulty)
			}
		}
	})
}

// TestListLibrary_FiltersByTopic covers ListLibrary's topic filter,
// which (unlike Difficulty, already covered above) had no direct test —
// its only prior coverage was via the now-deleted, production-unused
// ListProblems, which had its own separate, parallel implementation of
// this same filter.
func TestListLibrary_FiltersByTopic(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Now()

	mustAdd := func(title, slug string, topics []string) {
		t.Helper()
		if _, err := s.AddProblem(ctx, AddProblemInput{
			Title: title, URL: slug, Difficulty: DifficultyEasy, Topics: topics,
			Grade: scheduler.Good, At: at,
		}); err != nil {
			t.Fatalf("AddProblem(%s): unexpected err: %v", title, err)
		}
	}
	mustAdd("Two Sum", "two-sum", []string{"Arrays & Hashing"})
	mustAdd("Best Time to Buy and Sell Stock", "best-time", []string{"Arrays & Hashing", "Sliding Window"})
	mustAdd("Climbing Stairs", "climbing-stairs", []string{"1-D Dynamic Programming"})

	arraysTopic := "Arrays & Hashing"
	items, total, err := s.ListLibrary(ctx, ListProblemsFilter{Topic: &arraysTopic, Limit: 10})
	if err != nil {
		t.Fatalf("ListLibrary: unexpected err: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2 (Two Sum, Best Time to Buy and Sell Stock)", total)
	}
	if len(items) != 2 {
		t.Errorf("len(items) = %d, want 2", len(items))
	}
	for _, it := range items {
		if it.Slug == "climbing-stairs" {
			t.Error("climbing-stairs (no Arrays & Hashing tag) should not match the topic filter")
		}
	}
}

// TestListLibrary_SearchEscapesLikeMetacharacters guards against a real
// failure mode of the naive version of this filter: p.title LIKE
// ?ESCAPE'\' means a raw "%" or "_" in the search term would otherwise
// be interpreted as a SQL wildcard rather than a literal character —
// searching "100%" would match every title, and "_score" would match
// any five-character-prefixed title, not just ones that actually contain
// an underscore.
func TestListLibrary_SearchEscapesLikeMetacharacters(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Now()

	mustAdd := func(title, slug string) {
		t.Helper()
		if _, err := s.AddProblem(ctx, AddProblemInput{
			Title: title, URL: slug, Difficulty: DifficultyEasy, Grade: scheduler.Good, At: at,
		}); err != nil {
			t.Fatalf("AddProblem(%s): unexpected err: %v", title, err)
		}
	}
	mustAdd("100% Done", "pct-done")
	mustAdd("Normal Problem", "normal")
	mustAdd("under_score", "under-score")

	percent := "%"
	items, total, err := s.ListLibrary(ctx, ListProblemsFilter{Search: &percent, Limit: 10})
	if err != nil {
		t.Fatalf("ListLibrary: unexpected err: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].Slug != "pct-done" {
		t.Errorf(`search "%%" = %v (total %d), want only pct-done (literal match, not a wildcard matching everything)`, sluglist(items), total)
	}

	underscore := "_"
	items, total, err = s.ListLibrary(ctx, ListProblemsFilter{Search: &underscore, Limit: 10})
	if err != nil {
		t.Fatalf("ListLibrary: unexpected err: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].Slug != "under-score" {
		t.Errorf(`search "_" = %v (total %d), want only under-score (literal match, not a single-char wildcard matching everything)`, sluglist(items), total)
	}
}

func sluglist(items []DueItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Slug
	}
	return out
}
