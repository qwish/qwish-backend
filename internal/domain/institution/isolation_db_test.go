package institution

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

// An institute sees only attempts on its own quizzes, even for a student who
// also belongs to another institute (spec D15).
func TestStudentDetailIsInstituteIsolated(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	ctx := context.Background()
	quiz := func(inst, author, title string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO quizzes (institution_id, created_by, title, type, status)
			VALUES ($1,$2,$3,'knowledge_check','published') RETURNING id`, inst, author, title).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO quiz_attempts (quiz_id, user_id, status, completed_at, score_pct) VALUES ($1,$2,'completed',now(),80)`, id, s.Student); err != nil {
			t.Fatal(err)
		}
		return id
	}
	qa := quiz(s.InstA, s.AdminA, "Quiz only A sees")
	qb := quiz(s.InstB, s.AdminB, "Quiz only B sees")
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id IN ($1,$2)`, qa, qb)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id IN ($1,$2)`, qa, qb)
	})

	h := NewHandler(pool, nil, nil, "", "")
	r := withURLParam(withAuth(httptest.NewRequest("GET", "/", nil), s.AdminA, "institution_admin", s.InstA), "userId", s.Student)
	w := httptest.NewRecorder()
	h.GetStudent(w, r)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "Quiz only A sees") || strings.Contains(body, "Quiz only B sees") {
		t.Fatalf("institute A must see only its own attempts: %d %s", w.Code, body)
	}
}

func TestGetGroupReportsJoiningSwitch(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	pool.Exec(context.Background(), `UPDATE groups SET joining_enabled=false WHERE id=$1`, s.ClassB)
	r := withURLParam(withAuth(httptest.NewRequest("GET", "/", nil), s.AdminB, "institution_admin", s.InstB), "groupId", s.ClassB)
	w := httptest.NewRecorder()
	NewHandler(pool, nil, nil, "", "").GetGroup(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"joining_enabled":false`) {
		t.Fatalf("group detail must report the joining switch: %d %s", w.Code, w.Body)
	}
}

func TestGroupRosterAverageIsInstituteIsolated(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	ctx := context.Background()
	quiz := func(inst, author string, score float64) string {
		var id string
		pool.QueryRow(ctx, `INSERT INTO quizzes (institution_id, created_by, title, type, status) VALUES ($1,$2,'q','knowledge_check','published') RETURNING id`, inst, author).Scan(&id)
		pool.Exec(ctx, `INSERT INTO quiz_attempts (quiz_id,user_id,status,completed_at,score_pct) VALUES ($1,$2,'completed',now(),$3)`, id, s.Student, score)
		return id
	}
	qa, qb := quiz(s.InstA, s.AdminA, 10), quiz(s.InstB, s.AdminB, 90)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id IN ($1,$2)`, qa, qb)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id IN ($1,$2)`, qa, qb)
	})
	r := withURLParam(withAuth(httptest.NewRequest("GET", "/", nil), s.AdminB, "institution_admin", s.InstB), "groupId", s.ClassB)
	w := httptest.NewRecorder()
	NewHandler(pool, nil, nil, "", "").GetGroup(w, r)
	if !strings.Contains(w.Body.String(), `"average_score":90`) {
		t.Fatalf("B's class roster average must exclude A's quizzes: %s", w.Body)
	}
}
