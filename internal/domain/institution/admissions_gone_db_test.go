package institution

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// Admissions (084), roster rows and edit requests (085) are gone; institute
// endpoints that used to read them must keep working.
func TestInstituteEndpointsWorkWithoutAdmissions(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	h := NewHandler(pool, nil, nil, "", "")

	w := httptest.NewRecorder()
	h.Overview(w, withAuth(httptest.NewRequest("GET", "/", nil), s.AdminB, "institution_admin", s.InstB))
	var ov struct {
		Data map[string]any `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &ov)
	if _, stale := ov.Data["pending"]; w.Code != 200 || stale {
		t.Fatalf("overview must work with no ERP queues (roster, edit requests): %d %s", w.Code, w.Body)
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
