package activity

import (
	"strings"
	"testing"
)

// Roll numbers were retired (spec D16); the disclosure must not promise them.
func TestDisclosureMatchesSharedFields(t *testing.T) {
	if strings.Contains(strings.ToLower(IdentityDisclosure), "roll") {
		t.Fatalf("disclosure still mentions roll numbers: %q", IdentityDisclosure)
	}
}
