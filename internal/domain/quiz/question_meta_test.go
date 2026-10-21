package quiz

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/qwish/backend/internal/jsonx"
)

func TestValidateQuestionMeta(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	s := func(v string) *string { return &v }
	ok := []AddQuestionReq{
		{},
		{Difficulty: f(0.40)},
		{Difficulty: f(1.00), QuestionMeta: QuestionMeta{BloomLevel: s("analyse"), AnswerExplanation: s(strings.Repeat("é", maxMetaChars))}},
		{QuestionMeta: QuestionMeta{BloomLevel: s("")}}, // empty clears
	}
	for i, req := range ok {
		if err := validateQuestionMeta(req); err != nil {
			t.Errorf("ok[%d] rejected: %v", i, err)
		}
	}
	bad := []AddQuestionReq{
		{Difficulty: f(0.39)},
		{Difficulty: f(1.01)},
		{Difficulty: f(math.NaN())},
		{QuestionMeta: QuestionMeta{BloomLevel: s("analyze")}},
		{QuestionMeta: QuestionMeta{KeyConcepts: s(strings.Repeat("x", maxMetaChars+1))}},
	}
	for i, req := range bad {
		if err := validateQuestionMeta(req); !errors.Is(err, ErrInvalidQuestion) {
			t.Errorf("bad[%d] got %v, want ErrInvalidQuestion", i, err)
		}
	}
}

// The metadata is an embedded struct; it must stay flat on the wire, including
// for the admin endpoint's strict decoder.
func TestQuestionMetaWireShape(t *testing.T) {
	body := `{"type":"multiple_choice","prompt":"p","options":["a","b"],"correct_answer":"a",
		"difficulty":0.7,"bloom_level":"apply","answer_explanation":"e","why_not_others":"w","key_concepts":"k","difficulty_rationale":"r"}`
	d := jsonx.NewDecoder(strings.NewReader(body))
	d.DisallowUnknownFields()
	var req AddQuestionReq
	if err := d.Decode(&req); err != nil {
		t.Fatal(err)
	}
	if req.Difficulty == nil || *req.Difficulty != 0.7 || req.BloomLevel == nil || *req.BloomLevel != "apply" ||
		req.DifficultyRationale == nil || *req.DifficultyRationale != "r" {
		t.Fatalf("metadata not decoded: %+v", req)
	}

	out, _ := json.Marshal(Question{Difficulty: 0.6, QuestionMeta: req.QuestionMeta})
	var flat map[string]any
	json.Unmarshal(out, &flat)
	for _, k := range []string{"difficulty", "bloom_level", "answer_explanation", "why_not_others", "key_concepts", "difficulty_rationale"} {
		if _, ok := flat[k]; !ok {
			t.Errorf("response missing %s: %s", k, out)
		}
	}
}
