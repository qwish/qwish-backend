package activity

import (
	"encoding/json"
	"strings"
	"testing"
)

func qs(t *testing.T, raw string) []Question {
	t.Helper()
	var out []Question
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func ans(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

const sample = `[
 {"id":"name","type":"short_text","label":"Team name","required":true,"max_length":20},
 {"id":"topic","type":"single_choice","label":"Topic","required":true,"options":[{"id":"ai","label":"AI"},{"id":"web","label":"Web"}]},
 {"id":"days","type":"multiple_choice","label":"Days","options":[{"id":"mon","label":"Mon"},{"id":"tue","label":"Tue"},{"id":"wed","label":"Wed"}],"max_select":2},
 {"id":"size","type":"number","label":"Size","min":1,"max":5},
 {"id":"when","type":"date","label":"When"},
 {"id":"ok","type":"acknowledgement","label":"I agree","required":true}
]`

func TestValidateQuestions(t *testing.T) {
	if err := ValidateQuestions("form", qs(t, sample)); err != nil {
		t.Fatalf("valid schema rejected: %v", err)
	}
	bad := map[string]string{
		"duplicate id":      `[{"id":"a","type":"short_text","label":"A"},{"id":"a","type":"short_text","label":"B"}]`,
		"unknown type":      `[{"id":"a","type":"file","label":"A"}]`,
		"choice no options": `[{"id":"a","type":"single_choice","label":"A","options":[{"id":"x","label":"X"}]}]`,
		"dup option":        `[{"id":"a","type":"single_choice","label":"A","options":[{"id":"x","label":"X"},{"id":"x","label":"Y"}]}]`,
		"empty label":       `[{"id":"a","type":"short_text","label":" "}]`,
		"bad id":            `[{"id":"A B","type":"short_text","label":"A"}]`,
		"min>max":           `[{"id":"a","type":"number","label":"A","min":5,"max":1}]`,
		"max_select>opts":   `[{"id":"a","type":"multiple_choice","label":"A","options":[{"id":"x","label":"X"},{"id":"y","label":"Y"}],"max_select":3}]`,
		"empty":             `[]`,
	}
	for name, raw := range bad {
		if ValidateQuestions("form", qs(t, raw)) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Polls: exactly one choice question.
	if ValidateQuestions("poll", qs(t, `[{"id":"a","type":"short_text","label":"A"}]`)) == nil {
		t.Error("poll with text question accepted")
	}
	if ValidateQuestions("poll", qs(t, `[{"id":"a","type":"single_choice","label":"A","options":[{"id":"x","label":"X"},{"id":"y","label":"Y"}]},{"id":"b","type":"single_choice","label":"B","options":[{"id":"x","label":"X"},{"id":"y","label":"Y"}]}]`)) == nil {
		t.Error("poll with two questions accepted")
	}
}

func TestValidateAnswers(t *testing.T) {
	q := qs(t, sample)
	good := `{"name":"Rockets","topic":"ai","days":["mon","tue"],"size":3,"when":"2026-10-20","ok":true}`
	if err := ValidateAnswers(q, ans(t, good), true); err != nil {
		t.Fatalf("valid answers rejected: %v", err)
	}
	// Optional fields may be absent or null.
	if err := ValidateAnswers(q, ans(t, `{"name":"R","topic":"web","ok":true,"days":null}`), true); err != nil {
		t.Fatalf("optional omission rejected: %v", err)
	}
	bad := map[string]string{
		"unknown question":  `{"name":"R","topic":"ai","ok":true,"x":1}`,
		"unknown option":    `{"name":"R","topic":"ml","ok":true}`,
		"too long":          `{"name":"` + strings.Repeat("a", 21) + `","topic":"ai","ok":true}`,
		"missing required":  `{"topic":"ai","ok":true}`,
		"blank required":    `{"name":"  ","topic":"ai","ok":true}`,
		"too many selected": `{"name":"R","topic":"ai","ok":true,"days":["mon","tue","wed"]}`,
		"dup selection":     `{"name":"R","topic":"ai","ok":true,"days":["mon","mon"]}`,
		"out of range":      `{"name":"R","topic":"ai","ok":true,"size":9}`,
		"bad date":          `{"name":"R","topic":"ai","ok":true,"when":"20/10/2026"}`,
		"ack false":         `{"name":"R","topic":"ai","ok":false}`,
		"wrong type":        `{"name":5,"topic":"ai","ok":true}`,
	}
	for name, raw := range bad {
		if ValidateAnswers(q, ans(t, raw), true) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Drafts skip required checks but still reject malformed values.
	if err := ValidateAnswers(q, ans(t, `{"name":"R"}`), false); err != nil {
		t.Fatalf("partial draft rejected: %v", err)
	}
	if ValidateAnswers(q, ans(t, `{"topic":"ml"}`), false) == nil {
		t.Error("draft with unknown option accepted")
	}
}

func TestCSVCell(t *testing.T) {
	for in, want := range map[string]string{
		"=SUM(A1)": "'=SUM(A1)", "+1": "'+1", "-2": "'-2", "@x": "'@x", "\tx": "'\tx", "plain": "plain",
	} {
		if got := csvCell(in); got != want {
			t.Errorf("csvCell(%q)=%q want %q", in, got, want)
		}
	}
}

func TestTemplatesAreValid(t *testing.T) {
	for _, tpl := range templates {
		if err := ValidateQuestions(tpl.Kind, tpl.Questions); err != nil {
			t.Errorf("template %s: %v", tpl.ID, err)
		}
	}
}
