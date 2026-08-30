package algorithms

import "testing"

func TestProbabilisticStructures(t *testing.T) {
	c := NewCountMinSketch(256, 4)
	c.Add("quiz-a", 7)
	if c.Estimate("quiz-a") < 7 {
		t.Fatal("count-min under-counted")
	}
	s := NewSpaceSaving(2)
	for i := 0; i < 20; i++ {
		s.Add("popular")
	}
	s.Add("other")
	if s.Top()[0].Key != "popular" {
		t.Fatal("heavy hitter missing")
	}
	h := NewHyperLogLog(10)
	for i := 0; i < 1000; i++ {
		h.Add(string(rune(i)) + "user")
	}
	if n := h.Count(); n < 850 || n > 1150 {
		t.Fatalf("HLL estimate out of tolerance: %d", n)
	}
	r := NewHashRing([]string{"a", "b", "c"}, 64)
	if r.Node("user-1") == "" {
		t.Fatal("hash ring returned no node")
	}
}
