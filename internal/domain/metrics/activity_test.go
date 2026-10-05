package metrics

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestActivityWindowUsesLocalDayAndWholeWeeks(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// UTC Sunday is still Saturday at this institution.
	now := time.Date(2026, 3, 8, 2, 0, 0, 0, time.UTC)
	from, to := activityWindow(now, loc)
	if to.Format(DateLayout) != "2026-03-07" || from.Weekday() != time.Sunday {
		t.Fatalf("window = %s to %s", from, to)
	}
	days := 0
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		days++
	}
	if days != 364 {
		t.Fatalf("days = %d, want 364", days)
	}
}

func TestInstitutionActivityRejectsMissingInstitution(t *testing.T) {
	w := httptest.NewRecorder()
	// A request cannot turn a missing token scope into an arbitrary institute.
	(&Handler{}).InstitutionActivity(w, httptest.NewRequest("GET", "/activity-heatmap?institution_id=other", nil))
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestActivityScopesCountsAndFillsZeroDays(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	a := seedScopeFixture(t, pool)
	b := seedScopeFixture(t, pool)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM enrollments WHERE user_id IN ($1,$2)`, a.StudentID, b.StudentID)
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE user_id IN ($1,$2)`, a.StudentID, b.StudentID)
	})
	for _, f := range []scopeFixture{a, b} {
		_, err := pool.Exec(ctx, `INSERT INTO enrollments (institution_id,user_id,full_name,status)
   SELECT $1,$2,'Activity student','active'
   WHERE NOT EXISTS (SELECT 1 FROM enrollments WHERE institution_id=$1 AND user_id=$2 AND status IN ('active','suspended'))`, f.InstitutionID, f.StudentID)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Historical completions belong to the institute even after a transfer.
	if _, err := pool.Exec(ctx, `UPDATE enrollments SET joined_at='2024-01-01', status='transferred', ended_at='2025-06-06' WHERE user_id=$1 AND institution_id=$2`, a.StudentID, a.InstitutionID); err != nil {
		t.Fatal(err)
	}
	// The active institution field must not override the enrollment membership.
	if _, err := pool.Exec(ctx, `UPDATE users SET institution_id=$1 WHERE id=$2`, b.InstitutionID, a.StudentID); err != nil {
		t.Fatal(err)
	}
	timestamps := []string{"2025-06-04T18:29:00Z", "2025-06-04T18:30:00Z"}
	for i, quiz := range []string{a.QuizID, a.OtherQuizID} {
		if _, err := pool.Exec(ctx, `UPDATE quizzes SET created_at=$1 WHERE id=$2`, timestamps[i], quiz); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO quiz_attempts (quiz_id,user_id,status,completed_at) VALUES ($1,$2,'completed',$3)`, quiz, a.StudentID, timestamps[i]); err != nil {
			t.Fatal(err)
		}
	}
	// An outside student's attempt on this institute's quiz and this institute's
	// student's attempt elsewhere are both excluded. So are unfinished attempts.
	for _, item := range []struct{ quiz, user, status string }{
		{a.QuizID, b.StudentID, "completed"},
		{b.QuizID, a.StudentID, "completed"},
		{a.QuizID, a.StudentID, "abandoned"},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO quiz_attempts (quiz_id,user_id,status,completed_at) VALUES ($1,$2,$3,'2025-06-04T18:30:00Z')`, item.quiz, item.user, item.status); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2025, 6, 5, 12, 0, 0, 0, time.UTC)
	got, err := NewMetricsService(pool).Activity(ctx, a.InstitutionID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Days) < 358 || len(got.Days) > 364 {
		t.Fatalf("days = %d", len(got.Days))
	}
	created, completed := 0, 0
	for _, day := range got.Days {
		created += day.QuizzesCreated
		completed += day.QuizzesCompleted
		if strings.HasPrefix(day.Date, "2025-06-0") && (day.Date == "2025-06-04" || day.Date == "2025-06-05") {
			if day.QuizzesCreated != 1 || day.QuizzesCompleted != 1 {
				t.Fatalf("midnight bucket: %+v", day)
			}
		} else if day.QuizzesCreated != 0 || day.QuizzesCompleted != 0 {
			t.Fatalf("expected quiet day: %+v", day)
		}
	}
	if created != 2 || completed != 2 {
		t.Fatalf("totals = %d creations, %d completions", created, completed)
	}
}
