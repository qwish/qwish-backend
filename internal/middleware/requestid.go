package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

type requestIDKey struct{}

// RequestID gives every response an X-Request-Id ("7f3a-19c2") that support
// can quote back, and puts it on the request context for logs.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 4)
		rand.Read(b)
		id := hex.EncodeToString(b[:2]) + "-" + hex.EncodeToString(b[2:])
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// GetRequestID returns the request's id, or "".
func GetRequestID(r *http.Request) string {
	id, _ := r.Context().Value(requestIDKey{}).(string)
	return id
}

// ClientIP is the caller's address as the rate limiters see it.
func ClientIP(r *http.Request) string { return clientIP(r) }
