-- Recall-DSA schema (SPEC.md §3). Idempotent by design: every statement is
-- safe to re-run against an already-initialized database. Seed data lives
-- in seed_topics.sql, deliberately NOT here — this file runs on every
-- startup, but seeding must run only once (see db.go's Open), or a user's
-- deliberate deletion of a seeded topic would silently resurrect on the
-- next restart.

CREATE TABLE IF NOT EXISTS problems (
    id         INTEGER PRIMARY KEY,
    title      TEXT NOT NULL,
    url        TEXT NOT NULL,
    difficulty TEXT NOT NULL CHECK (difficulty IN ('Easy', 'Medium', 'Hard')),
    slug       TEXT NOT NULL UNIQUE
);

-- Append-only audit log. `grade` stores the full SM-2 grade (not a
-- collapsed solved/failed flag) so grade history survives review_state
-- being overwritten on every re-grade (SPEC.md §3 notes).
CREATE TABLE IF NOT EXISTS attempts (
    id           INTEGER PRIMARY KEY,
    problem_id   INTEGER NOT NULL REFERENCES problems (id) ON DELETE CASCADE,
    attempted_at TEXT NOT NULL, -- RFC3339 timestamp
    grade        TEXT NOT NULL CHECK (grade IN ('Failed', 'Hard', 'Good', 'Easy'))
);

CREATE INDEX IF NOT EXISTS idx_attempts_problem_id ON attempts (problem_id);

-- One row per problem, once solved once. Live scheduler state, mutated in
-- place by internal/scheduler's Apply.
CREATE TABLE IF NOT EXISTS review_state (
    problem_id       INTEGER PRIMARY KEY REFERENCES problems (id) ON DELETE CASCADE,
    ease_factor      REAL NOT NULL,
    interval_days    INTEGER NOT NULL,
    repetitions      INTEGER NOT NULL,
    next_review_date TEXT NOT NULL, -- ISO8601 date (YYYY-MM-DD), so lexical
                                     -- comparison against "today" is correct
    last_grade       TEXT NOT NULL CHECK (last_grade IN ('Failed', 'Hard', 'Good', 'Easy')),
    last_reviewed_at TEXT NOT NULL  -- RFC3339 timestamp
);

-- Powers the entire due-queue query (SPEC.md §4): due rows, most overdue
-- first, optionally filtered by topic via problem_topics.
CREATE INDEX IF NOT EXISTS idx_review_state_next_review_date ON review_state (next_review_date);

CREATE TABLE IF NOT EXISTS topics (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE
);

CREATE TABLE IF NOT EXISTS problem_topics (
    problem_id INTEGER NOT NULL REFERENCES problems (id) ON DELETE CASCADE,
    topic_id   INTEGER NOT NULL REFERENCES topics (id) ON DELETE CASCADE,
    PRIMARY KEY (problem_id, topic_id)
);

CREATE INDEX IF NOT EXISTS idx_problem_topics_topic_id ON problem_topics (topic_id);
