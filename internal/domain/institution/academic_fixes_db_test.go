package institution

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAcademicMembershipAuthorizationAndIdempotence(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	h := NewHandler(pool, nil, nil, "", "")
	ctx := context.Background()
	call := func(fn http.HandlerFunc, admin, inst, user string) *httptest.ResponseRecorder {
		r := withURLParam(withURLParam(withAuth(httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"user_id":%q}`, user))), admin, "institution_admin", inst), "groupId", s.ClassB), "userId", user)
		w := httptest.NewRecorder()
		fn(w, r)
		return w
	}
	for _, fn := range []http.HandlerFunc{h.AddStudentToGroup, h.RemoveStudentFromGroup, h.AddTeacherToGroup, h.RemoveTeacherFromGroup} {
		if w := call(fn, s.AdminA, s.InstA, s.Student); w.Code != 404 {
			t.Fatalf("tenant boundary: %d %s", w.Code, w.Body)
		}
	}
	var audits int
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE target_id=$1`, s.ClassB).Scan(&audits)
	if audits != 0 {
		t.Fatal("unauthorized audit")
	}
	if w := call(h.AddTeacherToGroup, s.AdminB, s.InstB, s.Student); w.Code != 400 {
		t.Fatalf("student assigned: %d", w.Code)
	}
	var teacher string
	if err := pool.QueryRow(ctx, `INSERT INTO users(supabase_uid,full_name,display_name,email,role,institution_id) VALUES(gen_random_uuid(),'Teacher','Teacher',gen_random_uuid()||'@test.invalid','teacher',$1) RETURNING id`, s.InstB).Scan(&teacher); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM group_teachers WHERE user_id=$1`, teacher)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, teacher)
	})
	for _, status := range []string{"pending", "suspended"} {
		pool.Exec(ctx, `UPDATE users SET status=$2 WHERE id=$1`, teacher, status)
		if w := call(h.AddTeacherToGroup, s.AdminB, s.InstB, teacher); w.Code != 400 {
			t.Fatalf("%s teacher assigned: %d", status, w.Code)
		}
	}
	pool.Exec(ctx, `UPDATE users SET status='active' WHERE id=$1`, teacher)

	pool.Exec(ctx, `UPDATE users SET institution_id=$2 WHERE id=$1`, teacher, s.InstA)
	if w := call(h.AddTeacherToGroup, s.AdminB, s.InstB, teacher); w.Code != 400 {
		t.Fatalf("external teacher: %d", w.Code)
	}
	pool.Exec(ctx, `UPDATE users SET institution_id=$2,deleted_at=now() WHERE id=$1`, teacher, s.InstB)
	if w := call(h.AddTeacherToGroup, s.AdminB, s.InstB, teacher); w.Code != 400 {
		t.Fatalf("deleted teacher: %d", w.Code)
	}
	pool.Exec(ctx, `UPDATE users SET deleted_at=NULL WHERE id=$1`, teacher)
	if w := call(h.AddTeacherToGroup, uuid.NewString(), s.InstB, teacher); w.Code != 500 {
		t.Fatalf("missing audit actor: %d", w.Code)
	}
	var memberships int
	pool.QueryRow(ctx, `SELECT count(*) FROM group_teachers WHERE group_id=$1 AND user_id=$2`, s.ClassB, teacher).Scan(&memberships)
	if memberships != 0 {
		t.Fatal("audit failure did not roll back membership")
	}
	for i := 0; i < 2; i++ {
		if w := call(h.AddTeacherToGroup, s.AdminB, s.InstB, teacher); w.Code != 200 {
			t.Fatalf("valid teacher: %d %s", w.Code, w.Body)
		}
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE target_id=$1 AND action_type='add_teacher_to_group'`, s.ClassB).Scan(&audits)
	if audits != 1 {
		t.Fatalf("repeat generated %d audits", audits)
	}
	pool.Exec(ctx, `UPDATE groups SET archived_at=now() WHERE id=$1`, s.ClassB)
	if w := call(h.AddTeacherToGroup, s.AdminB, s.InstB, teacher); w.Code != 409 {
		t.Fatalf("ended class: %d", w.Code)
	}
	for i := 0; i < 2; i++ {
		if w := call(h.RemoveStudentFromGroup, s.AdminB, s.InstB, s.Student); w.Code != 200 {
			t.Fatalf("historical correction: %d", w.Code)
		}
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM group_student_history WHERE group_id=$1 AND user_id=$2`, s.ClassB, s.Student).Scan(&audits)
	if audits != 1 {
		t.Fatalf("history: %d", audits)
	}
}

func TestAcademicRosterAndDerivedLabels(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	ctx := context.Background()
	h := NewHandler(pool, nil, nil, "", "")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO enrollments(institution_id,user_id,full_name,status,joined_at,ended_at) VALUES($1,$2,'old','left',now()-interval '2 years',now()-interval '1 year')`, s.InstB, s.Student)
	w := httptest.NewRecorder()
	h.GetGroup(w, withURLParam(withAuth(httptest.NewRequest("GET", "/", nil), s.AdminB, "institution_admin", s.InstB), "groupId", s.ClassB))
	if w.Code != 200 || strings.Count(w.Body.String(), `"enrollment_id"`) != 1 || !strings.Contains(w.Body.String(), `"average_score":null`) {
		t.Fatalf("roster: %d %s", w.Code, w.Body)
	}
	exec(`UPDATE groups SET grade='10',section='A' WHERE id=$1`, s.ClassB)
	check := func(want string) {
		t.Helper()
		var state string
		if err := pool.QueryRow(ctx, `SELECT class_label_state||':'||COALESCE(grade,'')||':'||COALESCE(section,'') FROM enrollments WHERE user_id=$1 AND institution_id=$2 AND status='active'`, s.Student, s.InstB).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state != want {
			t.Fatalf("labels %s want %s", state, want)
		}
	}
	check("derived:10:A")
	var other string
	if err := pool.QueryRow(ctx, `INSERT INTO groups(institution_id,name,invite_code,grade,section) VALUES($1,'Other',gen_random_uuid()::text,'11','B') RETURNING id`, s.InstB).Scan(&other); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO group_students(group_id,user_id) VALUES($1,$2)`, other, s.Student)
	check("ambiguous::")
	exec(`UPDATE groups SET archived_at=now() WHERE id=$1`, other)
	check("derived:10:A")
	exec(`UPDATE groups SET archived_at=NULL WHERE id=$1`, other)
	check("ambiguous::")
	exec(`DELETE FROM group_students WHERE group_id=$1`, other)
	check("derived:10:A")
	exec(`UPDATE groups SET grade=NULL,section=NULL WHERE id=$1`, s.ClassB)
	check("unset::")
}

func TestAcademicMetricsPersistAssignmentAttribution(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	ctx := t.Context()
	h := NewHandler(pool, nil, nil, "", "")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	var other, quiz, assignment1, assignment2 string
	for dest, sql := range map[*string]string{
		&other: `INSERT INTO groups(institution_id,name,invite_code) VALUES($1,'Other',gen_random_uuid()::text) RETURNING id`,
		&quiz:  `INSERT INTO quizzes(institution_id,created_by,title,type,status) VALUES($1,$2,'Shared','knowledge_check','published') RETURNING id`,
	} {
		var err error
		if dest == &quiz {
			err = pool.QueryRow(ctx, sql, s.InstB, s.AdminB).Scan(dest)
		} else {
			err = pool.QueryRow(ctx, sql, s.InstB).Scan(dest)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, x := range []struct {
		group string
		id    *string
	}{{s.ClassB, &assignment1}, {other, &assignment2}} {
		if err := pool.QueryRow(ctx, `INSERT INTO learning_assignments(institution_id,group_id,quiz_id,purpose,created_by) VALUES($1,$2,$3,'practice',$4) RETURNING id`, s.InstB, x.group, quiz, s.AdminB).Scan(x.id); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO learning_assignment_recipients(assignment_id,student_id) VALUES($1,$2)`, *x.id, s.Student)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id=$1`, quiz)
		pool.Exec(ctx, `DELETE FROM learning_assignments WHERE quiz_id=$1`, quiz)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, quiz)
	})
	exec(`INSERT INTO quiz_attempts(quiz_id,user_id,status,score_pct,completed_at,assignment_id) VALUES($1,$2,'completed',20,now(),$3),($1,$2,'completed',80,now(),$3),($1,$2,'completed',100,now(),$4),($1,$2,'completed',0,now()-interval '31 days',$3),($1,$2,'completed',0,now(),NULL)`, quiz, s.Student, assignment1, assignment2)
	// A transfer must not change historical averages or retake counts.
	exec(`DELETE FROM group_students WHERE group_id=$1 AND user_id=$2`, s.ClassB, s.Student)
	w := httptest.NewRecorder()
	h.GetGroup(w, withURLParam(withAuth(httptest.NewRequest("GET", "/", nil), s.AdminB, "institution_admin", s.InstB), "groupId", s.ClassB))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"average_score":50`) || !strings.Contains(w.Body.String(), `"attempt_count_30d":2`) {
		t.Fatalf("detail metrics: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	h.ListGroups(w, withAuth(httptest.NewRequest("GET", "/", nil), s.AdminB, "institution_admin", s.InstB))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"average_score_30d":50`) || !strings.Contains(w.Body.String(), `"average_score_30d":100`) {
		t.Fatalf("list metrics: %d %s", w.Code, w.Body)
	}
}

func TestAcademicReopenArchivedDepartment(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	ctx := t.Context()
	var dept string
	if err := pool.QueryRow(ctx, `INSERT INTO departments(institution_id,name,archived_at) VALUES($1,'Archived',now()) RETURNING id`, s.InstB).Scan(&dept); err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, `UPDATE groups SET department_id=$2,archived_at=now() WHERE id=$1`, s.ClassB, dept)
	w := httptest.NewRecorder()
	NewHandler(pool, nil, nil, "", "").ReopenGroup(w, withURLParam(withAuth(httptest.NewRequest("POST", "/", nil), s.AdminB, "institution_admin", s.InstB), "groupId", s.ClassB))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "DEPARTMENT_ARCHIVED") {
		t.Fatalf("reopen: %d %s", w.Code, w.Body)
	}
}

func TestAcademicConcurrentGradeDerivation(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	ctx := t.Context()
	var first, second string
	for i, dest := range []*string{&first, &second} {
		if err := pool.QueryRow(ctx, `INSERT INTO groups(institution_id,name,invite_code,grade) VALUES($1,'Concurrent',gen_random_uuid()::text,$2) RETURNING id`, s.InstB, fmt.Sprint(i+10)).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan error, 2)
	for _, group := range []string{first, second} {
		go func(g string) {
			request := withURLParam(withAuth(httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"user_id":%q}`, s.Student))), s.AdminB, "institution_admin", s.InstB), "groupId", g)
			response := httptest.NewRecorder()
			NewHandler(pool, nil, nil, "", "").AddStudentToGroup(response, request)
			if response.Code != 200 {
				results <- fmt.Errorf("concurrent membership: %d %s", response.Code, response.Body)
				return
			}
			results <- nil
		}(group)
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var state string
	if err := pool.QueryRow(ctx, `SELECT class_label_state FROM enrollments WHERE user_id=$1 AND institution_id=$2 AND status='active'`, s.Student, s.InstB).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "ambiguous" {
		t.Fatalf("concurrent labels: %s", state)
	}
}

func TestAcademicStaffSearchBeyondFirstHundred(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	ctx := t.Context()
	h := NewHandler(pool, nil, nil, "", "")
	_, err := pool.Exec(ctx, `INSERT INTO users(supabase_uid,full_name,display_name,email,role,institution_id)
 SELECT gen_random_uuid(),'Staff '||n,'Staff '||lpad(n::text,3,'0'),gen_random_uuid()||'@staff.invalid','teacher',$1 FROM generate_series(1,121) n`, s.InstB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE institution_id=$1 AND role='teacher'`, s.InstB) })
	w := httptest.NewRecorder()
	h.ListTeachers(w, withAuth(httptest.NewRequest("GET", "/?page=6&limit=20&status=active", nil), s.AdminB, "institution_admin", s.InstB))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Staff 101") || !strings.Contains(w.Body.String(), `"total":121`) {
		t.Fatalf("later staff page: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	h.ListStaff(w, withAuth(httptest.NewRequest("GET", "/?search=adminb&limit=20", nil), s.AdminB, "institution_admin", s.InstB))
	if w.Code != 200 || !strings.Contains(w.Body.String(), s.AdminB) {
		t.Fatalf("leadership admin search: %d %s", w.Code, w.Body)
	}
}

func TestAcademicFiveCurrentCurricula(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	ctx := t.Context()
	var year string
	if err := pool.QueryRow(ctx, `INSERT INTO academic_years(institution_id,name,starts_on,ends_on) VALUES($1,'Current',(now() AT TIME ZONE 'Asia/Kolkata')::date,(now() AT TIME ZONE 'Asia/Kolkata')::date) RETURNING id`, s.InstB).Scan(&year); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM class_curricula WHERE institution_id=$1`, s.InstB)
		pool.Exec(ctx, `DELETE FROM curriculum_versions WHERE institution_id=$1`, s.InstB)
		pool.Exec(ctx, `DELETE FROM curricula WHERE institution_id=$1`, s.InstB)
		pool.Exec(ctx, `DELETE FROM academic_years WHERE institution_id=$1`, s.InstB)
	})
	for i := 0; i < 5; i++ {
		var curriculum, version string
		if err := pool.QueryRow(ctx, `INSERT INTO curricula(institution_id,name) VALUES($1,$2) RETURNING id`, s.InstB, fmt.Sprintf("Subject %d", i)).Scan(&curriculum); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO curriculum_versions(institution_id,curriculum_id,label,subject,grade,status,published_at) VALUES($1,$2,'Edition A',$3,'10','published',now()) RETURNING id`, s.InstB, curriculum, fmt.Sprintf("Subject %d", i)).Scan(&version); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO class_curricula(institution_id,group_id,academic_year_id,curriculum_id,version_id) VALUES($1,$2,$3,$4,$5)`, s.InstB, s.ClassB, year, curriculum, version); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	NewHandler(pool, nil, nil, "", "").ListGroups(w, withAuth(httptest.NewRequest("GET", "/", nil), s.AdminB, "institution_admin", s.InstB))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"current_curriculum_count":5`) || strings.Count(w.Body.String(), `"assignment_id"`) != 5 {
		t.Fatalf("five curricula: %d %s", w.Code, w.Body)
	}
	// Inclusive midnight: UTC evening is already the next institute date.
	var day string
	if err := pool.QueryRow(ctx, `SELECT ('2026-10-06T18:30:00Z'::timestamptz AT TIME ZONE timezone)::date::text FROM institutions WHERE id=$1`, s.InstB).Scan(&day); err != nil || day != "2026-10-07" {
		t.Fatalf("midnight: %s %v", day, err)
	}
}

func TestAcademicLabelUpdatesDoNotLockAccount(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, s.Student); err != nil {
		t.Fatal(err)
	}
	updateCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err = pool.Exec(updateCtx, `UPDATE groups SET grade='12' WHERE id=$1`, s.ClassB); err != nil {
		t.Fatalf("label-only update waited on unrelated account lock: %v", err)
	}
}
