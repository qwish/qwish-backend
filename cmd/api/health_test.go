package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReadinessChecksDatabaseAndDraining(t *testing.T) {
	for _, tc := range []struct {
		name     string
		draining bool
		dbErr    error
		status   int
	}{
		{"healthy", false, nil, http.StatusOK},
		{"database unavailable", false, errors.New("sensitive database error"), http.StatusServiceUnavailable},
		{"draining", true, nil, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var draining atomic.Bool
			draining.Store(tc.draining)
			called := false
			ping := func(ctx context.Context) error {
				called = true
				if _, ok := ctx.Deadline(); !ok {
					t.Error("database ping has no deadline")
				}
				return tc.dbErr
			}
			w := httptest.NewRecorder()
			readinessHandler(ping, &draining)(w, httptest.NewRequest(http.MethodGet, "/ready", nil))
			if w.Code != tc.status {
				t.Fatalf("got %d, want %d", w.Code, tc.status)
			}
			if called == tc.draining {
				t.Fatal("database ping must be skipped while draining")
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("readiness must not be cached")
			}
			if tc.dbErr != nil && strings.Contains(w.Body.String(), tc.dbErr.Error()) {
				t.Fatalf("readiness exposed the database error: %s", w.Body.String())
			}
			if tc.status == http.StatusServiceUnavailable && !strings.Contains(w.Body.String(), `"success":false`) {
				t.Fatalf("expected failure response: %s", w.Body.String())
			}
		})
	}
}
