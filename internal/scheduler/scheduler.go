// Package scheduler implements the project's SM-2 variant scheduler
// (SPEC.md §5). It is a pure, dependency-free function over in-memory
// state — no I/O, no dates, no DB concerns. The tuning constants below
// (ease deltas, first-interval days, the Hard 1.2x rule, the 45-day cap)
// were deliberately chosen through design discussion (see SPEC.md §5 and
// CLAUDE.md) — do not change them without flagging it explicitly.
package scheduler

import (
	"errors"
	"fmt"
	"math"
)

// Grade is the outcome of a single review attempt.
type Grade string

const (
	Failed Grade = "Failed"
	Hard   Grade = "Hard"
	Good   Grade = "Good"
	Easy   Grade = "Easy"
)

// ErrInvalidGrade is returned by Apply when grade is not one of the four
// Grade constants.
var ErrInvalidGrade = errors.New("scheduler: invalid grade")

const (
	initialEase = 2.5
	easeFloor   = 1.3

	failedEaseDelta = 0.20
	hardEaseDelta   = 0.15
	easyEaseDelta   = 0.15

	firstIntervalFailedDays = 1
	firstIntervalHardDays   = 3
	firstIntervalGoodDays   = 5
	firstIntervalEasyDays   = 7

	hardMultiplier  = 1.2
	intervalCapDays = 45.0
)

// ReviewState is the per-problem SM-2 state. It intentionally excludes
// problem identity and dates (next_review_date, timestamps) — those are
// the service layer's responsibility (SPEC.md §4), not this package's.
type ReviewState struct {
	EaseFactor   float64
	IntervalDays int
	Repetitions  int
}

// NewReviewState returns the initial state for a problem that has never
// been reviewed.
func NewReviewState() ReviewState {
	return ReviewState{EaseFactor: initialEase}
}

// Apply grades the current state and returns the resulting next state.
// s is not mutated; Apply is a pure function. If grade is not one of the
// four valid Grade values, it returns s unchanged along with
// ErrInvalidGrade.
func (s ReviewState) Apply(grade Grade) (ReviewState, error) {
	switch grade {
	case Failed, Hard, Good, Easy:
	default:
		return s, fmt.Errorf("%w: %q", ErrInvalidGrade, grade)
	}

	// Failed is an unconditional reset, independent of the current
	// repetitions count — including a second (or Nth) consecutive
	// Failed, where repetitions simply stays at 0 and ease keeps
	// dropping each time (SPEC.md §5).
	if grade == Failed {
		return ReviewState{
			EaseFactor:   clampEase(s.EaseFactor - failedEaseDelta),
			IntervalDays: firstIntervalFailedDays,
			Repetitions:  0,
		}, nil
	}

	if s.Repetitions == 0 {
		return applyFirstInterval(s, grade), nil
	}

	return applySubsequentInterval(s, grade), nil
}

// applyFirstInterval handles the rep 0→1 transition: the grade sets the
// first interval directly rather than growing multiplicatively.
func applyFirstInterval(s ReviewState, grade Grade) ReviewState {
	next := ReviewState{EaseFactor: s.EaseFactor, Repetitions: 1}
	switch grade {
	case Hard:
		next.EaseFactor = clampEase(s.EaseFactor - hardEaseDelta)
		next.IntervalDays = firstIntervalHardDays
	case Good:
		next.IntervalDays = firstIntervalGoodDays
	case Easy:
		next.EaseFactor = s.EaseFactor + easyEaseDelta
		next.IntervalDays = firstIntervalEasyDays
	}
	return next
}

// applySubsequentInterval handles rep >= 1: multiplicative growth, except
// Hard which is capped at a flat 1.2x regardless of ease, followed by the
// 45-day cap and rounding — applied in that order (SPEC.md §5, CLAUDE.md).
func applySubsequentInterval(s ReviewState, grade Grade) ReviewState {
	next := ReviewState{Repetitions: s.Repetitions + 1}

	var multiplier float64
	switch grade {
	case Hard:
		multiplier = hardMultiplier
		next.EaseFactor = clampEase(s.EaseFactor - hardEaseDelta)
	case Good:
		multiplier = s.EaseFactor
		next.EaseFactor = s.EaseFactor
	case Easy:
		multiplier = s.EaseFactor
		next.EaseFactor = s.EaseFactor + easyEaseDelta
	}

	raw := float64(s.IntervalDays) * multiplier
	capped := math.Min(raw, intervalCapDays)
	next.IntervalDays = roundHalfAwayFromZero(capped)
	return next
}

func clampEase(ease float64) float64 {
	if ease < easeFloor {
		return easeFloor
	}
	return ease
}

// roundHalfAwayFromZero rounds x to the nearest integer, rounding an
// exact .5 away from zero (never to-even/"banker's rounding"). All
// interval_days values passed through this package are non-negative, so
// this is equivalent to math.Round; the wrapper exists to name the rule
// at call sites and give it an isolated unit test.
func roundHalfAwayFromZero(x float64) int {
	return int(math.Round(x))
}
