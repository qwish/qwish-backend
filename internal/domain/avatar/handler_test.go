package avatar

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func router() *chi.Mux {
	h := NewHandler()
	r := chi.NewRouter()
	r.Get("/avatars/options", h.Meta)
	r.Get("/avatars/{seed}", h.Get)
	return r
}

func TestServeSVG(t *testing.T) {
	rr := httptest.NewRecorder()
	router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/avatars/user_42?hairStyle=afro&expression=happy", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Fatalf("content-type = %q", ct)
	}
	if !strings.HasPrefix(rr.Body.String(), "<svg") {
		t.Fatal("body is not an svg")
	}
}

func TestServeMeta(t *testing.T) {
	rr := httptest.NewRecorder()
	router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/avatars/options", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "afro") {
		t.Fatalf("meta missing vocab: %s", rr.Body.String())
	}
}
