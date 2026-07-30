package teacher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qwish/backend/internal/domain/metrics"
	"github.com/qwish/backend/internal/middleware"
)

func request(t *testing.T, url, userID, instID string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	ctx := req.Context()
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, userID)
	if instID != "" {
		ctx = context.WithValue(ctx, middleware.ContextKeyInstID, instID)
	}
	return req.WithContext(ctx)
}

func TestTeacherScopeDefaultsToClasses(t *testing.T) {
	pool := openTestDB(t)
	f := seedTeacherFixture(t, pool)
	resolve := MetricsScopeResolver(pool)

	sc, note, err := resolve(request(t, "/teacher/metrics", f.TeacherID, f.InstitutionID))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sc.Kind != metrics.ScopeClasses {
		t.Errorf("kind = %q, want teacher_classes", sc.Kind)
	}
	if sc.ID != f.TeacherID {
		t.Errorf("id = %q, want the teacher id", sc.ID)
	}
	if note.Reason != "" {
		t.Errorf("assigned teacher got a fallback reason: %q", note.Reason)
	}
}

// The quizzes path returns before any group lookup, so this needs no database:
// a nil pool proves the short-circuit is real rather than incidental.
func TestTeacherScopeQuizzes(t *testing.T) {
	resolve := MetricsScopeResolver(nil)

	sc, note, err := resolve(request(t, "/teacher/metrics?scope=quizzes",
		"22222222-2222-2222-2222-222222222222", "11111111-1111-1111-1111-111111111111"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sc.Kind != metrics.ScopeQuizzes || note.Effective != metrics.ScopeQuizzes {
		t.Errorf("kind = %q, note = %+v", sc.Kind, note)
	}
}

// PRD §5.4: an unassigned teacher sees all institution students. Analytics must
// honour that and must say so, or the teacher reads institution-wide numbers as
// their own class's.
func TestUnassignedTeacherFallsBackToInstitution(t *testing.T) {
	pool := openTestDB(t)
	f := seedTeacherFixture(t, pool)
	resolve := MetricsScopeResolver(pool)

	sc, note, err := resolve(request(t, "/teacher/metrics", f.LonerTeacherID, f.InstitutionID))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sc.Kind != metrics.ScopeInstitution {
		t.Errorf("kind = %q, want institution", sc.Kind)
	}
	if sc.ID != f.InstitutionID {
		t.Errorf("id = %q, want the institution id", sc.ID)
	}
	if note.Requested != metrics.ScopeClasses || note.Effective != metrics.ScopeInstitution {
		t.Errorf("note = %+v", note)
	}
	if note.Reason == "" {
		t.Error("fallback carries no reason for the UI to show")
	}
}

// The quizzes scope never falls back: a teacher with no classes may still have
// authored quizzes, and "no classes" is not a reason to widen that view.
func TestUnassignedTeacherQuizzesScopeDoesNotFallBack(t *testing.T) {
	// Also nil-pool: reaching the database here would itself be the bug.
	resolve := MetricsScopeResolver(nil)

	sc, note, err := resolve(request(t, "/teacher/metrics?scope=quizzes",
		"33333333-3333-3333-3333-333333333333", "11111111-1111-1111-1111-111111111111"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sc.Kind != metrics.ScopeQuizzes || note.Reason != "" {
		t.Errorf("kind = %q, note = %+v", sc.Kind, note)
	}
}

func TestTeacherScopeRejectsUnknownKind(t *testing.T) {
	// Parsing fails before anything is looked up, so no database is needed.
	resolve := MetricsScopeResolver(nil)

	_, _, err := resolve(request(t, "/teacher/metrics?scope=everything",
		"44444444-4444-4444-4444-444444444444", "11111111-1111-1111-1111-111111111111"))
	if err == nil {
		t.Fatal("want an error for an unknown scope")
	}
	if !errors.Is(err, metrics.ErrBadScopeRequest) {
		t.Errorf("err = %v, want it to wrap ErrBadScopeRequest so the handler answers 400", err)
	}
}

// A token with no user id is never answerable, whatever the scope.
func TestTeacherScopeRejectsMissingUser(t *testing.T) {
	resolve := MetricsScopeResolver(nil)
	req := httptest.NewRequest(http.MethodGet, "/teacher/metrics", nil)
	if _, _, err := resolve(req); !errors.Is(err, metrics.ErrBadScopeRequest) {
		t.Errorf("err = %v, want ErrBadScopeRequest", err)
	}
}
