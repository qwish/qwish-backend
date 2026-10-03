package institution

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// Admissions tables are gone (migration 084); institute endpoints that used to
// read them must keep working.
func TestInstituteEndpointsWorkWithoutAdmissions(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	h := NewHandler(pool, nil, nil, "", "")
	ctx := context.Background()
	pool.Exec(ctx, `INSERT INTO enrollments (institution_id, full_name, email, status) VALUES ($1,'roster','roster@example.test','pending_claim')`, s.InstB)

	w := httptest.NewRecorder()
	h.Overview(w, withAuth(httptest.NewRequest("GET", "/", nil), s.AdminB, "institution_admin", s.InstB))
	var ov struct {
		Data struct {
			Pending map[string]any `json:"pending"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &ov)
	if w.Code != 200 || ov.Data.Pending["unclaimed_enrollments"] != float64(1) {
		t.Fatalf("overview pending counts broken: %d %s", w.Code, w.Body)
	}

	w = httptest.NewRecorder()
	h.ActionCentre(w, withAuth(httptest.NewRequest("GET", "/", nil), s.AdminB, "institution_admin", s.InstB))
	if w.Code != 200 {
		t.Fatalf("action centre: %d %s", w.Code, w.Body)
	}

	w = httptest.NewRecorder()
	h.FindStudents(w, withAuth(httptest.NewRequest("GET", "/?q=student", nil), s.AdminB, "institution_admin", s.InstB))
	if w.Code != 200 {
		t.Fatalf("find students: %d %s", w.Code, w.Body)
	}

	w = httptest.NewRecorder()
	h.ExplainStudent(w, withAuth(httptest.NewRequest("GET", "/?user_id="+s.Student, nil), s.AdminB, "institution_admin", s.InstB))
	if w.Code != 200 {
		t.Fatalf("explain student: %d %s", w.Code, w.Body)
	}

	w = httptest.NewRecorder()
	h.AuditLog(w, withAuth(httptest.NewRequest("GET", "/", nil), s.AdminB, "institution_admin", s.InstB))
	if w.Code != 200 {
		t.Fatalf("audit log: %d %s", w.Code, w.Body)
	}
}
