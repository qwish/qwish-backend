package metrics

import "fmt"

// ScopeKind names the dimension a request is filtered on. The kind decides
// which sources can answer at all: a source with no predicate for the active
// kind has its metrics dropped with a reason rather than answered platform-wide.
type ScopeKind string

const (
	ScopeNone        ScopeKind = ""                // platform-wide; super admin only
	ScopeInstitution ScopeKind = "institution"     // one institution
	ScopeClasses     ScopeKind = "teacher_classes" // students in a teacher's groups
	ScopeQuizzes     ScopeKind = "teacher_quizzes" // quizzes a teacher authored
)

// Scope is a resolved filter: the kind, plus the id it filters on. ID is empty
// exactly when Kind is ScopeNone.
type Scope struct {
	Kind ScopeKind
	ID   string
}

func (s Scope) Active() bool { return s.Kind != ScopeNone && s.ID != "" }

// ParseScopeKind reads the teacher-facing `scope` query parameter. Only the two
// teacher kinds are selectable: institution scope is derived from the token, so
// accepting it here would let a teacher widen their own view.
func ParseScopeKind(s string) (ScopeKind, error) {
	switch s {
	case "", "classes":
		return ScopeClasses, nil
	case "quizzes":
		return ScopeQuizzes, nil
	default:
		return "", fmt.Errorf("unknown scope %q; valid values are classes, quizzes", s)
	}
}

// DropReason is the message shown to the caller when a metric cannot answer the
// active scope. Phrased for the role that will read it.
func DropReason(kind ScopeKind) string {
	switch kind {
	case ScopeInstitution:
		return "not institution-scopable"
	case ScopeClasses:
		return "not available when scoped to your classes"
	case ScopeQuizzes:
		return "not available when scoped to your quizzes"
	default:
		return "not scopable"
	}
}

// scopePredicate renders a source's filter for the active kind at parameter
// position n, or "" when the source cannot answer that kind.
func scopePredicate(s source, kind ScopeKind, n int) string {
	if kind == ScopeNone {
		return ""
	}
	tmpl, ok := s.Scopes[kind]
	if !ok {
		return ""
	}
	return fmt.Sprintf(tmpl, n)
}

// classMembers is the set of students taught by $n. Used by every
// student-centred source under ScopeClasses.
const classMembers = `SELECT gs.user_id
             FROM group_students gs
             JOIN group_teachers gt ON gt.group_id = gs.group_id
            WHERE gt.user_id = $%d`

// authoredQuizzes is the set of quizzes written by $n.
const authoredQuizzes = `SELECT id FROM quizzes WHERE created_by = $%d`
