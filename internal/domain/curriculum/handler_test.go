package curriculum

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/qwish/backend/internal/middleware"
)

func requestAs(method, path, body, role, inst, user string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := context.WithValue(r.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyInstID, inst)
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, user)
	return r.WithContext(ctx)
}

func routes(svc *Service) http.Handler {
	r := chi.NewRouter()
	h := NewHandler(svc)
	r.Route("/institution", h.InstitutionRoutes)
	r.Route("/teacher", h.TeacherRoutes)
	return r
}

func TestRoutesRejectInvalidScopeBeforeDatabaseAccess(t *testing.T) {
	for _, tc := range []struct{ role, inst, user, path string }{
		{"teacher", uuid.NewString(), uuid.NewString(), "/institution/curricula"},
		{"institution_admin", "", uuid.NewString(), "/institution/curricula"},
		{"institution_admin", uuid.Nil.String(), uuid.NewString(), "/institution/curricula"},
		{"institution_admin", uuid.NewString(), "", "/institution/curricula"},
		{"student", uuid.NewString(), uuid.NewString(), "/teacher/classes/" + uuid.NewString() + "/curricula"},
	} {
		w := httptest.NewRecorder()
		routes(nil).ServeHTTP(w, requestAs("GET", tc.path, "", tc.role, tc.inst, tc.user))
		if w.Code != http.StatusForbidden {
			t.Fatalf("%+v: got %d", tc, w.Code)
		}
	}
}

func TestRoutesValidateBodiesBeforeDatabaseAccess(t *testing.T) {
	versionID := uuid.NewString()
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/institution/curricula", `{"name":"Math","institution_id":"forged"}`},
		{"POST", "/institution/academic-years", `{"name":"Year","starts_on":"2026-01-01","ends_on":"2025-01-01"}`},
		{"POST", "/institution/academic-years", `{} {}`},
		{"POST", "/institution/academic-years", `null`},
		{"PUT", "/institution/curriculum-versions/" + versionID, `{"revision":0}`},
		{"POST", "/institution/curriculum-versions/" + versionID + "/publish", `{"revision":-1}`},
		{"GET", "/institution/curriculum-versions/not-a-uuid", ``},
		{"GET", "/institution/curricula?limit=999", ``},
		{"GET", "/institution/curricula?page=-1", ``},
		{"POST", "/institution/groups/" + uuid.NewString() + "/curricula", `{"academic_year_id":"x","version_id":"x"}`},
	} {
		w := httptest.NewRecorder()
		routes(nil).ServeHTTP(w, requestAs(tc.method, tc.path, tc.body, "institution_admin", uuid.NewString(), uuid.NewString()))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s %s: got %d: %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestDecodeRejectsOversizedBody(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"`+strings.Repeat("a", 1024*1024)+`"}`))
	var target YearInput
	if decode(w, r, &target) {
		t.Fatal("accepted oversized body")
	}
}
