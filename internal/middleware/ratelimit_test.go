package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGCRAAllowsBurstThenRejects(t *testing.T) {
	rl := &rateLimiter{clients: make(map[string]*gcraState), max: 3, window: time.Minute}
	for i := 0; i < 3; i++ {
		if ok, _ := rl.allow("user"); !ok {
			t.Fatalf("request %d in configured burst was rejected", i+1)
		}
	}
	if ok, retry := rl.allow("user"); ok || retry <= 0 {
		t.Fatalf("expected rejection with retry duration, got ok=%v retry=%v", ok, retry)
	}
}

func TestRateLimitByJSONFieldNormalizesEmail(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := RateLimitByJSONField(1, time.Minute, "email")(next)

	first := httptest.NewRequest(http.MethodPost, "/auth/verify-otp", strings.NewReader(`{"email":" Alice@Example.COM "}`))
	firstResult := httptest.NewRecorder()
	handler.ServeHTTP(firstResult, first)
	if firstResult.Code != http.StatusNoContent {
		t.Fatalf("first request status = %d, want %d", firstResult.Code, http.StatusNoContent)
	}

	second := httptest.NewRequest(http.MethodPost, "/auth/verify-otp", strings.NewReader(`{"email":"alice@example.com"}`))
	secondResult := httptest.NewRecorder()
	handler.ServeHTTP(secondResult, second)
	if secondResult.Code != http.StatusTooManyRequests {
		t.Fatalf("normalized duplicate status = %d, want %d", secondResult.Code, http.StatusTooManyRequests)
	}
}

func TestGCRAKeysAreIndependent(t *testing.T) {
	rl := &rateLimiter{clients: make(map[string]*gcraState), max: 1, window: time.Minute}
	if ok, _ := rl.allow("a"); !ok {
		t.Fatal("first key rejected")
	}
	if ok, _ := rl.allow("b"); !ok {
		t.Fatal("second key shared the first key's budget")
	}
}

func TestRemainingCountsDown(t *testing.T) {
	rl := &rateLimiter{clients: map[string]*gcraState{}, max: 5, window: time.Minute}
	if got := rl.remaining("x"); got != 5 {
		t.Fatalf("fresh key: remaining = %d, want 5", got)
	}
	rl.allow("x")
	rl.allow("x")
	rl.allow("x")
	if got := rl.remaining("x"); got != 2 {
		t.Fatalf("after 3 of 5: remaining = %d, want 2", got)
	}
	rl.allow("x")
	rl.allow("x")
	if got := rl.remaining("x"); got != 0 {
		t.Fatalf("exhausted: remaining = %d, want 0", got)
	}
}

func TestClientIPTrustsProxyHeadersOnlyFromPrivatePeers(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	r.RemoteAddr = "198.51.100.7:4000" // public peer: header is spoofable
	if got := clientIP(r); got != "198.51.100.7" {
		t.Fatalf("public peer: got %s", got)
	}
	r.RemoteAddr = "127.0.0.1:4000" // via local Caddy
	if got := clientIP(r); got != "203.0.113.9" {
		t.Fatalf("proxied: got %s", got)
	}
}
