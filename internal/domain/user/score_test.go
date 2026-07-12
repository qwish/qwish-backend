package user

import "testing"

func TestScaleQwish(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{0, 100},    // fresh user starts at the floor
		{100, 980},  // perfect weighted score hits the ceiling
		{50, 540},   // midpoint
		{-5, 100},   // clamped below
		{150, 980},  // clamped above
	}
	for _, c := range cases {
		if got := scaleQwish(c.in); got != c.want {
			t.Errorf("scaleQwish(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
