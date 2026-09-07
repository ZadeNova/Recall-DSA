package service

import (
	"context"
	"fmt"
	"strings"
)

// ListLibrary is the Library page's paginated, SRS-aware view: every
// logged problem (like ListProblems) but joined with its review_state —
// safe as an inner join since AddProblem always records a first grade,
// so every problem has a review_state row from the moment it's created.
// Returns the requested page plus the total count matching the filter
// (ignoring Limit/Offset), for pagination.
func (s *Service) ListLibrary(ctx context.Context, filter ListProblemsFilter) ([]DueItem, int, error) {
	from := `FROM review_state rs JOIN problems p ON p.id = rs.problem_id`
	from, args := appendTopicJoin(from, nil, filter.Topic)

	var conditions []string
	if filter.Difficulty != nil {
		conditions = append(conditions, `p.difficulty = ?`)
		args = append(args, *filter.Difficulty)
	}
	if filter.Search != nil && *filter.Search != "" {
		conditions = append(conditions, `p.title LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(*filter.Search)+"%")
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	var total int
	countQuery := `SELECT COUNT(DISTINCT p.id) ` + from + where
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("service: count library: %w", err)
	}

	query := `SELECT p.id, p.title, p.url, p.difficulty, p.slug,
		rs.ease_factor, rs.interval_days, rs.repetitions,
		rs.next_review_date, rs.last_grade, rs.last_reviewed_at ` +
		from + where + ` ORDER BY ` + libraryOrderBy(filter.Sort) + ` LIMIT ? OFFSET ?`
	pageArgs := append(append([]any{}, args...), filter.Limit, filter.Offset)

	rows, err := s.db.QueryContext(ctx, query, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("service: list library: %w", err)
	}
	defer rows.Close()

	items, err := s.scanReviewItems(ctx, rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// libraryOrderBy maps a sort name to a whitelisted ORDER BY clause —
// never built from raw user input, so there's no injection surface even
// though it's concatenated directly into the query.
func libraryOrderBy(sort string) string {
	switch sort {
	case "title":
		return "p.title ASC"
	case "difficulty":
		return "CASE p.difficulty WHEN 'Easy' THEN 1 WHEN 'Medium' THEN 2 WHEN 'Hard' THEN 3 ELSE 4 END ASC, p.title ASC"
	default:
		return "rs.next_review_date ASC, p.title ASC"
	}
}

// escapeLike escapes a LIKE pattern's special characters so a search
// term containing "%" or "_" is matched literally, not as a wildcard.
func escapeLike(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s)
}
