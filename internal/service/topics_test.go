package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
)

func TestListTopics_IncludesSeedAndCreated(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	topic, err := s.CreateTopic(ctx, "Segment Tree")
	if err != nil {
		t.Fatalf("CreateTopic: unexpected err: %v", err)
	}
	if topic.Name != "Segment Tree" || topic.ID == 0 {
		t.Errorf("CreateTopic returned %+v", topic)
	}

	topics, err := s.ListTopics(ctx)
	if err != nil {
		t.Fatalf("ListTopics: unexpected err: %v", err)
	}
	// 35 seeded (internal/db/schema.sql) + 1 created here.
	if len(topics) != 36 {
		t.Errorf("len(topics) = %d, want 36", len(topics))
	}
}

// TestCreateTopic_RejectsCaseInsensitiveDuplicate also guards against a
// real regression: this used to surface the raw SQLite driver error
// verbatim ("constraint failed: UNIQUE constraint failed: topics.name
// (2067)") straight into the UI — a routine, expected user mistake
// (typing a name that already exists) reading like an internal crash.
// CreateTopic now detects this specific failure and returns
// ErrDuplicateTopic instead, whose message is plain English with no SQL
// vocabulary or error codes.
func TestCreateTopic_RejectsCaseInsensitiveDuplicate(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	_, err := s.CreateTopic(ctx, "linked list")
	if err == nil {
		t.Fatal("expected error creating a case-variant duplicate of the seeded 'Linked List' topic")
	}
	if !errors.Is(err, ErrDuplicateTopic) {
		t.Errorf("err = %v, want errors.Is(err, ErrDuplicateTopic)", err)
	}
	if strings.Contains(err.Error(), "constraint") || strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("err.Error() = %q leaks raw SQL vocabulary, want a plain-English message", err.Error())
	}
}

// TestRenameTopic_RejectsDuplicateName covers the same regression for
// RenameTopic — renaming to a name that already exists hits the same
// UNIQUE constraint and must translate the same way.
func TestRenameTopic_RejectsDuplicateName(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	topic, err := s.CreateTopic(ctx, "Temp Name")
	if err != nil {
		t.Fatalf("CreateTopic: unexpected err: %v", err)
	}

	err = s.RenameTopic(ctx, topic.ID, "linked list")
	if err == nil {
		t.Fatal("expected error renaming to a case-variant duplicate of the seeded 'Linked List' topic")
	}
	if !errors.Is(err, ErrDuplicateTopic) {
		t.Errorf("err = %v, want errors.Is(err, ErrDuplicateTopic)", err)
	}
	if strings.Contains(err.Error(), "constraint") || strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("err.Error() = %q leaks raw SQL vocabulary, want a plain-English message", err.Error())
	}
}

func TestRenameTopic(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	topic, err := s.CreateTopic(ctx, "Temp Name")
	if err != nil {
		t.Fatalf("CreateTopic: unexpected err: %v", err)
	}
	if err := s.RenameTopic(ctx, topic.ID, "Final Name"); err != nil {
		t.Fatalf("RenameTopic: unexpected err: %v", err)
	}

	var name string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM topics WHERE id = ?`, topic.ID).Scan(&name); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if name != "Final Name" {
		t.Errorf("name = %q, want %q", name, "Final Name")
	}
}

func TestDeleteTopic_RemovesAssociationNotProblem(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	topic, err := s.CreateTopic(ctx, "Temp Topic")
	if err != nil {
		t.Fatalf("CreateTopic: unexpected err: %v", err)
	}
	problem, err := s.AddProblem(ctx, AddProblemInput{
		Title: "Two Sum", URL: "two-sum", Difficulty: DifficultyEasy,
		Topics: []string{"Temp Topic"}, Grade: scheduler.Good, At: time.Now(),
	})
	if err != nil {
		t.Fatalf("AddProblem: unexpected err: %v", err)
	}

	if err := s.DeleteTopic(ctx, topic.ID); err != nil {
		t.Fatalf("DeleteTopic: unexpected err: %v", err)
	}

	var problemStillExists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM problems WHERE id = ?`, problem.ID).Scan(&problemStillExists); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if problemStillExists != 1 {
		t.Errorf("problem was deleted along with its topic — SPEC.md §8 requires it survive")
	}

	var associationCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM problem_topics WHERE topic_id = ?`, topic.ID).Scan(&associationCount); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if associationCount != 0 {
		t.Errorf("problem_topics association count = %d, want 0", associationCount)
	}
}

func TestCreateTopic_RejectsEmptyOrWhitespaceName(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	for _, name := range []string{"", "   ", "\t\n"} {
		if _, err := s.CreateTopic(ctx, name); err == nil {
			t.Errorf("CreateTopic(%q) = nil error, want error", name)
		}
	}
}

// TestCreateTopic_TrimsName asserts a name is stored trimmed rather than
// creating a topic that's visually identical to an existing one but
// distinct by a leading/trailing space — the same guarantee attachTopics
// (internal/service/problems.go) already provides for topics created
// inline via the Add/Edit Problem form.
func TestCreateTopic_TrimsName(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	topic, err := s.CreateTopic(ctx, "  Segment Tree  ")
	if err != nil {
		t.Fatalf("CreateTopic: unexpected err: %v", err)
	}
	if topic.Name != "Segment Tree" {
		t.Errorf("topic.Name = %q, want trimmed %q", topic.Name, "Segment Tree")
	}
}

func TestRenameTopic_RejectsEmptyOrWhitespaceName(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	topic, err := s.CreateTopic(ctx, "Temp Name")
	if err != nil {
		t.Fatalf("CreateTopic: unexpected err: %v", err)
	}
	if err := s.RenameTopic(ctx, topic.ID, "   "); err == nil {
		t.Error("RenameTopic to a whitespace-only name = nil error, want error")
	}
}

// TestRenameTopic_MissingIDReturnsNotFound and
// TestDeleteTopic_MissingIDReturnsNotFound guard against a real
// regression: neither method checked RowsAffected, so acting on an ID
// that doesn't exist silently "succeeded" — a stale page (e.g. two
// browser tabs, one already deleting the topic the other still shows)
// would report success for an action that did nothing.
func TestRenameTopic_MissingIDReturnsNotFound(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	err := s.RenameTopic(ctx, 999999, "New Name")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err = %v, want errors.Is(err, sql.ErrNoRows)", err)
	}
}

func TestDeleteTopic_MissingIDReturnsNotFound(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	err := s.DeleteTopic(ctx, 999999)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err = %v, want errors.Is(err, sql.ErrNoRows)", err)
	}
}
