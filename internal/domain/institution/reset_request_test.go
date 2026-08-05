package institution

import "testing"

func TestValidateResetRequest(t *testing.T) {
	cases := []struct {
		name     string
		codeType string
		reason   string
		wantOK   bool
	}{
		{"student code with a real reason", "student", "Code leaked in a WhatsApp group", true},
		{"teacher code", "teacher", "Former staff member still sharing it", true},
		{"both codes", "both", "Suspected wide leak after a data breach", true},
		{"unknown code type", "admin", "Code leaked in a WhatsApp group", false},
		{"empty code type", "", "Code leaked in a WhatsApp group", false},
		{"blank reason", "student", "", false},
		{"whitespace-only reason", "student", "           ", false},
		{"reason too short", "student", "leaked", false},
		// Ten characters of a non-Latin script is ten characters. Counting bytes
		// would refuse a perfectly good Devanagari or Kannada reason.
		{"ten non-ascii runes", "student", "कोड लीक हुआ", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := validateResetRequest(c.codeType, c.reason)
			if c.wantOK && msg != "" {
				t.Errorf("expected acceptance, got %q", msg)
			}
			if !c.wantOK && msg == "" {
				t.Error("expected rejection, got acceptance")
			}
		})
	}
}
