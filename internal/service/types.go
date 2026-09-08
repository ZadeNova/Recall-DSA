package service

import (
	"time"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
)

// Difficulty is LeetCode's own problem difficulty. It is deliberately a
// distinct type from scheduler.Grade even though both happen to use the
// words "Easy"/"Hard" — they are unrelated concepts (a problem's fixed
// difficulty vs. how well you did on a given attempt).
type Difficulty string

const (
	DifficultyEasy   Difficulty = "Easy"
	DifficultyMedium Difficulty = "Medium"
	DifficultyHard   Difficulty = "Hard"
)

func (d Difficulty) valid() bool {
	switch d {
	case DifficultyEasy, DifficultyMedium, DifficultyHard:
		return true
	default:
		return false
	}
}

// Valid reports whether d is one of the three recognized difficulties —
// exported so callers outside this package (e.g. httpapi's bulk-import
// row validation) can check without duplicating the enum.
func (d Difficulty) Valid() bool {
	return d.valid()
}

// Problem is a row from the problems table, with its topic tags resolved.
type Problem struct {
	ID         int64
	Title      string
	URL        string
	Difficulty Difficulty
	Slug       string
	Topics     []string
}

// Topic is a row from the topics table.
type Topic struct {
	ID   int64
	Name string
}

// ReviewState is the persisted, date-aware review state for a problem —
// scheduler.ReviewState plus the dates SPEC.md §4 says are this layer's
// responsibility, not the scheduler's.
type ReviewState struct {
	EaseFactor     float64
	IntervalDays   int
	Repetitions    int
	NextReviewDate time.Time
	LastGrade      scheduler.Grade
	LastReviewedAt time.Time
}

// DueItem is one row of the due-queue (SPEC.md §4): a problem joined with
// its current review state.
type DueItem struct {
	Problem
	ReviewState
}

// AddProblemInput describes a single-add form submission (SPEC.md §9):
// creating a problem and logging its first grade is one action.
type AddProblemInput struct {
	Title      string
	URL        string
	Difficulty Difficulty
	Topics     []string
	Grade      scheduler.Grade
	At         time.Time
}

// UpdateProblemInput describes an edit to an existing problem's fields
// (SPEC.md §9). Topics are replaced wholesale, not merged.
type UpdateProblemInput struct {
	Title      string
	URL        string
	Difficulty Difficulty
	Topics     []string
}

// ListProblemsFilter narrows ListLibrary's results (SPEC.md §7) by
// topic and/or difficulty, plus search/sort/pagination. A nil Topic or
// Difficulty means "no filter on that dimension." Limit must be > 0 —
// ListLibrary passes it straight into a SQL LIMIT clause, so a zero
// value returns zero rows rather than "unlimited."
type ListProblemsFilter struct {
	Topic      *string
	Difficulty *Difficulty
	Search     *string
	Sort       string // "next_review" (default), "title", or "difficulty"
	Limit      int
	Offset     int
}
