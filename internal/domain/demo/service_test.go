package demo

import (
	"encoding/json"
	"testing"

	"github.com/qwish/backend/internal/domain/quiz"
	"github.com/qwish/backend/internal/domain/scoring"
)

func mc(id, correct string) quiz.Question {
	return quiz.Question{ID: id, Type: "multiple_choice", CorrectAnswer: json.RawMessage(`"` + correct + `"`)}
}
func ans(id, a string) Answer {
	return Answer{QuestionID: id, Answer: json.RawMessage(`"` + a + `"`)}
}

func TestGrade(t *testing.T) {
	cfg := &scoring.Config{BasePointsPerQuestion: 10}
	questions := []quiz.Question{mc("q1", "A"), mc("q2", "B"), mc("q3", "C"), mc("q4", "D")}

	// 3 right, 1 wrong (q3), q4 skipped counts as wrong → 2/4 = 50%.
	got := grade(questions, []Answer{ans("q1", "A"), ans("q2", "B"), ans("q3", "X")}, cfg)
	if got.TotalCorrect != 2 || got.TotalQuestions != 4 || got.ScorePct != 50 {
		t.Fatalf("got %+v, want 2/4 = 50%%", got)
	}

	// all correct → 100%
	all := grade(questions, []Answer{ans("q1", "A"), ans("q2", "B"), ans("q3", "C"), ans("q4", "D")}, cfg)
	if all.ScorePct != 100 {
		t.Fatalf("got %+v, want 100%%", all)
	}

	// none answered → 0%
	none := grade(questions, nil, cfg)
	if none.ScorePct != 0 || none.TotalCorrect != 0 {
		t.Fatalf("got %+v, want 0%%", none)
	}
}
