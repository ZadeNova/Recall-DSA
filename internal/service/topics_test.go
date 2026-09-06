package service

import (
	"context"
	"testing"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/scheduler"
)

func TestListTopics_IncludesSeedAndCreated(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	topic, err := s.CreateTopic(ctx, "Monotonic Stack")
	if err != nil {
		t.Fatalf("CreateTopic: unexpected err: %v", err)
	}
	if topic.Name != "Monotonic Stack" || topic.ID == 0 {
		t.Errorf("CreateTopic returned %+v", topic)
	}

	topics, err := s.ListTopics(ctx)
	if err != nil {
		t.Fatalf("ListTopics: unexpected err: %v", err)
	}
	// 18 seeded (internal/db/schema.sql) + 1 created here.
	if len(topics) != 19 {
		t.Errorf("len(topics) = %d, want 19", len(topics))
	}
}

func TestCreateTopic_RejectsCaseInsensitiveDuplicate(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	if _, err := s.CreateTopic(ctx, "linked list"); err == nil {
		t.Fatal("expected error creating a case-variant duplicate of the seeded 'Linked List' topic")
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
