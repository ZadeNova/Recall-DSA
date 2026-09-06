package scheduler

import (
	"errors"
	"math"
	"testing"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestNewReviewState(t *testing.T) {
	s := NewReviewState()
	if !almostEqual(s.EaseFactor, 2.5) || s.IntervalDays != 0 || s.Repetitions != 0 {
		t.Fatalf("NewReviewState() = %+v, want {2.5, 0, 0}", s)
	}
}

func TestClampEase(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{2.5, 2.5},
		{1.3, 1.3},
		{1.29999, 1.3},
		{0.5, 1.3},
	}
	for _, c := range cases {
		if got := clampEase(c.in); !almostEqual(got, c.want) {
			t.Errorf("clampEase(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRoundHalfAwayFromZero(t *testing.T) {
	cases := []struct {
		in   float64
		want int
	}{
		{12.4, 12}, // frac < .5 rounds down — proves not-ceil
		{12.6, 13}, // frac > .5 rounds up — proves not-floor
		{12.5, 13}, // exact half, odd integer part
		{44.5, 45}, // exact half, EVEN integer part — banker's rounding would wrongly give 44
	}
	for _, c := range cases {
		if got := roundHalfAwayFromZero(c.in); got != c.want {
			t.Errorf("roundHalfAwayFromZero(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestApply_InvalidGrade(t *testing.T) {
	for _, bad := range []Grade{"banana", ""} {
		s := ReviewState{EaseFactor: 2.5, IntervalDays: 5, Repetitions: 1}
		got, err := s.Apply(bad)
		if !errors.Is(err, ErrInvalidGrade) {
			t.Errorf("Apply(%q) err = %v, want ErrInvalidGrade", bad, err)
		}
		if got != s {
			t.Errorf("Apply(%q) state = %+v, want unchanged %+v", bad, got, s)
		}
	}
}

func TestFirstInterval_AllGrades(t *testing.T) {
	cases := []struct {
		grade        Grade
		wantInterval int
		wantEase     float64
		wantReps     int
	}{
		{Failed, 1, 2.30, 0},
		{Hard, 3, 2.35, 1},
		{Good, 5, 2.50, 1},
		{Easy, 7, 2.65, 1},
	}
	for _, c := range cases {
		got, err := NewReviewState().Apply(c.grade)
		if err != nil {
			t.Fatalf("Apply(%v) unexpected err: %v", c.grade, err)
		}
		if got.IntervalDays != c.wantInterval || !almostEqual(got.EaseFactor, c.wantEase) || got.Repetitions != c.wantReps {
			t.Errorf("Apply(%v) = %+v, want {interval:%d ease:%v reps:%d}", c.grade, got, c.wantInterval, c.wantEase, c.wantReps)
		}
	}
}

// TestTraceA_SpecWorkedExample reproduces SPEC.md §5's hand-verified worked
// trace exactly (ease starting 2.5, Good/Good/Good/Hard/Good):
//
//	step  grade  interval  ease
//	1     Good   5         2.50
//	2     Good   13        2.50
//	3     Good   33        2.50
//	4     Hard   40        2.35
//	5     Good   45        2.35   (capped)
func TestTraceA_SpecWorkedExample(t *testing.T) {
	steps := []struct {
		grade        Grade
		wantInterval int
		wantEase     float64
	}{
		{Good, 5, 2.50},
		{Good, 13, 2.50},
		{Good, 33, 2.50},
		{Hard, 40, 2.35},
		{Good, 45, 2.35},
	}
	state := NewReviewState()
	for i, step := range steps {
		var err error
		state, err = state.Apply(step.grade)
		if err != nil {
			t.Fatalf("step %d (%v): unexpected err: %v", i+1, step.grade, err)
		}
		if state.IntervalDays != step.wantInterval || !almostEqual(state.EaseFactor, step.wantEase) {
			t.Fatalf("step %d (%v): state = %+v, want {interval:%d ease:%v}", i+1, step.grade, state, step.wantInterval, step.wantEase)
		}
	}
}

// TestTraceB_FailedReset covers the Failed-reset behavior Trace A never
// exercises (ease starting 2.5, Good/Good/Failed/Good/Hard/Failed/Failed/Good):
//
//	step  grade   reps before→after  interval  ease
//	1     Good    0→1                5         2.50
//	2     Good    1→2                13        2.50
//	3     Failed  2→0                1         2.30
//	4     Good    0→1                5         2.30
//	5     Hard    1→2                6         2.15
//	6     Failed  2→0                1         1.95
//	7     Failed  0→0                1         1.75
//	8     Good    0→1                5         1.75
func TestTraceB_FailedReset(t *testing.T) {
	steps := []struct {
		grade          Grade
		wantRepsBefore int
		wantRepsAfter  int
		wantInterval   int
		wantEase       float64
	}{
		{Good, 0, 1, 5, 2.50},
		{Good, 1, 2, 13, 2.50},
		{Failed, 2, 0, 1, 2.30},
		{Good, 0, 1, 5, 2.30},
		{Hard, 1, 2, 6, 2.15},
		{Failed, 2, 0, 1, 1.95},
		{Failed, 0, 0, 1, 1.75},
		{Good, 0, 1, 5, 1.75},
	}
	state := NewReviewState()
	for i, step := range steps {
		if state.Repetitions != step.wantRepsBefore {
			t.Fatalf("step %d (%v): reps before = %d, want %d", i+1, step.grade, state.Repetitions, step.wantRepsBefore)
		}
		var err error
		state, err = state.Apply(step.grade)
		if err != nil {
			t.Fatalf("step %d (%v): unexpected err: %v", i+1, step.grade, err)
		}
		if state.Repetitions != step.wantRepsAfter || state.IntervalDays != step.wantInterval || !almostEqual(state.EaseFactor, step.wantEase) {
			t.Fatalf("step %d (%v): state = %+v, want {reps:%d interval:%d ease:%v}", i+1, step.grade, state, step.wantRepsAfter, step.wantInterval, step.wantEase)
		}
	}
}

func TestEaseFactorFloor_ConsecutiveFailedGrades(t *testing.T) {
	state := NewReviewState()
	wantEase := initialEase
	for i := 1; i <= 7; i++ {
		var err error
		state, err = state.Apply(Failed)
		if err != nil {
			t.Fatalf("failed grade %d: unexpected err: %v", i, err)
		}
		wantEase = clampEase(wantEase - failedEaseDelta)
		if !almostEqual(state.EaseFactor, wantEase) {
			t.Fatalf("failed grade %d: ease = %v, want %v", i, state.EaseFactor, wantEase)
		}
		if state.IntervalDays != 1 || state.Repetitions != 0 {
			t.Fatalf("failed grade %d: state = %+v, want interval 1 and reps 0", i, state)
		}
	}
	if !almostEqual(state.EaseFactor, easeFloor) {
		t.Fatalf("after 7 consecutive Failed grades, ease = %v, want floor %v", state.EaseFactor, easeFloor)
	}
}

func TestEaseFactorFloor_ConsecutiveHardGrades(t *testing.T) {
	state, err := NewReviewState().Apply(Good) // bootstrap: interval 5, ease 2.5, reps 1
	if err != nil {
		t.Fatalf("bootstrap Good: unexpected err: %v", err)
	}

	wantEase := state.EaseFactor
	wantReps := state.Repetitions
	for i := 1; i <= 9; i++ {
		prevInterval := state.IntervalDays

		state, err = state.Apply(Hard)
		if err != nil {
			t.Fatalf("hard grade %d: unexpected err: %v", i, err)
		}

		wantEase = clampEase(wantEase - hardEaseDelta)
		wantReps++
		wantInterval := int(math.Round(math.Min(float64(prevInterval)*hardMultiplier, intervalCapDays)))

		if !almostEqual(state.EaseFactor, wantEase) {
			t.Fatalf("hard grade %d: ease = %v, want %v", i, state.EaseFactor, wantEase)
		}
		if state.Repetitions != wantReps {
			t.Fatalf("hard grade %d: reps = %d, want %d (must strictly increment, never reset)", i, state.Repetitions, wantReps)
		}
		if state.IntervalDays != wantInterval {
			t.Fatalf("hard grade %d: interval = %d, want %d", i, state.IntervalDays, wantInterval)
		}
	}
	if !almostEqual(state.EaseFactor, easeFloor) {
		t.Fatalf("after 9 consecutive Hard grades from ease 2.5, ease = %v, want floor %v", state.EaseFactor, easeFloor)
	}
}

func TestIntervalCap_Boundary(t *testing.T) {
	cases := []struct {
		name         string
		state        ReviewState
		wantInterval int
	}{
		{"below_cap_not_clamped", ReviewState{IntervalDays: 10, EaseFactor: 4.43, Repetitions: 2}, 44},
		{"exactly_at_cap", ReviewState{IntervalDays: 10, EaseFactor: 4.5, Repetitions: 2}, 45},
		{"just_over_cap_clamped", ReviewState{IntervalDays: 10, EaseFactor: 4.6, Repetitions: 2}, 45},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.state.Apply(Good)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.IntervalDays != c.wantInterval {
				t.Errorf("IntervalDays = %d, want %d", got.IntervalDays, c.wantInterval)
			}
			if !almostEqual(got.EaseFactor, c.state.EaseFactor) {
				t.Errorf("Good must not change ease: got %v, want unchanged %v", got.EaseFactor, c.state.EaseFactor)
			}
		})
	}
}

func TestIntervalCap_HardRuleExceedsCap(t *testing.T) {
	s := ReviewState{IntervalDays: 40, EaseFactor: 1.5, Repetitions: 5}
	got, err := s.Apply(Hard)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// raw = 40 * 1.2 = 48, which exceeds the 45-day cap — must be clamped,
	// not left at 48 (CLAUDE.md: cap applies after the Hard 1.2x rule).
	if got.IntervalDays != 45 {
		t.Errorf("IntervalDays = %d, want 45 (capped)", got.IntervalDays)
	}
	if !almostEqual(got.EaseFactor, 1.35) {
		t.Errorf("EaseFactor = %v, want 1.35", got.EaseFactor)
	}
	if got.Repetitions != 6 {
		t.Errorf("Repetitions = %d, want 6 (Hard must increment, not reset)", got.Repetitions)
	}
}

func TestApply_RoundingIsHalfAwayFromZeroNotFloorOrCeil(t *testing.T) {
	cases := []struct {
		name         string
		state        ReviewState
		wantInterval int
	}{
		// raw = 7 * 1.9 = 13.3 — ceil would wrongly give 14.
		{"disproves_ceil", ReviewState{IntervalDays: 7, EaseFactor: 1.9, Repetitions: 2}, 13},
		// raw = 7 * 2.1 = 14.7 — floor would wrongly give 14.
		{"disproves_floor", ReviewState{IntervalDays: 7, EaseFactor: 2.1, Repetitions: 2}, 15},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.state.Apply(Good)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.IntervalDays != c.wantInterval {
				t.Errorf("IntervalDays = %d, want %d", got.IntervalDays, c.wantInterval)
			}
		})
	}
}

func TestApply_UnboundedEaseGrowthStaysWithinIntervalCap(t *testing.T) {
	state := NewReviewState()
	reachedCap := false
	for i := 1; i <= 1000; i++ {
		var err error
		state, err = state.Apply(Easy)
		if err != nil {
			t.Fatalf("easy grade %d: unexpected err: %v", i, err)
		}

		wantEase := initialEase + easyEaseDelta*float64(i)
		if !almostEqual(state.EaseFactor, wantEase) {
			t.Fatalf("easy grade %d: ease = %v, want %v (linear closed form)", i, state.EaseFactor, wantEase)
		}
		if state.Repetitions != i {
			t.Fatalf("easy grade %d: reps = %d, want %d", i, state.Repetitions, i)
		}
		if state.IntervalDays > 45 {
			t.Fatalf("easy grade %d: interval = %d, exceeds 45-day cap", i, state.IntervalDays)
		}
		if reachedCap && state.IntervalDays != 45 {
			t.Fatalf("easy grade %d: interval = %d, want pinned at 45 once reached", i, state.IntervalDays)
		}
		if state.IntervalDays == 45 {
			reachedCap = true
		}
	}
	if !reachedCap {
		t.Fatal("expected interval to reach the 45-day cap within 1000 consecutive Easy grades")
	}
}
