package editrequest

import "testing"

func TestWarningsFor(t *testing.T) {
	pending := func(field, v string) Request { return Request{Status: "pending", Field: field, ProposedValue: v} }
	if w := warningsFor(pending("roll_number", "11A-012"), true, 0); len(w) != 1 {
		t.Errorf("taken roll: got %v", w)
	}
	if w := warningsFor(pending("section", "B"), false, 2); len(w) != 1 {
		t.Errorf("section with classes: got %v", w)
	}
	if w := warningsFor(pending("section", "B"), false, 0); len(w) != 0 {
		t.Errorf("section without classes: got %v", w)
	}
	if w := warningsFor(pending("admission_date", "31/02/2026"), false, 0); len(w) != 1 {
		t.Errorf("bad date: got %v", w)
	}
	resolved := Request{Status: "approved", Field: "roll_number", ProposedValue: "x"}
	if w := warningsFor(resolved, true, 3); len(w) != 0 {
		t.Errorf("resolved requests carry no warnings: got %v", w)
	}
}
