package leadership_test

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	qdb "github.com/qwish/backend/internal/db"
)

func TestDepartmentArchiveSerializesWithPlacementAndReopen(t *testing.T) {
	for _, operation := range []string{"place", "reopen", "role"} {
		t.Run(operation, func(t *testing.T) {
			w := build(t)
			ctx := t.Context()
			dept := id(t, w.call(t, 201, w.admin, "POST", "/i/departments", map[string]any{"name": "Concurrent"}))
			if operation == "reopen" {
				if _, err := w.pool.Exec(ctx, `UPDATE groups SET archived_at=now(),department_id=$2 WHERE id=$1`, w.bareClass, dept); err != nil {
					t.Fatal(err)
				}
			}
			// Hold the shared institution lock until both operations are ready.
			tx, err := w.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, `SELECT id FROM institutions WHERE id=$1 FOR UPDATE`, w.inst); err != nil {
				t.Fatal(err)
			}
			results := make(chan int, 2)
			ready := make(chan bool, 2)
			request := func(method, path, body string) {
				ready <- true
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				r.Header.Set("X-User", w.admin)
				out := httptest.NewRecorder()
				w.router.ServeHTTP(out, r)
				results <- out.Code
			}
			go request("DELETE", "/i/departments/"+dept, "")
			switch operation {
			case "place":
				go request("PUT", "/i/groups/"+w.bareClass+"/department", fmt.Sprintf(`{"department_id":%q}`, dept))
			case "role":
				go request("POST", "/i/staff-roles", fmt.Sprintf(`{"user_id":%q,"role":"hod","department_id":%q,"reason":"test"}`, w.t1, dept))
			case "reopen":
				go func() {
					ready <- true
					code, _ := qdb.ReopenClass(ctx, w.pool, w.bareClass, "institution_id=$2", w.inst)
					results <- code
				}()
			}
			<-ready
			<-ready
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				status := <-results
				if status >= 500 {
					t.Fatalf("concurrent operation: %d", status)
				}
			}
			var invalid bool
			err = w.pool.QueryRow(ctx, `SELECT d.archived_at IS NOT NULL AND (EXISTS(SELECT 1 FROM groups g WHERE g.department_id=d.id AND g.archived_at IS NULL) OR EXISTS(SELECT 1 FROM staff_role_assignments a WHERE a.department_id=d.id AND a.revoked_at IS NULL)) FROM departments d WHERE d.id=$1`, dept).Scan(&invalid)
			if err != nil {
				t.Fatal(err)
			}
			if invalid {
				t.Fatal("archived department acquired active relationship")
			}
		})
	}
}
