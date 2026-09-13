package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
)

// TestListTopics_IncludesSeedAndCreated deliberately does not pin the
// exact seed count (internal/db/db_test.go already does that, in the one
// place it should live) — it only asserts ListTopics grows by exactly
// one when a topic is created, so adding a seed topic later doesn't also
// break this test for an unrelated reason.
func TestListTopics_IncludesSeedAndCreated(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	before, err := s.ListTopics(ctx)
	if err != nil {
		t.Fatalf("ListTopics: unexpected err: %v", err)
	}

	topic, err := s.CreateTopic(ctx, "Segment Tree")
	if err != nil {
		t.Fatalf("CreateTopic: unexpected err: %v", err)
	}
	if topic.Name != "Segment Tree" || topic.ID == 0 {
		t.Errorf("CreateTopic returned %+v", topic)
	}

	after, err := s.ListTopics(ctx)
	if err != nil {
		t.Fatalf("ListTopics: unexpected err: %v", err)
	}
	if len(after) != len(before)+1 {
		t.Errorf("len(topics) = %d, want %d (seed count + 1 created)", len(after), len(before)+1)
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

// TestTopicName_TrimmedAndValidated covers name normalization for both
// CreateTopic and RenameTopic in one place: empty/whitespace-only names
// are rejected by both, and a name with surrounding whitespace is stored
// trimmed rather than creating a topic that's visually identical to an
// existing one but distinct by a leading/trailing space — the same
// guarantee attachTopics (internal/service/problems.go) already
// provides for topics created inline via the Add/Edit Problem form.
func TestTopicName_TrimmedAndValidated(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	for _, name := range []string{"", "   ", "\t\n"} {
		if _, err := s.CreateTopic(ctx, name); err == nil {
			t.Errorf("CreateTopic(%q) = nil error, want error", name)
		}
	}

	temp, err := s.CreateTopic(ctx, "Temp Name")
	if err != nil {
		t.Fatalf("CreateTopic: unexpected err: %v", err)
	}
	if err := s.RenameTopic(ctx, temp.ID, "   "); err == nil {
		t.Error("RenameTopic to a whitespace-only name = nil error, want error")
	}

	topic, err := s.CreateTopic(ctx, "  Segment Tree  ")
	if err != nil {
		t.Fatalf("CreateTopic: unexpected err: %v", err)
	}
	if topic.Name != "Segment Tree" {
		t.Errorf("topic.Name = %q, want trimmed %q", topic.Name, "Segment Tree")
	}
}
