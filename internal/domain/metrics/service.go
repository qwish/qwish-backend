package metrics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrScopeUnsupported is returned when a snapshot shape cannot be expressed
// under the requested scope at all. Callers turn it into a 400: an empty
// schedule reads as "nothing is expiring" rather than "not answerable".
var ErrScopeUnsupported = errors.New("this shape cannot be scoped that way")

// userScopePred filters a hand-written snapshot query on a users alias.
// "TRUE" for an unscoped request keeps the surrounding SQL valid without a
// branch per query; "FALSE" marks a shape the caller must drop instead of run.
func userScopePred(sc Scope, alias string, n int) string {
	switch sc.Kind {
	case ScopeNone:
		return "TRUE"
	case ScopeInstitution:
		return fmt.Sprintf("%s.institution_id = $%d", alias, n)
	case ScopeClasses:
		return fmt.Sprintf(`%s.id IN (SELECT gs.user_id
			     FROM group_students gs
			     JOIN group_teachers gt ON gt.group_id = gs.group_id
			    WHERE gt.user_id = $%d)`, alias, n)
	default: // ScopeQuizzes has no user linkage
		return "FALSE"
	}
}

// quizScopePred is the same for a quizzes alias.
func quizScopePred(sc Scope, alias string, n int) string {
	switch sc.Kind {
	case ScopeNone:
		return "TRUE"
	case ScopeInstitution:
		return fmt.Sprintf("%s.institution_id = $%d", alias, n)
	case ScopeQuizzes:
		return fmt.Sprintf("%s.created_by = $%d", alias, n)
	default: // ScopeClasses has no quiz linkage
		return "FALSE"
	}
}

// scopeArgs is the argument slice for a snapshot query: one element when the
// scope is active, none otherwise.
func scopeArgs(sc Scope) []any {
	if !sc.Active() {
		return nil
	}
	return []any{sc.ID}
}

type MetricsService struct{ db *pgxpool.Pool }

func NewMetricsService(db *pgxpool.Pool) *MetricsService { return &MetricsService{db: db} }

// rowsToMaps keys a result by column name, so the JSON shape follows the
// requested metrics without a struct per metric combination.
func rowsToMaps(rows pgx.Rows) ([]map[string]any, error) {
	defer rows.Close()
	fields := rows.FieldDescriptions()
	var out []map[string]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		m := make(map[string]any, len(vals))
		for i, fd := range fields {
			if i < len(vals) {
				m[fd.Name] = vals[i]
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *MetricsService) Series(ctx context.Context, sel []MetricDef, w Window, sc Scope) ([]map[string]any, error) {
	sql, args := BuildSeriesQuery(sel, w, sc)
	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	out, err := rowsToMaps(rows)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []map[string]any{} // an empty window serialises as [], never null
	}
	return out, nil
}

func (s *MetricsService) Totals(ctx context.Context, sel []MetricDef, w Window, sc Scope) (map[string]any, error) {
	sql, args := BuildTotalsQuery(sel, w, sc)
	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	maps, err := rowsToMaps(rows)
	if err != nil {
		return nil, err
	}
	if len(maps) == 0 {
		return map[string]any{}, nil
	}
	delete(maps[0], "placeholder")
	return maps[0], nil
}

// band is a labelled numeric range, half-open: [Lo, Hi).
type band struct {
	Label  string
	Lo, Hi float64
}

// difficultyBands are fixed here rather than computed so the histogram and the
// five-slot categorical legend cannot drift apart. questions.difficulty is
// NUMERIC(3,2) constrained to 0.40-1.00 (migrations/020_quiz_domains.sql:91),
// so the top band's upper edge is 1.01 to keep the 1.00 maximum inside it.
var difficultyBands = []band{
	{"Very easy", 0.40, 0.52},
	{"Easy", 0.52, 0.64},
	{"Moderate", 0.64, 0.76},
	{"Hard", 0.76, 0.88},
	{"Very hard", 0.88, 1.01},
}

// streakBands group current_streak. Grouping by the raw value would emit one
// row per distinct streak length, which is a list, not a distribution.
var streakBands = []band{
	{"None", 0, 1},
	{"1-3 days", 1, 4},
	{"4-7 days", 4, 8},
	{"8-14 days", 8, 15},
	{"15-30 days", 15, 31},
	{"31+ days", 31, 1e9},
}

// countBands counts every band in a single round trip. The band edges stay in
// Go so the labels and ranges live in one place instead of being duplicated in
// SQL; they are shipped as arrays and the caller's query runs once per band as
// a LATERAL. The per-band loop this replaces cost one round trip per band — six
// for the streak histogram — for counts Postgres computes in one pass.
//
// The query must reference the band edges as the lateral columns e.lo and e.hi
// (not placeholders: inside a LATERAL, $1 is still a query parameter and would
// bind to the whole array). The caller's own placeholders — the scope filter,
// or none at all when unscoped — occupy $1..$n, so the two edge arrays follow
// at $n+1 and $n+2.
func (s *MetricsService) countBands(
	ctx context.Context, bands []band, query string, args ...any,
) ([]map[string]any, error) {
	los := make([]float64, len(bands))
	his := make([]float64, len(bands))
	for i, b := range bands {
		los[i], his[i] = b.Lo, b.Hi
	}

	loN, hiN := len(args)+1, len(args)+2
	full := fmt.Sprintf(
		`SELECT e.ord, c.count
		   FROM unnest($%d::float8[], $%d::float8[]) WITH ORDINALITY AS e(lo, hi, ord)
		   CROSS JOIN LATERAL (%s) AS c(count)
		  ORDER BY e.ord`, loN, hiN, query)

	rows, err := s.db.Query(ctx, full, append(append([]any{}, args...), los, his)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make([]int64, len(bands))
	for rows.Next() {
		var ord int
		var n int64
		if err := rows.Scan(&ord, &n); err != nil {
			return nil, err
		}
		if ord >= 1 && ord <= len(counts) {
			counts[ord-1] = n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]map[string]any, 0, len(bands))
	for i, b := range bands {
		out = append(out, map[string]any{
			"label": b.Label, "lo": b.Lo, "hi": b.Hi, "count": counts[i],
		})
	}
	return out, nil
}

func (s *MetricsService) labelledCounts(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var label string
		var n int64
		if err := rows.Scan(&label, &n); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"label": label, "count": n})
	}
	return out, rows.Err()
}

// Distributions returns snapshot shapes — "what is the mix right now" — which
// is why they do not live in /metrics. Shapes the active scope cannot express
// are omitted and reported, matching how /metrics reports dropped metrics.
func (s *MetricsService) Distributions(ctx context.Context, sc Scope) (map[string]any, []DroppedMetric, error) {
	out := map[string]any{}
	var dropped []DroppedMetric
	args := scopeArgs(sc)
	reason := DropReason(sc.Kind)

	drop := func(id string) { dropped = append(dropped, DroppedMetric{ID: id, Reason: reason}) }

	// Score histogram: ten fixed 10-point bins, zero-filled by generate_series
	// so the x-axis is stable regardless of what data exists. It answers every
	// kind — the quizzes scope filters the attempt's quiz rather than the taker.
	hist, err := s.labelledHistogram(ctx, sc)
	if err != nil {
		return nil, nil, err
	}
	out["score_histogram"] = hist

	if quizScopePred(sc, "q", 1) == "FALSE" {
		drop("difficulty_bands")
	} else {
		bands, err := s.countBands(ctx, difficultyBands, `
			SELECT COUNT(*)
			FROM questions qn
			JOIN quizzes q ON q.id = qn.quiz_id
			WHERE q.deleted_at IS NULL
			  AND qn.difficulty >= e.lo AND qn.difficulty < e.hi
			  AND `+quizScopePred(sc, "q", 1), args...)
		if err != nil {
			return nil, nil, err
		}
		out["difficulty_bands"] = bands
	}

	// streaks has no institution column, so it scopes through users.
	if userScopePred(sc, "u", 1) == "FALSE" {
		drop("streak_bands")
	} else {
		streaks, err := s.countBands(ctx, streakBands, `
			SELECT COUNT(*)
			FROM streaks st
			JOIN users u ON u.id = st.user_id
			WHERE u.deleted_at IS NULL
			  AND st.current_streak >= e.lo AND st.current_streak < e.hi
			  AND `+userScopePred(sc, "u", 1), args...)
		if err != nil {
			return nil, nil, err
		}
		out["streak_bands"] = streaks
	}

	if userScopePred(sc, "users", 1) == "FALSE" {
		drop("role_mix")
	} else if out["role_mix"], err = s.labelledCounts(ctx, `
		SELECT role AS label, COUNT(*) AS count
		FROM users
		WHERE deleted_at IS NULL AND `+userScopePred(sc, "users", 1)+`
		GROUP BY 1 ORDER BY 2 DESC`, args...); err != nil {
		return nil, nil, err
	}

	// Institutions have no parent institution, so this shape answers no scope.
	if sc.Active() {
		drop("institution_type_mix")
	} else if out["institution_type_mix"], err = s.labelledCounts(ctx, `
		SELECT type AS label, COUNT(*) AS count
		FROM institutions WHERE deleted_at IS NULL GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return nil, nil, err
	}

	if quizScopePred(sc, "quizzes", 1) == "FALSE" {
		drop("quiz_status_funnel")
	} else if out["quiz_status_funnel"], err = s.labelledCounts(ctx, `
		SELECT status AS label, COUNT(*) AS count
		FROM quizzes
		WHERE deleted_at IS NULL AND `+quizScopePred(sc, "quizzes", 1)+`
		GROUP BY 1 ORDER BY 2 DESC`, args...); err != nil {
		return nil, nil, err
	}

	return out, dropped, nil
}

func (s *MetricsService) labelledHistogram(ctx context.Context, sc Scope) ([]map[string]any, error) {
	// The quizzes scope filters the attempt's quiz; the others filter the taker.
	pred := userScopePred(sc, "u", 1)
	if sc.Kind == ScopeQuizzes {
		pred = "qa.quiz_id IN (SELECT id FROM quizzes WHERE created_by = $1)"
	}
	rows, err := s.db.Query(ctx, `
		WITH bins AS (SELECT generate_series(0, 90, 10) AS lo)
		SELECT b.lo, b.lo + 10 AS hi, COALESCE(c.n, 0) AS count
		FROM bins b
		LEFT JOIN (
		  SELECT LEAST(FLOOR(qa.score_pct / 10) * 10, 90) AS lo, COUNT(*) AS n
		  FROM quiz_attempts qa
		  JOIN users u ON u.id = qa.user_id
		  WHERE qa.status = 'completed' AND qa.score_pct IS NOT NULL
		    AND `+pred+`
		  GROUP BY 1
		) c ON c.lo = b.lo
		ORDER BY b.lo`, scopeArgs(sc)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var lo, hi, n int64
		if err := rows.Scan(&lo, &hi, &n); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"lo": lo, "hi": hi, "count": n})
	}
	return out, rows.Err()
}

// PointsLiability is a schedule of points about to expire, grouped by month.
// Not a time series of the past, which is why it is its own endpoint.
// Served by idx_ledger_expires_positive from migration 019.
func (s *MetricsService) PointsLiability(ctx context.Context, sc Scope) (map[string]any, error) {
	// points_ledger has no quiz linkage. Returning an empty schedule would read
	// as "nothing is expiring", so refuse instead.
	if sc.Kind == ScopeQuizzes {
		return nil, ErrScopeUnsupported
	}

	rows, err := s.db.Query(ctx, `
		SELECT to_char(date_trunc('month', pl.expires_at AT TIME ZONE 'Asia/Kolkata'), 'YYYY-MM') AS month,
		       SUM(pl.amount) AS points
		FROM points_ledger pl
		JOIN users u ON u.id = pl.user_id
		WHERE pl.amount > 0
		  AND pl.expires_at IS NOT NULL
		  AND pl.expires_at > now()
		  AND `+userScopePred(sc, "u", 1)+`
		GROUP BY 1
		ORDER BY 1`, scopeArgs(sc)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	months := []map[string]any{}
	var total int64
	for rows.Next() {
		var month string
		var points int64
		if err := rows.Scan(&month, &points); err != nil {
			return nil, err
		}
		months = append(months, map[string]any{"month": month, "points": points})
		total += points
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return map[string]any{
		"as_of":    time.Now().UTC().Format(time.RFC3339),
		"timezone": BucketTimezone,
		"total":    total,
		"months":   months,
	}, nil
}

func (s *MetricsService) InstitutionExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM institutions WHERE id = $1 AND deleted_at IS NULL)`, id).
		Scan(&exists)
	return exists, err
}
