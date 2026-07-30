package institution

import (
	"fmt"
	"net/http"

	"github.com/qwish/backend/internal/domain/metrics"
	"github.com/qwish/backend/internal/middleware"
)

// MetricsScopeResolver pins every institution-admin analytics request to the
// institution on the caller's token. No query parameter widens or redirects it:
// there is deliberately no code path by which an institution admin names
// another institution.
func MetricsScopeResolver() metrics.ScopeResolver {
	return func(r *http.Request) (metrics.Scope, metrics.ScopeNote, error) {
		instID := middleware.GetInstitutionID(r)
		if instID == "" {
			return metrics.Scope{}, metrics.ScopeNote{},
				fmt.Errorf("%w: your account is not linked to an institution", metrics.ErrBadScopeRequest)
		}
		note := metrics.ScopeNote{
			Requested: metrics.ScopeInstitution,
			Effective: metrics.ScopeInstitution,
		}
		return metrics.Scope{Kind: metrics.ScopeInstitution, ID: instID}, note, nil
	}
}
