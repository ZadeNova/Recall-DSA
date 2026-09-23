package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
)

// recordAttempt appends a row to the append-only attempts log. It does
// not touch review_state — see RecordReview, which is what a grading
// action should actually call (SPEC.md §2: grading is a single, unified
// action, not two separate steps a caller must remember to compose).
// Deliberately unexported: every real caller needs both attempt-logging
// and review_state advanced together, so there's no legitimate reason to
// call this alone from outside the package.
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
		rs, err := recordReview(ctx, tx, s.loc, problemID, grade, at, nil)
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
//
// nextReviewDateOverride, if non-nil, replaces the normally-computed
// at+interval date. Bulk import uses this to stagger initial review dates
// across ~2 weeks (SPEC.md §9) while still logging every row's attempt as
// graded "now" — the schedule is spread, not the audit trail.
func recordReview(ctx context.Context, q querier, loc *time.Location, problemID int64, grade scheduler.Grade, at time.Time, nextReviewDateOverride *time.Time) (ReviewState, error) {
	current, err := loadReviewState(ctx, q, problemID)
	if err != nil {
		return ReviewState{}, err
	}

	next, err := current.Apply(grade)
	if err != nil {
		return ReviewState{}, fmt.Errorf("service: apply grade for problem %d: %w", problemID, err)
	}

	nextReviewDate := at.In(loc).AddDate(0, 0, next.IntervalDays)
	if nextReviewDateOverride != nil {
		nextReviewDate = *nextReviewDateOverride
	}
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

// activeOnly is the one definition of "in rotation", shared by every query
// that lists or counts due work (RecommendDue, RecommendUpcoming, CountDue,
// DueStats, UpcomingByDay). If one of them missed it, its count would
// disagree with the list next to it. All of them alias review_state as rs.
const activeOnly = "rs.paused_at IS NULL"

// RecommendDue is the entire recommendation engine (SPEC.md §4): due
// review_state rows, most overdue first, optionally filtered to one
// topic, paginated. "Today" is computed in the service's configured
// timezone, not the host clock's. Returns the requested page plus the
// total count matching the filter (ignoring limit/offset), for
// pagination controls and for stats that need the true total.
func (s *Service) RecommendDue(ctx context.Context, topic *string, limit, offset int) ([]DueItem, int, error) {
	from := `FROM review_state rs JOIN problems p ON p.id = rs.problem_id`
	from, args := appendTopicJoin(from, nil, topic)

	where := ` WHERE rs.next_review_date <= ? AND ` + activeOnly
	args = append(args, s.today())

	return s.queryReviewItems(ctx, from, where, dueOrderBy, args, limit, offset)
}

// RecommendUpcoming returns problems due after today but within the next
// `days` days, soonest first, optionally filtered to one topic (matching
// RecommendDue's filter, so a single topic selection can drive both
// sections of the /due page together) and paginated — a read-only
// glance ahead, not a recommendation: SPEC.md §4's due-query
// (RecommendDue) is the only thing that decides what's actionable right
// now. Returns the requested page plus the total count matching the
// filter (ignoring limit/offset).
func (s *Service) RecommendUpcoming(ctx context.Context, topic *string, days, limit, offset int) ([]DueItem, int, error) {
	horizon := s.now().In(s.loc).AddDate(0, 0, days).Format("2006-01-02")

	from := `FROM review_state rs JOIN problems p ON p.id = rs.problem_id`
	from, args := appendTopicJoin(from, nil, topic)

	where := ` WHERE rs.next_review_date > ? AND rs.next_review_date <= ? AND ` + activeOnly
	args = append(args, s.today(), horizon)

	return s.queryReviewItems(ctx, from, where, dueOrderBy, args, limit, offset)
}

// dueOrderBy orders both due-queue views: most overdue first, then by
// problem id as a tiebreaker.
//
// The tiebreaker is load-bearing, not cosmetic. next_review_date is a
// date, not a timestamp, so ties are the normal case rather than an edge
// case — a bulk import deliberately clusters roughly targetPerDay
// problems onto each identical date (see BulkImportProblems), and an
// overdue backlog piles up the same way. SQL guarantees no ordering
// among rows tied on every ORDER BY term, so without this, paging is two
// independent queries whose tied rows may come back in different
// orders: page 2 could repeat a problem already shown on page 1 while
// another never appears on any page at all. Silently dropping a due
// problem breaks the one guarantee this tool exists to provide, so the
// sort has to be total. The topic-filtered variant of this query is the
// one where that's a live risk rather than a theoretical one — it sorts
// through a temp b-tree (EXPLAIN QUERY PLAN: "USE TEMP B-TREE FOR ORDER
// BY"), which carries no stability guarantee at all.
//
// Written as rs.problem_id rather than the equal p.id deliberately:
// problem_id is review_state's INTEGER PRIMARY KEY, hence the implicit
// rowid of idx_review_state_next_review_date, so the index already
// yields exactly this order and the tiebreaker is free. Ordering by the
// identical p.id instead makes SQLite add "USE TEMP B-TREE FOR LAST TERM
// OF ORDER BY" — same rows, same order, a sort it doesn't need.
const dueOrderBy = "rs.next_review_date ASC, rs.problem_id ASC"

// queryReviewItems is the query shape shared by RecommendDue,
// RecommendUpcoming, and ListLibrary (internal/service/library.go): a
// COUNT(DISTINCT p.id) for the total, then the same 11-column paginated
// SELECT against review_state JOIN problems, scanned via
// scanReviewItems. Only from/where/orderBy differ between callers —
// collapsing the rest into one place means the column list (and its
// correspondence to scanReviewItems's Scan args) exists exactly once,
// instead of three copies that could silently drift out of sync with
// each other and with the scan.
// limit <= 0 means "no limit" (return every matching row), not "limit of
// zero rows" — SQL's own LIMIT 0 would otherwise silently return nothing
// while total still reports every matching row, which reads as a bug
// (0 items but a non-zero total) rather than "no pagination requested."
func (s *Service) queryReviewItems(ctx context.Context, from, where, orderBy string, args []any, limit, offset int) ([]DueItem, int, error) {
	var total int
	countQuery := `SELECT COUNT(DISTINCT p.id) ` + from + where
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("service: count review items: %w", err)
	}

	query := `SELECT p.id, p.title, p.url, p.difficulty, p.slug,
		       rs.ease_factor, rs.interval_days, rs.repetitions,
		       rs.next_review_date, rs.last_grade, rs.last_reviewed_at, rs.paused_at ` +
		from + where + ` ORDER BY ` + orderBy
	pageArgs := append([]any{}, args...)
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		pageArgs = append(pageArgs, limit, offset)
	}

	rows, err := s.db.QueryContext(ctx, query, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("service: query review items: %w", err)
	}
	defer rows.Close()

	items, err := s.scanReviewItems(ctx, rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// appendTopicJoin adds the topic-filter JOIN clause shared by every
// topic-filterable query — RecommendDue, RecommendUpcoming, ListLibrary,
// and DueStats — when topic is non-nil, so their join semantics can't
// drift apart (the Due page in particular relies on its list and its
// stats row scoping a topic filter identically). Returns the (possibly
// unmodified) query and args with the topic value appended if
// applicable.
func appendTopicJoin(query string, args []any, topic *string) (string, []any) {
	if topic == nil {
		return query, args
	}
	query += `
		JOIN problem_topics pt ON pt.problem_id = p.id
		JOIN topics t ON t.id = pt.topic_id AND t.name = ?`
	return query, append(args, *topic)
}

// scanReviewItems is the shared row-scanning logic behind every caller
// of queryReviewItems (RecommendDue, RecommendUpcoming, ListLibrary) —
// they select the same columns, differing only in from/where/orderBy.
// Topics are batch-loaded in one query AFTER the
// outer rows are fully drained (see attachTopicsToItems) rather than one
// query per row while rows is still open — both to avoid an N+1 query
// per page, and because a per-row query on an open outer cursor would
// deadlock if this ever ran under sql.DB.SetMaxOpenConns(1) (standard
// advice for a single-writer SQLite app): the inner query would wait
// forever for a connection the outer, unfinished iteration is still
// holding.
func (s *Service) scanReviewItems(ctx context.Context, rows *sql.Rows) ([]DueItem, error) {
	var items []DueItem
	for rows.Next() {
		var item DueItem
		var nextReviewDateStr, lastReviewedAtStr string
		var pausedAtStr sql.NullString
		if err := rows.Scan(
			&item.ID, &item.Title, &item.URL, &item.Difficulty, &item.Slug,
			&item.EaseFactor, &item.IntervalDays, &item.Repetitions,
			&nextReviewDateStr, &item.LastGrade, &lastReviewedAtStr, &pausedAtStr,
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
		if pausedAtStr.Valid {
			pausedAt, err := time.Parse(time.RFC3339, pausedAtStr.String)
			if err != nil {
				return nil, fmt.Errorf("service: parse paused_at: %w", err)
			}
			item.PausedAt = &pausedAt
		}

		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := s.attachTopicsToItems(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}

// attachTopicsToItems batch-loads topics for every item in one query
// (bounded by the app's own pagination cap — at most 50 items per page,
// see internal/httpapi/pagination.go — well under SQLite's parameter
// limit, so no chunking is needed) instead of one query per item.
// ORDER BY t.name on the batched query keeps each individual item's
// topics alphabetically sorted, matching the old per-item query's
// ordering: a subset of a globally name-sorted sequence is itself
// name-sorted, regardless of how rows for different items interleave.
func (s *Service) attachTopicsToItems(ctx context.Context, items []DueItem) error {
	if len(items) == 0 {
		return nil
	}

	ids := make([]int64, len(items))
	indexByID := make(map[int64]int, len(items))
	for i, item := range items {
		ids[i] = item.ID
		indexByID[item.ID] = i
	}
	marks, args := sqlInList(ids)

	rows, err := s.db.QueryContext(ctx, `
		SELECT pt.problem_id, t.name
		FROM problem_topics pt
		JOIN topics t ON t.id = pt.topic_id
		WHERE pt.problem_id IN (`+marks+`)
		ORDER BY t.name`, args...)
	if err != nil {
		return fmt.Errorf("service: batch load topics: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var problemID int64
		var name string
		if err := rows.Scan(&problemID, &name); err != nil {
			return fmt.Errorf("service: scan batched topic: %w", err)
		}
		idx := indexByID[problemID]
		items[idx].Topics = append(items[idx].Topics, name)
	}
	return rows.Err()
}
