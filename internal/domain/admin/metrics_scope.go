package admin

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/metrics"
)

// MetricsScopeResolver keeps the super admin's optional institution filter: no
// parameter means platform-wide, a valid one narrows to that institution.
//
// Unlike the institution and teacher resolvers, this one reads an id from the
// query — a super admin is allowed to look at any institution.
func MetricsScopeResolver(db *pgxpool.Pool) metrics.ScopeResolver {
	svc := metrics.NewMetricsService(db)
	return func(r *http.Request) (metrics.Scope, metrics.ScopeNote, error) {
		raw := strings.TrimSpace(r.URL.Query().Get("institution_id"))
		if raw == "" {
			return metrics.Scope{}, metrics.ScopeNote{}, nil
		}
		exists, err := svc.InstitutionExists(r.Context(), raw)
		if err != nil {
			// An unparseable uuid surfaces here as a query error, not a 500 —
			// the caller sent a bad filter, so say so.
			return metrics.Scope{}, metrics.ScopeNote{},
				fmt.Errorf("%w: institution_id must be a valid uuid", metrics.ErrBadScopeRequest)
		}
		if !exists {
			return metrics.Scope{}, metrics.ScopeNote{},
				fmt.Errorf("%w: institution not found", metrics.ErrBadScopeRequest)
		}
		note := metrics.ScopeNote{
			Requested: metrics.ScopeInstitution,
			Effective: metrics.ScopeInstitution,
		}
		return metrics.Scope{Kind: metrics.ScopeInstitution, ID: raw}, note, nil
	}
}
