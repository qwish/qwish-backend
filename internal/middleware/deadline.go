package middleware

import (
	"net/http"
	"time"
)

// WriteDeadline overrides the server's normal write deadline for long jobs.
func WriteDeadline(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(timeout))
			next.ServeHTTP(w, r)
		})
	}
}
