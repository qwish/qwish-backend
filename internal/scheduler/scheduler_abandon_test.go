package scheduler

import (
	"testing"
	"time"
)

func TestStaleCutoff(t *testing.T) {
	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	cutoff := staleCutoff(now)

	cases := []struct {
		name      string
		startedAt time.Time
		wantStale bool
	}{
		{"fresh attempt is not stale", now.Add(-5 * time.Minute), false},
		{"one hour old is not stale", now.Add(-1 * time.Hour), false},
		{"just under threshold is not stale", now.Add(-119 * time.Minute), false},
		{"just over threshold is stale", now.Add(-121 * time.Minute), true},
		{"three hours old is stale", now.Add(-3 * time.Hour), true},
		{"a day old is stale", now.Add(-24 * time.Hour), true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.startedAt.Before(cutoff); got != c.wantStale {
				t.Errorf("startedAt=%v cutoff=%v: got stale=%v, want %v",
					c.startedAt, cutoff, got, c.wantStale)
			}
		})
	}
}

// The threshold is a product decision, not an arbitrary number: questions
// default to 15s each (migrations/001_initial.sql:133), so a real attempt runs
// minutes. This asserts nobody drops it to something a slow user could trip.
func TestStaleAttemptAgeIsGenerous(t *testing.T) {
	if staleAttemptAge < time.Hour {
		t.Errorf("staleAttemptAge = %v; a threshold under an hour risks marking "+
			"a live attempt abandoned", staleAttemptAge)
	}
}
