package user

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qwish/backend/internal/middleware"
)

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

func TestPickMyQuizRejectsMalformedExclusionBeforeDatabaseAccess(t *testing.T) {
	h := NewHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/users/me/quiz-pick?exclude_id=not-a-uuid", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, "student")
	recorder := httptest.NewRecorder()

	h.PickMyQuiz(recorder, req.WithContext(ctx))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if !strings.Contains(recorder.Body.String(), "exclude_id must be a UUID") {
		t.Fatalf("response did not explain invalid exclusion: %s", recorder.Body.String())
	}
}

func TestPickMyQuizIsStudentOnlyBeforeDatabaseAccess(t *testing.T) {
	h := NewHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/users/me/quiz-pick", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, "teacher")
	recorder := httptest.NewRecorder()

	h.PickMyQuiz(recorder, req.WithContext(ctx))

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}
