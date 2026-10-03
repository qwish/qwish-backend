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
