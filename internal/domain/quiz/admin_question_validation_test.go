package quiz

import (
	"encoding/json"
	"testing"
)

func TestValidateAdminQuestionTypes(t *testing.T) {
	valid := []AddQuestionReq{
		{Type: "multiple_choice", Options: json.RawMessage(`["a","b"]`), CorrectAnswer: json.RawMessage(`"a"`)},
		{Type: "confidence_based", Options: json.RawMessage(`["a","b"]`), CorrectAnswer: json.RawMessage(`"b"`)},
		{Type: "eliminate_wrong", Options: json.RawMessage(`["a","b"]`), CorrectAnswer: json.RawMessage(`"a"`)},
		{Type: "puzzle", Options: json.RawMessage(`["a","b"]`), CorrectAnswer: json.RawMessage(`"a"`)},
		{Type: "speed_chain", Options: json.RawMessage(`["a","b"]`), CorrectAnswer: json.RawMessage(`"a"`)},
		{Type: "arrange_order", Options: json.RawMessage(`["a","b"]`), CorrectAnswer: json.RawMessage(`["a","b"]`)},
		{Type: "clue_reveal", Options: json.RawMessage(`[]`), CorrectAnswer: json.RawMessage(`"answer"`), Clues: json.RawMessage(`["clue"]`)},
	}
	for _, question := range valid {
		if err := validateAdminQuestion(question); err != nil {
			t.Errorf("%s rejected: %v", question.Type, err)
		}
	}
}

func TestValidateAdminQuestionRejectsBadShapes(t *testing.T) {
	bad := []AddQuestionReq{
		{Type: "unknown", CorrectAnswer: json.RawMessage(`"x"`)},
		{Type: "multiple_choice", Options: json.RawMessage(`["a","b"]`), CorrectAnswer: json.RawMessage(`"c"`)},
		{Type: "arrange_order", Options: json.RawMessage(`["a"]`), CorrectAnswer: json.RawMessage(`["a"]`)},
		{Type: "clue_reveal", CorrectAnswer: json.RawMessage(`"x"`), Clues: json.RawMessage(`[]`)},
	}
	for _, question := range bad {
		if err := validateAdminQuestion(question); err == nil {
			t.Errorf("%s accepted invalid shape", question.Type)
		}
	}
}
