package admin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

// consoleFixture seeds one of everything the console endpoints read.
type consoleFixture struct {
	admin, moderator       string
	inst, dupInst          string
	student, teacher       string
	quiz, question, report string
	contact                string
}

func seedConsole(t *testing.T, pool *pgxpool.Pool) consoleFixture {
	t.Helper()
	ctx := context.Background()
	tag := uuid.NewString()[:8]
	var f consoleFixture
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	must("admin", pool.QueryRow(ctx, `INSERT INTO admin_accounts (supabase_uid,name,email,role)
		VALUES (gen_random_uuid(),'Console Admin','ca-'||$1||'@qwish.test','super_admin') RETURNING id`, tag).Scan(&f.admin))
	must("moderator", pool.QueryRow(ctx, `INSERT INTO admin_accounts (supabase_uid,name,email,role)
		VALUES (gen_random_uuid(),'Console Mod','cm-'||$1||'@qwish.test','moderator') RETURNING id`, tag).Scan(&f.moderator))
	must("institution", pool.QueryRow(ctx, `INSERT INTO institutions
		(name,type,contact_email,student_referral_code,teacher_referral_code,status,
		 onboarding_admin_name,onboarding_phone,onboarding_website,onboarding_city)
		VALUES ('Console College '||$1,'college','principal@console-'||$1||'.edu','SC'||$1,'TC'||$1,'verified',
		        'Dr. Test','+91 20 0000','https://www.console-'||$1||'.edu/about','Pune') RETURNING id`, tag).Scan(&f.inst))
	must("duplicate", pool.QueryRow(ctx, `INSERT INTO institutions
		(name,type,contact_email,student_referral_code,teacher_referral_code,status)
		VALUES ('Another Name '||$1,'college','office@console-'||$1||'.edu','SD'||$1,'TD'||$1,'pending') RETURNING id`, tag).Scan(&f.dupInst))
	must("student", pool.QueryRow(ctx, `INSERT INTO users (supabase_uid,full_name,display_name,email,role,institution_id,current_streak,total_points)
		VALUES (gen_random_uuid(),'Stu','Stu','stu-'||$1||'@qwish.test','student',$2,5,300) RETURNING id`, tag, f.inst).Scan(&f.student))
	must("teacher", pool.QueryRow(ctx, `INSERT INTO users (supabase_uid,full_name,display_name,email,role,institution_id)
		VALUES (gen_random_uuid(),'Tea','Tea','tea-'||$1||'@qwish.test','teacher',$2) RETURNING id`, tag, f.inst).Scan(&f.teacher))
	must("quiz", pool.QueryRow(ctx, `INSERT INTO quizzes (created_by,title,type,status,institution_id,question_count)
		VALUES ($1,'Console Quiz','knowledge_check','published',$2,1) RETURNING id`, f.teacher, f.inst).Scan(&f.quiz))
	must("question", pool.QueryRow(ctx, `INSERT INTO questions (quiz_id,position,type,prompt,options,correct_answer)
		VALUES ($1,1,'multiple_choice','Atomic number of helium?','["1","2","4"]','"4"') RETURNING id`, f.quiz).Scan(&f.question))
	var attempt string
	must("attempt", pool.QueryRow(ctx, `INSERT INTO quiz_attempts (quiz_id,user_id,status,score_pct,total_correct,total_questions,started_at,completed_at)
		VALUES ($1,$2,'completed',100,1,1,now()-interval '5 seconds',now()) RETURNING id`, f.quiz, f.student).Scan(&attempt))
	_, err := pool.Exec(ctx, `INSERT INTO question_responses (attempt_id,question_id,answer,is_correct,time_taken_ms,points_earned)
		VALUES ($1,$2,'"4"',true,3000,10)`, attempt, f.question)
	must("response", err)
	must("report", pool.QueryRow(ctx, `INSERT INTO reports (reporter_id,quiz_id,question_id,reason,description,priority)
		VALUES ($1,$2,$3,'incorrect_answer','Helium is 2, not 4','high') RETURNING id`, f.student, f.quiz, f.question).Scan(&f.report))
	must("contact", pool.QueryRow(ctx, `INSERT INTO contact_submissions (topic,name,email,message)
		VALUES ('support','Visitor','v-'||$1||'@example.test','Hello') RETURNING id`, tag).Scan(&f.contact))
	_, err = pool.Exec(ctx, `INSERT INTO device_tokens (user_id,token,platform,app_version) VALUES ($1,'tok-'||$2,'android','1.2.0')`, f.student, tag)
	must("device", err)
	_, err = pool.Exec(ctx, `INSERT INTO points_ledger (user_id,amount,reason,reference_id,balance_after,expires_at)
		VALUES ($1,300,'quiz_attempt',$2,300,now()+interval '10 days')`, f.student, attempt)
	must("ledger", err)

	t.Cleanup(func() {
		c := context.Background()
		pool.Exec(c, `DELETE FROM reports WHERE id=$1`, f.report)
		pool.Exec(c, `DELETE FROM points_ledger WHERE user_id=$1`, f.student)
		pool.Exec(c, `DELETE FROM quiz_attempts WHERE quiz_id=$1`, f.quiz)
		pool.Exec(c, `DELETE FROM questions WHERE quiz_id=$1`, f.quiz)
		pool.Exec(c, `DELETE FROM quizzes WHERE id=$1`, f.quiz)
		pool.Exec(c, `DELETE FROM users WHERE id IN ($1,$2)`, f.student, f.teacher)
		pool.Exec(c, `DELETE FROM contact_submissions WHERE id=$1`, f.contact)
		pool.Exec(c, `DELETE FROM institutions WHERE id IN ($1,$2)`, f.inst, f.dupInst)
		pool.Exec(c, `DELETE FROM audit_log WHERE admin_id IN ($1,$2)`, f.admin, f.moderator)
		pool.Exec(c, `DELETE FROM admin_accounts WHERE id IN ($1,$2)`, f.admin, f.moderator)
	})
	return f
}

func consoleRouter(h *Handler, adminID string) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := context.WithValue(req.Context(), middleware.ContextKeyAdminID, adminID)
			ctx = context.WithValue(ctx, middleware.ContextKeyRole, "super_admin")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/overview", h.Overview)
	r.Get("/institutions", h.ListInstitutions)
	r.Get("/institutions/queue", h.InstitutionQueue)
	r.Get("/institutions/{institutionId}", h.GetInstitution)
	r.Get("/institutions/{institutionId}/duplicates", h.InstitutionDuplicates)
	r.Get("/institutions/{institutionId}/point-rules", h.InstitutionPointRules)
	r.Put("/institutions/{institutionId}/multiplier", h.SetInstitutionMultiplier)
	r.Get("/students/{userId}/profile", h.StudentProfile)
	r.Get("/student-scores", h.StudentScores)
	r.Get("/teacher-stats", h.TeacherStats)
	r.Get("/users/{userId}", h.GetUser)
	r.Get("/users/{userId}/ledger", h.UserLedger)
	r.Get("/reports", h.ListReports)
	r.Get("/reports/{reportId}/evidence", h.ReportEvidence)
	r.Post("/reports/{reportId}/resolve", h.ResolveReport)
	r.Patch("/contact-submissions/{id}", h.UpdateContactSubmission)
	r.Patch("/admin-accounts/{adminId}", h.UpdateAdminAccount)
	r.Get("/security-policy", h.GetSecurityPolicy)
	r.Put("/security-policy", h.PutSecurityPolicy)
	r.Get("/points-reserve", h.GetPointsReserve)
	r.Put("/points-reserve", h.PutPointsReserve)
	r.Get("/health", h.Health)
	r.Post("/announcements/estimate", h.EstimateAnnouncementReach)
	r.Get("/audit-log", h.AuditLog)
	return r
}

func do(t *testing.T, srv http.Handler, method, path string, body interface{}) (int, map[string]interface{}, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &env)
	raw := env.Data
	if raw == nil {
		raw = rec.Body.Bytes()
	}
	var obj map[string]interface{}
	json.Unmarshal(raw, &obj)
	return rec.Code, obj, raw
}

func TestConsoleEndpoints(t *testing.T) {
	pool := openTestDB(t)
	f := seedConsole(t, pool)
	h := &Handler{db: pool}
	srv := consoleRouter(h, f.admin)

	t.Run("overview carries queue depth", func(t *testing.T) {
		code, body, _ := do(t, srv, "GET", "/overview", nil)
		if code != 200 {
			t.Fatalf("status %d", code)
		}
		rep := body["reports"].(map[string]interface{})
		if rep["high"].(float64) < 1 {
			t.Errorf("high reports = %v", rep["high"])
		}
		if _, ok := body["previous"].(map[string]interface{})["active_users_week"]; !ok {
			t.Errorf("previous-period comparison missing: %v", body["previous"])
		}
		if body["points_expiring"].(map[string]interface{})["points_14d"].(float64) < 300 {
			t.Errorf("points expiring = %v", body["points_expiring"])
		}
	})

	t.Run("institution profile and duplicates", func(t *testing.T) {
		_, body, _ := do(t, srv, "GET", "/institutions/"+f.inst, nil)
		if body["city"] != "Pune" || body["contact_name"] != "Dr. Test" {
			t.Errorf("profile fields missing: %v", body)
		}
		_, dup, _ := do(t, srv, "GET", "/institutions/"+f.inst+"/duplicates", nil)
		matches := dup["matches"].([]interface{})
		if len(matches) != 1 || matches[0].(map[string]interface{})["id"] != f.dupInst {
			t.Fatalf("duplicates = %v", dup)
		}
	})

	t.Run("multiplier change is recorded", func(t *testing.T) {
		code, _, _ := do(t, srv, "PUT", "/institutions/"+f.inst+"/multiplier", map[string]interface{}{"value": 1.1})
		if code != 400 {
			t.Errorf("missing reason accepted: %d", code)
		}
		code, _, _ = do(t, srv, "PUT", "/institutions/"+f.inst+"/multiplier", map[string]interface{}{"value": 1.1, "reason": "pilot"})
		if code != 200 {
			t.Fatalf("set multiplier: %d", code)
		}
		_, rules, _ := do(t, srv, "GET", "/institutions/"+f.inst+"/point-rules", nil)
		if rules["point_multiplier"].(float64) != 1.1 || len(rules["history"].([]interface{})) != 1 {
			t.Errorf("rules = %v", rules)
		}
		_, _, raw := do(t, srv, "GET", "/institutions?non_default_multiplier=1&city=Pune", nil)
		if !strings.Contains(string(raw), f.inst) || !strings.Contains(string(raw), `"point_multiplier":1.1`) {
			t.Errorf("list filter/columns: %s", raw)
		}
		_, _, raw = do(t, srv, "GET", "/institutions/queue", nil)
		if !strings.Contains(string(raw), f.dupInst) {
			t.Errorf("queue missing pending institution: %s", raw)
		}
	})

	t.Run("student score, teacher stats, devices, ledger", func(t *testing.T) {
		_, prof, _ := do(t, srv, "GET", "/students/"+f.student+"/profile", nil)
		score, ok := prof["score"].(map[string]interface{})
		if !ok {
			t.Fatalf("no score block: %v", prof)
		}
		if s := score["qwish_score"].(float64); s < 100 || s > 900 {
			t.Errorf("qwish_score out of range: %v", s)
		}
		if p := score["percentile"].(float64); p < 1 || p > 100 {
			t.Errorf("percentile out of range: %v", p)
		}
		if score["factors"].(map[string]interface{})["accuracy"].(float64) != 100 {
			t.Errorf("accuracy = %v", score["factors"])
		}
		_, _, raw := do(t, srv, "GET", "/teacher-stats?ids="+f.teacher, nil)
		var stats []map[string]interface{}
		json.Unmarshal(raw, &stats)
		if len(stats) != 1 || stats[0]["quizzes_authored"].(float64) != 1 || stats[0]["students_participating_30d"].(float64) != 1 {
			t.Errorf("teacher stats = %s", raw)
		}
		_, user, _ := do(t, srv, "GET", "/users/"+f.student, nil)
		if len(user["devices"].([]interface{})) != 1 {
			t.Errorf("devices = %v", user["devices"])
		}
		_, _, raw = do(t, srv, "GET", "/users/"+f.student+"/ledger", nil)
		if !strings.Contains(string(raw), "Console Quiz") {
			t.Errorf("ledger lacks quiz title: %s", raw)
		}
	})

	t.Run("report evidence and resolution", func(t *testing.T) {
		_, ev, _ := do(t, srv, "GET", "/reports/"+f.report+"/evidence", nil)
		q := ev["question"].(map[string]interface{})
		if q["responses"].(float64) != 1 || q["points_credited"].(float64) != 10 {
			t.Errorf("evidence = %v", q)
		}
		if code, _, _ := do(t, srv, "POST", "/reports/"+f.report+"/resolve", map[string]string{"resolution": "bogus", "note": "x"}); code != 400 {
			t.Errorf("bogus resolution accepted: %d", code)
		}
		if code, _, _ := do(t, srv, "POST", "/reports/"+f.report+"/resolve", map[string]string{"resolution": "author_warned"}); code != 400 {
			t.Errorf("missing note accepted: %d", code)
		}
		if code, _, _ := do(t, srv, "POST", "/reports/"+f.report+"/resolve", map[string]string{"resolution": "author_warned", "note": "Key fixed; author warned"}); code != 200 {
			t.Fatalf("resolve: %d", code)
		}
		var status, resolution, note string
		pool.QueryRow(context.Background(), `SELECT status, resolution, resolution_note FROM reports WHERE id=$1`, f.report).Scan(&status, &resolution, &note)
		if status != "resolved" || resolution != "author_warned" || note != "Key fixed; author warned" {
			t.Errorf("stored = %s %s %q", status, resolution, note)
		}
		var warned int
		pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM audit_log WHERE action_type='warn_author' AND target_id=$1`, f.teacher).Scan(&warned)
		if warned != 1 {
			t.Errorf("author warning not audited")
		}
	})

	t.Run("contact triage", func(t *testing.T) {
		code, _, _ := do(t, srv, "PATCH", "/contact-submissions/"+f.contact, map[string]interface{}{
			"status": "in_progress", "assignee_id": f.moderator, "internal_note": "Called back"})
		if code != 200 {
			t.Fatalf("patch contact: %d", code)
		}
		var assignee, note, status string
		pool.QueryRow(context.Background(), `SELECT assignee_id::text, internal_note, status FROM contact_submissions WHERE id=$1`, f.contact).Scan(&assignee, &note, &status)
		if assignee != f.moderator || note != "Called back" || status != "in_progress" {
			t.Errorf("stored = %s %q %s", assignee, note, status)
		}
		if code, _, _ := do(t, srv, "PATCH", "/contact-submissions/"+f.contact, map[string]interface{}{"assignee_id": uuid.NewString()}); code != 400 {
			t.Errorf("unknown assignee accepted: %d", code)
		}
	})

	t.Run("admin account guards", func(t *testing.T) {
		// The moderator can't demote the only super_admin (the fixture admin) —
		// run as the moderator so the self-edit guard doesn't fire first.
		var others int
		pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM admin_accounts WHERE role='super_admin' AND status='active' AND deleted_at IS NULL AND id<>$1`, f.admin).Scan(&others)
		modSrv := consoleRouter(h, f.moderator)
		code, _, _ := do(t, modSrv, "PATCH", "/admin-accounts/"+f.admin, map[string]string{"role": "moderator", "reason": "test"})
		if others == 0 && code != 409 {
			t.Errorf("last super_admin demoted: %d", code)
		}
		if code, _, _ := do(t, srv, "PATCH", "/admin-accounts/"+f.moderator, map[string]string{"role": "root"}); code != 400 {
			t.Errorf("invalid role accepted: %d", code)
		}
		if code, _, _ := do(t, srv, "PATCH", "/admin-accounts/"+f.moderator, map[string]string{"role": "support_agent", "reason": "rotation"}); code != 200 {
			t.Errorf("role change: %d", code)
		}
		_, _, raw := do(t, srv, "GET", "/audit-log?target_id="+f.moderator, nil)
		if !strings.Contains(string(raw), "change_admin_role") || !strings.Contains(string(raw), "rotation") {
			t.Errorf("audit target filter/reason missing: %s", raw)
		}
	})

	t.Run("policy, reserve, health, reach", func(t *testing.T) {
		if code, _, _ := do(t, srv, "PUT", "/security-policy", map[string]interface{}{"require_admin_passkeys": true}); code != 400 {
			t.Errorf("policy without reason: %d", code)
		}
		do(t, srv, "PUT", "/security-policy", map[string]interface{}{"require_admin_passkeys": true, "reason": "t"})
		_, pol, _ := do(t, srv, "GET", "/security-policy", nil)
		if pol["require_admin_passkeys"] != true {
			t.Errorf("policy = %v", pol)
		}
		do(t, srv, "PUT", "/security-policy", map[string]interface{}{"require_admin_passkeys": false, "reason": "t"})

		do(t, srv, "PUT", "/points-reserve", map[string]interface{}{"reserve": 5000000, "warn_pct": 85, "reason": "budget"})
		_, res, _ := do(t, srv, "GET", "/points-reserve", nil)
		if res["reserve"].(float64) != 5000000 || res["warn_pct"].(float64) != 85 {
			t.Errorf("reserve = %v", res)
		}
		do(t, srv, "PUT", "/points-reserve", map[string]interface{}{"reserve": nil, "warn_pct": 90, "reason": "reset"})

		_, health, _ := do(t, srv, "GET", "/health", nil)
		if health["status"] == nil || len(health["checks"].([]interface{})) < 3 {
			t.Errorf("health = %v", health)
		}
		_, reach, _ := do(t, srv, "POST", "/announcements/estimate", map[string]interface{}{"audience": "institution", "institution_ids": []string{f.inst}})
		if reach["recipients"].(float64) != 2 || reach["push_reachable"].(float64) != 1 {
			t.Errorf("reach = %v", reach)
		}
	})
}

func TestAdminSessionRevocation(t *testing.T) {
	pool := openTestDB(t)
	f := seedConsole(t, pool)
	sid := "sess-" + uuid.NewString()
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM admin_sessions WHERE session_id=$1`, sid) })
	payload, _ := json.Marshal(map[string]interface{}{"session_id": sid, "amr": []map[string]string{{"method": "otp"}}})
	token := "x." + base64.RawURLEncoding.EncodeToString(payload) + ".y"

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	chain := func(r *http.Request) *httptest.ResponseRecorder {
		r.Header.Set("Authorization", "Bearer "+token)
		r = r.WithContext(context.WithValue(r.Context(), middleware.ContextKeyAdminID, f.admin))
		rec := httptest.NewRecorder()
		middleware.TrackAdminSessions(pool)(inner).ServeHTTP(rec, r)
		return rec
	}
	if rec := chain(httptest.NewRequest("GET", "/", nil)); rec.Code != 204 {
		t.Fatalf("first request: %d", rec.Code)
	}
	// The upsert is async.
	var n int
	for i := 0; i < 20 && n == 0; i++ {
		time.Sleep(50 * time.Millisecond)
		pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM admin_sessions WHERE session_id=$1 AND method='otp'`, sid).Scan(&n)
	}
	if n != 1 {
		t.Fatalf("session not recorded")
	}
	pool.Exec(context.Background(), `UPDATE admin_sessions SET revoked_at=now() WHERE session_id=$1`, sid)
	if rec := chain(httptest.NewRequest("GET", "/", nil)); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked session allowed: %d", rec.Code)
	}
}
