package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// PauseProblems takes the given problems out of active rotation
// (NEW_FEATURES.md §1) and returns how many were newly paused.
//
// Already-paused problems are skipped (paused_at IS NULL), so re-pausing
// keeps the original timestamp and isn't counted. Unknown and duplicate
// ids are harmless for the same reason: they simply don't match.
// Attempt history and scheduler state are untouched.
func (s *Service) PauseProblems(ctx context.Context, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	marks, args := sqlInList(ids)
	args = append([]any{s.now().UTC().Format(time.RFC3339)}, args...)

	res, err := s.db.ExecContext(ctx,
		`UPDATE review_state SET paused_at = ? WHERE problem_id IN (`+marks+`) AND paused_at IS NULL`, args...)
	if err != nil {
		return 0, fmt.Errorf("service: pause problems: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("service: pause problems rows affected: %w", err)
	}
	return int(n), nil
}

// UnpauseProblems puts paused problems back into rotation and returns
// how many were unpaused (NEW_FEATURES.md §2).
//
// Only problems that are actually paused are touched — an active problem
// in the selection is left alone, since giving it a new date would
// silently postpone its review.
//
// Ease, interval and repetitions are kept as they were. Next-review dates
// are staggered so a batch doesn't all land on one day: problems are
// ordered most-overdue-first and spread over ceil(n / targetPerDay) days
// starting tomorrow (unlike bulk import, with no 14-day minimum — three
// problems shouldn't be spread over two weeks). Each problem gets the
// LATER of its existing next_review_date and its slot, so a short pause
// never pulls a review forward: grading before it's due inflates ease on
// a false signal. After a long pause the old date is in the past and the
// slot wins.
func (s *Service) UnpauseProblems(ctx context.Context, ids []int64, targetPerDay int) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	var unpaused int
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		paused, err := loadPausedForUnpause(ctx, tx, ids)
		if err != nil {
			return err
		}
		n := len(paused)
		if n == 0 {
			return nil
		}
		windowDays := staggerWindowDays(n, targetPerDay, 1)
		today := s.Today()

		for i, r := range paused {
			// next_review_date is YYYY-MM-DD, so string comparison is date comparison.
			slot := today.AddDate(0, 0, 1+i*windowDays/n).Format("2006-01-02")
			next := r.nextReview
			if slot > next {
				next = slot
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE review_state SET paused_at = NULL, next_review_date = ? WHERE problem_id = ?`,
				next, r.id); err != nil {
				return fmt.Errorf("service: unpause problem %d: %w", r.id, err)
			}
		}
		unpaused = n
		return nil
	})
	if err != nil {
		return 0, err
	}
	return unpaused, nil
}

type pausedRow struct {
	id         int64
	nextReview string
}

// loadPausedForUnpause reads which of ids are actually paused, most
// overdue first. It returns a fully read slice (rows closed) so the caller
// can run its UPDATEs on the same transaction's connection afterwards.
func loadPausedForUnpause(ctx context.Context, tx *sql.Tx, ids []int64) ([]pausedRow, error) {
	marks, args := sqlInList(ids)
	rows, err := tx.QueryContext(ctx,
		`SELECT problem_id, next_review_date FROM review_state
		 WHERE problem_id IN (`+marks+`) AND paused_at IS NOT NULL
		 ORDER BY next_review_date ASC, problem_id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("service: load paused problems: %w", err)
	}
	defer rows.Close()

	var paused []pausedRow
	for rows.Next() {
		var r pausedRow
		if err := rows.Scan(&r.id, &r.nextReview); err != nil {
			return nil, fmt.Errorf("service: scan paused problem: %w", err)
		}
		paused = append(paused, r)
	}
	return paused, rows.Err()
}
