package service

import (
	"context"
	"testing"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
)

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

	t.Run("sort by title ascending", func(t *testing.T) {
		items, _, err := s.ListLibrary(ctx, ListProblemsFilter{Sort: "title", Limit: 10})
		if err != nil {
			t.Fatalf("ListLibrary: unexpected err: %v", err)
		}
		want := []string{"3sum", "climbing-stairs", "trapping-rain-water", "two-sum"}
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

	t.Run("pagination returns the right slice and total count", func(t *testing.T) {
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

func sluglist(items []DueItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Slug
	}
	return out
}
