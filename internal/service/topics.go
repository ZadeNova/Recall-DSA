package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ListTopics returns every topic, alphabetically.
func (s *Service) ListTopics(ctx context.Context) ([]Topic, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM topics ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("service: list topics: %w", err)
	}
	defer rows.Close()

	var topics []Topic
	for rows.Next() {
		var t Topic
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, fmt.Errorf("service: scan topic: %w", err)
		}
		topics = append(topics, t)
	}
	return topics, rows.Err()
}

// TopicCount is a topic alongside how many problems currently carry it —
// the Topics page's "Active Problems" column and Topic Distribution
// section.
type TopicCount struct {
	Topic
	Count int
}

// TopicCounts returns every topic with its problem count, alphabetically
// by name. LEFT JOIN so a topic with zero problems still appears with
// Count 0, rather than being dropped.
func (s *Service) TopicCounts(ctx context.Context) ([]TopicCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, COUNT(pt.problem_id)
		FROM topics t
		LEFT JOIN problem_topics pt ON pt.topic_id = t.id
		GROUP BY t.id
		ORDER BY t.name`)
	if err != nil {
		return nil, fmt.Errorf("service: topic counts: %w", err)
	}
	defer rows.Close()

	var counts []TopicCount
	for rows.Next() {
		var tc TopicCount
		if err := rows.Scan(&tc.ID, &tc.Name, &tc.Count); err != nil {
			return nil, fmt.Errorf("service: scan topic count: %w", err)
		}
		counts = append(counts, tc)
	}
	return counts, rows.Err()
}

// CreateTopic adds a new topic tag (SPEC.md §8). The name-uniqueness
// check is case-insensitive at the database level (topics.name COLLATE
// NOCASE), so a duplicate under different casing surfaces here as an
// error rather than silently forking the tag.
func (s *Service) CreateTopic(ctx context.Context, name string) (Topic, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Topic{}, errors.New("service: topic name is empty")
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO topics (name) VALUES (?)`, name)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return Topic{}, fmt.Errorf("%w: %q", ErrDuplicateTopic, name)
		}
		return Topic{}, fmt.Errorf("service: create topic %q: %w", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Topic{}, fmt.Errorf("service: create topic %q: %w", name, err)
	}
	return Topic{ID: id, Name: name}, nil
}

// RenameTopic changes a topic's display name in place; existing
// problem_topics associations are untouched since they reference the id.
func (s *Service) RenameTopic(ctx context.Context, id int64, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return errors.New("service: topic name is empty")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE topics SET name = ? WHERE id = ?`, newName, id)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return fmt.Errorf("%w: %q", ErrDuplicateTopic, newName)
		}
		return fmt.Errorf("service: rename topic %d: %w", id, err)
	}
	return requireRowsAffected(res, fmt.Sprintf("service: topic %d", id))
}

// DeleteTopic removes a topic. Per SPEC.md §8, this only drops the tag
// association (problem_topics rows, via ON DELETE CASCADE) — the
// problems themselves are never touched.
func (s *Service) DeleteTopic(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM topics WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("service: delete topic %d: %w", id, err)
	}
	if err := requireRowsAffected(res, fmt.Sprintf("service: topic %d", id)); err != nil {
		return err
	}
	return nil
}
