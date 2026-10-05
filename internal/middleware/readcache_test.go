package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func cacheRequest(user, query string) *http.Request {
	r := httptest.NewRequest("GET", "/api/v1/users/me/stats"+query, nil)
	ctx := context.WithValue(r.Context(), ContextKeyUserID, user)
	ctx = context.WithValue(ctx, ContextKeyRole, "student")
	return r.WithContext(ctx)
}
func TestReadCacheIsolatesUsersFiltersAndInvalidation(t *testing.T) {
	c := NewResponseCache()
	var calls atomic.Int32
	h := c.Wrap(time.Minute, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		JSON(w, 200, map[string]string{"user": GetUserID(r)})
	})
	for _, r := range []*http.Request{cacheRequest("a", ""), cacheRequest("a", ""), cacheRequest("b", ""), cacheRequest("a", "?period=7d")} {
		w := httptest.NewRecorder()
		h(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("cache scope mixed: calls=%d", calls.Load())
	}
	c.Invalidate("users")
	h(httptest.NewRecorder(), cacheRequest("a", ""))
	if calls.Load() != 4 {
		t.Fatal("write did not invalidate")
	}
}
func TestReadCacheNeverCachesErrorsAndPreservesHeaders(t *testing.T) {
	c := NewResponseCache()
	calls := 0
	h := c.Wrap(time.Minute, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "3")
		Error(w, 503, "UNAVAILABLE", "retry")
	})
	for range 2 {
		w := httptest.NewRecorder()
		h(w, cacheRequest("a", ""))
		if w.Code != 503 || w.Header().Get("Retry-After") != "3" {
			t.Fatal("error response changed")
		}
	}
	if calls != 2 {
		t.Fatal("cached an error")
	}
}
func TestCanceledCacheRequestDoesNotReadResponseWhileHandlerWrites(t *testing.T) {
	c := NewResponseCache()
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	h := c.Wrap(time.Minute, func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		<-release
		for range 30 {
			_, _ = w.Write([]byte("data"))
		}
		close(finished)
	})
	ctx, cancel := context.WithCancel(context.Background())
	r := cacheRequest("a", "").WithContext(ctx)
	done := make(chan struct{})
	go func() { h(httptest.NewRecorder(), r); close(done) }()
	<-entered
	cancel()
	<-done
	close(release)
	<-finished
}
