// Package service is the boundary between HTTP handlers and the database
// (SPEC.md §6): handlers stay thin, business logic and transactions live
// here. It composes internal/db (storage) and internal/scheduler (the
// pure SM-2 algorithm).
package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrDuplicateTopic and ErrDuplicateSlug are returned (wrapped, so
// errors.Is still matches) when a write would violate a UNIQUE
// constraint that corresponds to a routine, expected user mistake — a
// topic name or problem URL that already exists — rather than a genuine
// system failure. Callers can format these directly: their Error() text
// is plain English, not SQL vocabulary, because CreateTopic/RenameTopic/
// UpdateProblem detect this specific case (see isUniqueConstraintErr)
// and translate it before it reaches the HTTP layer, instead of letting
// whatever the SQLite driver says leak straight into the page — a
// duplicate name is something a user does routinely, not a bug.
var (
	ErrDuplicateTopic = errors.New("a topic with that name already exists")
	ErrDuplicateSlug  = errors.New("a problem with that URL already exists")
)

// isUniqueConstraintErr reports whether err is specifically a UNIQUE
// constraint violation, checked by SQLite result code (2067) rather than
// matching on the error's text — the message format isn't a stable API
// across driver versions, but the numeric code is part of SQLite's own
// documented result-code table.
func isUniqueConstraintErr(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}

// requireRowsAffected returns a wrapped sql.ErrNoRows if res reports zero
// rows affected — the shared check behind every ID-keyed UPDATE/DELETE
// in this package (UpdateProblem, DeleteProblem, RenameTopic,
// DeleteTopic), so acting on an ID that doesn't exist fails the same way
// everywhere instead of silently "succeeding" at doing nothing.
func requireRowsAffected(res sql.Result, notFoundMsg string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("service: check rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%s: %w", notFoundMsg, sql.ErrNoRows)
	}
	return nil
}

// querier is satisfied by both *sql.DB and *sql.Tx, so the same
// query/exec helpers work whether called standalone or inside a
// transaction shared with other writes.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Service is the application's service layer. loc is the fixed timezone
// SPEC.md §4 requires "today" and next_review_date to be computed
// against, independent of the host machine's clock.
type Service struct {
	db  *sql.DB
	loc *time.Location
	now func() time.Time
}

// New constructs a Service. loc should be loaded by the caller (e.g.
// time.LoadLocation("Asia/Singapore")) — this package makes no timezone
// assumptions of its own, so a self-hoster can configure their own.
func New(db *sql.DB, loc *time.Location) *Service {
	return &Service{db: db, loc: loc, now: time.Now}
}

// withTx runs fn inside a transaction, committing on success and rolling
// back on any error fn returns.
func (s *Service) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("service: begin tx: %w", err)
	}

	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("service: commit tx: %w", err)
	}
	return nil
}

// today returns "now" in the service's configured timezone, as an
// ISO8601 date string — matching how next_review_date is stored, so
// lexical comparison in the due-query is correct (SPEC.md §4).
func (s *Service) today() string {
	return s.now().In(s.loc).Format("2006-01-02")
}

// Today is today's calendar date at midnight in the service's
// configured timezone — exported so callers outside the package (e.g.
// the Library page, computing a day-offset display against
// DueItem.NextReviewDate) can do date math without duplicating the
// timezone handling that already lives here.
func (s *Service) Today() time.Time {
	now := s.now().In(s.loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.loc)
}
