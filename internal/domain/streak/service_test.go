package streak

import (
	"testing"
	"time"
)

func TestLocalDay(t *testing.T) {
	ist, _ := time.LoadLocation("Asia/Kolkata")
	edt, _ := time.LoadLocation("America/New_York")

	cases := []struct {
		name string
		now  time.Time
		loc  *time.Location
		want string
	}{
		// 03:00 IST is still 21:30 UTC the previous day — Truncate(24h) got these wrong.
		{"early morning IST", time.Date(2026, 8, 4, 3, 0, 0, 0, ist), ist, "2026-08-04"},
		{"midday IST", time.Date(2026, 8, 4, 13, 0, 0, 0, ist), ist, "2026-08-04"},
		{"late evening EDT", time.Date(2026, 8, 4, 21, 0, 0, 0, edt), edt, "2026-08-04"},
		{"midday UTC", time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC), time.UTC, "2026-08-04"},
	}
	for _, c := range cases {
		if got := localDay(c.now, c.loc).Format("2006-01-02"); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestNextStreak(t *testing.T) {
	today := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	day := func(offset int) *string {
		s := today.AddDate(0, 0, offset).Format("2006-01-02")
		return &s
	}

	cases := []struct {
		name      string
		current   int
		last      *string
		wantNext  int
		wantBroke bool
		wantDone  bool
	}{
		{"first ever completion", 0, nil, 1, false, false},
		{"already completed today", 5, day(0), 5, false, true},
		{"consecutive day", 5, day(-1), 6, false, false},
		{"grace: missed one day", 5, day(-2), 6, false, false},
		{"broken: missed two days", 5, day(-3), 1, true, false},
		{"broken: long inactivity", 12, day(-30), 1, true, false},
		{"already reset, returning after gap", 0, day(-9), 1, true, false},
	}
	for _, c := range cases {
		next, broke, done := nextStreak(c.current, c.last, today)
		if next != c.wantNext || broke != c.wantBroke || done != c.wantDone {
			t.Errorf("%s: got (%d,%v,%v), want (%d,%v,%v)",
				c.name, next, broke, done, c.wantNext, c.wantBroke, c.wantDone)
		}
	}
}
