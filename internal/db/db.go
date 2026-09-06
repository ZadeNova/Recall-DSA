// Package db owns opening the SQLite connection and applying the schema
// (SPEC.md §3). It has no knowledge of the scheduler or business rules —
// that belongs to the service layer built on top of it (SPEC.md §6).
package db

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Open opens (creating if needed) the SQLite database at path, configures
// it per SPEC.md §10 — WAL mode, foreign-key enforcement, and a
// busy_timeout as cheap insurance against an incidental overlapping
// request from the two personal devices, not real concurrency control —
// and applies the schema idempotently.
//
// Foreign-key enforcement matters concretely here: SQLite disables it by
// default even when ON DELETE CASCADE is declared in the schema, which
// would silently break the cascading delete SPEC.md §9 requires.
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		path,
	)

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}

	if _, err := conn.Exec(schemaSQL); err != nil {
		conn.Close()
		return nil, fmt.Errorf("db: apply schema: %w", err)
	}

	return conn, nil
}
