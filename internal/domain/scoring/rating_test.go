package scoring

import "testing"

func TestRatingBehaviour(t *testing.T) {
	// Hard wins are worth more than easy wins.
	easy, _ := NewRating().Update(300, 0, true, 0)
	hard, _ := NewRating().Update(700, 0, true, 0)
	if !(hard.Theta-RatingStart > 2*(easy.Theta-RatingStart)) {
		t.Fatalf("hard win %.1f should dwarf easy win %.1f", hard.Theta-RatingStart, easy.Theta-RatingStart)
	}

	// A guessable question pays less for a correct answer.
	guessed, _ := NewRating().Update(500, 0.25, true, 0)
	plain, _ := NewRating().Update(500, 0, true, 0)
	if guessed.Theta >= plain.Theta {
		t.Fatalf("guess floor should shrink the gain: %.1f vs %.1f", guessed.Theta, plain.Theta)
	}

	// A steady 70% learner on medium questions: the published score climbs
	// gradually, never jumps by much, and settles near true ability.
	r := NewRating()
	prev := r.Score()
	for i := 0; i < 300; i++ {
		r, _ = r.Update(500, 0, i%10 < 7, 50)
		if i > 20 {
			if step := r.Score() - prev; step > 15 || step < -15 {
				t.Fatalf("answer %d moved the score by %.1f", i, step)
			}
		}
		prev = r.Score()
	}
	// logit(0.7)*100 ≈ 85 above the question difficulty.
	if r.Theta < 540 || r.Theta > 630 || r.Score() < 480 {
		t.Fatalf("settled at theta=%.1f score=%.1f", r.Theta, r.Score())
	}

	// Idle time widens sigma (score dips), bounded by the starting sigma.
	if aged := r.Age(30); aged.Sigma <= r.Sigma || aged.Score() >= r.Score() {
		t.Fatal("idle days should widen sigma")
	}
	if r.Age(1e6).Sigma != RatingSigmaStart {
		t.Fatal("aging must cap at the starting sigma")
	}

	// Questions answered correctly get easier, missed ones harder.
	if _, d := NewRating().Update(500, 0, true, 0); d >= 0 {
		t.Fatal("correct answer should lower b")
	}
	if b := SeedDifficulty(0.5); b != RatingStart {
		t.Fatalf("median difficulty should seed at %v, got %v", RatingStart, b)
	}
}
