package quiz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/middleware"
)

func TestRatingRejectsInvalidStarsAndReview(t *testing.T) {
	h := NewHandler(nil) // Rejected bodies must never reach storage.
	for _, body := range []string{`{}`, `{"stars":0}`, `{"stars":6}`, `{"stars":-1}`, `{"stars":2.5}`, `{"stars":"5"}`, `{"stars":null}`, `{"stars":5,"review":"text"}`, `{"stars":5} {"stars":1}`, `{"stars":1,"stars":5}`} {
		t.Run(body, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.PutRating(w, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400", w.Code)
			}
		})
	}
}

func TestRatingsAccessRoles(t *testing.T) {
	h := NewHandler(nil)
	for _, role := range []string{"student", "teacher", "institution_admin", "moderator", "support_agent", "super_admin", ""} {
		t.Run(role, func(t *testing.T) {
			r := chi.NewRouter()
			r.With(middleware.RequireRole("super_admin")).Get("/admin/quizzes/{quizId}/ratings", h.AdminRatings)
			r.With(middleware.RequireRole("student")).Get("/quizzes/{quizId}/rating", h.GetRating)
			r.With(middleware.RequireRole("student")).Put("/quizzes/{quizId}/rating", h.PutRating)
			for _, route := range []struct{ method, path, allowed string }{
				{"GET", "/admin/quizzes/invalid/ratings", "super_admin"},
				{"GET", "/quizzes/invalid/rating", "student"},
				{"PUT", "/quizzes/invalid/rating", "student"},
			} {
				req := httptest.NewRequest(route.method, route.path, strings.NewReader(`{"stars":5}`))
				req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole, role))
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				want := http.StatusForbidden
				if role == route.allowed {
					want = http.StatusBadRequest
				} // Invalid ID, before storage.
				if w.Code != want {
					t.Fatalf("%s %s: status=%d want=%d", route.method, route.path, w.Code, want)
				}
			}
		})
	}
}
