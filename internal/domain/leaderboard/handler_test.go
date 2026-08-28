package leaderboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetRejectsUnknownScopeBeforeDatabaseAccess(t *testing.T) {
	h := NewHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard?scope=domain&domain=quantitative", nil)
	recorder := httptest.NewRecorder()

	h.Get(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if !strings.Contains(recorder.Body.String(), "scope must be institution or global") {
		t.Fatalf("response did not explain valid scopes: %s", recorder.Body.String())
	}
}
