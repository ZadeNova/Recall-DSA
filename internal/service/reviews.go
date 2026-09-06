package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
)

// RecordAttempt appends a row to the append-only attempts log. It does
// not touch review_state — see RecordReview, which is what a grading
// action should actually call (SPEC.md §2: grading is a single, unified
// action, not two separate steps a caller must remember to compose).
func (s *Service) RecordAttempt(ctx context.Context, problemID int64, grade scheduler.Grade, at time.Time) error {
	return recordAttempt(ctx, s.db, problemID, grade, at)
}

func recordAttempt(ctx context.Context, q querier, problemID int64, grade scheduler.Grade, at time.Time) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO attempts (problem_id, attempted_at, grade) VALUES (?, ?, ?)`,
		problemID, at.UTC().Format(time.RFC3339), grade,
	)
	if err != nil {
		return fmt.Errorf("service: record attempt for problem %d: %w", problemID, err)
	}
	return nil
}

// RecordReview grades problemID: it logs the attempt AND advances the
// SM-2 review_state atomically, in one transaction. This is the single
// method a caller needs to grade either a brand-new problem's first
// attempt or an already-reviewed problem (SPEC.md §2).
func (s *Service) RecordReview(ctx context.Context, problemID int64, grade scheduler.Grade, at time.Time) (ReviewState, error) {
	var result ReviewState
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := recordAttempt(ctx, tx, problemID, grade, at); err != nil {
			return err
		}
		rs, err := recordReview(ctx, tx, s.loc, problemID, grade, at)
		if err != nil {
			return err
		}
		result = rs
		return nil
	})
	return result, err
}

// recordReview applies the scheduler to problemID's current review_state
// (or a fresh one, if this is its first grade) and upserts the result.
// It deliberately does not log the attempt itself — every caller in this
// project needs both together, so that composition lives in
// RecordReview/AddProblem, not duplicated here.
func recordReview(ctx context.Context, q querier, loc *time.Location, problemID int64, grade scheduler.Grade, at time.Time) (ReviewState, error) {
	current, err := loadReviewState(ctx, q, problemID)
	if err != nil {
		return ReviewState{}, err
	}

	next, err := current.Apply(grade)
	if err != nil {
		return ReviewState{}, fmt.Errorf("service: apply grade for problem %d: %w", problemID, err)
	}

	nextReviewDate := at.In(loc).AddDate(0, 0, next.IntervalDays)
	lastReviewedAt := at.UTC()

	_, err = q.ExecContext(ctx, `
		INSERT INTO review_state (problem_id, ease_factor, interval_days, repetitions, next_review_date, last_grade, last_reviewed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(problem_id) DO UPDATE SET
			ease_factor = excluded.ease_factor,
			interval_days = excluded.interval_days,
			repetitions = excluded.repetitions,
			next_review_date = excluded.next_review_date,
			last_grade = excluded.last_grade,
			last_reviewed_at = excluded.last_reviewed_at`,
		problemID, next.EaseFactor, next.IntervalDays, next.Repetitions,
		nextReviewDate.Format("2006-01-02"), grade, lastReviewedAt.Format(time.RFC3339),
	)
	if err != nil {
		return ReviewState{}, fmt.Errorf("service: upsert review_state for problem %d: %w", problemID, err)
	}

	return ReviewState{
		EaseFactor:     next.EaseFactor,
		IntervalDays:   next.IntervalDays,
		Repetitions:    next.Repetitions,
		NextReviewDate: nextReviewDate,
		LastGrade:      grade,
		LastReviewedAt: lastReviewedAt,
	}, nil
}

func loadReviewState(ctx context.Context, q querier, problemID int64) (scheduler.ReviewState, error) {
	var rs scheduler.ReviewState
	err := q.QueryRowContext(ctx,
		`SELECT ease_factor, interval_days, repetitions FROM review_state WHERE problem_id = ?`, problemID,
	).Scan(&rs.EaseFactor, &rs.IntervalDays, &rs.Repetitions)
	if errors.Is(err, sql.ErrNoRows) {
		return scheduler.NewReviewState(), nil
	}
	if err != nil {
		return scheduler.ReviewState{}, fmt.Errorf("service: load review_state for problem %d: %w", problemID, err)
	}
	return rs, nil
}

// RecommendDue is the entire recommendation engine (SPEC.md §4): due
// review_state rows, most overdue first, optionally filtered to one
// topic. "Today" is computed in the service's configured timezone, not
// the host clock's.
func (s *Service) RecommendDue(ctx context.Context, topic *string) ([]DueItem, error) {
	query := `
		SELECT p.id, p.title, p.url, p.difficulty, p.slug,
		       rs.ease_factor, rs.interval_days, rs.repetitions,
		       rs.next_review_date, rs.last_grade, rs.last_reviewed_at
		FROM review_state rs
		JOIN problems p ON p.id = rs.problem_id`
	query, args := appendTopicJoin(query, nil, topic)

	query += `
		WHERE rs.next_review_date <= ?
		ORDER BY rs.next_review_date ASC`
	args = append(args, s.today())

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("service: recommend due: %w", err)
	}
	defer rows.Close()

	return s.scanReviewItems(ctx, rows)
}

// RecommendUpcoming returns problems due after today but within the next
// `days` days, soonest first, optionally filtered to one topic (matching
// RecommendDue's filter, so a single topic selection can drive both
// sections of the /due page together) — a read-only glance ahead, not a
// recommendation: SPEC.md §4's due-query (RecommendDue) is the only thing
// that decides what's actionable right now.
func (s *Service) RecommendUpcoming(ctx context.Context, topic *string, days int) ([]DueItem, error) {
	horizon := s.now().In(s.loc).AddDate(0, 0, days).Format("2006-01-02")

	query := `
		SELECT p.id, p.title, p.url, p.difficulty, p.slug,
		       rs.ease_factor, rs.interval_days, rs.repetitions,
		       rs.next_review_date, rs.last_grade, rs.last_reviewed_at
		FROM review_state rs
		JOIN problems p ON p.id = rs.problem_id`
	query, args := appendTopicJoin(query, nil, topic)

	query += `
		WHERE rs.next_review_date > ? AND rs.next_review_date <= ?
		ORDER BY rs.next_review_date ASC`
	args = append(args, s.today(), horizon)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("service: recommend upcoming: %w", err)
	}
	defer rows.Close()

	return s.scanReviewItems(ctx, rows)
}

// appendTopicJoin adds the topic-filter JOIN clause shared by RecommendDue
// and RecommendUpcoming when topic is non-nil, so the two queries' join
// semantics can't drift apart. Returns the (possibly unmodified) query
// and args with the topic value appended if applicable.
func appendTopicJoin(query string, args []any, topic *string) (string, []any) {
	if topic == nil {
		return query, args
	}
	query += `
		JOIN problem_topics pt ON pt.problem_id = p.id
		JOIN topics t ON t.id = pt.topic_id AND t.name = ?`
	return query, append(args, *topic)
}

// scanReviewItems is the shared row-scanning logic for RecommendDue and
// RecommendUpcoming — both select the same columns, just with a
// different WHERE clause.
func (s *Service) scanReviewItems(ctx context.Context, rows *sql.Rows) ([]DueItem, error) {
	var items []DueItem
	for rows.Next() {
		var item DueItem
		var nextReviewDateStr, lastReviewedAtStr string
		if err := rows.Scan(
			&item.ID, &item.Title, &item.URL, &item.Difficulty, &item.Slug,
			&item.EaseFactor, &item.IntervalDays, &item.Repetitions,
			&nextReviewDateStr, &item.LastGrade, &lastReviewedAtStr,
		); err != nil {
			return nil, fmt.Errorf("service: scan review item: %w", err)
		}

		var err error
		item.NextReviewDate, err = time.ParseInLocation("2006-01-02", nextReviewDateStr, s.loc)
		if err != nil {
			return nil, fmt.Errorf("service: parse next_review_date: %w", err)
		}
		item.LastReviewedAt, err = time.Parse(time.RFC3339, lastReviewedAtStr)
		if err != nil {
			return nil, fmt.Errorf("service: parse last_reviewed_at: %w", err)
		}

		topics, err := loadProblemTopics(ctx, s.db, item.ID)
		if err != nil {
			return nil, err
		}
		item.Topics = topics

		items = append(items, item)
	}
	return items, rows.Err()
}
