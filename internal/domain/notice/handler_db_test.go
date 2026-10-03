package notice

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qwish/backend/internal/middleware"
)

func asUser(r *http.Request, userID, role, instID string) *http.Request {
	ctx := context.WithValue(r.Context(), middleware.ContextKeyUserID, userID)
	ctx = context.WithValue(ctx, middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyInstID, instID)
	return r.WithContext(ctx)
}

func TestSendHandlerStatusCodes(t *testing.T) {
	pool := openTestDB(t)
	sc := seedSchool(t, pool)
	h := NewHandler(NewService(pool, (&sink{sent: map[string]int{}}).emit))
	send := func(userID, role, body string) int {
		w := httptest.NewRecorder()
		h.Send(w, asUser(httptest.NewRequest("POST", "/", strings.NewReader(body)), userID, role, sc.Inst))
		return w.Code
	}
	ok := `{"title":"Quiz","body":"Friday","category":"test","group_ids":["` + sc.SciA + `"]}`
	if c := send(sc.TeachSciA, "teacher", ok); c != 201 {
		t.Fatalf("own class: want 201, got %d", c)
	}
	if c := send(sc.TeachSciA, "teacher", `{"title":"Quiz","body":"Friday","category":"test","group_ids":["`+sc.SciB+`"]}`); c != 403 {
		t.Fatalf("other class: want 403, got %d", c)
	}
	if c := send(sc.TeachSciA, "teacher", `{"title":"","body":"x","category":"test","group_ids":["`+sc.SciA+`"]}`); c != 400 {
		t.Fatalf("empty title: want 400, got %d", c)
	}
	// The institution route marks institution_admin as Admin: whole institute allowed.
	if c := send(sc.Admin, "institution_admin", `{"title":"Holiday","body":"Monday","category":"general","institution_wide":true}`); c != 201 {
		t.Fatalf("admin institution-wide: want 201, got %d", c)
	}
}
