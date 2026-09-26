package attempt

import (
	"encoding/json"
	"testing"
)

func TestResolveLegacyAnswerMatchesDeliveredOption(t *testing.T) {
	choices := json.RawMessage(`[{"id":"old-a","label":"A"},{"id":"old-b","label":"B"}]`)
	answer, id, err := resolveAnswerOption("multiple_choice", choices, json.RawMessage(`"B"`), nil)
	if err != nil || id == nil || *id != "old-b" || string(answer) != `"B"` {
		t.Fatalf("legacy resolution %s %v %v", answer, id, err)
	}
	stale := "new-b"
	if _, _, err := resolveAnswerOption("multiple_choice", choices, json.RawMessage(`"B"`), &stale); err == nil {
		t.Fatal("accepted an option not delivered")
	}
	if _, _, err := resolveAnswerOption("multiple_choice", choices, json.RawMessage(`null`), nil); err == nil {
		t.Fatal("accepted null answer")
	}
	if _, _, err := resolveAnswerOption("arrange_order", choices, json.RawMessage(`["A","B"]`), &stale); err == nil {
		t.Fatal("accepted option id for ordered answer")
	}
}
