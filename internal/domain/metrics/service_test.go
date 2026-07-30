package metrics

import (
	"strings"
	"testing"
)

func TestUserScopePred(t *testing.T) {
	cases := []struct {
		sc       Scope
		want     string
		contains string
	}{
		{Scope{}, "TRUE", ""},
		{Scope{Kind: ScopeInstitution, ID: "x"}, "", "u.institution_id = $1"},
		{Scope{Kind: ScopeClasses, ID: "x"}, "", "group_teachers"},
		{Scope{Kind: ScopeQuizzes, ID: "x"}, "FALSE", ""},
	}
	for _, c := range cases {
		got := userScopePred(c.sc, "u", 1)
		if c.want != "" && got != c.want {
			t.Errorf("userScopePred(%q) = %q, want %q", c.sc.Kind, got, c.want)
		}
		if c.contains != "" && !strings.Contains(got, c.contains) {
			t.Errorf("userScopePred(%q) = %q, want it to contain %q", c.sc.Kind, got, c.contains)
		}
	}
}

func TestQuizScopePred(t *testing.T) {
	if got := quizScopePred(Scope{}, "q", 1); got != "TRUE" {
		t.Errorf("unscoped = %q, want TRUE", got)
	}
	if got := quizScopePred(Scope{Kind: ScopeQuizzes, ID: "x"}, "q", 1); !strings.Contains(got, "q.created_by = $1") {
		t.Errorf("quizzes scope = %q", got)
	}
	if got := quizScopePred(Scope{Kind: ScopeClasses, ID: "x"}, "q", 1); got != "FALSE" {
		t.Errorf("classes scope = %q, want FALSE", got)
	}
}

// An inactive scope binds nothing, so a snapshot query built for it must take
// no arguments — a stray nil would have no placeholder to bind against.
func TestScopeArgs(t *testing.T) {
	if got := scopeArgs(Scope{}); len(got) != 0 {
		t.Errorf("unscoped args = %v, want none", got)
	}
	got := scopeArgs(Scope{Kind: ScopeInstitution, ID: "abc"})
	if len(got) != 1 || got[0] != "abc" {
		t.Errorf("scoped args = %v, want [abc]", got)
	}
}
