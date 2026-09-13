package service

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/db"
)

func newTestService(t *testing.T) *Service {
	t.Helper()

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: unexpected err: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	loc, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Fatalf("LoadLocation: unexpected err: %v", err)
	}

	return New(conn, loc)
}

// fixedClock pins Service.now so date-dependent behavior (RecommendDue,
// RecordReview's next_review_date) is deterministic in tests.
func fixedClock(s *Service, t time.Time) {
	s.now = func() time.Time { return t }
}

// TestMissingID_ReturnsNotFound covers a real regression across four
// methods that share the same shape (an ID-keyed UPDATE/DELETE): none of
// them checked RowsAffected, so acting on an ID that doesn't exist
// silently "succeeded" — a stale page (e.g. two browser tabs, one
// already deleting the topic/problem the other still shows) would
// report success for an action that did nothing.
func TestMissingID_ReturnsNotFound(t *testing.T) {
	const missingID = 999999
	cases := []struct {
		name string
		op   func(s *Service) error
	}{
		{"UpdateProblem", func(s *Service) error {
			return s.UpdateProblem(context.Background(), missingID, UpdateProblemInput{
				Title: "Ghost", URL: "ghost", Difficulty: DifficultyEasy,
			})
		}},
		{"DeleteProblem", func(s *Service) error {
			return s.DeleteProblem(context.Background(), missingID)
		}},
		{"RenameTopic", func(s *Service) error {
			return s.RenameTopic(context.Background(), missingID, "New Name")
		}},
		{"DeleteTopic", func(s *Service) error {
			return s.DeleteTopic(context.Background(), missingID)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTestService(t)
			if err := c.op(s); !errors.Is(err, sql.ErrNoRows) {
				t.Errorf("err = %v, want errors.Is(err, sql.ErrNoRows)", err)
			}
		})
	}
}
