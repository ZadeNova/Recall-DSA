// Package service is the boundary between HTTP handlers and the database
// (SPEC.md §6): handlers stay thin, business logic and transactions live
// here. It composes internal/db (storage) and internal/scheduler (the
// pure SM-2 algorithm).
package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

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
