package metrics

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

func (s *MetricsService) Series(ctx context.Context, sel []MetricDef, w Window, instID *string) ([]map[string]any, error) {
	sql, args := BuildSeriesQuery(sel, w, instID)
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

func (s *MetricsService) Totals(ctx context.Context, sel []MetricDef, w Window, instID *string) (map[string]any, error) {
	sql, args := BuildTotalsQuery(sel, w, instID)
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

// countBands runs one COUNT per band. Keeping the edges in Go means the labels
// and the ranges live in one place instead of being duplicated in SQL.
func (s *MetricsService) countBands(
	ctx context.Context, bands []band, query string, inst any,
) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(bands))
	for _, b := range bands {
		var n int64
		if err := s.db.QueryRow(ctx, query, b.Lo, b.Hi, inst).Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"label": b.Label, "lo": b.Lo, "hi": b.Hi, "count": n,
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
// is why they do not live in /metrics.
func (s *MetricsService) Distributions(ctx context.Context, instID *string) (map[string]any, error) {
	var inst any
	if instID != nil {
		inst = *instID
	}
	out := map[string]any{}

	// Score histogram: ten fixed 10-point bins, zero-filled by generate_series
	// so the x-axis is stable regardless of what data exists.
	hist, err := s.labelledHistogram(ctx, inst)
	if err != nil {
		return nil, err
	}
	out["score_histogram"] = hist

	bands, err := s.countBands(ctx, difficultyBands, `
		SELECT COUNT(*)
		FROM questions qn
		JOIN quizzes q ON q.id = qn.quiz_id
		WHERE q.deleted_at IS NULL
		  AND qn.difficulty >= $1 AND qn.difficulty < $2
		  AND ($3::uuid IS NULL OR q.institution_id = $3)`, inst)
	if err != nil {
		return nil, err
	}
	out["difficulty_bands"] = bands

	// streaks has no institution column, so it scopes through users.
	streaks, err := s.countBands(ctx, streakBands, `
		SELECT COUNT(*)
		FROM streaks st
		JOIN users u ON u.id = st.user_id
		WHERE u.deleted_at IS NULL
		  AND st.current_streak >= $1 AND st.current_streak < $2
		  AND ($3::uuid IS NULL OR u.institution_id = $3)`, inst)
	if err != nil {
		return nil, err
	}
	out["streak_bands"] = streaks

	if out["role_mix"], err = s.labelledCounts(ctx, `
		SELECT role AS label, COUNT(*) AS count
		FROM users
		WHERE deleted_at IS NULL AND ($1::uuid IS NULL OR institution_id = $1)
		GROUP BY 1 ORDER BY 2 DESC`, inst); err != nil {
		return nil, err
	}

	// Institutions have no parent institution, so this shape ignores the filter.
	if out["institution_type_mix"], err = s.labelledCounts(ctx, `
		SELECT type AS label, COUNT(*) AS count
		FROM institutions WHERE deleted_at IS NULL GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return nil, err
	}

	if out["quiz_status_funnel"], err = s.labelledCounts(ctx, `
		SELECT status AS label, COUNT(*) AS count
		FROM quizzes
		WHERE deleted_at IS NULL AND ($1::uuid IS NULL OR institution_id = $1)
		GROUP BY 1 ORDER BY 2 DESC`, inst); err != nil {
		return nil, err
	}

	return out, nil
}

func (s *MetricsService) labelledHistogram(ctx context.Context, inst any) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx, `
		WITH bins AS (SELECT generate_series(0, 90, 10) AS lo)
		SELECT b.lo, b.lo + 10 AS hi, COALESCE(c.n, 0) AS count
		FROM bins b
		LEFT JOIN (
		  SELECT LEAST(FLOOR(qa.score_pct / 10) * 10, 90) AS lo, COUNT(*) AS n
		  FROM quiz_attempts qa
		  JOIN users u ON u.id = qa.user_id
		  WHERE qa.status = 'completed' AND qa.score_pct IS NOT NULL
		    AND ($1::uuid IS NULL OR u.institution_id = $1)
		  GROUP BY 1
		) c ON c.lo = b.lo
		ORDER BY b.lo`, inst)
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
func (s *MetricsService) PointsLiability(ctx context.Context, instID *string) (map[string]any, error) {
	var inst any
	if instID != nil {
		inst = *instID
	}

	rows, err := s.db.Query(ctx, `
		SELECT to_char(date_trunc('month', pl.expires_at AT TIME ZONE 'Asia/Kolkata'), 'YYYY-MM') AS month,
		       SUM(pl.amount) AS points
		FROM points_ledger pl
		JOIN users u ON u.id = pl.user_id
		WHERE pl.amount > 0
		  AND pl.expires_at IS NOT NULL
		  AND pl.expires_at > now()
		  AND ($1::uuid IS NULL OR u.institution_id = $1)
		GROUP BY 1
		ORDER BY 1`, inst)
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
