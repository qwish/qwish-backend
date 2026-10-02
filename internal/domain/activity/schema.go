package activity

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Question is one field of a form or the single question of a poll. IDs are
// stable: answers are keyed by question and option id, never by label or
// position, so descriptive edits never change what a stored answer means.
type Question struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Label     string   `json:"label"`
	Help      string   `json:"help,omitempty"`
	Required  bool     `json:"required,omitempty"`
	Options   []Option `json:"options,omitempty"`
	MinSelect int      `json:"min_select,omitempty"`
	MaxSelect int      `json:"max_select,omitempty"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	MaxLength int      `json:"max_length,omitempty"`
}

type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

const (
	maxQuestions   = 50
	maxOptions     = 30
	shortTextLimit = 300
	longTextLimit  = 5000
	maxLabelLength = 500
	maxHelpLength  = 1000
)

var idPattern = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)

var questionTypes = map[string]bool{
	"short_text": true, "long_text": true, "single_choice": true, "multiple_choice": true,
	"number": true, "date": true, "acknowledgement": true,
}

func isChoice(t string) bool { return t == "single_choice" || t == "multiple_choice" }

// textLimit is the effective character cap for a text question.
func (q Question) textLimit() int {
	hard := shortTextLimit
	if q.Type == "long_text" {
		hard = longTextLimit
	}
	if q.MaxLength > 0 && q.MaxLength < hard {
		return q.MaxLength
	}
	return hard
}

// ValidateQuestions checks an authored schema. A poll is exactly one choice question.
func ValidateQuestions(kind string, qs []Question) error {
	if len(qs) == 0 || len(qs) > maxQuestions {
		return fmt.Errorf("between 1 and %d questions are required", maxQuestions)
	}
	if kind == "poll" && (len(qs) != 1 || !isChoice(qs[0].Type)) {
		return errors.New("a poll has exactly one single_choice or multiple_choice question")
	}
	seen := map[string]bool{}
	for i, q := range qs {
		at := fmt.Sprintf("question %d", i+1)
		if !idPattern.MatchString(q.ID) || seen[q.ID] {
			return fmt.Errorf("%s: id must be unique and match [a-z0-9_-]{1,40}", at)
		}
		seen[q.ID] = true
		if !questionTypes[q.Type] {
			return fmt.Errorf("%s: unknown type %q", at, q.Type)
		}
		if strings.TrimSpace(q.Label) == "" || utf8.RuneCountInString(q.Label) > maxLabelLength || utf8.RuneCountInString(q.Help) > maxHelpLength {
			return fmt.Errorf("%s: label is required (max %d) and help is at most %d characters", at, maxLabelLength, maxHelpLength)
		}
		if q.MaxLength < 0 || q.MinSelect < 0 || q.MaxSelect < 0 {
			return fmt.Errorf("%s: limits cannot be negative", at)
		}
		if q.Min != nil && q.Max != nil && *q.Min > *q.Max {
			return fmt.Errorf("%s: min is greater than max", at)
		}
		if !isChoice(q.Type) {
			if len(q.Options) > 0 {
				return fmt.Errorf("%s: only choice questions have options", at)
			}
			continue
		}
		if len(q.Options) < 2 || len(q.Options) > maxOptions {
			return fmt.Errorf("%s: choice questions need 2–%d options", at, maxOptions)
		}
		opts := map[string]bool{}
		for _, o := range q.Options {
			if !idPattern.MatchString(o.ID) || opts[o.ID] || strings.TrimSpace(o.Label) == "" || utf8.RuneCountInString(o.Label) > maxLabelLength {
				return fmt.Errorf("%s: option ids must be unique and labels non-empty", at)
			}
			opts[o.ID] = true
		}
		if q.MaxSelect > len(q.Options) || (q.MaxSelect > 0 && q.MinSelect > q.MaxSelect) || q.MinSelect > len(q.Options) {
			return fmt.Errorf("%s: selection limits do not fit the options", at)
		}
	}
	return nil
}

// ValidateAnswers checks answers against a published schema. Unknown questions
// and options are rejected; required questions are only enforced on submit.
func ValidateAnswers(qs []Question, answers map[string]json.RawMessage, submit bool) error {
	byID := make(map[string]Question, len(qs))
	for _, q := range qs {
		byID[q.ID] = q
	}
	for id := range answers {
		if _, ok := byID[id]; !ok {
			return fmt.Errorf("unknown question %q", id)
		}
	}
	for _, q := range qs {
		raw, present := answers[q.ID]
		if !present || string(raw) == "null" {
			if submit && q.Required {
				return fmt.Errorf("%q is required", q.Label)
			}
			continue
		}
		if err := validateAnswer(q, raw, submit); err != nil {
			return fmt.Errorf("%q: %w", q.Label, err)
		}
	}
	return nil
}

func validateAnswer(q Question, raw json.RawMessage, submit bool) error {
	switch q.Type {
	case "short_text", "long_text":
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return errors.New("expected text")
		}
		if utf8.RuneCountInString(s) > q.textLimit() {
			return fmt.Errorf("at most %d characters", q.textLimit())
		}
		if submit && q.Required && strings.TrimSpace(s) == "" {
			return errors.New("is required")
		}
	case "single_choice":
		var s string
		if json.Unmarshal(raw, &s) != nil || !q.hasOption(s) {
			return errors.New("choose one of the listed options")
		}
	case "multiple_choice":
		var picks []string
		if json.Unmarshal(raw, &picks) != nil {
			return errors.New("expected a list of options")
		}
		seen := map[string]bool{}
		for _, p := range picks {
			if !q.hasOption(p) || seen[p] {
				return errors.New("choose listed options, each at most once")
			}
			seen[p] = true
		}
		if q.MaxSelect > 0 && len(picks) > q.MaxSelect {
			return fmt.Errorf("choose at most %d", q.MaxSelect)
		}
		if submit && (len(picks) < q.MinSelect || (q.Required && len(picks) == 0)) {
			return fmt.Errorf("choose at least %d", max(q.MinSelect, 1))
		}
	case "number":
		var n float64
		if json.Unmarshal(raw, &n) != nil {
			return errors.New("expected a number")
		}
		if (q.Min != nil && n < *q.Min) || (q.Max != nil && n > *q.Max) {
			return errors.New("number is out of range")
		}
	case "date":
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return errors.New("expected a date")
		}
		if _, err := time.Parse(time.DateOnly, s); err != nil {
			return errors.New("expected a YYYY-MM-DD date")
		}
	case "acknowledgement":
		var b bool
		if json.Unmarshal(raw, &b) != nil {
			return errors.New("expected true or false")
		}
		if submit && q.Required && !b {
			return errors.New("must be acknowledged")
		}
	}
	return nil
}

func (q Question) hasOption(id string) bool {
	for _, o := range q.Options {
		if o.ID == id {
			return true
		}
	}
	return false
}

// csvCell neutralises spreadsheet formulas in exported text.
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
