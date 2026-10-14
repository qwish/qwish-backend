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
	// Learner activity is left to the TTL...
	for _, table := range []string{"users", "question_responses", "quiz_attempts", "admin_sessions"} {
		c.Invalidate(table)
	}
	h(httptest.NewRecorder(), cacheRequest("a", ""))
	if calls.Load() != 3 {
		t.Fatal("activity write cleared cached reads")
	}
	// ...while a content change clears them.
	c.Invalidate("quizzes")
	h(httptest.NewRecorder(), cacheRequest("a", ""))
	if calls.Load() != 4 {
		t.Fatal("content write did not invalidate")
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

// A write clears only the writer's own cached reads, so they see their change
// at once while everyone else keeps their cached entries.
func TestClearOnWriteClearsOnlyTheWriter(t *testing.T) {
	c := NewResponseCache()
	var calls atomic.Int32
	read := c.Wrap(time.Minute, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		JSON(w, 200, map[string]string{"user": GetUserID(r)})
	})
	// Stands in for Authenticate: records the caller, then runs the handler.
	authed := func(user string, next http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			noteActor(r.Context(), user, "")
			next(w, r.WithContext(context.WithValue(r.Context(), ContextKeyUserID, user)))
		})
	}
	write := func(user string) {
		r := httptest.NewRequest("POST", "/api/v1/attempts/x/answers", nil)
		c.ClearOnWrite(authed(user, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(httptest.NewRecorder(), r)
	}
	get := func(user string) { read(httptest.NewRecorder(), cacheRequest(user, "")) }

	get("a")
	get("b")
	write("a")
	get("a")
	get("b")
	if calls.Load() != 3 {
		t.Fatalf("calls=%d, want 3: the writer reloads, the other user stays cached", calls.Load())
	}
	// A user ID that is a prefix of another must not clear it.
	get("ab")
	write("a")
	get("ab")
	if calls.Load() != 4 {
		t.Fatalf("calls=%d, want 4: clearing a must not clear ab", calls.Load())
	}
}
