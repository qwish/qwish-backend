package user

import "testing"

func TestRecommendationReasonPriority(t *testing.T) {
	tests := []struct {
		name string
		quiz RecommendedQuiz
		want string
	}{
		{"interest", RecommendedQuiz{interestMatch: true, weaknessScore: 20}, "Matches your interests"},
		{"weakness", RecommendedQuiz{weaknessScore: 12, saved: true}, "Build strength in this topic"},
		{"saved", RecommendedQuiz{saved: true}, "From your saved assessments"},
		{"level", RecommendedQuiz{difficultyScore: 18}, "A good match for your level"},
		{"fallback", RecommendedQuiz{}, "Popular with Qwish learners"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := recommendationReason(tt.quiz); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
