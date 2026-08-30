package admin

import "testing"

func TestBloomFilterHasNoFalseNegatives(t *testing.T) {
	filter := newBloomFilter(4, 0.01)
	values := []string{"alice@example.com", "alice student", "roll-204", "नमस्ते"}
	for _, value := range values {
		filter.add(value)
	}
	for _, value := range values {
		if !filter.mightContain(value) {
			t.Fatalf("false negative for %q", value)
		}
	}
}

func TestStudentSearchBloomMatchesSubstrings(t *testing.T) {
	s := newStudentSearchBloom()
	grams := append(normalizedTrigrams("Alice Student"), normalizedTrigrams("alice@example.com")...)
	filter := newBloomFilter(len(grams), 0.01)
	for _, gram := range grams {
		filter.add(gram)
	}
	s.filter, s.loaded = filter, true

	for _, query := range []string{"ALI", "student", "example.com"} {
		if !s.mightContain(query) {
			t.Fatalf("false negative for substring %q", query)
		}
	}
	if s.mightContain("zzzzzz") {
		t.Fatal("definite miss unexpectedly passed the Bloom prefilter")
	}
}

func TestNormalizedTrigramsUsesRunes(t *testing.T) {
	grams := normalizedTrigrams("  नमस्ते  ")
	if len(grams) == 0 || grams[0] != "नमस" {
		t.Fatalf("unexpected unicode trigrams: %#v", grams)
	}
}
