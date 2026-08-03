package quiz

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"testing"
)

// The list query appends LIMIT/OFFSET at $(len(args)+1) and $(len(args)+2).
// If studentListWhere ever emits a placeholder that disagrees with its arg
// count, ListForStudent breaks at runtime with a bind error. Cover every
// combination of the optional filters.
func TestStudentListWherePlaceholders(t *testing.T) {
	ph := regexp.MustCompile(`\$(\d+)`)

	for _, inst := range []string{"", "inst-1"} {
		for _, typ := range []string{"", "play_and_win"} {
			for _, saved := range []string{"", "true"} {
				for _, search := range []string{"", "math"} {
					name := fmt.Sprintf("inst=%q type=%q saved=%q search=%q", inst, typ, saved, search)
					where, args := studentListWhere(inst, typ, saved, search, "user-1")

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
					// Every placeholder from $1..$max must be present, or an
					// arg is silently unused and the filters shift.
					for i := 1; i <= max; i++ {
						if !seen[i] {
							t.Errorf("%s: gap — $%d never referenced (where=%s)", name, i, where)
						}
					}
				}
			}
		}
	}
}

func TestFindSeqScans(t *testing.T) {
	// Shape mirrors EXPLAIN FORMAT JSON: nested "Plans" arrays.
	raw := `{"Node Type":"Limit","Plans":[
		{"Node Type":"Sort","Plans":[
			{"Node Type":"Seq Scan","Relation Name":"quizzes"},
			{"Node Type":"Index Scan","Relation Name":"users"},
			{"Node Type":"Nested Loop","Plans":[
				{"Node Type":"Seq Scan","Relation Name":"institutions"}]}]}]}`

	var plan map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatal(err)
	}
	got := findSeqScans(plan)
	want := []string{"quizzes", "institutions"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
