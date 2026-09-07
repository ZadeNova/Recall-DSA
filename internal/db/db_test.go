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

func TestOpen_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: unexpected err: %v", err)
	}
	first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: unexpected err: %v", err)
	}
	defer second.Close()

	var count int
	if err := second.QueryRow(`SELECT COUNT(*) FROM topics`).Scan(&count); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if count != 35 {
		t.Errorf("topic count after reopen = %d, want 35 (re-applying schema must not duplicate seed rows)", count)
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

func TestDeleteProblem_CascadesToAttemptsAndReviewState(t *testing.T) {
	conn := openTestDB(t)

	res, err := conn.Exec(
		`INSERT INTO problems (title, url, difficulty, slug) VALUES ('Two Sum', 'https://leetcode.com/problems/two-sum/', 'Easy', 'two-sum')`,
	)
	if err != nil {
		t.Fatalf("insert problem: unexpected err: %v", err)
	}
	problemID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	if _, err := conn.Exec(
		`INSERT INTO attempts (problem_id, attempted_at, grade) VALUES (?, '2026-01-01T00:00:00Z', 'Good')`,
		problemID,
	); err != nil {
		t.Fatalf("insert attempt: unexpected err: %v", err)
	}

	if _, err := conn.Exec(
		`INSERT INTO review_state (problem_id, ease_factor, interval_days, repetitions, next_review_date, last_grade, last_reviewed_at)
		 VALUES (?, 2.5, 5, 1, '2026-01-06', 'Good', '2026-01-01T00:00:00Z')`,
		problemID,
	); err != nil {
		t.Fatalf("insert review_state: unexpected err: %v", err)
	}

	if _, err := conn.Exec(`DELETE FROM problems WHERE id = ?`, problemID); err != nil {
		t.Fatalf("delete problem: unexpected err: %v", err)
	}

	var attemptsCount int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM attempts WHERE problem_id = ?`, problemID).Scan(&attemptsCount); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if attemptsCount != 0 {
		t.Errorf("attempts rows remaining after problem delete = %d, want 0 (cascade)", attemptsCount)
	}

	var reviewStateCount int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM review_state WHERE problem_id = ?`, problemID).Scan(&reviewStateCount); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if reviewStateCount != 0 {
		t.Errorf("review_state rows remaining after problem delete = %d, want 0 (cascade)", reviewStateCount)
	}
}

func TestTopics_NameUniqueCaseInsensitive(t *testing.T) {
	conn := openTestDB(t)

	// "Linked List" is already seeded — inserting a different-case variant
	// must still violate the UNIQUE constraint (COLLATE NOCASE), proving
	// case-insensitivity rather than just exact-string uniqueness.
	if _, err := conn.Exec(`INSERT INTO topics (name) VALUES ('linked list')`); err == nil {
		t.Fatal("expected a unique constraint violation inserting a different-case duplicate topic name, got nil error")
	}
}
