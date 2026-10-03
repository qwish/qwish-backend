package notice

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// openTestDB connects to TEST_DATABASE_URL, or skips the test.
func openTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping database integration test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping TEST_DATABASE_URL: %v", err)
	}
	return pool
}

// school is one institute with two departments:
//
//	Science: SciA (TeachSciA teaches it; S1, S3 members), SciB (S1 member)
//	Commerce: ComA (S2 member)
//	HodSci leads Science; Admin is the institution admin; S3 is suspended.
type school struct {
	Inst, Science, Commerce  string
	SciA, SciB, ComA         string
	TeachSciA, HodSci, Admin string
	S1, S2, S3               string
}

func seedSchool(t *testing.T, pool *pgxpool.Pool) school {
	t.Helper()
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var s school
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	must("institute", pool.QueryRow(ctx, `
		INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
		VALUES ('notice '||$1, 'school', 'notice-'||$1||'@example.test', 'SN'||$1, 'TN'||$1, 'verified')
		RETURNING id`, tag).Scan(&s.Inst))
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM notices WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM group_students WHERE group_id IN (SELECT id FROM groups WHERE institution_id=$1)`, s.Inst)
		pool.Exec(ctx, `DELETE FROM group_teachers WHERE group_id IN (SELECT id FROM groups WHERE institution_id=$1)`, s.Inst)
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM staff_role_assignments WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM groups WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM departments WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []string{s.TeachSciA, s.HodSci, s.Admin, s.S1, s.S2, s.S3})
		pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, s.Inst)
	})
	dept := func(name string, dest *string) {
		must(name, pool.QueryRow(ctx, `INSERT INTO departments (institution_id, name) VALUES ($1,$2) RETURNING id`, s.Inst, name).Scan(dest))
	}
	dept("Science", &s.Science)
	dept("Commerce", &s.Commerce)
	class := func(name, deptID string, dest *string) {
		must(name, pool.QueryRow(ctx, `INSERT INTO groups (institution_id, name, invite_code, department_id)
			VALUES ($1,$2,$2||$3,$4) RETURNING id`, s.Inst, name, tag, deptID).Scan(dest))
	}
	class("SciA", s.Science, &s.SciA)
	class("SciB", s.Science, &s.SciB)
	class("ComA", s.Commerce, &s.ComA)
	user := func(role, label string, dest *string) {
		must(label, pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
			VALUES (gen_random_uuid(), $1, $1, $1||'-'||$2||'@example.test', $3, $4) RETURNING id`,
			label, tag, role, s.Inst).Scan(dest))
	}
	user("teacher", "teach-sci-a", &s.TeachSciA)
	user("teacher", "hod-sci", &s.HodSci)
	user("institution_admin", "admin", &s.Admin)
	user("student", "s1", &s.S1)
	user("student", "s2", &s.S2)
	user("student", "s3", &s.S3)
	_, err := pool.Exec(ctx, `INSERT INTO group_teachers (group_id, user_id) VALUES ($1,$2)`, s.SciA, s.TeachSciA)
	must("group_teachers", err)
	_, err = pool.Exec(ctx, `INSERT INTO staff_role_assignments (institution_id, user_id, role, department_id) VALUES ($1,$2,'hod',$3)`,
		s.Inst, s.HodSci, s.Science)
	must("hod", err)
	for _, u := range []string{s.S1, s.S2, s.S3} {
		_, err = pool.Exec(ctx, `INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at) VALUES ($1,$2,'student','active',now())`, s.Inst, u)
		must("enrollment", err)
	}
	for _, m := range [][2]string{{s.SciA, s.S1}, {s.SciB, s.S1}, {s.ComA, s.S2}, {s.SciA, s.S3}} {
		_, err = pool.Exec(ctx, `INSERT INTO group_students (group_id, user_id) VALUES ($1,$2)`, m[0], m[1])
		must("group_students", err)
	}
	// Suspended after joining, the way it happens in practice.
	_, err = pool.Exec(ctx, `UPDATE enrollments SET status='suspended' WHERE institution_id=$1 AND user_id=$2`, s.Inst, s.S3)
	must("suspend", err)
	return s
}
