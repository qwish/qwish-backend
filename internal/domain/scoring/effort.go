package scoring

import (
	"math"
	"time"
)

// Effort discount. An answer is full evidence of ability only when the learner
// plausibly read and worked it. Two signals discount it, and neither ever
// rewards: a response faster than reading allows, and the app sitting in the
// background on that question. Hidden time is client-reported, so it can only
// lower a weight; missing telemetry proves nothing and costs nothing.
const (
	rapidGuessFloorMs  = 3000
	rapidGuessShare    = 0.10 // of the question's time limit
	rapidGuessDiscount = 0.9
	hiddenThresholdMs  = 5000
	hiddenDiscount     = 0.5
)

// EffortDiscount is how much of one answer's evidence to drop (0 = none).
// timeMs ≤ 0 means the time is unknown (e.g. offline replays) and is not
// penalised. hiddenMs is the total time the app reported hidden on this
// question.
func EffortDiscount(timeMs, timeLimitSec, hiddenMs int) float64 {
	d := 0.0
	if timeMs > 0 && float64(timeMs) < math.Max(rapidGuessFloorMs, rapidGuessShare*float64(timeLimitSec)*1000) {
		d = rapidGuessDiscount
	}
	if hiddenMs >= hiddenThresholdMs {
		d = math.Max(d, hiddenDiscount)
	}
	return d
}

// RatingIdleAfter is how long without new evidence before a rating is shown
// as idle. Age() is already widening its sigma by then.
const RatingIdleAfter = 30 * 24 * time.Hour

func RatingStatus(updatedAt, now time.Time) string {
	if now.Sub(updatedAt) >= RatingIdleAfter {
		return "idle"
	}
	return "active"
}
