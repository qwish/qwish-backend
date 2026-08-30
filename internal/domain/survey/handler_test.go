package survey

import "testing"

func TestValidAnswer(t *testing.T) {
	options := []string{"Yes", "No"}
	cases := []struct {
		name, typ string
		value     any
		want      bool
	}{
		{"valid choice", "single_choice", "Yes", true},
		{"unknown choice", "single_choice", "Maybe", false},
		{"valid rating", "rating", float64(5), true},
		{"fractional rating", "rating", 2.5, false},
		{"oversized text", "free_text", string(make([]byte, 4001)), false},
		{"valid multi", "multiple_choice", []any{"Yes", "No"}, true},
		{"duplicate multi", "multiple_choice", []any{"Yes", "Yes"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := validAnswer(c.typ, options, c.value); got != c.want {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestValidQuestion(t *testing.T) {
	if validQuestion(questionInput{Type: "single_choice", Prompt: "Pick", Options: []string{"one"}}) {
		t.Fatal("choice question accepted one option")
	}
	if !validQuestion(questionInput{Type: "rating", Prompt: "Rate", Options: []string{}}) {
		t.Fatal("valid rating rejected")
	}
	if validQuestion(questionInput{Type: "single_choice", Prompt: "Pick", Options: []string{"same", "same"}}) {
		t.Fatal("duplicate options were accepted")
	}
}

func TestRequiredBlankAnswers(t *testing.T) {
	if !blankAnswer("free_text", "   ") {
		t.Fatal("blank text was not detected")
	}
	if !blankAnswer("multiple_choice", []any{}) {
		t.Fatal("empty selection was not detected")
	}
	if blankAnswer("free_text", "answer") {
		t.Fatal("non-empty text was marked blank")
	}
}

func TestReceiptHashIsStableAndSurveyScoped(t *testing.T) {
	a := receiptHash("survey-a", "browser-receipt-123456")
	b := receiptHash("survey-a", "browser-receipt-123456")
	c := receiptHash("survey-b", "browser-receipt-123456")
	if a != b {
		t.Fatal("same receipt did not produce a stable hash")
	}
	if a == c {
		t.Fatal("receipt hash was not scoped to the survey")
	}
}
