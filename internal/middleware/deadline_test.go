package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestWriteDeadlineWorksThroughRequestLogWrapper(t *testing.T) {
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	start := time.Now()
	called := false
	h := RequestLog(WriteDeadline(15 * time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})))
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/internal/cron/reset-streaks", nil))
	if !called {
		t.Fatal("cron handler was not called")
	}
	if w.deadline.Before(start.Add(15*time.Minute)) || w.deadline.After(time.Now().Add(15*time.Minute)) {
		t.Fatalf("write deadline did not reach the underlying writer: %s", w.deadline)
	}
}
