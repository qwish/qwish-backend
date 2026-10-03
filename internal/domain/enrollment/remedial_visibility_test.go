package enrollment

import (
	"context"
	"encoding/json"
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
