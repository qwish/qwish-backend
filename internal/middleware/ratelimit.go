package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// gcraState stores the theoretical arrival time used by the Generic Cell Rate
// Algorithm. Unlike a fixed window, GCRA does not allow a double burst at a
// window boundary.
type gcraState struct{ tat time.Time }

// rateLimiter is an in-memory, per-key GCRA rate limiter. It is process
// local — adequate for protecting low-volume public endpoints (e.g. the contact
// form) against bursts and naive spam. It is NOT a distributed limiter; behind
// multiple instances each replica enforces the limit independently.
type rateLimiter struct {
	mu      sync.Mutex
	clients map[string]*gcraState
	max     int
	window  time.Duration
	keyKind string
}

// RateLimit returns middleware that allows at most max requests per client IP
// within window. Excess requests get 429 with a Retry-After header.
func RateLimit(max int, window time.Duration) func(http.Handler) http.Handler {
	rl := &rateLimiter{
		clients: make(map[string]*gcraState),
		max:     max,
		window:  window,
		keyKind: "ip",
	}
	if distributedLimits == nil {
		go rl.cleanupLoop()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			allowed, retryAfter, _, err := rl.allowRequest(r, ip)
			if err != nil {
				Error(w, http.StatusServiceUnavailable, "RATE_LIMIT_UNAVAILABLE", "please retry shortly")
				return
			}
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(retrySeconds(retryAfter)))
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
		clients: make(map[string]*gcraState),
		max:     max,
		window:  window,
		keyKind: "body:" + field,
	}
	if distributedLimits == nil {
		go rl.cleanupLoop()
	}

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

			allowed, retryAfter, remaining, err := rl.allowRequest(r, key)
			if err != nil {
				Error(w, http.StatusServiceUnavailable, "RATE_LIMIT_UNAVAILABLE", "please retry shortly")
				return
			}
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(retrySeconds(retryAfter)))
				w.Header().Set("X-RateLimit-Remaining", "0")
				Error(w, http.StatusTooManyRequests, "RATE_LIMITED",
					"too many requests for this "+field+", please try again later")
				return
			}
			// Lets a sign-in form say "2 tries left" before the pause, not after.
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitByUser limits requests keyed by authenticated user ID rather than IP.
// Must be mounted after Authenticate. IP keying is wrong for these routes: a
// whole school sits behind one NAT address and would share a single budget.
// Falls back to IP when no user is on the request.
func RateLimitByUser(max int, window time.Duration) func(http.Handler) http.Handler {
	rl := &rateLimiter{
		clients: make(map[string]*gcraState),
		max:     max,
		window:  window,
		keyKind: "user",
	}
	if distributedLimits == nil {
		go rl.cleanupLoop()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := GetUserID(r)
			if key == "" {
				key = "ip:" + clientIP(r)
			}
			allowed, retryAfter, _, err := rl.allowRequest(r, key)
			if err != nil {
				Error(w, http.StatusServiceUnavailable, "RATE_LIMIT_UNAVAILABLE", "please retry shortly")
				return
			}
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(retrySeconds(retryAfter)))
				Error(w, http.StatusTooManyRequests, "RATE_LIMITED",
					"too many requests, please slow down")
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

	if rl.max <= 0 || rl.window <= 0 {
		return false, rl.window
	}
	interval := rl.window / time.Duration(rl.max)
	burstTolerance := interval * time.Duration(rl.max-1)
	state, ok := rl.clients[ip]
	if !ok {
		state = &gcraState{}
		rl.clients[ip] = state
	}
	tat := state.tat
	if tat.Before(now) {
		tat = now
	}
	allowedAt := tat.Add(-burstTolerance)
	if now.Before(allowedAt) {
		return false, allowedAt.Sub(now)
	}
	state.tat = tat.Add(interval)
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
			if now.After(c.tat.Add(rl.window)) {
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

// remaining is how many more requests key may make right now, without
// consuming one. GCRA admits request j while tat+(j-1)*interval-burst <= now.
func (rl *rateLimiter) remaining(key string) int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.max <= 0 || rl.window <= 0 {
		return 0
	}
	now := time.Now()
	interval := rl.window / time.Duration(rl.max)
	burst := interval * time.Duration(rl.max-1)
	tat := now
	if st, ok := rl.clients[key]; ok && st.tat.After(now) {
		tat = st.tat
	}
	gap := now.Add(burst).Sub(tat)
	if gap < 0 {
		return 0 // truncating a negative gap would round toward zero, i.e. up
	}
	n := int(gap/interval) + 1
	if n > rl.max {
		return rl.max
	}
	return n
}

// Configure before serving requests. Each replica shares the same atomic GCRA state.
var distributedLimits *pgxpool.Pool

func ConfigureDistributedRateLimits(pool *pgxpool.Pool) { distributedLimits = pool }
func (rl *rateLimiter) allowRequest(r *http.Request, key string) (bool, time.Duration, int, error) {
	if distributedLimits == nil {
		ok, retry := rl.allow(key)
		return ok, retry, rl.remaining(key), nil
	}
	route := chi.RouteContext(r.Context()).RoutePattern()
	ns := fmt.Sprintf("%s|%s|%d|%d", route, rl.keyKind, rl.max, rl.window)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	var ok bool
	var retry float64
	var remaining int
	err := distributedLimits.QueryRow(ctx, `SELECT allowed,retry_seconds,remaining FROM consume_rate_limit($1,$2,$3,$4)`, ns, key, rl.max, rl.window.Seconds()).Scan(&ok, &retry, &remaining)
	return ok, time.Duration(retry * float64(time.Second)), remaining, err
}

func retrySeconds(d time.Duration) int { return max(1, int(math.Ceil(d.Seconds()))) }
