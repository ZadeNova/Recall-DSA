package service

import (
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
