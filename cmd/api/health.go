package main

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	mw "github.com/qwish/backend/internal/middleware"
)

func readinessHandler(ping func(context.Context) error, draining *atomic.Bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if draining.Load() {
			mw.Error(w, http.StatusServiceUnavailable, "NOT_READY", "server is draining")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := ping(ctx); err != nil {
			mw.Error(w, http.StatusServiceUnavailable, "NOT_READY", "database is unavailable")
			return
		}
		mw.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
