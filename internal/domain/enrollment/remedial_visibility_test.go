package enrollment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStudentNeverSeesRemedialMarkers(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	rg, _ := newClass(t, pool, f.InstitutionID, false)
	pool.Exec(ctx, `UPDATE groups SET kind='remedial', name='Fractions practice' WHERE id=$1`, rg)
	pool.Exec(ctx, `INSERT INTO group_students (group_id, user_id) VALUES ($1,$2)`, rg, f.StudentID)

	list, err := svc.ListMine(ctx, f.StudentID)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(list)
	if strings.Contains(string(b), "remedial") {
		t.Fatalf("student payload leaks remedial marker: %s", b)
	}
	if list[0].ClassName != nil && *list[0].ClassName == "Fractions practice" {
		t.Fatal("profile class must be the main class, not the remedial group")
	}
}

// A practice group only ever takes students from its source class, after
// creation as well as at it.
func TestRemedialAddStudentMustComeFromSourceClass(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	rg, _ := newClass(t, pool, f.InstitutionID, false)
	pool.Exec(ctx, `UPDATE groups SET kind='remedial', source_group_id=$2 WHERE id=$1`, rg, f.GroupID)
	pool.Exec(ctx, `INSERT INTO group_teachers (group_id, user_id) VALUES ($1,$2)`, rg, f.TeacherID)
	var outsider string
	if err := pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		VALUES (gen_random_uuid(), 'out', 'out', 'out-'||gen_random_uuid()||'@example.test', 'student') RETURNING id`).Scan(&outsider); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM group_teachers WHERE group_id=$1`, rg)
		pool.Exec(ctx, `DELETE FROM group_students WHERE user_id=$1`, outsider)
		pool.Exec(ctx, `DELETE FROM enrollments WHERE user_id=$1`, outsider)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, outsider)
	})
	pool.Exec(ctx, `INSERT INTO enrollments (institution_id, user_id, full_name, status) VALUES ($1,$2,'out','active')`, f.InstitutionID, outsider)

	if err := svc.AddStudentToClass(ctx, f.TeacherID, rg, outsider); !errors.Is(err, ErrNotInSourceClass) {
		t.Fatalf("student outside the source class: want ErrNotInSourceClass, got %v", err)
	}
	if err := svc.AddStudentToClass(ctx, f.TeacherID, rg, f.StudentID); err != nil {
		t.Fatalf("student from the source class: %v", err)
	}
}
