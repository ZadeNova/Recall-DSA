package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: unexpected err: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestOpen_CreatesAllTables(t *testing.T) {
	conn := openTestDB(t)

	for _, table := range []string{"problems", "attempts", "review_state", "topics", "problem_topics"} {
		var name string
		err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %q: %v", table, err)
		}
	}
}

func TestOpen_SeedsStandardTopics(t *testing.T) {
	conn := openTestDB(t)

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM topics`).Scan(&count); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if count != 35 {
		t.Errorf("topic count = %d, want 35 (NeetCode 150's 18 categories + 17 LeetCode gap-fill tags)", count)
	}
}

// TestOpen_DeletedSeedTopicDoesNotResurrect guards against a real
// regression: seeding once lived in schema.sql as INSERT OR IGNORE,
// which ran on every Open (schema.sql is meant to be re-applied every
// startup) — so deleting a seeded topic via the Topics page, then
// restarting the app, silently brought it back, because an absent row
// no longer conflicts with OR IGNORE. Seeding must run only once, when
// the table is first created, and respect a user's deletion afterward.
func TestOpen_DeletedSeedTopicDoesNotResurrect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: unexpected err: %v", err)
	}
	if _, err := first.Exec(`DELETE FROM topics WHERE name = ?`, "Database"); err != nil {
		t.Fatalf("delete topic: unexpected err: %v", err)
	}
	var countAfterDelete int
	if err := first.QueryRow(`SELECT COUNT(*) FROM topics`).Scan(&countAfterDelete); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if countAfterDelete != 34 {
		t.Fatalf("count after deleting one topic = %d, want 34", countAfterDelete)
	}
	first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: unexpected err: %v", err)
	}
	defer second.Close()

	var exists bool
	if err := second.QueryRow(`SELECT EXISTS(SELECT 1 FROM topics WHERE name = ?)`, "Database").Scan(&exists); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if exists {
		t.Error("deleted seed topic \"Database\" reappeared after restart — seeding must only run once, not on every Open")
	}

	var countAfterReopen int
	if err := second.QueryRow(`SELECT COUNT(*) FROM topics`).Scan(&countAfterReopen); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if countAfterReopen != 34 {
		t.Errorf("count after restart = %d, want 34 (the deletion should persist)", countAfterReopen)
	}
}

func TestOpen_ForeignKeysEnabled(t *testing.T) {
	conn := openTestDB(t)

	var enabled int
	if err := conn.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if enabled != 1 {
		t.Errorf("PRAGMA foreign_keys = %d, want 1 (enabled) — required for ON DELETE CASCADE to actually fire", enabled)
	}
}

// TestOpen_SerializesAccessOnOneConnection pins the single-connection
// pool. It is not a tuning preference: every write in the service layer
// reads before it writes inside the same transaction, and Go's BeginTx
// issues a plain BEGIN, which in WAL mode takes a read snapshot and only
// upgrades to a write lock at the first write. A second connection that
// commits in that window makes the upgrade fail immediately with
// SQLITE_BUSY_SNAPSHOT, which busy_timeout cannot retry — waiting can
// never make a stale snapshot valid. One connection means there is no
// second writer to lose that race to.
func TestOpen_SerializesAccessOnOneConnection(t *testing.T) {
	conn := openTestDB(t)
	if got := conn.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1 (read-then-write transactions rely on there being no second writer)", got)
	}
}

func hasPausedAtColumn(t *testing.T, conn *sql.DB) bool {
	t.Helper()
	var exists bool
	err := conn.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('review_state') WHERE name = 'paused_at')`).Scan(&exists)
	if err != nil {
		t.Fatalf("check paused_at column: %v", err)
	}
	return exists
}

func TestOpen_FreshDBHasPausedAtColumn(t *testing.T) {
	conn := openTestDB(t)
	if !hasPausedAtColumn(t, conn) {
		t.Error("fresh database is missing review_state.paused_at")
	}
}

// TestOpen_AddsPausedAtToExistingDB covers the real deployment path: a
// database created before pause/unpause shipped. CREATE TABLE IF NOT
// EXISTS is a no-op against it, so the column has to be added by
// ensurePausedAtColumn — and existing rows must read as active (NULL).
func TestOpen_AddsPausedAtToExistingDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open old db: %v", err)
	}
	_, err = old.Exec(`
		CREATE TABLE problems (
			id INTEGER PRIMARY KEY, title TEXT NOT NULL, url TEXT NOT NULL,
			difficulty TEXT NOT NULL CHECK (difficulty IN ('Easy', 'Medium', 'Hard')),
			slug TEXT NOT NULL UNIQUE
		);
		CREATE TABLE review_state (
			problem_id INTEGER PRIMARY KEY REFERENCES problems (id) ON DELETE CASCADE,
			ease_factor REAL NOT NULL, interval_days INTEGER NOT NULL, repetitions INTEGER NOT NULL,
			next_review_date TEXT NOT NULL,
			last_grade TEXT NOT NULL CHECK (last_grade IN ('Failed', 'Hard', 'Good', 'Easy')),
			last_reviewed_at TEXT NOT NULL
		);
		INSERT INTO problems (id, title, url, difficulty, slug)
			VALUES (1, 'Two Sum', 'https://leetcode.com/problems/two-sum/', 'Easy', 'two-sum');
		INSERT INTO review_state VALUES (1, 2.5, 5, 1, '2026-10-01', 'Good', '2026-09-26T00:00:00Z');`)
	if err != nil {
		t.Fatalf("seed old db: %v", err)
	}
	old.Close()

	conn, err := Open(path)
	if err != nil {
		t.Fatalf("Open on pre-pause database: %v", err)
	}
	defer conn.Close()

	if !hasPausedAtColumn(t, conn) {
		t.Fatal("paused_at column was not added to an existing review_state table")
	}
	var pausedAt sql.NullString
	if err := conn.QueryRow(`SELECT paused_at FROM review_state WHERE problem_id = 1`).Scan(&pausedAt); err != nil {
		t.Fatalf("read existing row: %v", err)
	}
	if pausedAt.Valid {
		t.Errorf("existing row paused_at = %q, want NULL (active)", pausedAt.String)
	}
}

func TestOpen_PausedAtMigrationIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	for i := 1; i <= 2; i++ {
		conn, err := Open(path)
		if err != nil {
			t.Fatalf("Open #%d: %v", i, err)
		}
		if !hasPausedAtColumn(t, conn) {
			t.Errorf("Open #%d: paused_at missing", i)
		}
		conn.Close()
	}
}
