package teacher

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// teacherFixture is one institution holding two teachers and two students:
//
//	TeacherID       — assigned to GroupID, which contains StudentID
//	LonerTeacherID  — assigned to no group
//	OtherStudentID  — in the institution but in no group
//
// Rows are removed in dependency order on cleanup; users.institution_id has no
// ON DELETE clause, so the users go before the institution.
type teacherFixture struct {
	InstitutionID  string
	TeacherID      string
	LonerTeacherID string
	StudentID      string
	OtherStudentID string
	GroupID        string
	QuizID         string
	OtherQuizID    string
}

func seedTeacherFixture(t *testing.T, pool *pgxpool.Pool) teacherFixture {
	t.Helper()
	ctx := context.Background()
	// A per-run suffix keeps the unique constraints on email and referral codes
	// satisfied when the suite runs twice against the same scratch database.
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var f teacherFixture

	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}

	must("institution", pool.QueryRow(ctx, `
		INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
		VALUES ('Fixture School '||$1, 'school', 'fixture-'||$1||'@example.test', 'S'||$1, 'T'||$1, 'verified')
		RETURNING id`, tag).Scan(&f.InstitutionID))

	newUser := func(role, label string, dest *string) {
		t.Helper()
		must(label, pool.QueryRow(ctx, `
			INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
			VALUES (gen_random_uuid(), $1, $1, $2, $3, $4)
			RETURNING id`,
			label+" "+tag, label+"-"+tag+"@example.test", role, f.InstitutionID).Scan(dest))
	}
	newUser("teacher", "assigned-teacher", &f.TeacherID)
	newUser("teacher", "loner-teacher", &f.LonerTeacherID)
	newUser("student", "group-student", &f.StudentID)
	newUser("student", "other-student", &f.OtherStudentID)

	must("group", pool.QueryRow(ctx, `
		INSERT INTO groups (institution_id, name, invite_code)
		VALUES ($1, 'Fixture Class', 'INV'||$2)
		RETURNING id`, f.InstitutionID, tag).Scan(&f.GroupID))

	_, err := pool.Exec(ctx,
		`INSERT INTO group_teachers (group_id, user_id) VALUES ($1, $2)`, f.GroupID, f.TeacherID)
	must("group_teachers", err)
	_, err = pool.Exec(ctx,
		`INSERT INTO group_students (group_id, user_id) VALUES ($1, $2)`, f.GroupID, f.StudentID)
	must("group_students", err)

	must("quiz", pool.QueryRow(ctx, `
		INSERT INTO quizzes (institution_id, created_by, title, type, status)
		VALUES ($1, $2, 'Fixture Quiz', 'knowledge_check', 'published')
		RETURNING id`, f.InstitutionID, f.TeacherID).Scan(&f.QuizID))
	must("other quiz", pool.QueryRow(ctx, `
		INSERT INTO quizzes (institution_id, created_by, title, type, status)
		VALUES ($1, $2, 'Other Quiz', 'knowledge_check', 'published')
		RETURNING id`, f.InstitutionID, f.LonerTeacherID).Scan(&f.OtherQuizID))

	t.Cleanup(func() {
		ctx := context.Background()
		quizzes := []string{f.QuizID, f.OtherQuizID}
		users := []string{f.TeacherID, f.LonerTeacherID, f.StudentID, f.OtherStudentID}
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id = ANY($1)`, quizzes)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id = ANY($1)`, quizzes)
		pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, f.GroupID)
		pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, users)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id = $1`, f.InstitutionID)
	})

	return f
}
