package teacher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func classRequest(teacherID, instID, classID string) *http.Request {
	req := teacherRequest(httptest.NewRequest("POST", "/", nil), teacherID, instID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("classId", classID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestTeacherEndsAndReopensOwnClass(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	h := NewHandler(pool)
	ctx := context.Background()

	w := httptest.NewRecorder()
	h.EndClass(w, classRequest(s.Student, s.InstB, s.ClassB)) // not a teacher of it
	if w.Code != 404 {
		t.Fatalf("non-teacher end: want 404, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.EndClass(w, classRequest(s.TeacherB, s.InstB, s.ClassB))
	var archived *time.Time
	pool.QueryRow(ctx, `SELECT archived_at FROM groups WHERE id=$1`, s.ClassB).Scan(&archived)
	if w.Code != 200 || archived == nil {
		t.Fatalf("end: %d archived=%v", w.Code, archived)
	}
	w = httptest.NewRecorder()
	h.ReopenClass(w, classRequest(s.TeacherB, s.InstB, s.ClassB))
	pool.QueryRow(ctx, `SELECT archived_at FROM groups WHERE id=$1`, s.ClassB).Scan(&archived)
	if w.Code != 200 || archived != nil {
		t.Fatalf("reopen: %d archived=%v", w.Code, archived)
	}

	req := teacherRequest(httptest.NewRequest("GET", "/", nil), s.TeacherB, s.InstB)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("classId", s.ClassB)
	w = httptest.NewRecorder()
	h.GetClass(w, req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	if !strings.Contains(w.Body.String(), `"kind":"class"`) || !strings.Contains(w.Body.String(), `"archived_at":null`) {
		t.Fatalf("class detail must carry kind and archived_at: %s", w.Body)
	}
}

func TestListClassesCanIncludeEnded(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	pool.Exec(context.Background(), `UPDATE groups SET archived_at=now(), grade='9' WHERE id=$1`, s.ClassB)
	h := NewHandler(pool)
	w := httptest.NewRecorder()
	h.ListClasses(w, teacherRequest(httptest.NewRequest("GET", "/", nil), s.TeacherB, s.InstB))
	if strings.Contains(w.Body.String(), s.ClassB) {
		t.Fatalf("default list must hide ended classes: %s", w.Body)
	}
	w = httptest.NewRecorder()
	h.ListClasses(w, teacherRequest(httptest.NewRequest("GET", "/?include_ended=1", nil), s.TeacherB, s.InstB))
	if b := w.Body.String(); !strings.Contains(b, s.ClassB) || !strings.Contains(b, `"kind":"class"`) || !strings.Contains(b, `"grade":"9"`) {
		t.Fatalf("include_ended must return the ended class with its fields: %s", b)
	}
}
