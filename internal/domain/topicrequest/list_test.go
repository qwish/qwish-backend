package topicrequest

import (
	"regexp"
	"strconv"
	"testing"
)

func TestTopicListWherePlaceholders(t *testing.T) {
	ph := regexp.MustCompile(`\$(\d+)`)
	for _, status := range []string{"", "open", "done"} {
		for _, assigned := range []string{"", "none"} {
			for _, search := range []string{"", "redox"} {
				where, args, bad := topicListWhere("inst", status, assigned, search)
				if bad != "" {
					t.Fatalf("%s/%s/%s rejected: %s", status, assigned, search, bad)
				}
				max := 0
				for _, m := range ph.FindAllStringSubmatch(where, -1) {
					if n, _ := strconv.Atoi(m[1]); n > max {
						max = n
					}
				}
				if max != len(args) {
					t.Errorf("status=%q assigned=%q search=%q: $%d but %d args", status, assigned, search, max, len(args))
				}
			}
		}
	}
	if _, _, bad := topicListWhere("inst", "archived", "", ""); bad == "" {
		t.Error("unknown status should be rejected")
	}
}
