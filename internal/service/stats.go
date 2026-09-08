package service

import (
	"context"
	"fmt"
)

// DifficultyCounts is how many logged problems fall into each LeetCode
// difficulty — a plain struct rather than a map so templates can access
// .Easy/.Medium/.Hard directly.
type DifficultyCounts struct {
	Easy   int
	Medium int
	Hard   int
}

// DayCount is how many problems are scheduled exactly DaysFromNow days
// out (1 = tomorrow, 2 = in two days, ...).
type DayCount struct {
	DaysFromNow int
	Count       int
}

// CountProblems is the total number of logged problems, regardless of
// review state — Home's "Total Tracked" stat.
func (s *Service) CountProblems(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM problems`).Scan(&count); err != nil {
		return 0, fmt.Errorf("service: count problems: %w", err)
	}
	return count, nil
}

// DifficultyBreakdown is how many logged problems fall into each
// difficulty — Home's "Difficulty Split" stat. This describes the
// problems themselves, not how well they've been graded (that's a
// different, deliberately separate axis — see FRONTEND.md).
func (s *Service) DifficultyBreakdown(ctx context.Context) (DifficultyCounts, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT difficulty, COUNT(*) FROM problems GROUP BY difficulty`)
	if err != nil {
		return DifficultyCounts{}, fmt.Errorf("service: difficulty breakdown: %w", err)
	}
	defer rows.Close()

	var counts DifficultyCounts
	for rows.Next() {
		var difficulty string
		var n int
		if err := rows.Scan(&difficulty, &n); err != nil {
			return DifficultyCounts{}, fmt.Errorf("service: scan difficulty breakdown: %w", err)
		}
		switch Difficulty(difficulty) {
		case DifficultyEasy:
			counts.Easy = n
		case DifficultyMedium:
			counts.Medium = n
		case DifficultyHard:
			counts.Hard = n
		}
	}
	return counts, rows.Err()
}

// CountDue is how many problems are due for review right now — feeds
// the nav's "N due today" pill, shown on every page. Deliberately a
// plain count rather than reusing RecommendDue, which does the extra
// joins/ordering the actual gradable list needs but this badge doesn't.
func (s *Service) CountDue(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM review_state WHERE next_review_date <= ?`, s.today()).Scan(&count); err != nil {
		return 0, fmt.Errorf("service: count due: %w", err)
	}
	return count, nil
}

// DueStats returns how many problems are due right now (next_review_date
// <= today) and, of those, how many are strictly overdue (< today),
// optionally scoped to one topic. Powers the Due page's stats row and
// its live refresh after grading via htmx — both need these counts
// independent of whichever page of results happens to be displayed,
// which is why this doesn't just derive them from RecommendDue's items.
func (s *Service) DueStats(ctx context.Context, topic *string) (due, overdue int, err error) {
	today := s.today()
	from := `FROM review_state rs JOIN problems p ON p.id = rs.problem_id`
	from, args := appendTopicJoin(from, nil, topic)

	query := `SELECT COUNT(*), COALESCE(SUM(CASE WHEN rs.next_review_date < ? THEN 1 ELSE 0 END), 0) ` +
		from + ` WHERE rs.next_review_date <= ?`
	queryArgs := append([]any{today}, args...)
	queryArgs = append(queryArgs, today)

	if err := s.db.QueryRowContext(ctx, query, queryArgs...).Scan(&due, &overdue); err != nil {
		return 0, 0, fmt.Errorf("service: due stats: %w", err)
	}
	return due, overdue, nil
}

// UpcomingByDay groups upcoming reviews by exact day-offset from today
// (1..days), zero-filled so every offset is always present — Home's
// "Review Load" sidebar. Purely descriptive, like RecommendUpcoming: it
// does not decide what's actionable, RecommendDue remains the only
// thing that does (SPEC.md §4).
func (s *Service) UpcomingByDay(ctx context.Context, days int) ([]DayCount, error) {
	today := s.now().In(s.loc)
	horizon := today.AddDate(0, 0, days).Format("2006-01-02")

	rows, err := s.db.QueryContext(ctx, `
		SELECT next_review_date, COUNT(*)
		FROM review_state
		WHERE next_review_date > ? AND next_review_date <= ?
		GROUP BY next_review_date`,
		s.today(), horizon,
	)
	if err != nil {
		return nil, fmt.Errorf("service: upcoming by day: %w", err)
	}
	defer rows.Close()

	countByDate := make(map[string]int)
	for rows.Next() {
		var date string
		var n int
		if err := rows.Scan(&date, &n); err != nil {
			return nil, fmt.Errorf("service: scan upcoming by day: %w", err)
		}
		countByDate[date] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make([]DayCount, days)
	for i := 0; i < days; i++ {
		daysFromNow := i + 1
		date := today.AddDate(0, 0, daysFromNow).Format("2006-01-02")
		result[i] = DayCount{DaysFromNow: daysFromNow, Count: countByDate[date]}
	}
	return result, nil
}
