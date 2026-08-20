package attempt

import "testing"

func TestClampReplayMs(t *testing.T) {
	cases := []struct{ in, want int }{
		{-5, 0},             // nonsense floors at zero
		{0, 0},              // instant answers stay instant and take the guess penalty
		{4200, 4200},        // ordinary value passes through
		{600000, 600000},    // exactly the cap
		{9_999_999, 600000}, // a client claiming three hours per question is capped
	}
	for _, c := range cases {
		if got := clampReplayMs(c.in); got != c.want {
			t.Errorf("clampReplayMs(%d) = %d; want %d", c.in, got, c.want)
		}
	}
}
