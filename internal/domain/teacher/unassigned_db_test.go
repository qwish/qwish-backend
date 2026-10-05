package teacher

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// A teacher with no class sees no students — not the whole institution's
// roster. The list is empty and a student's profile is not found.
func TestUnassignedTeacherSeesNoStudents(t *testing.T) {
	pool := openTestDB(t)
	f := seedTeacherFixture(t, pool)
	h := NewHandler(pool)

	w := httptest.NewRecorder()
	h.ListStudents(w, teacherRequest(httptest.NewRequest("GET", "/teacher/students", nil), f.LonerTeacherID, f.InstitutionID))
	var body struct {
		Data []json.RawMessage   `json:"data"`
		Meta struct{ Total int } `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("%v: %s", err, w.Body)
	}
	if len(body.Data) != 0 || body.Meta.Total != 0 {
		t.Errorf("unassigned teacher lists %d students (total %d), want none", len(body.Data), body.Meta.Total)
	}

	req := teacherRequest(httptest.NewRequest("GET", "/", nil), f.LonerTeacherID, f.InstitutionID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("userId", f.StudentID)
	w = httptest.NewRecorder()
	h.GetStudent(w, req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	if w.Code != 404 {
		t.Errorf("unassigned teacher opens a student profile: %d, want 404", w.Code)
	}
	if h.canSeeStudent(req, f.LonerTeacherID, f.InstitutionID, f.StudentID) {
		t.Error("canSeeStudent lets an unassigned teacher through")
	}

	// The assigned teacher still sees their own student.
	w = httptest.NewRecorder()
	h.ListStudents(w, teacherRequest(httptest.NewRequest("GET", "/teacher/students", nil), f.TeacherID, f.InstitutionID))
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.Meta.Total != 1 {
		t.Errorf("assigned teacher total = %d, want 1", body.Meta.Total)
	}
}
