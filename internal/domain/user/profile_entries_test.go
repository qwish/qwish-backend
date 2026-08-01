package user

import "testing"

func TestValidKind(t *testing.T) {
	for _, k := range []string{"experience", "certification", "achievement", "course"} {
		if !validKind(k) {
			t.Fatalf("validKind(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"", "education", "skill", "EXPERIENCE"} {
		if validKind(k) {
			t.Fatalf("validKind(%q) = true, want false", k)
		}
	}
}
