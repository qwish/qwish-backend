package scoring

import (
	"math"
	"testing"
	"time"
)

func TestEffortDiscount(t *testing.T) {
	cases := []struct {
		name                     string
		timeMs, limitS, hiddenMs int
		want                     float64
	}{
		{"considered answer", 20000, 30, 0, 0},
		{"rapid guess under 3s floor", 900, 10, 0, 0.9},
		{"rapid guess under 10% of a long limit", 9000, 120, 0, 0.9},
		{"just over the floor", 3000, 30, 0, 0},
		{"unknown time is not penalised", 0, 30, 0, 0},
		{"timed out is not a rapid guess", 30000, 30, 0, 0},
		{"backgrounded for 5s", 20000, 30, 5000, 0.5},
		{"brief notification glance", 20000, 30, 4999, 0},
		{"rapid and backgrounded keeps the larger discount", 900, 30, 8000, 0.9},
	}
	for _, c := range cases {
		if got := EffortDiscount(c.timeMs, c.limitS, c.hiddenMs); got != c.want {
			t.Errorf("%s: EffortDiscount(%d,%d,%d)=%v want %v", c.name, c.timeMs, c.limitS, c.hiddenMs, got, c.want)
		}
	}
}

func TestObsWeightClamps(t *testing.T) {
	for d, want := range map[float64]float64{0: 1, 0.5: 0.5, 1: 0, -3: 1, 7: 0, math.NaN(): 1} {
		if got := (RatingObs{Discount: d}).weight(); got != want {
			t.Errorf("Discount %v: weight %v want %v", d, got, want)
		}
	}
}

func TestRatingStatus(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	if s := RatingStatus(now.Add(-29*24*time.Hour), now); s != "active" {
		t.Fatalf("29 days: %s", s)
	}
	if s := RatingStatus(now.Add(-RatingIdleAfter), now); s != "idle" {
		t.Fatalf("30 days: %s", s)
	}
}
