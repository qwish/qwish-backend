package portfolio

import (
	"strings"
	"testing"
)

func TestStaffContentStripsRestrictedAndBookkeeping(t *testing.T) {
	raw := []byte(`{"id":"e1","kind":"experience","subtype":"internship","title":"Intern","status":"","pinned":false,"revision":2,
		"details":{"supervisor":"Ms X, +91 98...","work_mode":"remote","responsibilities":"APIs"},"skills":["Go"]}`)
	got, err := staffContent(raw)
	if err != nil {
		t.Fatal(err)
	}
	details := got["details"].(map[string]any)
	if _, ok := details["supervisor"]; ok {
		t.Fatal("supervisor contact leaked to staff view")
	}
	if details["work_mode"] != "remote" || got["title"] != "Intern" {
		t.Fatalf("content lost: %v", got)
	}
	for _, k := range []string{"status", "pinned", "revision", "review"} {
		if _, ok := got[k]; ok {
			t.Errorf("bookkeeping key %q kept", k)
		}
	}
	if _, err := staffContent([]byte(`not json`)); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("bad JSON not reported: %v", err)
	}
}

func TestDecisionInput(t *testing.T) {
	cases := []struct {
		decision, comment string
		ok                bool
	}{
		{"reviewed", "", true},
		{"reviewed", "Nice work", true},
		{"changes_requested", "", false}, // a reason is required
		{"changes_requested", "Add the certificate link", true},
		{"approved", "", false},
		{"reviewed", strings.Repeat("x", 2001), false},
	}
	for _, c := range cases {
		if got := validDecision(c.decision, c.comment) == nil; got != c.ok {
			t.Errorf("validDecision(%q, %d chars) ok=%v want %v", c.decision, len(c.comment), got, c.ok)
		}
	}
}
