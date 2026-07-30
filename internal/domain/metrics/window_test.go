package metrics

import (
	"strings"
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, IST)
}

func TestParseGranularity(t *testing.T) {
	cases := []struct {
		in      string
		want    Granularity
		wantErr bool
	}{
		{"", GranDay, false}, // default
		{"hour", GranHour, false},
		{"day", GranDay, false},
		{"week", GranWeek, false},
		{"month", GranMonth, false},
		{"quarter", GranQuarter, false},
		{"minute", "", true},
		{"DAY", "", true}, // case-sensitive; the UI sends what the catalog gave it
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParseGranularity(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("ParseGranularity(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Errorf("ParseGranularity(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestResolveWindowDefaults(t *testing.T) {
	now := day(2026, time.July, 30)
	w, err := ResolveWindow("", "", "", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !w.To.Equal(day(2026, time.July, 30)) {
		t.Errorf("To = %v, want 2026-07-30", w.To)
	}
	// Default window is the last 30 days *inclusive*, so From is today-29.
	if !w.From.Equal(day(2026, time.July, 1)) {
		t.Errorf("From = %v, want 2026-07-01", w.From)
	}
	if w.Gran != GranDay {
		t.Errorf("Gran = %q, want day", w.Gran)
	}
	if w.Days() != 30 {
		t.Errorf("Days() = %d, want 30", w.Days())
	}
}

func TestResolveWindowRejectsFromAfterTo(t *testing.T) {
	now := day(2026, time.July, 30)
	if _, err := ResolveWindow("2026-07-30", "2026-07-01", "day", now); err == nil {
		t.Fatal("expected an error when from > to")
	}
}

func TestResolveWindowRejectsUnparseableDates(t *testing.T) {
	now := day(2026, time.July, 30)
	for _, bad := range []string{"30-07-2026", "2026-7-1", "yesterday", "2026-13-01"} {
		if _, err := ResolveWindow(bad, "2026-07-30", "day", now); err == nil {
			t.Errorf("ResolveWindow(%q) = nil error, want a parse error", bad)
		}
	}
}

// A single-day window is 1 day, not 0. This is the off-by-one that makes a
// "today" preset return an empty chart.
func TestSameDayWindowIsOneDay(t *testing.T) {
	now := day(2026, time.July, 30)
	w, err := ResolveWindow("2026-07-30", "2026-07-30", "hour", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Days() != 1 {
		t.Errorf("Days() = %d, want 1", w.Days())
	}
}

func TestGranularityCaps(t *testing.T) {
	cases := []struct {
		name    string
		from    string
		to      string
		gran    string
		wantErr bool
	}{
		{"hour within 7 days ok", "2026-07-24", "2026-07-30", "hour", false},
		{"hour over 7 days rejected", "2026-07-01", "2026-07-30", "hour", true},
		{"day at exactly 92 days ok", "2026-05-01", "2026-07-31", "day", false},
		{"day over 92 days rejected", "2026-01-01", "2026-07-30", "day", true},
		{"week within 366 days ok", "2026-01-01", "2026-07-30", "week", false},
		{"week over 366 days rejected", "2024-01-01", "2026-07-30", "week", true},
		{"month within 1096 days ok", "2024-01-01", "2026-07-30", "month", false},
		{"month over 1096 days rejected", "2020-01-01", "2026-07-30", "month", true},
		{"quarter within 1096 days ok", "2024-01-01", "2026-07-30", "quarter", false},
	}
	now := day(2026, time.July, 31)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ResolveWindow(c.from, c.to, c.gran, now)
			if (err != nil) != c.wantErr {
				t.Errorf("ResolveWindow(%q,%q,%q) err = %v, wantErr %v",
					c.from, c.to, c.gran, err, c.wantErr)
			}
		})
	}
}

// The cap error has to name the granularity and the limit, because the UI shows
// this string to an admin who then has to pick a valid combination.
func TestCapErrorMentionsGranularityAndLimit(t *testing.T) {
	now := day(2026, time.July, 31)
	_, err := ResolveWindow("2026-01-01", "2026-07-30", "hour", now)
	if err == nil {
		t.Fatal("expected a cap error")
	}
	msg := err.Error()
	for _, want := range []string{"hour", "7"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
}

// compare=previous must land on the immediately preceding window of equal
// length, touching neither edge of the current one.
func TestPreviousWindow(t *testing.T) {
	w := Window{From: day(2026, time.July, 1), To: day(2026, time.July, 30), Gran: GranDay}
	p := w.Previous()

	if !p.To.Equal(day(2026, time.June, 30)) {
		t.Errorf("Previous().To = %v, want 2026-06-30 (the day before From)", p.To)
	}
	if !p.From.Equal(day(2026, time.June, 1)) {
		t.Errorf("Previous().From = %v, want 2026-06-01", p.From)
	}
	if p.Days() != w.Days() {
		t.Errorf("Previous().Days() = %d, want %d — compare windows must be equal length", p.Days(), w.Days())
	}
	if p.Gran != w.Gran {
		t.Errorf("Previous() changed granularity to %q", p.Gran)
	}
}

func TestLastYearWindow(t *testing.T) {
	w := Window{From: day(2026, time.July, 1), To: day(2026, time.July, 30), Gran: GranDay}
	p := w.LastYear()
	if !p.From.Equal(day(2025, time.July, 1)) || !p.To.Equal(day(2025, time.July, 30)) {
		t.Errorf("LastYear() = %v..%v, want 2025-07-01..2025-07-30", p.From, p.To)
	}
}

func TestPGInterval(t *testing.T) {
	cases := map[Granularity]string{
		GranHour:    "1 hour",
		GranDay:     "1 day",
		GranWeek:    "1 week",
		GranMonth:   "1 month",
		GranQuarter: "3 months",
	}
	for g, want := range cases {
		if got := g.PGInterval(); got != want {
			t.Errorf("%q.PGInterval() = %q, want %q", g, got, want)
		}
	}
}
