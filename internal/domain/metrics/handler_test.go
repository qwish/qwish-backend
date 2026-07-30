package metrics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func decodeMetrics(t *testing.T, rec *httptest.ResponseRecorder) []MetricDef {
	t.Helper()
	var body struct {
		Data struct {
			Metrics []MetricDef `json:"metrics"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body.String())
	}
	return body.Data.Metrics
}

// The catalog a role sees must contain only metrics that role can actually be
// answered on. A picker built from a wider catalog offers metrics that will
// always drop.
func TestCatalogFiltersByScopeKind(t *testing.T) {
	h := &Handler{resolve: func(*http.Request) (Scope, ScopeNote, error) {
		return Scope{Kind: ScopeQuizzes, ID: "t"},
			ScopeNote{Requested: ScopeQuizzes, Effective: ScopeQuizzes}, nil
	}}

	rec := httptest.NewRecorder()
	h.Catalog(rec, httptest.NewRequest(http.MethodGet, "/teacher/metrics/catalog", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	got := decodeMetrics(t, rec)
	if len(got) == 0 {
		t.Fatal("quizzes catalog is empty")
	}
	for _, m := range got {
		if !m.answers(ScopeQuizzes) {
			t.Errorf("metric %q cannot answer a quizzes scope but is advertised", m.ID)
		}
		if m.ID == "signups" {
			t.Error("signups advertised under a quizzes scope")
		}
	}
}

func TestUnscopedCatalogIsWhole(t *testing.T) {
	h := &Handler{resolve: func(*http.Request) (Scope, ScopeNote, error) {
		return Scope{}, ScopeNote{}, nil
	}}
	rec := httptest.NewRecorder()
	h.Catalog(rec, httptest.NewRequest(http.MethodGet, "/admin/metrics/catalog", nil))

	if got := decodeMetrics(t, rec); len(got) != len(Catalog()) {
		t.Errorf("unscoped catalog has %d of %d metrics", len(got), len(Catalog()))
	}
}

// A resolver rejecting the caller's request is a 400 with its message, not an
// opaque 500 — the caller can fix a bad scope, but only if told what was wrong.
func TestBadScopeRequestIsFourHundred(t *testing.T) {
	h := &Handler{resolve: func(*http.Request) (Scope, ScopeNote, error) {
		return Scope{}, ScopeNote{}, ErrBadScopeRequest
	}}
	rec := httptest.NewRecorder()
	h.Catalog(rec, httptest.NewRequest(http.MethodGet, "/teacher/metrics/catalog?scope=nope", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400\n%s", rec.Code, rec.Body.String())
	}
}
