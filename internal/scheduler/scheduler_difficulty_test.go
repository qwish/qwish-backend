package scheduler

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestDeriveDifficulty(t *testing.T) {
	// No responses: pure prior, regardless of signals.
	if got := deriveDifficulty(0.6, 0, 0, 0, 0); !approx(got, 0.6) {
		t.Fatalf("cold start should equal prior 0.6, got %v", got)
	}

	// Large sample, everyone correct, fast, no clues → easiest floor 0.4.
	if got := deriveDifficulty(1.0, 100000, 1.0, 0, 0); got > 0.42 {
		t.Fatalf("all-correct/fast should approach floor 0.4, got %v", got)
	}

	// Large sample, everyone wrong, slow, full clues → hardest ceil 1.0.
	if got := deriveDifficulty(0.4, 100000, 0.0, 1.0, 1.0); got < 0.98 {
		t.Fatalf("all-wrong/slow should approach ceil 1.0, got %v", got)
	}

	// Result always stays within the coefficient range.
	for _, tc := range []struct {
		prior           float64
		n               int
		p, tRatio, clue float64
	}{
		{0.4, 5, 0.5, 0.5, 0}, {1.0, 50, 0.9, 0.1, 0}, {0.6, 20, 0.3, 0.8, 0.5},
	} {
		got := deriveDifficulty(tc.prior, tc.n, tc.p, tc.tRatio, tc.clue)
		if got < 0.4 || got > 1.0 {
			t.Fatalf("out of range: %v for %+v", got, tc)
		}
	}

	// At n == prior strength, correctness has equal empirical/prior weight;
	// time and clue signals are confidence-weighted the same way.
	got := deriveDifficulty(0.4, 20, 0.0, 0, 0)
	if !approx(got, 0.40+0.60*(0.65*0.5)) {
		t.Fatalf("half-weight blend wrong: %v", got)
	}
}
