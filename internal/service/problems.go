package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// extractSlug normalizes a pasted LeetCode URL (or an already-bare slug)
// to its canonical slug. LeetCode URLs vary in shape (trailing slash,
// /description/ suffix, query params) — SPEC.md §3/§9 require matching on
// slug, not the raw URL string, so this has to run before any dedupe
// check.
func extractSlug(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("service: problem URL/slug is empty")
	}

	if strings.Contains(raw, "/problems/") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("service: parse problem URL %q: %w", raw, err)
		}
		segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		for i, seg := range segments {
			if seg == "problems" && i+1 < len(segments) && segments[i+1] != "" {
				return strings.ToLower(segments[i+1]), nil
			}
		}
		return "", fmt.Errorf("service: could not find a slug in URL %q", raw)
	}

	// Not a URL — treat the input itself as an already-bare slug.
	return strings.ToLower(strings.Trim(raw, "/")), nil
}

// ExtractSlug normalizes a pasted LeetCode URL or bare slug to its
// canonical slug — exported so callers outside this package (e.g.
// httpapi's bulk-import preview) can validate/dedupe rows before commit
// using this project's one LeetCode-URL-parsing implementation.
func ExtractSlug(raw string) (string, error) {
	return extractSlug(raw)
}

// canonicalURL reconstructs the display/storage URL from a slug (SPEC.md
// §3), rather than keeping whatever variant of the URL was typed in.
func canonicalURL(slug string) string {
	return fmt.Sprintf("https://leetcode.com/problems/%s/", slug)
}

func findProblemIDBySlug(ctx context.Context, q querier, slug string) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM problems WHERE slug = ?`, slug).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("service: find problem by slug %q: %w", slug, err)
	}
	return id, nil
}

func loadProblem(ctx context.Context, q querier, id int64) (Problem, error) {
	var p Problem
	err := q.QueryRowContext(ctx, `SELECT id, title, url, difficulty, slug FROM problems WHERE id = ?`, id).
		Scan(&p.ID, &p.Title, &p.URL, &p.Difficulty, &p.Slug)
	if err != nil {
		return Problem{}, fmt.Errorf("service: load problem %d: %w", id, err)
	}
	topics, err := loadProblemTopics(ctx, q, id)
	if err != nil {
		return Problem{}, err
	}
	p.Topics = topics
	return p, nil
}

func loadProblemTopics(ctx context.Context, q querier, problemID int64) ([]string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT t.name FROM topics t
		JOIN problem_topics pt ON pt.topic_id = t.id
		WHERE pt.problem_id = ?
		ORDER BY t.name`, problemID)
	if err != nil {
		return nil, fmt.Errorf("service: load topics for problem %d: %w", problemID, err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("service: scan topic name: %w", err)
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func insertProblem(ctx context.Context, q querier, title, canonicalURL string, difficulty Difficulty, slug string) (int64, error) {
	res, err := q.ExecContext(ctx,
		`INSERT INTO problems (title, url, difficulty, slug) VALUES (?, ?, ?, ?)`,
		title, canonicalURL, difficulty, slug,
	)
	if err != nil {
		return 0, fmt.Errorf("service: insert problem %q: %w", slug, err)
	}
	return res.LastInsertId()
}

// attachTopics ensures each named topic exists (creating it freely if
// not — SPEC.md §3) and links it to problemID. Blank names are ignored.
func attachTopics(ctx context.Context, q querier, problemID int64, topicNames []string) error {
	for _, name := range topicNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO topics (name) VALUES (?)`, name); err != nil {
			return fmt.Errorf("service: ensure topic %q: %w", name, err)
		}
		if _, err := q.ExecContext(ctx, `
			INSERT OR IGNORE INTO problem_topics (problem_id, topic_id)
			SELECT ?, id FROM topics WHERE name = ?`, problemID, name,
		); err != nil {
			return fmt.Errorf("service: attach topic %q to problem %d: %w", name, problemID, err)
		}
	}
	return nil
}

// replaceTopics swaps a problem's full tag set (used by UpdateProblem —
// an edit replaces topics wholesale, it doesn't merge).
func replaceTopics(ctx context.Context, q querier, problemID int64, topicNames []string) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM problem_topics WHERE problem_id = ?`, problemID); err != nil {
		return fmt.Errorf("service: clear topics for problem %d: %w", problemID, err)
	}
	return attachTopics(ctx, q, problemID, topicNames)
}

// AddProblemResult is what AddProblem did. Created distinguishes the two
// outcomes the caller cannot otherwise tell apart: a genuinely new
// problem, versus a grade recorded against one that already existed. The
// second case discards the submitted title/difficulty/topics (SPEC.md
// §9 keeps the existing row's fields), so a caller that reports both
// outcomes identically is hiding the fact that the user's input went
// nowhere. The Problem is embedded, so callers that only want the row
// can keep reading .ID/.Title/.Topics directly off the result.
type AddProblemResult struct {
	Problem
	Created bool
}

// AddProblem finds or creates the problem identified by input.URL's slug,
// attaches its topics (on creation only), and records the first
// attempt/grade — all as one atomic action, matching SPEC.md §2 ("this
// creates the problems row _and_ logs the first attempt/grade in the same
// action"). If a problem with this slug already exists, no duplicate is
// created: SPEC.md §9 says the grade is instead recorded against the
// existing row, and its existing topics are left as-is — the returned
// result's Created reports which of the two happened.
func (s *Service) AddProblem(ctx context.Context, input AddProblemInput) (AddProblemResult, error) {
	if strings.TrimSpace(input.Title) == "" {
		return AddProblemResult{}, errors.New("service: problem title is empty")
	}
	if !input.Difficulty.valid() {
		return AddProblemResult{}, fmt.Errorf("service: invalid difficulty %q", input.Difficulty)
	}
	slug, err := extractSlug(input.URL)
	if err != nil {
		return AddProblemResult{}, err
	}

	var result AddProblemResult
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		problemID, err := findProblemIDBySlug(ctx, tx, slug)
		if err != nil {
			return err
		}

		result.Created = problemID == 0
		if result.Created {
			problemID, err = insertProblem(ctx, tx, input.Title, canonicalURL(slug), input.Difficulty, slug)
			if err != nil {
				return err
			}
			if err := attachTopics(ctx, tx, problemID, input.Topics); err != nil {
				return err
			}
		}

		if err := recordAttempt(ctx, tx, problemID, input.Grade, input.At); err != nil {
			return err
		}
		if _, err := recordReview(ctx, tx, s.loc, problemID, input.Grade, input.At, nil); err != nil {
			return err
		}

		result.Problem, err = loadProblem(ctx, tx, problemID)
		return err
	})
	if err != nil {
		return AddProblemResult{}, err
	}
	return result, nil
}

// GetProblem fetches a single problem by id (e.g. to populate an edit
// form). Returns a wrapped sql.ErrNoRows if no such problem exists.
func (s *Service) GetProblem(ctx context.Context, id int64) (Problem, error) {
	return loadProblem(ctx, s.db, id)
}

// UpdateProblem edits an existing problem's fields (SPEC.md §9). Topics
// are replaced wholesale with input.Topics, not merged.
func (s *Service) UpdateProblem(ctx context.Context, problemID int64, input UpdateProblemInput) error {
	if strings.TrimSpace(input.Title) == "" {
		return errors.New("service: problem title is empty")
	}
	if !input.Difficulty.valid() {
		return fmt.Errorf("service: invalid difficulty %q", input.Difficulty)
	}
	slug, err := extractSlug(input.URL)
	if err != nil {
		return err
	}

	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE problems SET title = ?, url = ?, difficulty = ?, slug = ? WHERE id = ?`,
			input.Title, canonicalURL(slug), input.Difficulty, slug, problemID,
		)
		if err != nil {
			if isUniqueConstraintErr(err) {
				return fmt.Errorf("%w: %q", ErrDuplicateSlug, input.URL)
			}
			return fmt.Errorf("service: update problem %d: %w", problemID, err)
		}
		if err := requireRowsAffected(res, fmt.Sprintf("service: problem %d", problemID)); err != nil {
			return err
		}
		return replaceTopics(ctx, tx, problemID, input.Topics)
	})
}

// DeleteProblem removes a problem. Per SPEC.md §9, this cascades to its
// attempts and review_state rows (enforced by the schema's ON DELETE
// CASCADE plus PRAGMA foreign_keys=ON in internal/db).
func (s *Service) DeleteProblem(ctx context.Context, problemID int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM problems WHERE id = ?`, problemID)
	if err != nil {
		return fmt.Errorf("service: delete problem %d: %w", problemID, err)
	}
	return requireRowsAffected(res, fmt.Sprintf("service: problem %d", problemID))
}
