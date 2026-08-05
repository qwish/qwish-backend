package institution

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// LIMIT/OFFSET are appended at $(len(args)+1) and $(len(args)+2), so a
// placeholder that disagrees with the arg count is a runtime bind error.
func TestAuditLogWherePlaceholders(t *testing.T) {
	ph := regexp.MustCompile(`\$(\d+)`)

	for _, action := range []string{"", "update_point_rules"} {
		for _, from := range []string{"", "2026-01-01"} {
			for _, to := range []string{"", "2026-12-31"} {
				name := fmt.Sprintf("action=%q from=%q to=%q", action, from, to)
				where, args := auditLogWhere("inst-1", action, from, to)

				max := 0
				seen := map[int]bool{}
				for _, m := range ph.FindAllStringSubmatch(where, -1) {
					n, _ := strconv.Atoi(m[1])
					seen[n] = true
					if n > max {
						max = n
					}
				}
				if max != len(args) {
					t.Errorf("%s: highest placeholder $%d but %d args", name, max, len(args))
				}
				for i := 1; i <= max; i++ {
					if !seen[i] {
						t.Errorf("%s: gap — $%d never referenced (where=%s)", name, i, where)
					}
				}
			}
		}
	}
}

// The institution scope is an OR of three branches. If it ever loses its parens,
// `AND action_type = $2` binds to the last branch alone and the filtered query
// returns every institution-targeted row regardless of action — a silent leak of
// other institutions' rows into this one's log.
func TestAuditLogScopeStaysParenthesised(t *testing.T) {
	where, _ := auditLogWhere("inst-1", "update_point_rules", "", "")

	scope := where[:strings.Index(where, " AND ")]
	if !strings.HasPrefix(scope, "(") || !strings.HasSuffix(scope, ")") {
		t.Fatalf("institution scope must be wrapped in parens before any AND, got %q", scope)
	}
	if strings.Count(scope, "(") != strings.Count(scope, ")") {
		t.Errorf("unbalanced parens in scope clause: %q", scope)
	}
	// And the AND must sit outside that group, not inside it.
	if strings.Contains(scope, "action_type") {
		t.Errorf("action filter leaked inside the scope group: %q", scope)
	}
}

// Ownership is recorded on the row (migration 037). Losing this branch would
// re-hide every entry whose target is neither the institution nor one of its
// users — group changes and teacher invites, which is the bug 037 fixed.
func TestAuditLogScopeMatchesOwnedEntries(t *testing.T) {
	where, args := auditLogWhere("inst-1", "", "", "")

	if !strings.Contains(where, "al.institution_id = $1") {
		t.Errorf("scope must match entries by recorded owner, got %q", where)
	}
	// The legacy branches stay: handlers outside this package write no
	// institution_id, and super-admin actions on this institution must remain
	// visible to it.
	if !strings.Contains(where, "al.target_id = $1") {
		t.Errorf("scope must still match entries targeting the institution row, got %q", where)
	}
	if !strings.Contains(where, "SELECT id FROM users WHERE institution_id = $1") {
		t.Errorf("scope must still match entries targeting the institution's users, got %q", where)
	}
	if len(args) != 1 {
		t.Errorf("all three branches share $1; expected 1 arg, got %d", len(args))
	}
}
