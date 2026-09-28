package topicrequest

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/notification"
	"github.com/qwish/backend/internal/middleware"
)

func TestTeacherUpdateDoneLinksQuizAndNotifiesStudent(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	id := func(sql string, args ...any) string {
		t.Helper()
		var v string
		if err := db.QueryRow(ctx, sql, args...).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	tag := uuid.NewString()
	inst := id(`INSERT INTO institutions(name,type,contact_email,status,student_referral_code,teacher_referral_code) VALUES('S','school',$1,'verified',$2,$3) RETURNING id`, tag+"@s.test", tag+"s", tag+"t")
	teacher := id(`INSERT INTO users(supabase_uid,full_name,display_name,email,role,institution_id) VALUES(gen_random_uuid(),'T','T',$1,'teacher',$2) RETURNING id`, tag+"t@s.test", inst)
	student := id(`INSERT INTO users(supabase_uid,full_name,display_name,email,role,institution_id) VALUES(gen_random_uuid(),'S','S',$1,'student',$2) RETURNING id`, tag+"s@s.test", inst)
	quiz := id(`INSERT INTO quizzes(institution_id,created_by,title,type,status) VALUES($1,$2,'Isotopes','knowledge_check','published') RETURNING id`, inst, teacher)
	req := id(`INSERT INTO topic_requests(student_id,institution_id,topic) VALUES($1,$2,'Isotopes') RETURNING id`, student, inst)
	t.Cleanup(func() {
		db.Exec(ctx, `DELETE FROM user_notifications WHERE user_id=$1`, student)
		db.Exec(ctx, `DELETE FROM topic_requests WHERE id=$1`, req)
		db.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, quiz)
		db.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, teacher, student)
		db.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, inst)
	})

	h := NewHandler(db)
	h.SetNotifier(notification.NewService(db, "", "", ""), "https://teacher.test")
	call := func(body string) int {
		r := chi.NewRouter()
		r.Patch("/{requestId}", h.TeacherUpdate)
		rq := httptest.NewRequest("PATCH", "/"+req, strings.NewReader(body))
		c := context.WithValue(rq.Context(), middleware.ContextKeyUserID, teacher)
		c = context.WithValue(c, middleware.ContextKeyInstID, inst)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, rq.WithContext(c))
		return w.Code
	}
	if code := call(`{"status":"done","quiz_id":"` + quiz + `"}`); code != 200 {
		t.Fatalf("update: %d", code)
	}
	if code := call(`{"status":"done"}`); code != 200 { // repeat: no second notification
		t.Fatalf("repeat update: %d", code)
	}
	var linked *string
	var owner string
	_ = db.QueryRow(ctx, `SELECT resolved_quiz_id::text, assigned_to::text FROM topic_requests WHERE id=$1`, req).Scan(&linked, &owner)
	if linked == nil || *linked != quiz || owner != teacher {
		t.Fatalf("quiz link %v owner %s", linked, owner)
	}
	var n int
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM user_notifications WHERE user_id=$1 AND kind='topic_request'`, student).Scan(&n)
	if n != 1 {
		t.Fatalf("student notifications = %d", n)
	}
}
