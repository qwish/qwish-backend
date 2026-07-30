package admin

import (
	"context"

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

func (s *MetricsService) InstitutionExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM institutions WHERE id = $1 AND deleted_at IS NULL)`, id).
		Scan(&exists)
	return exists, err
}
