package attempt

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestApplyOptionOrderKeepsLabelsAndOptionIDsTogether(t *testing.T) {
	options, choices, storedOrder, err := applyOptionOrder(
		json.RawMessage(`["first","second","third"]`),
		json.RawMessage(`[{"id":"a","label":"first"},{"id":"b","label":"second"},{"id":"c","label":"third"}]`),
		[]int{2, 0, 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(options) != `["third","first","second"]` {
		t.Fatalf("options = %s", options)
	}
	if string(choices) != `[{"id":"c","label":"third"},{"id":"a","label":"first"},{"id":"b","label":"second"}]` {
		t.Fatalf("choices = %s", choices)
	}
	if string(storedOrder) != `[2,0,1]` {
		t.Fatalf("stored order = %s", storedOrder)
	}
}

func TestApplyOptionOrderIgnoresInvalidSnapshot(t *testing.T) {
	options := json.RawMessage(`["first","second"]`)
	choices := json.RawMessage(`[{"id":"a","label":"first"},{"id":"b","label":"second"}]`)
	gotOptions, gotChoices, order, err := applyOptionOrder(options, choices, []int{0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotOptions, options) || !reflect.DeepEqual(gotChoices, choices) || string(order) != `[]` {
		t.Fatalf("invalid snapshot changed response: options=%s choices=%s order=%s", gotOptions, gotChoices, order)
	}
}

func TestShuffleQuestionOptionsProducesReplayableOrder(t *testing.T) {
	options := json.RawMessage(`["first","second","third"]`)
	choices := json.RawMessage(`[{"id":"a","label":"first"},{"id":"b","label":"second"},{"id":"c","label":"third"}]`)
	shuffledOptions, shuffledChoices, order, err := shuffleQuestionOptions(options, choices)
	if err != nil {
		t.Fatal(err)
	}
	var indices []int
	if err := json.Unmarshal(order, &indices); err != nil {
		t.Fatal(err)
	}
	replayedOptions, replayedChoices, _, err := applyOptionOrder(options, choices, indices)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(shuffledOptions, replayedOptions) || !reflect.DeepEqual(shuffledChoices, replayedChoices) {
		t.Fatalf("shuffle could not be replayed: options=%s/%s choices=%s/%s", shuffledOptions, replayedOptions, shuffledChoices, replayedChoices)
	}
}
