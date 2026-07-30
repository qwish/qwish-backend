package metrics

import (
	"context"
	"testing"
	"time"
)

// fixtureWindow covers the seeded attempts, which are recorded at now().
func fixtureWindow() Window {
	now := time.Now().In(IST)
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, IST)
	return Window{From: today.AddDate(0, 0, -7), To: today, Gran: GranDay}
}

// seedAttempt records one completed attempt by user on quiz, now.
func seedAttempt(t *testing.T, f scopeFixture, userID, quizID string) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO quiz_attempts (quiz_id, user_id, status, score_pct, started_at, completed_at)
		VALUES ($1, $2, 'completed', 80, now(), now())`, quizID, userID)
	if err != nil {
		t.Fatalf("seed attempt: %v", err)
	}
}

func totalFor(t *testing.T, svc *MetricsService, sc Scope, id string) float64 {
	t.Helper()
	sel, _, err := SelectMetrics([]string{id}, sc.Kind)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}
	totals, err := svc.Totals(context.Background(), sel, fixtureWindow(), sc)
	if err != nil {
		t.Fatalf("Totals: %v", err)
	}
	v, ok := totals[id]
	if !ok {
		t.Fatalf("totals has no %q: %+v", id, totals)
	}
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	default:
		t.Fatalf("totals[%q] is %T", id, v)
		return 0
	}
}

// A class scope must count the teacher's own students and no one else's.
func TestClassScopeExcludesOutsiders(t *testing.T) {
	pool := openTestDB(t)
	f := seedScopeFixture(t, pool)
	svc := NewMetricsService(pool)

	seedAttempt(t, f, f.StudentID, f.QuizID)      // in the class
	seedAttempt(t, f, f.OtherStudentID, f.QuizID) // same institution, no class

	classScope := Scope{Kind: ScopeClasses, ID: f.TeacherID}
	if got := totalFor(t, svc, classScope, "attempts_completed"); got != 1 {
		t.Errorf("class scope counted %v attempts, want 1", got)
	}

	instScope := Scope{Kind: ScopeInstitution, ID: f.InstitutionID}
	if got := totalFor(t, svc, instScope, "attempts_completed"); got != 2 {
		t.Errorf("institution scope counted %v attempts, want 2", got)
	}
}

// A quizzes scope must count attempts on the teacher's quizzes regardless of
// who took them, and none on another author's quiz.
func TestQuizScopeExcludesOtherAuthors(t *testing.T) {
	pool := openTestDB(t)
	f := seedScopeFixture(t, pool)
	svc := NewMetricsService(pool)

	seedAttempt(t, f, f.OtherStudentID, f.QuizID) // their quiz, outsider taker
	seedAttempt(t, f, f.StudentID, f.OtherQuizID) // their student, other author

	quizScope := Scope{Kind: ScopeQuizzes, ID: f.TeacherID}
	if got := totalFor(t, svc, quizScope, "attempts_completed"); got != 1 {
		t.Errorf("quiz scope counted %v attempts, want 1", got)
	}
}

// Distributions must omit shapes it cannot express rather than answering them
// institution-wide.
func TestDistributionsDropsUnanswerableShapes(t *testing.T) {
	pool := openTestDB(t)
	f := seedScopeFixture(t, pool)
	svc := NewMetricsService(pool)

	shapes, dropped, err := svc.Distributions(context.Background(),
		Scope{Kind: ScopeQuizzes, ID: f.TeacherID})
	if err != nil {
		t.Fatalf("Distributions: %v", err)
	}
	if _, present := shapes["streak_bands"]; present {
		t.Error("streak_bands answered under a quizzes scope")
	}
	var sawStreaks bool
	for _, d := range dropped {
		if d.ID == "streak_bands" {
			sawStreaks = true
		}
	}
	if !sawStreaks {
		t.Errorf("streak_bands not reported as dropped: %+v", dropped)
	}
	if _, present := shapes["score_histogram"]; !present {
		t.Error("score_histogram should answer a quizzes scope")
	}
}

// Every scope kind must produce SQL Postgres actually accepts. The unit tests
// prove the text; only this proves the query plans.
func TestEveryScopeKindExecutes(t *testing.T) {
	pool := openTestDB(t)
	f := seedScopeFixture(t, pool)
	svc := NewMetricsService(pool)
	ctx := context.Background()

	for _, sc := range []Scope{
		{},
		{Kind: ScopeInstitution, ID: f.InstitutionID},
		{Kind: ScopeClasses, ID: f.TeacherID},
		{Kind: ScopeQuizzes, ID: f.TeacherID},
	} {
		sel, _, err := SelectMetrics(nil, sc.Kind)
		if err != nil {
			t.Fatalf("SelectMetrics(%q): %v", sc.Kind, err)
		}
		if _, err := svc.Series(ctx, sel, fixtureWindow(), sc); err != nil {
			t.Errorf("Series(%q): %v", sc.Kind, err)
		}
		if _, err := svc.Totals(ctx, sel, fixtureWindow(), sc); err != nil {
			t.Errorf("Totals(%q): %v", sc.Kind, err)
		}
		if _, _, err := svc.Distributions(ctx, sc); err != nil {
			t.Errorf("Distributions(%q): %v", sc.Kind, err)
		}
	}
}

func TestPointsLiabilityRefusesQuizScope(t *testing.T) {
	pool := openTestDB(t)
	f := seedScopeFixture(t, pool)
	svc := NewMetricsService(pool)

	if _, err := svc.PointsLiability(context.Background(),
		Scope{Kind: ScopeQuizzes, ID: f.TeacherID}); err == nil {
		t.Fatal("want ErrScopeUnsupported")
	}
}
