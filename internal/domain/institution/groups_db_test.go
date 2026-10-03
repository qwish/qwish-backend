package institution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGroupWritesAreInstituteScoped(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	h := NewHandler(pool, nil, nil, "", "")
	ctx := context.Background()

	w := httptest.NewRecorder()
	h.ArchiveGroup(w, withURLParam(withAuth(httptest.NewRequest("DELETE", "/", nil), s.AdminA, "institution_admin", s.InstA), "groupId", s.ClassB))
	var archived *time.Time
	pool.QueryRow(ctx, `SELECT archived_at FROM groups WHERE id=$1`, s.ClassB).Scan(&archived)
	if w.Code != 404 || archived != nil {
		t.Fatalf("cross-institute archive: code=%d archived=%v", w.Code, archived)
	}

	w = httptest.NewRecorder()
	h.UpdateGroup(w, withURLParam(withAuth(httptest.NewRequest("PATCH", "/", strings.NewReader(`{"name":"hijacked"}`)), s.AdminA, "institution_admin", s.InstA), "groupId", s.ClassB))
	var name string
	pool.QueryRow(ctx, `SELECT name FROM groups WHERE id=$1`, s.ClassB).Scan(&name)
	if w.Code != 404 || name == "hijacked" {
		t.Fatalf("cross-institute update: code=%d name=%q", w.Code, name)
	}
}

func TestEndAndReopenKeepsMembers(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	h := NewHandler(pool, nil, nil, "", "")
	ctx := context.Background()
	call := func(fn http.HandlerFunc) int {
		w := httptest.NewRecorder()
		fn(w, withURLParam(withAuth(httptest.NewRequest("POST", "/", nil), s.AdminB, "institution_admin", s.InstB), "groupId", s.ClassB))
		return w.Code
	}
	if c := call(h.ArchiveGroup); c != 200 {
		t.Fatalf("end: %d", c)
	}
	var members int
	pool.QueryRow(ctx, `SELECT count(*) FROM group_students WHERE group_id=$1`, s.ClassB).Scan(&members)
	if members != 1 {
		t.Fatalf("ending a class must keep members, got %d", members)
	}
	if c := call(h.ReopenGroup); c != 200 {
		t.Fatalf("reopen: %d", c)
	}
	var archived *time.Time
	pool.QueryRow(ctx, `SELECT archived_at FROM groups WHERE id=$1`, s.ClassB).Scan(&archived)
	if archived != nil {
		t.Fatal("reopen must clear archived_at")
	}
	// Classes ended over 90 days ago can't be reopened.
	pool.Exec(ctx, `UPDATE groups SET archived_at=now()-interval '91 days' WHERE id=$1`, s.ClassB)
	if c := call(h.ReopenGroup); c != 409 {
		t.Fatalf("reopen after 90 days: want 409, got %d", c)
	}
}

func TestCreateAndUpdateGroupGradeSection(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	h := NewHandler(pool, nil, nil, "", "")
	ctx := context.Background()
	w := httptest.NewRecorder()
	h.UpdateGroup(w, withURLParam(withAuth(httptest.NewRequest("PATCH", "/", strings.NewReader(`{"grade":"10","section":"B"}`)), s.AdminB, "institution_admin", s.InstB), "groupId", s.ClassB))
	var grade, section, name string
	pool.QueryRow(ctx, `SELECT COALESCE(grade,''), COALESCE(section,''), name FROM groups WHERE id=$1`, s.ClassB).Scan(&grade, &section, &name)
	if w.Code != 200 || grade != "10" || section != "B" || name != "B class" {
		t.Fatalf("partial update: code=%d grade=%q section=%q name=%q", w.Code, grade, section, name)
	}
}

func TestCreateGroupWithGradeAndDetailFields(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	h := NewHandler(pool, nil, nil, "", "")
	w := httptest.NewRecorder()
	h.CreateGroup(w, withAuth(httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"10-A","grade":"10","section":"A"}`)), s.AdminB, "institution_admin", s.InstB))
	body := w.Body.String()
	if w.Code != 201 || !strings.Contains(body, `"grade":"10"`) || !strings.Contains(body, `"kind":"class"`) {
		t.Fatalf("create: %d %s", w.Code, body)
	}
	w = httptest.NewRecorder()
	h.GetGroup(w, withURLParam(withAuth(httptest.NewRequest("GET", "/", nil), s.AdminB, "institution_admin", s.InstB), "groupId", s.ClassB))
	if !strings.Contains(w.Body.String(), `"kind":"class"`) || !strings.Contains(w.Body.String(), `"archived_at":null`) {
		t.Fatalf("group detail must carry kind and archived_at: %s", w.Body)
	}
}

func TestListGroupsCarriesClassFields(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	pool.Exec(context.Background(), `UPDATE groups SET grade='10', section='B' WHERE id=$1`, s.ClassB)
	w := httptest.NewRecorder()
	NewHandler(pool, nil, nil, "", "").ListGroups(w, withAuth(httptest.NewRequest("GET", "/", nil), s.AdminB, "institution_admin", s.InstB))
	b := w.Body.String()
	for _, want := range []string{`"grade":"10"`, `"section":"B"`, `"kind":"class"`, `"joining_enabled":true`} {
		if !strings.Contains(b, want) {
			t.Fatalf("list groups missing %s: %s", want, b)
		}
	}
}
