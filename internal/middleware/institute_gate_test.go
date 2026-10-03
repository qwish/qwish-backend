package middleware

import "testing"

// A suspended institute blocks its teachers, but a student only loses that
// institute for the request: their other institute and switching keep working.
func TestInstituteGate(t *testing.T) {
	cases := []struct {
		role, status   string
		block, dropIDs bool
	}{
		{"teacher", "suspended", true, false},
		{"student", "suspended", false, true},
		{"student", "verified", false, false},
		{"teacher", "verified", false, false},
		{"parent", "suspended", false, false},
	}
	for _, c := range cases {
		block, drop := instituteGate(c.role, c.status)
		if block != c.block || drop != c.dropIDs {
			t.Errorf("%s/%s: block=%v drop=%v, want %v %v", c.role, c.status, block, drop, c.block, c.dropIDs)
		}
	}
}
