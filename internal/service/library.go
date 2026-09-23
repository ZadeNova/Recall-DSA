package service

import (
	"context"
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
	switch filter.Status {
	case StatusAll:
	case StatusPaused:
		conditions = append(conditions, `rs.paused_at IS NOT NULL`)
	default:
		conditions = append(conditions, activeOnly)
	}
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

	return s.queryReviewItems(ctx, from, where, libraryOrderBy(filter.Sort), args, filter.Limit, filter.Offset)
}

// libraryOrderBy maps a sort name to a whitelisted ORDER BY clause —
// never built from raw user input, so there's no injection surface even
// though it's concatenated directly into the query.
//
// Every branch ends in rs.problem_id so the sort is total: title and
// difficulty both tie freely, and rows tied on every ORDER BY term have
// no guaranteed order, which makes paging (two independent queries at
// different offsets) able to repeat one row and skip another. See
// dueOrderBy in reviews.go for the full reasoning, including why the
// tiebreaker is spelled rs.problem_id rather than the equal p.id.
//
// Every sort leads with pausedLast so that, when paused rows are shown
// (status=all), they sort after all active ones instead of mixing in
// with stale review dates. It's a no-op under status=active.
func libraryOrderBy(sort string) string {
	const pausedLast = "(rs.paused_at IS NOT NULL) ASC, "
	switch sort {
	case "title":
		return pausedLast + "p.title ASC, rs.problem_id ASC"
	case "difficulty":
		return pausedLast + "CASE p.difficulty WHEN 'Easy' THEN 1 WHEN 'Medium' THEN 2 WHEN 'Hard' THEN 3 ELSE 4 END ASC, p.title ASC, rs.problem_id ASC"
	default:
		return pausedLast + "rs.next_review_date ASC, p.title ASC, rs.problem_id ASC"
	}
}

// escapeLike escapes a LIKE pattern's special characters so a search
// term containing "%" or "_" is matched literally, not as a wildcard.
func escapeLike(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s)
}
