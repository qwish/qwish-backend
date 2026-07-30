package institution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qwish/backend/internal/domain/metrics"
	"github.com/qwish/backend/internal/middleware"
)

// The institution scope comes from the token and nothing else. A query
// parameter naming another institution must have no effect.
func TestInstitutionScopeIgnoresQueryParams(t *testing.T) {
	resolve := MetricsScopeResolver()

	req := httptest.NewRequest(http.MethodGet,
		"/institution/metrics?institution_id=99999999-9999-9999-9999-999999999999&scope=quizzes", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyInstID,
		"11111111-1111-1111-1111-111111111111")
	req = req.WithContext(ctx)

	sc, note, err := resolve(req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sc.Kind != metrics.ScopeInstitution {
		t.Errorf("kind = %q, want institution", sc.Kind)
	}
	if sc.ID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("id = %q, want the token's institution", sc.ID)
	}
	if note.Effective != metrics.ScopeInstitution || note.Requested != metrics.ScopeInstitution {
		t.Errorf("note = %+v", note)
	}
	if note.Reason != "" {
		t.Errorf("reason = %q, want empty — no substitution was made", note.Reason)
	}
}

// A token with no institution cannot be answered at all.
func TestInstitutionScopeRejectsMissingInstitution(t *testing.T) {
	resolve := MetricsScopeResolver()
	req := httptest.NewRequest(http.MethodGet, "/institution/metrics", nil)

	if _, _, err := resolve(req); err == nil {
		t.Fatal("want an error when the token carries no institution")
	}
}
