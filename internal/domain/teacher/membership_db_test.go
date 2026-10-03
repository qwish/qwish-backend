package teacher

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

type twoInstitutes struct{ InstA, InstB, TeacherB, Student, ClassB string }

// seedTwoInstitutes: a student enrolled at A and B whose active institute is
// A, in a class at B taught by TeacherB.
func seedTwoInstitutes(t *testing.T, pool *pgxpool.Pool) twoInstitutes {
	t.Helper()
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var s twoInstitutes
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for label, dest := range map[string]*string{"a": &s.InstA, "b": &s.InstB} {
		must(pool.QueryRow(ctx, `INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
			VALUES ($1||$2, 'school', $1||$2||'@example.test', 'S'||$1||$2, 'T'||$1||$2, 'verified') RETURNING id`, label, tag).Scan(dest))
	}
	must(pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
		VALUES (gen_random_uuid(), 'tb', 'tb', 'tb'||$1||'@example.test', 'teacher', $2) RETURNING id`, tag, s.InstB).Scan(&s.TeacherB))
	must(pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		VALUES (gen_random_uuid(), 'two', 'two', 'two'||$1||'@example.test', 'student') RETURNING id`, tag).Scan(&s.Student))
	must(pool.QueryRow(ctx, `INSERT INTO groups (institution_id, name, invite_code) VALUES ($1,'B class','B'||$2) RETURNING id`, s.InstB, tag).Scan(&s.ClassB))
	_, err := pool.Exec(ctx, `INSERT INTO group_teachers (group_id, user_id) VALUES ($1,$2)`, s.ClassB, s.TeacherB)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at, join_route)
		VALUES ($1,$3,'two','active',now()-interval '2 days','code'), ($2,$3,'two','active',now()-interval '1 day','code')`, s.InstA, s.InstB, s.Student)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO group_students (group_id, user_id) VALUES ($1,$2)`, s.ClassB, s.Student)
	must(err)
	_, err = pool.Exec(ctx, `UPDATE users SET institution_id=$1 WHERE id=$2`, s.InstA, s.Student)
	must(err)
	t.Cleanup(func() {
		insts := []string{s.InstA, s.InstB}
		pool.Exec(ctx, `DELETE FROM group_students WHERE group_id=$1`, s.ClassB)
		pool.Exec(ctx, `DELETE FROM group_teachers WHERE group_id=$1`, s.ClassB)
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id = ANY($1)`, insts)
		pool.Exec(ctx, `DELETE FROM groups WHERE institution_id = ANY($1)`, insts)
		pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []string{s.TeacherB, s.Student})
		pool.Exec(ctx, `DELETE FROM institutions WHERE id = ANY($1)`, insts)
	})
	return s
}

func teacherRequest(r *http.Request, teacherID, instID string) *http.Request {
	ctx := context.WithValue(r.Context(), middleware.ContextKeyUserID, teacherID)
	ctx = context.WithValue(ctx, middleware.ContextKeyRole, "teacher")
	ctx = context.WithValue(ctx, middleware.ContextKeyInstID, instID)
	return r.WithContext(ctx)
}

func TestTeacherSeesStudentWhoseActiveInstituteIsOther(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	h := NewHandler(pool)

	req := teacherRequest(httptest.NewRequest("GET", "/", nil), s.TeacherB, s.InstB)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("userId", s.Student)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.GetStudent(w, req)
	if w.Code != 200 {
		t.Fatalf("teacher at B must see a B student whose active institute is A; got %d %s", w.Code, w.Body)
	}

	w = httptest.NewRecorder()
	h.ListStudents(w, teacherRequest(httptest.NewRequest("GET", "/", nil), s.TeacherB, s.InstB))
	if w.Code != 200 || !strings.Contains(w.Body.String(), s.Student) {
		t.Fatalf("teacher roster at B must list the student; got %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"join_route":"code"`) {
		t.Fatalf("roster rows must say how the student joined: %s", w.Body)
	}
}

func TestGetClassReportsJoiningSwitch(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	pool.Exec(context.Background(), `UPDATE groups SET joining_enabled=false WHERE id=$1`, s.ClassB)
	req := teacherRequest(httptest.NewRequest("GET", "/", nil), s.TeacherB, s.InstB)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("classId", s.ClassB)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	NewHandler(pool).GetClass(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"joining_enabled":false`) {
		t.Fatalf("class detail must report the joining switch: %d %s", w.Code, w.Body)
	}
}

// Institute B's roster average must not include attempts on institute A's
// private quizzes (D15).
func TestRosterAverageIgnoresOtherInstitutesQuizzes(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	ctx := context.Background()
	quiz := func(inst string, score float64) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO quizzes (institution_id, created_by, title, type, status, visibility)
			VALUES ($1,$2,'q','knowledge_check','published','institution') RETURNING id`, inst, s.TeacherB).Scan(&id); err != nil {
			t.Fatal(err)
		}
		pool.Exec(ctx, `INSERT INTO quiz_attempts (quiz_id,user_id,status,completed_at,score_pct) VALUES ($1,$2,'completed',now(),$3)`, id, s.Student, score)
		return id
	}
	qa, qb := quiz(s.InstA, 10), quiz(s.InstB, 90)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id IN ($1,$2)`, qa, qb)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id IN ($1,$2)`, qa, qb)
	})
	w := httptest.NewRecorder()
	NewHandler(pool).ListStudents(w, teacherRequest(httptest.NewRequest("GET", "/", nil), s.TeacherB, s.InstB))
	if !strings.Contains(w.Body.String(), `"average_score":90`) {
		t.Fatalf("B's roster average must use only B's (or public) quizzes: %s", w.Body)
	}
	req := teacherRequest(httptest.NewRequest("GET", "/", nil), s.TeacherB, s.InstB)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("classId", s.ClassB)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w = httptest.NewRecorder()
	NewHandler(pool).GetClass(w, req)
	if n := strings.Count(w.Body.String(), `"average_score":90`); n != 2 {
		t.Fatalf("class and roster averages must both exclude A's quizzes (want 2 matches, got %d): %s", n, w.Body)
	}
}
