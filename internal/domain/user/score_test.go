package user

import (
	"testing"

	"github.com/qwish/backend/internal/domain/scoring"
)

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
