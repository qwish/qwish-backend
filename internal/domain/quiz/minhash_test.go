package quiz

import "testing"

func TestMinhashNearDuplicateSimilarity(t *testing.T) {
	a := "Which protocol is used to securely load web pages?"
	b := "Which protocol is used for securely loading web pages?"
	if got := jaccardPrompt(a, b); got < 0.60 {
		t.Fatalf("similar prompts scored %v", got)
	}
	if minhashBands(a) == minhashBands("Calculate the area of a triangle") {
		t.Fatal("unrelated prompts shared all bands")
	}
}
