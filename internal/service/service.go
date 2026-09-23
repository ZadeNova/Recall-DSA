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
	"strings"
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
func New(db *sql.DB, loc *time.Location, opts ...Option) *Service {
	s := &Service{db: db, loc: loc, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Option configures a Service at construction.
type Option func(*Service)

// WithClock replaces the source of "now". Everything date-dependent in
// the app — which problems are due, what next_review_date a grade lands
// on, the day-offsets on the Library page — derives from it, so pinning
// it makes all of that testable, including from packages outside this
// one (SPEC.md §4 calls the day-boundary behavior out as
// correctness-critical, and it can only be tested by controlling the
// clock).
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// Now is the service's current time. Callers that need to timestamp an
// action (e.g. httpapi's grade and add-problem handlers, filling in
// AddProblemInput.At) must use this rather than time.Now directly, so
// there is exactly one clock in the application and pinning it via
// WithClock actually pins everything.
func (s *Service) Now() time.Time {
	return s.now()
}

// withTx runs fn inside a transaction, committing on success and rolling
// back on any error fn returns — or on a panic, which is rolled back
// before the panic continues unwinding. Without that, a panic would
// escape with the transaction still open, and since net/http recovers
// per-request the process would survive while never returning that
// connection to the pool. The pool holds exactly one connection (see
// db.Open), so a single leak would wedge every subsequent request rather
// than merely degrading throughput.
func (s *Service) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("service: begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
	}()

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

// sqlInList returns "?,?,?" for vals, plus vals as query args, for a
// `WHERE x IN (...)` clause. Every caller passes a bounded list (one page
// of rows, one pasted import batch, or a bulk request capped by the
// handler), so it never needs chunking under SQLite's parameter limit.
func sqlInList[T any](vals []T) (string, []any) {
	marks := make([]string, len(vals))
	args := make([]any, len(vals))
	for i, v := range vals {
		marks[i] = "?"
		args[i] = v
	}
	return strings.Join(marks, ","), args
}
