package attempt

import "testing"

func TestApplyServerGates(t *testing.T) {
	cases := []struct {
		name                                      string
		isCorrect                                 bool
		pts                                       int64
		timeTakenMs, timeLimitSeconds, comboLevel int
		wantCorrect                               bool
		wantPts                                   int64
		wantTimedOut                              bool
		wantCombo                                 int
	}{
		{"correct in time advances combo", true, 100, 5000, 30, 3, true, 100, false, 4},
		{"wrong answer resets combo", false, 0, 5000, 30, 7, false, 0, false, 0},
		{"late answer voided and combo reset", true, 100, 40000, 30, 7, false, 0, true, 0},
		{"within grace still counts", true, 100, 31000, 30, 0, true, 100, false, 1},
		{"just past grace is late", true, 100, 32001, 30, 0, false, 0, true, 0},
		{"no time limit means never late", true, 100, 999999, 0, 0, true, 100, false, 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			correct, pts, timedOut, combo := applyServerGates(c.isCorrect, c.pts, c.timeTakenMs, c.timeLimitSeconds, c.comboLevel)
			if correct != c.wantCorrect || pts != c.wantPts || timedOut != c.wantTimedOut || combo != c.wantCombo {
				t.Errorf("got (%v, %d, %v, %d), want (%v, %d, %v, %d)",
					correct, pts, timedOut, combo, c.wantCorrect, c.wantPts, c.wantTimedOut, c.wantCombo)
			}
		})
	}
}
