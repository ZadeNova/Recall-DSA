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

//go:embed seed_topics.sql
var seedTopicsSQL string

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

	// One connection, so every read and write is serialized.
	//
	// This is not just a performance posture — it closes a correctness
	// hole busy_timeout cannot. Every write in the service layer reads
	// before it writes inside the same transaction (load review_state,
	// then upsert it; look up a slug, then insert). Go's BeginTx issues a
	// plain BEGIN, which in WAL mode takes a read snapshot first and only
	// tries to upgrade to a write lock at the first write. If another
	// connection committed in between, that upgrade fails immediately
	// with SQLITE_BUSY_SNAPSHOT — the busy handler is never consulted,
	// because no amount of waiting can make a stale snapshot valid. With
	// a single connection there is no second writer, so the upgrade can
	// never lose that race.
	//
	// Safe here because no query path opens a second connection while
	// holding the first: transactional work all goes through the *sql.Tx
	// itself, and the one place that queries after another query is
	// scanReviewItems, which fully drains its outer rows (releasing the
	// connection) before batch-loading topics. That's covered by
	// TestRecommendDue_NoConnectionPoolDeadlockUnderSingleConnection,
	// which runs under exactly this setting.
	conn.SetMaxOpenConns(1)

	if _, err := conn.Exec(schemaSQL); err != nil {
		conn.Close()
		return nil, fmt.Errorf("db: apply schema: %w", err)
	}

	if err := seedTopicsIfEmpty(conn); err != nil {
		conn.Close()
		return nil, err
	}

	return conn, nil
}

// seedTopicsIfEmpty runs seedTopicsSQL only the first time this database
// is ever opened (topics table still empty) — deliberately NOT run on
// every startup the way schema.sql is. Unlike schema.sql's tables/
// indexes, seeding isn't idempotent in the sense that matters here: if a
// user deliberately deletes a seeded topic via the Topics page and this
// ran unconditionally (e.g. via INSERT OR IGNORE) on every restart, that
// deletion would be silently undone the next time the app starts,
// because the row would no longer exist to conflict with. Gating on "is
// the table currently empty" only seeds a genuinely fresh database.
//
// One known edge case: a user who deletes every single topic (not just
// one) would see the full seed list return on next restart, since an
// empty table looks the same whether it's brand new or emptied by hand.
// Tracking "has this database ever been seeded" independent of the
// table's current contents would need a separate marker (e.g. a small
// metadata table) — not worth the extra schema for an edge case this
// narrow, when the ordinary case (delete the few topics you don't want)
// already works correctly.
func seedTopicsIfEmpty(conn *sql.DB) error {
	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM topics`).Scan(&count); err != nil {
		return fmt.Errorf("db: check existing topics: %w", err)
	}
	if count > 0 {
		return nil
	}
	if _, err := conn.Exec(seedTopicsSQL); err != nil {
		return fmt.Errorf("db: seed topics: %w", err)
	}
	return nil
}
