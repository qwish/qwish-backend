package user

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

// Point TEST_DATABASE_URL at a migrated scratch database — this test writes rows.
func TestPortfolioWorkflowDB(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping database integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	tag := fmt.Sprint(time.Now().UnixNano())
	var instID, me, other string
	if err := pool.QueryRow(ctx, `
		INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
		VALUES ('p'||$1, 'college', 'p'||$1||'@example.test', 'S'||$1, 'T'||$1, 'verified') RETURNING id`, tag).Scan(&instID); err != nil {
		t.Fatal(err)
	}
	for i, dest := range []*string{&me, &other} {
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
			VALUES (gen_random_uuid(), 'u', 'u', $1, 'student', $2) RETURNING id`,
			fmt.Sprintf("u%d-%s@example.test", i, tag), instID).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}

	h := NewProfileEntryHandler(pool)
	r := chi.NewRouter()
	r.Get("/e", h.List)
	r.Post("/e", h.Create)
	r.Patch("/e/{entryId}", h.Update)
	r.Post("/e/{entryId}/submit", h.Submit)
	r.Get("/e/{entryId}/reviews", h.Reviews)
	r.Put("/e/{entryId}/pin", h.Pin)
	call := func(as, method, path, body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUserID, as))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	list := func() []any {
		t.Helper()
		_, out := call(me, "GET", "/e", "")
		return out["data"].([]any)
	}
	entry := func(id string) map[string]any {
		for _, e := range list() {
			if m := e.(map[string]any); m["id"] == id {
				return m
			}
		}
		t.Fatalf("entry %s not listed", id)
		return nil
	}

	// A legacy-shaped create still works and lands as a plain draft.
	if code, _ := call(me, "POST", "/e", `{"kind":"course","title":"Algebra"}`); code != 201 {
		t.Fatalf("legacy create = %d", code)
	}

	code, out := call(me, "POST", "/e", `{"subtype":"project","title":"Line follower","skills":["C"]}`)
	if code != 201 {
		t.Fatalf("create = %d %v", code, out)
	}
	id := out["data"].(map[string]any)["id"].(string)
	if e := entry(id); e["kind"] != "experience" || e["status"] != "draft" {
		t.Fatalf("new project = %v", e)
	}

	if code, out := call(me, "POST", "/e/"+id+"/submit", ""); code != 400 || !strings.Contains(fmt.Sprint(out), "details.contribution") {
		t.Fatalf("incomplete submit = %d %v", code, out)
	}
	if code, out := call(me, "PATCH", "/e/"+id, `{"title":"Line follower","description":"Robot","details":{"contribution":"PID tuning","team_type":"team"}}`); code != 200 {
		t.Fatalf("patch = %d %v", code, out)
	}
	if code, out := call(me, "POST", "/e/"+id+"/submit", ""); code != 200 || out["data"].(map[string]any)["revision"] != 1.0 {
		t.Fatalf("submit = %d %v", code, out)
	}
	// Retry-safe: a second submit makes no new revision.
	if _, out := call(me, "POST", "/e/"+id+"/submit", ""); out["data"].(map[string]any)["revision"] != 1.0 {
		t.Fatalf("resubmit without edit = %v", out)
	}

	// Another student can't see or act on it.
	for _, c := range [][2]string{{"PATCH", "/e/" + id}, {"POST", "/e/" + id + "/submit"}, {"PUT", "/e/" + id + "/pin"}} {
		if code, _ := call(other, c[0], c[1], `{"title":"x","pinned":true}`); code != 404 {
			t.Fatalf("%s as other user = %d", c, code)
		}
	}
	if _, out := call(other, "GET", "/e/"+id+"/reviews", ""); len(out["data"].([]any)) != 0 {
		t.Fatal("other user read reviews")
	}

	// A teacher requests changes on revision 1 (teacher endpoints come later).
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_entry_reviews (revision_id, decision, comment)
		SELECT id, 'changes_requested', 'Add a demo link' FROM user_profile_entry_revisions WHERE entry_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, `UPDATE user_profile_entries SET status='changes_requested' WHERE id=$1`, id)
	if e := entry(id); e["review"].(map[string]any)["comment"] != "Add a demo link" {
		t.Fatalf("review not surfaced: %v", e)
	}
	if code, _ := call(me, "POST", "/e/"+id+"/submit", ""); code != 409 {
		t.Fatalf("resubmit without edit after changes_requested = %d", code)
	}
	// A legacy-shaped PATCH keeps portfolio fields and drops back to draft.
	call(me, "PATCH", "/e/"+id, `{"kind":"experience","title":"Line follower","description":"Robot","org":null}`)
	if e := entry(id); e["status"] != "draft" || e["details"].(map[string]any)["contribution"] != "PID tuning" {
		t.Fatalf("after legacy patch = %v", e)
	}
	call(me, "PATCH", "/e/"+id, `{"title":"Line follower","description":"Robot","links":["https://example.com/demo"]}`)
	if _, out := call(me, "POST", "/e/"+id+"/submit", ""); out["data"].(map[string]any)["revision"] != 2.0 {
		t.Fatalf("resubmit = %v", out)
	}
	if e := entry(id); e["review"] != nil {
		t.Fatalf("revision 1's review leaked onto revision 2: %v", e)
	}
	if _, out := call(me, "GET", "/e/"+id+"/reviews", ""); len(out["data"].([]any)) != 1 {
		t.Fatalf("history = %v", out)
	}
	var instAtSubmit string
	pool.QueryRow(ctx, `SELECT institution_id FROM user_profile_entry_revisions WHERE entry_id=$1 AND revision=2`, id).Scan(&instAtSubmit)
	if instAtSubmit != instID {
		t.Fatalf("institution context = %q", instAtSubmit)
	}

	// Highlights cap at three.
	for i := 0; i < 4; i++ {
		_, out := call(me, "POST", "/e", fmt.Sprintf(`{"kind":"achievement","title":"a%d"}`, i))
		code, _ := call(me, "PUT", "/e/"+out["data"].(map[string]any)["id"].(string)+"/pin", `{"pinned":true}`)
		if want := map[bool]int{true: 200, false: 409}[i < 3]; code != want {
			t.Fatalf("pin %d = %d, want %d", i, code, want)
		}
	}
}
