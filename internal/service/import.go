package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
)

// BulkImportRow is one parsed row of a pasted import batch (SPEC.md §9).
// Validation (non-empty title, valid difficulty, at least one topic) is
// the caller's responsibility — httpapi's preview step surfaces those
// errors to the user before commit ever calls this.
type BulkImportRow struct {
	Title      string
	URL        string
	Difficulty Difficulty
	Topics     []string
}

// BulkImportResult summarizes what a bulk import actually did.
type BulkImportResult struct {
	Created int // new problems inserted
	Merged  int // rows that matched an existing problem, safely refreshed
	Skipped int // rows that matched a problem with real review progress — left untouched
}

// SlugStatus describes what bulk import (or its preview) will do with a
// pasted row's slug.
type SlugStatus int

const (
	SlugNew           SlugStatus = iota // no existing problem — will be created
	SlugSafeToRefresh                   // exists, but hasn't been graded beyond its own creation — safe to re-stagger
	SlugProtected                       // exists with real review history — bulk import will leave it untouched
)

// CheckSlug reports what bulk import would do with slug, without writing
// anything — used by the import preview to warn about rows that would
// otherwise silently reset real review progress (see BulkImportProblems).
func (s *Service) CheckSlug(ctx context.Context, slug string) (SlugStatus, error) {
	problemID, err := findProblemIDBySlug(ctx, s.db, slug)
	if err != nil {
		return SlugNew, err
	}
	if problemID == 0 {
		return SlugNew, nil
	}
	n, err := attemptCount(ctx, s.db, problemID)
	if err != nil {
		return SlugNew, err
	}
	if n > 1 {
		return SlugProtected, nil
	}
	return SlugSafeToRefresh, nil
}

func attemptCount(ctx context.Context, q querier, problemID int64) (int, error) {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE problem_id = ?`, problemID).Scan(&n); err != nil {
		return 0, fmt.Errorf("service: count attempts for problem %d: %w", problemID, err)
	}
	return n, nil
}

// DefaultTargetPerDay is used when a caller doesn't specify a target (0
// or negative) — a reasonable middle ground for a first-time self-hoster
// who hasn't thought about pacing yet. Exported so httpapi's import form
// can pre-fill the same value shown here, rather than keeping its own
// copy that has to be remembered to match this one.
const DefaultTargetPerDay = 5

// minStaggerDays keeps small batches spread out as before — without
// this, a 3-row import would compress to a 1-day window and lose the
// point of staggering at all.
const minStaggerDays = 14

// staggerWindowDays sizes the spread for BulkImportProblems from an
// actual daily-review target rather than a fixed day count, so a large
// batch doesn't dump more reviews per day than the target regardless of
// how many rows are in it (SPEC.md §9's "~2 weeks" was sized for a much
// smaller batch than a full solve-history import can be).
func staggerWindowDays(n, targetPerDay int) int {
	if targetPerDay <= 0 {
		targetPerDay = DefaultTargetPerDay
	}
	windowDays := (n + targetPerDay - 1) / targetPerDay // ceiling division
	if windowDays < minStaggerDays {
		windowDays = minStaggerDays
	}
	return windowDays
}

// BulkImportProblems imports rows as one atomic batch (SPEC.md §9):
//   - every row is graded a baseline Hard, logged as attempted now
//   - dedupe is keyed on slug, same as AddProblem — a row matching an
//     existing problem records another attempt against it rather than
//     duplicating, UNLESS that problem already has real review progress
//     beyond its own creation (more than one logged attempt) — in that
//     case it's left completely untouched, so re-pasting an old CSV
//     export can't silently reset honestly-earned progress back to a
//     fresh Hard grade. This isn't a perfect "was this a real manual
//     re-grade" signal (attempts don't record their source), but it
//     converges to a stable, once-refreshed state rather than drifting
//     on repeated re-imports, without a bigger schema change.
//   - next_review_date is staggered by import order rather than
//     defaulting every row to the same day, so a bulk import doesn't
//     dump the entire batch into "overdue" simultaneously — the spread
//     targets roughly targetPerDay new reviews per day (0/negative
//     falls back to defaultTargetPerDay), floored at minStaggerDays so
//     small batches still spread out reasonably
func (s *Service) BulkImportProblems(ctx context.Context, rows []BulkImportRow, targetPerDay int) (BulkImportResult, error) {
	var result BulkImportResult
	n := len(rows)
	if n == 0 {
		return result, nil
	}

	windowDays := staggerWindowDays(n, targetPerDay)
	now := s.now()
	today := s.Today()

	err := s.withTx(ctx, func(tx *sql.Tx) error {
		for i, row := range rows {
			if strings.TrimSpace(row.Title) == "" {
				return fmt.Errorf("service: bulk import row %d: title is empty", i+1)
			}
			if !row.Difficulty.valid() {
				return fmt.Errorf("service: bulk import row %d: invalid difficulty %q", i+1, row.Difficulty)
			}
			slug, err := extractSlug(row.URL)
			if err != nil {
				return fmt.Errorf("service: bulk import row %d: %w", i+1, err)
			}

			problemID, err := findProblemIDBySlug(ctx, tx, slug)
			if err != nil {
				return err
			}

			isNew := problemID == 0
			if isNew {
				problemID, err = insertProblem(ctx, tx, row.Title, canonicalURL(slug), row.Difficulty, slug)
				if err != nil {
					return err
				}
				if err := attachTopics(ctx, tx, problemID, row.Topics); err != nil {
					return err
				}
			} else {
				existingAttempts, err := attemptCount(ctx, tx, problemID)
				if err != nil {
					return err
				}
				if existingAttempts > 1 {
					result.Skipped++
					continue
				}
			}

			offsetDays := i * windowDays / n
			nextReviewDate := today.AddDate(0, 0, offsetDays)

			if err := recordAttempt(ctx, tx, problemID, scheduler.Hard, now); err != nil {
				return err
			}
			if _, err := recordReview(ctx, tx, s.loc, problemID, scheduler.Hard, now, &nextReviewDate); err != nil {
				return err
			}

			if isNew {
				result.Created++
			} else {
				result.Merged++
			}
		}
		return nil
	})
	if err != nil {
		return BulkImportResult{}, err
	}
	return result, nil
}
