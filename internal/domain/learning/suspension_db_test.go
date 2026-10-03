package learning

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

// A student suspended at an institute can't see or start that institute's
// assigned work or curricula (suspension pauses the institute, per D13/D12).
func TestSuspendedEnrollmentPausesInstituteWork(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f := newInsightFixture(t, db)
	quizID, _ := f.quiz(1)
	f.exec(`UPDATE quizzes SET status='published',published_at=now() WHERE id=$1`, quizID)
	assignment := f.id(`INSERT INTO learning_assignments(institution_id,group_id,quiz_id,purpose,created_by) VALUES($1,$2,$3,'practice',$4) RETURNING id`, f.inst, f.group, quizID, f.teacher)
	f.exec(`INSERT INTO learning_assignment_recipients(assignment_id,student_id) VALUES($1,$2)`, assignment, f.student)
	f.exec(`UPDATE enrollments SET status='suspended' WHERE user_id=$1 AND institution_id=$2`, f.student, f.inst)

	asStudent := func(path string) string {
		req := httptest.NewRequest("GET", path, nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUserID, f.student))
		w := httptest.NewRecorder()
		if strings.Contains(path, "curricula") {
			f.h.StudentCurricula(w, req)
		} else {
			f.h.StudentAssignments(w, req)
		}
		return w.Body.String()
	}
	if body := asStudent("/users/me/assignments"); strings.Contains(body, assignment) {
		t.Fatalf("suspended student still sees the assignment: %s", body)
	}
	if body := asStudent("/users/me/curricula"); strings.Contains(body, f.group) {
		t.Fatalf("suspended student still sees the class curriculum: %s", body)
	}
	if _, err := f.svc.Start(context.Background(), f.student, quizID, assignment); err == nil {
		t.Fatal("suspended student must not start the institute's assignment")
	}
}
