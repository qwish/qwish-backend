package admin

import (
	"fmt"
	"time"
)

// IST is the platform's bucketing timezone. A FixedZone rather than
// LoadLocation("Asia/Kolkata") so bucketing is identical in a scratch container
// with no tzdata installed — India has no DST, so the offset is constant and
// the simplification is lossless.
var IST = time.FixedZone("IST", 5*60*60+30*60)

type Granularity string

const (
	GranHour    Granularity = "hour"
	GranDay     Granularity = "day"
	GranWeek    Granularity = "week"
	GranMonth   Granularity = "month"
	GranQuarter Granularity = "quarter"
)

// Granularities is the ordered list the catalog endpoint advertises, so the UI
// builds its picker from the server's vocabulary instead of its own copy.
var Granularities = []Granularity{GranHour, GranDay, GranWeek, GranMonth, GranQuarter}

func ParseGranularity(s string) (Granularity, error) {
	if s == "" {
		return GranDay, nil
	}
	for _, g := range Granularities {
		if Granularity(s) == g {
			return g, nil
		}
	}
	return "", fmt.Errorf("unknown granularity %q; valid values are hour, day, week, month, quarter", s)
}

// PGInterval is the step passed to generate_series. Postgres has no "quarter"
// interval, so a quarter is three months.
func (g Granularity) PGInterval() string {
	switch g {
	case GranHour:
		return "1 hour"
	case GranWeek:
		return "1 week"
	case GranMonth:
		return "1 month"
	case GranQuarter:
		return "3 months"
	default:
		return "1 day"
	}
}

// PGTrunc is the first argument to date_trunc. Postgres accepts 'quarter' here
// even though it has no quarter interval.
func (g Granularity) PGTrunc() string { return string(g) }

// MaxWindowDays caps each granularity so no response exceeds ~170 buckets.
// 365 daily buckets is not a readable chart, and the cap is what lets the UI
// show a real message instead of silently coarsening a request.
func MaxWindowDays(g Granularity) int {
	switch g {
	case GranHour:
		return 7
	case GranDay:
		return 92
	case GranWeek:
		return 366
	default: // month, quarter
		return 1096
	}
}

type Window struct {
	From time.Time // inclusive, midnight IST
	To   time.Time // inclusive, midnight IST
	Gran Granularity
}

const dateLayout = "2006-01-02"

// ResolveWindow parses the request's from/to/granularity, applies defaults, and
// enforces the caps. now is injected so this stays a pure function.
func ResolveWindow(from, to, gran string, now time.Time) (Window, error) {
	g, err := ParseGranularity(gran)
	if err != nil {
		return Window{}, err
	}

	y, m, d := now.In(IST).Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, IST)

	toT := today
	if to != "" {
		toT, err = time.ParseInLocation(dateLayout, to, IST)
		if err != nil {
			return Window{}, fmt.Errorf("invalid 'to' date %q; expected YYYY-MM-DD", to)
		}
	}

	// Default window is the last 30 days inclusive: today-29 .. today.
	fromT := toT.AddDate(0, 0, -29)
	if from != "" {
		fromT, err = time.ParseInLocation(dateLayout, from, IST)
		if err != nil {
			return Window{}, fmt.Errorf("invalid 'from' date %q; expected YYYY-MM-DD", from)
		}
	}

	if fromT.After(toT) {
		return Window{}, fmt.Errorf("'from' (%s) is after 'to' (%s)",
			fromT.Format(dateLayout), toT.Format(dateLayout))
	}

	w := Window{From: fromT, To: toT, Gran: g}
	if max := MaxWindowDays(g); w.Days() > max {
		return Window{}, fmt.Errorf(
			"a %d-day window is too long for %s granularity (max %d days); widen the granularity or narrow the range",
			w.Days(), g, max)
	}
	return w, nil
}

// Days is the inclusive length of the window: a single-day window is 1, not 0.
func (w Window) Days() int {
	return int(w.To.Sub(w.From).Hours()/24) + 1
}

// Previous is the immediately preceding window of equal length. It ends the day
// before From, so the two windows never share a bucket.
func (w Window) Previous() Window {
	n := w.Days()
	end := w.From.AddDate(0, 0, -1)
	return Window{From: end.AddDate(0, 0, -(n - 1)), To: end, Gran: w.Gran}
}

// LastYear is the same calendar window one year earlier.
func (w Window) LastYear() Window {
	return Window{From: w.From.AddDate(-1, 0, 0), To: w.To.AddDate(-1, 0, 0), Gran: w.Gran}
}
