package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// fixedWindow tracks request count for one client within the current window.
type fixedWindow struct {
	count   int
	resetAt time.Time
}

// rateLimiter is an in-memory, per-IP fixed-window rate limiter. It is process
// local — adequate for protecting low-volume public endpoints (e.g. the contact
// form) against bursts and naive spam. It is NOT a distributed limiter; behind
// multiple instances each replica enforces the limit independently.
type rateLimiter struct {
	mu      sync.Mutex
	clients map[string]*fixedWindow
	max     int
	window  time.Duration
}

// RateLimit returns middleware that allows at most max requests per client IP
// within window. Excess requests get 429 with a Retry-After header.
func RateLimit(max int, window time.Duration) func(http.Handler) http.Handler {
	rl := &rateLimiter{
		clients: make(map[string]*fixedWindow),
		max:     max,
		window:  window,
	}
	go rl.cleanupLoop()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if allowed, retryAfter := rl.allow(ip); !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
				Error(w, http.StatusTooManyRequests, "RATE_LIMITED",
					"too many requests, please try again later")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitByJSONField returns middleware that limits requests keyed by a
// string field in the JSON request body (e.g. "email"), independent of source
// IP. Useful for endpoints that trigger a per-recipient side effect such as
// sending an OTP email. The request body is buffered and restored so downstream
// handlers can decode it normally. Requests whose body is unreadable or missing
// the field are passed through untouched (the handler does its own validation).
func RateLimitByJSONField(max int, window time.Duration, field string) func(http.Handler) http.Handler {
	rl := &rateLimiter{
		clients: make(map[string]*fixedWindow),
		max:     max,
		window:  window,
	}
	go rl.cleanupLoop()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			r.Body.Close()
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			// Restore the body for the downstream handler regardless of outcome.
			r.Body = io.NopCloser(bytes.NewReader(body))

			var parsed map[string]json.RawMessage
			var raw string
			if json.Unmarshal(body, &parsed) == nil {
				if v, ok := parsed[field]; ok {
					json.Unmarshal(v, &raw) // ignore non-string values
				}
			}
			key := strings.ToLower(strings.TrimSpace(raw))
			if key == "" {
				next.ServeHTTP(w, r) // nothing to key on; let handler validate
				return
			}

			if allowed, retryAfter := rl.allow(key); !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
				Error(w, http.StatusTooManyRequests, "RATE_LIMITED",
					"too many requests for this "+field+", please try again later")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// allow records a request for ip and reports whether it is within the limit.
// When denied, it also returns how long until the window resets.
func (rl *rateLimiter) allow(ip string) (bool, time.Duration) {
	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	c, ok := rl.clients[ip]
	if !ok || now.After(c.resetAt) {
		rl.clients[ip] = &fixedWindow{count: 1, resetAt: now.Add(rl.window)}
		return true, 0
	}

	if c.count >= rl.max {
		return false, time.Until(c.resetAt)
	}
	c.count++
	return true, 0
}

// cleanupLoop periodically evicts expired windows so the map does not grow
// unbounded with one-off visitors.
func (rl *rateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.window)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		rl.mu.Lock()
		for ip, c := range rl.clients {
			if now.After(c.resetAt) {
				delete(rl.clients, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// clientIP extracts the originating client IP, honouring the proxy headers set
// by Render's load balancer, and falls back to the raw connection address.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First entry is the original client; the rest are intermediary proxies.
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
		return xrip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
