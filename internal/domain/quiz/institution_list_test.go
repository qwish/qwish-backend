package quiz

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Same failure mode as TestStudentListWherePlaceholders: ListForInstitution
// appends LIMIT/OFFSET at $(len(args)+1), so a placeholder that disagrees with
// the arg count is a runtime bind error, not a compile error.
func TestInstitutionListWherePlaceholders(t *testing.T) {
	ph := regexp.MustCompile(`\$(\d+)`)

	for _, status := range []string{"", "draft", "closed"} {
		for _, typ := range []string{"", "play_and_win"} {
			for _, search := range []string{"", "math"} {
				name := fmt.Sprintf("status=%q type=%q search=%q", status, typ, search)
				where, args := institutionListWhere("inst-1", status, typ, search)

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

// The whole point of this query existing: unlike the student feed it must not
// pin a status, and must not let another institution's public quizzes in.
func TestInstitutionListWhereScope(t *testing.T) {
	where, _ := institutionListWhere("inst-1", "", "", "")
	if strings.Contains(where, "status = 'published'") {
		t.Error("admin roster must not pin status='published'")
	}
	if strings.Contains(where, "visibility = 'public'") {
		t.Error("admin roster must not widen to public quizzes from other institutions")
	}
	if !strings.Contains(where, "q.institution_id = $1") {
		t.Errorf("admin roster must scope to the caller's institution, got %s", where)
	}
}
