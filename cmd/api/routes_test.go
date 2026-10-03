package main

import (
	"os"
	"strings"
	"testing"
)

// block returns the body of the first `r.Route(prefix, func...) { ... }` in src.
func block(t *testing.T, src, prefix string) string {
	t.Helper()
	start := strings.Index(src, `r.Route("`+prefix+`", func(r chi.Router) {`)
	if start < 0 {
		t.Fatalf("no %s route block", prefix)
	}
	depth, i := 0, strings.Index(src[start:], "{")+start
	for j := i; j < len(src); j++ {
		switch src[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[i:j]
			}
		}
	}
	t.Fatalf("unterminated %s block", prefix)
	return ""
}

// ponytail: source-level check because the router is built inline in main();
// move to a real router test if route setup is ever extracted into a function.
func TestTeacherToolRoutesLiveUnderTeacher(t *testing.T) {
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	teacher, leadership := block(t, src, "/teacher"), block(t, src, "/leadership")
	for _, route := range []string{
		`r.Post("/remedial-groups", teacherH.CreateRemedialGroup)`,
		`r.Get("/remedial-groups/{groupId}/progress", teacherH.RemedialProgress)`,
		`r.Get("/notices/audiences", noticeH.Audiences)`,
		`Post("/notices", noticeH.Send)`,
		`r.Get("/notices", noticeH.List)`,
	} {
		if !strings.Contains(teacher, route) {
			t.Errorf("%s must be in the /teacher block", route)
		}
		if strings.Contains(leadership, route) {
			t.Errorf("%s must not be in the /leadership block", route)
		}
	}
	if !strings.Contains(block(t, src, "/institution"), `Post("/notices", noticeH.Send)`) {
		t.Error("institution notices route missing")
	}
}
