package scheduler

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/notification"
)

func TestTeacherNotificationsAreIdempotent(t *testing.T) {
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
	group := id(`INSERT INTO groups(institution_id,name,invite_code) VALUES($1,'9B',$2) RETURNING id`, inst, tag)
	db.Exec(ctx, `INSERT INTO group_teachers(group_id,user_id) VALUES($1,$2)`, group, teacher)
	db.Exec(ctx, `INSERT INTO group_students(group_id,user_id) VALUES($1,$2)`, group, student)
	quiz := id(`INSERT INTO quizzes(institution_id,created_by,title,type,status) VALUES($1,$2,'Forces','knowledge_check','published') RETURNING id`, inst, teacher)
	a := id(`INSERT INTO learning_assignments(institution_id,group_id,quiz_id,purpose,due_at,created_by) VALUES($1,$2,$3,'practice',now()-interval '2 hours',$4) RETURNING id`, inst, group, quiz, teacher)
	db.Exec(ctx, `INSERT INTO learning_assignment_recipients(assignment_id,student_id) VALUES($1,$2)`, a, student)
	t.Cleanup(func() {
		db.Exec(ctx, `DELETE FROM user_notifications WHERE user_id=$1`, teacher)
		db.Exec(ctx, `DELETE FROM learning_assignments WHERE id=$1`, a)
		db.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, quiz)
		db.Exec(ctx, `DELETE FROM groups WHERE id=$1`, group)
		db.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, teacher, student)
		db.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, inst)
	})

	s := New(db, nil, nil, notification.NewService(db, "", "", ""), nil)
	for i := 0; i < 2; i++ {
		if err := s.SendTeacherNotifications(ctx, "https://teacher.test"); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM user_notifications WHERE user_id=$1 AND kind='assignment_overdue'`, teacher).Scan(&n)
	if n != 1 {
		t.Fatalf("overdue alerts = %d, want exactly 1 across two runs", n)
	}

	// Turning in-app off for the topic stops further alerts.
	db.Exec(ctx, `INSERT INTO teacher_notification_preferences(user_id,prefs) VALUES($1,'{"overdue_work":{"in_app":false,"email":false}}')`, teacher)
	t.Cleanup(func() { db.Exec(ctx, `DELETE FROM teacher_notification_preferences WHERE user_id=$1`, teacher) })
	a2 := id(`INSERT INTO learning_assignments(institution_id,group_id,quiz_id,purpose,due_at,created_by) VALUES($1,$2,$3,'practice',now()-interval '1 hour',$4) RETURNING id`, inst, group, quiz, teacher)
	db.Exec(ctx, `INSERT INTO learning_assignment_recipients(assignment_id,student_id) VALUES($1,$2)`, a2, student)
	t.Cleanup(func() { db.Exec(ctx, `DELETE FROM learning_assignments WHERE id=$1`, a2) })
	_ = s.SendTeacherNotifications(ctx, "https://teacher.test")
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM user_notifications WHERE user_id=$1 AND kind='assignment_overdue'`, teacher).Scan(&n)
	if n != 1 {
		t.Fatalf("alert sent despite in_app=false: %d", n)
	}
}
