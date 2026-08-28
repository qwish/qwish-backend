package user

import (
	"testing"

	"github.com/qwish/backend/internal/domain/scoring"
)

func TestScaleQwish(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{0, 100},   // fresh user starts at the floor
		{100, 900}, // perfect weighted score hits the ceiling
		{50, 500},  // midpoint
		{-5, 100},  // clamped below
		{150, 900}, // clamped above
	}
	for _, c := range cases {
		if got := scaleQwish(c.in); got != c.want {
			t.Errorf("scaleQwish(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestQwishScoreUsesConfidenceAndSmoothEngagement(t *testing.T) {
	base := scoring.QwishScoreFactors{
		TotalCorrect: 5, TotalQuestions: 5, Streak: 1, ActivityCount: 1,
		SpeedSum: 5, TotalDifficulty: 5, CorrectDifficulty: 5,
	}
	first := scoring.CalculateQwishScoreComponents(base)
	if first.Accuracy >= 1 {
		t.Fatalf("small perfect sample should be confidence-adjusted: %v", first.Accuracy)
	}
	if !(first.Consistency > 0 && first.Consistency < 1) {
		t.Fatalf("streak curve should be continuous: %v", first.Consistency)
	}
	if !(first.Activity > 0 && first.Activity < 1) {
		t.Fatalf("activity curve should be continuous: %v", first.Activity)
	}

	later := base
	later.Streak = 2
	later.ActivityCount = 2
	second := scoring.CalculateQwishScoreComponents(later)
	if second.Consistency <= first.Consistency || second.Activity <= first.Activity {
		t.Fatalf("engagement curves should increase smoothly: first=%+v second=%+v", first, second)
	}
}
