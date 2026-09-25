package admin

// Endpoints behind the super-admin console redesign: institution rules and
// duplicate lookup, learner and teacher insight, report evidence, contact
// triage, admin sessions and security policy, point reserve, service health
// and announcement reach. Schema: migrations/071_admin_console.sql.

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/middleware"
)

// ── Institutions ─────────────────────────────────────────────────────────────

// Mail providers anyone can register on; a shared domain there proves nothing.
var publicMailDomains = map[string]bool{
	"gmail.com": true, "yahoo.com": true, "yahoo.co.in": true, "outlook.com": true,
	"hotmail.com": true, "live.com": true, "icloud.com": true, "rediffmail.com": true,
	"protonmail.com": true, "proton.me": true, "aol.com": true, "zoho.com": true,
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func normName(s string) string { return nonAlnum.ReplaceAllString(strings.ToLower(s), "") }

func hostOf(raw string) string {
	h := strings.ToLower(strings.TrimSpace(raw))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	h = strings.TrimPrefix(h, "www.")
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	return h
}

// GET /api/v1/admin/institutions/{institutionId}/duplicates
//
// Other institutions that may be the same organisation: same normalised name,
// same non-public email domain, or same website host.
func (h *Handler) InstitutionDuplicates(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "institutionId")
	var name, email string
	var website *string
	if err := h.db.QueryRow(r.Context(),
		`SELECT name, contact_email, onboarding_website FROM institutions WHERE id=$1`, id).Scan(&name, &email, &website); err != nil {
		middleware.NotFound(w, "institution")
		return
	}
	domain := ""
	if at := strings.LastIndex(email, "@"); at >= 0 {
		domain = strings.ToLower(email[at+1:])
	}
	if publicMailDomains[domain] {
		domain = ""
	}
	site := ""
	if website != nil {
		site = hostOf(*website)
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT id, name, type, status, contact_email, COALESCE(onboarding_website,''), COALESCE(onboarding_city,''), created_at
		  FROM institutions
		 WHERE id <> $1 AND deleted_at IS NULL
		   AND (regexp_replace(lower(name), '[^a-z0-9]+', '', 'g') = $2
		        OR ($3 <> '' AND lower(split_part(contact_email, '@', 2)) = $3)
		        OR ($4 <> '' AND lower(onboarding_website) LIKE '%' || $4 || '%'))
		 ORDER BY created_at DESC LIMIT 10`, id, normName(name), domain, site)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type match struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		Type      string    `json:"type"`
		Status    string    `json:"status"`
		Email     string    `json:"contact_email"`
		City      string    `json:"city"`
		CreatedAt time.Time `json:"created_at"`
		Reasons   []string  `json:"reasons"`
	}
	out := []match{}
	for rows.Next() {
		var m match
		var ws string
		if rows.Scan(&m.ID, &m.Name, &m.Type, &m.Status, &m.Email, &ws, &m.City, &m.CreatedAt) != nil {
			continue
		}
		if normName(m.Name) == normName(name) {
			m.Reasons = append(m.Reasons, "same name")
		}
		if domain != "" && strings.HasSuffix(strings.ToLower(m.Email), "@"+domain) {
			m.Reasons = append(m.Reasons, "same email domain")
		}
		if site != "" && strings.Contains(strings.ToLower(ws), site) {
			m.Reasons = append(m.Reasons, "same website")
		}
		out = append(out, m)
	}
	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"checked": map[string]string{"name": name, "email_domain": domain, "website": site},
		"matches": out,
	})
}

// GET /api/v1/admin/institutions/{institutionId}/point-rules
func (h *Handler) InstitutionPointRules(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "institutionId")
	var mult float64
	var expiry int
	var grace bool
	if err := h.db.QueryRow(r.Context(),
		`SELECT point_multiplier::float8, point_expiry_months, streak_grace_enabled FROM institutions WHERE id=$1`, id,
	).Scan(&mult, &expiry, &grace); err != nil {
		middleware.NotFound(w, "institution")
		return
	}
	platform := map[string]json.RawMessage{}
	if rows, err := h.db.Query(r.Context(),
		`SELECT key, value FROM point_economy_config WHERE key IN ('base_points_per_question','referral_bonus')`); err == nil {
		for rows.Next() {
			var k string
			var v json.RawMessage
			if rows.Scan(&k, &v) == nil {
				platform[k] = v
			}
		}
		rows.Close()
	}
	type hRow struct {
		OldValue  *float64  `json:"old_value"`
		NewValue  float64   `json:"new_value"`
		Reason    string    `json:"reason"`
		ChangedBy string    `json:"changed_by"`
		CreatedAt time.Time `json:"created_at"`
	}
	history := []hRow{}
	if rows, err := h.db.Query(r.Context(), `
		SELECT h.old_value::float8, h.new_value::float8, h.reason, COALESCE(a.name,'System'), h.created_at
		  FROM institution_multiplier_history h LEFT JOIN admin_accounts a ON a.id = h.changed_by
		 WHERE h.institution_id=$1 ORDER BY h.created_at DESC LIMIT 50`, id); err == nil {
		for rows.Next() {
			var x hRow
			if rows.Scan(&x.OldValue, &x.NewValue, &x.Reason, &x.ChangedBy, &x.CreatedAt) == nil {
				history = append(history, x)
			}
		}
		rows.Close()
	}
	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"point_multiplier":     mult,
		"point_expiry_months":  expiry,
		"streak_grace_enabled": grace,
		"platform":             platform,
		"history":              history,
	})
}

// PUT /api/v1/admin/institutions/{institutionId}/multiplier — {value, reason}
func (h *Handler) SetInstitutionMultiplier(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "institutionId")
	var req struct {
		Value  float64 `json:"value"`
		Reason string  `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	// NUMERIC(4,2) caps at 99.99; anything outside 0.1–5 is a typo, not a policy.
	if req.Value < 0.1 || req.Value > 5 || req.Reason == "" {
		middleware.BadRequest(w, "value must be between 0.1 and 5, and a reason is required")
		return
	}
	req.Value = math.Round(req.Value*100) / 100
	adminID := middleware.GetAdminID(r)
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(r.Context())
	var old float64
	if err := tx.QueryRow(r.Context(),
		`SELECT point_multiplier::float8 FROM institutions WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&old); err != nil {
		middleware.NotFound(w, "institution")
		return
	}
	if _, err := tx.Exec(r.Context(),
		`UPDATE institutions SET point_multiplier=$1, updated_at=now() WHERE id=$2`, req.Value, id); err != nil {
		middleware.InternalError(w)
		return
	}
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO institution_multiplier_history (institution_id, old_value, new_value, reason, changed_by)
		 VALUES ($1,$2,$3,$4,$5)`, id, old, req.Value, req.Reason, nullableAdmin(adminID)); err != nil {
		middleware.InternalError(w)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		middleware.InternalError(w)
		return
	}
	logAuditChange(r.Context(), h.db, adminID, "set_institution_multiplier", "institution", id, req.Reason,
		map[string]float64{"point_multiplier": old}, map[string]float64{"point_multiplier": req.Value})
	middleware.JSON(w, http.StatusOK, map[string]float64{"point_multiplier": req.Value})
}

// ── Learners ─────────────────────────────────────────────────────────────────

// scoredStudents reads the stored skill rating (the same Qwish Score recruiters
// and the leaderboard see) over every active student, without the
// recruiter-visibility filter. The five factors are 90-day diagnostics only;
// they do not feed the score.
const scoredStudents = `WITH attempt_stats AS (
	SELECT user_id, COALESCE(SUM(total_correct),0)::float8 total_correct,
	       COALESCE(SUM(total_questions),0)::float8 total_questions,
	       COUNT(*)::float8 completed
	  FROM quiz_attempts WHERE status='completed' GROUP BY user_id
), recent AS (
	SELECT a.user_id,
	       COALESCE(SUM(a.total_correct),0)::float8 correct, COALESCE(SUM(a.total_questions),0)::float8 questions,
	       COUNT(*)::float8 completed
	  FROM quiz_attempts a
	 WHERE a.status='completed' AND a.completed_at > now() - interval '90 days' GROUP BY a.user_id
), recent_resp AS (
	SELECT a.user_id,
	       COALESCE(SUM(q.difficulty),0)::float8 total_difficulty,
	       COALESCE(SUM(q.difficulty) FILTER (WHERE qr.is_correct),0)::float8 correct_difficulty,
	       COALESCE(AVG(CASE WHEN qr.time_taken_ms < 1000 THEN .1
	         WHEN qr.time_taken_ms <= (q.time_limit_seconds*1000)/3.0 THEN 1.0
	         ELSE GREATEST((q.time_limit_seconds*1000.0-qr.time_taken_ms)/
	           NULLIF(q.time_limit_seconds*1000.0-q.time_limit_seconds*1000.0/3.0,0),.1)
	       END) FILTER (WHERE qr.is_correct AND qr.time_taken_ms IS NOT NULL),0)::float8 speed
	  FROM question_responses qr JOIN questions q ON q.id=qr.question_id
	  JOIN quiz_attempts a ON a.id=qr.attempt_id AND a.status='completed' AND a.completed_at > now() - interval '90 days'
	 GROUP BY a.user_id
), scored AS (
	SELECT u.id,
	       COALESCE(ls.qwish_score,100)::float8 qwish_score,
	       COALESCE(a.completed,0)::int completed,
	       CASE WHEN COALESCE(re.questions,0)>0 THEN re.correct/re.questions*100 ELSE 0 END accuracy,
	       CASE WHEN COALESCE(rr.total_difficulty,0)>0 THEN rr.correct_difficulty/rr.total_difficulty*100 ELSE 0 END difficulty,
	       (1-EXP(-COALESCE(u.current_streak,0)::float8/14))*100 consistency,
	       COALESCE(rr.speed,0)*100 speed,
	       (1-EXP(-COALESCE(re.completed,0)/20))*100 activity
	  FROM users u
	  LEFT JOIN attempt_stats a ON a.user_id=u.id
	  LEFT JOIN leaderboard_scores ls ON ls.user_id=u.id
	  LEFT JOIN recent re ON re.user_id=u.id LEFT JOIN recent_resp rr ON rr.user_id=u.id
	 WHERE u.role='student' AND u.status='active' AND u.deleted_at IS NULL
), ranked AS (
	SELECT *, CEIL(PERCENT_RANK() OVER (ORDER BY qwish_score)*99+1)::int percentile FROM scored
) `

type studentScore struct {
	ID         string             `json:"id"`
	QwishScore float64            `json:"qwish_score"`
	Percentile int                `json:"percentile"`
	Completed  int                `json:"assessments"`
	Factors    map[string]float64 `json:"factors"`
}

func (h *Handler) studentScores(ctx context.Context, ids []string) (map[string]studentScore, error) {
	rows, err := h.db.Query(ctx, scoredStudents+`
		SELECT id::text, qwish_score, percentile, completed, accuracy, difficulty, consistency, speed, activity
		  FROM ranked WHERE id::text = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]studentScore{}
	for rows.Next() {
		var s studentScore
		var acc, dif, con, spd, act float64
		if err := rows.Scan(&s.ID, &s.QwishScore, &s.Percentile, &s.Completed, &acc, &dif, &con, &spd, &act); err != nil {
			return nil, err
		}
		s.QwishScore = math.Round(s.QwishScore)
		s.Factors = map[string]float64{
			"accuracy": math.Round(acc), "difficulty": math.Round(dif), "consistency": math.Round(con),
			"speed": math.Round(spd), "activity": math.Round(act),
		}
		out[s.ID] = s
	}
	return out, nil
}

// GET /api/v1/admin/student-scores?ids=a,b,c — scores for a page of students.
func (h *Handler) StudentScores(w http.ResponseWriter, r *http.Request) {
	ids := splitIDs(r.URL.Query().Get("ids"), 50)
	if len(ids) == 0 {
		middleware.JSON(w, http.StatusOK, []studentScore{})
		return
	}
	scores, err := h.studentScores(r.Context(), ids)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	out := make([]studentScore, 0, len(scores))
	for _, id := range ids {
		if s, ok := scores[id]; ok {
			out = append(out, s)
		}
	}
	middleware.JSON(w, http.StatusOK, out)
}

// GET /api/v1/admin/students/{userId}/profile
func (h *Handler) StudentProfile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "userId")
	var name, email, status string
	var inst *string
	var points int64
	var streak, longest int
	var visible bool
	if err := h.db.QueryRow(r.Context(), `
		SELECT u.display_name, u.email, u.status, i.name, u.total_points, u.current_streak, u.longest_streak, u.recruiter_visible
		  FROM users u LEFT JOIN institutions i ON i.id=u.institution_id
		 WHERE u.id=$1 AND u.role='student' AND u.deleted_at IS NULL`, id,
	).Scan(&name, &email, &status, &inst, &points, &streak, &longest, &visible); err != nil {
		middleware.NotFound(w, "student")
		return
	}
	scores, err := h.studentScores(r.Context(), []string{id})
	if err != nil {
		middleware.InternalError(w)
		return
	}
	var expiring int64
	var expiringAt *time.Time
	h.db.QueryRow(r.Context(), `
		SELECT COALESCE(SUM(amount),0), MIN(expires_at) FROM points_ledger
		 WHERE user_id=$1 AND amount > 0 AND expires_at > now() AND expires_at <= now() + interval '30 days'`, id,
	).Scan(&expiring, &expiringAt)
	resp := map[string]interface{}{
		"id": id, "display_name": name, "email": email, "status": status, "institution": inst,
		"total_points": points, "current_streak": streak, "longest_streak": longest,
		"recruiter_visible":   visible,
		"points_expiring_30d": expiring, "points_expiring_at": expiringAt,
		"score": nil,
	}
	// Suspended students aren't ranked; the score block is simply absent.
	if s, ok := scores[id]; ok {
		resp["score"] = s
	}
	middleware.JSON(w, http.StatusOK, resp)
}

// GET /api/v1/admin/users/{userId}/ledger?limit=
func (h *Handler) UserLedger(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "userId")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 25
	}
	rows, err := h.db.Query(r.Context(), `
		SELECT l.id, l.amount, l.reason, l.balance_after, l.expires_at, l.created_at, q.title
		  FROM points_ledger l
		  LEFT JOIN quiz_attempts a ON l.reason='quiz_attempt' AND a.id=l.reference_id
		  LEFT JOIN quizzes q ON q.id=a.quiz_id
		 WHERE l.user_id=$1 ORDER BY l.created_at DESC LIMIT $2`, id, limit)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type row struct {
		ID           string     `json:"id"`
		Amount       int64      `json:"amount"`
		Reason       string     `json:"reason"`
		BalanceAfter int64      `json:"balance_after"`
		ExpiresAt    *time.Time `json:"expires_at"`
		CreatedAt    time.Time  `json:"created_at"`
		QuizTitle    *string    `json:"quiz_title"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if rows.Scan(&x.ID, &x.Amount, &x.Reason, &x.BalanceAfter, &x.ExpiresAt, &x.CreatedAt, &x.QuizTitle) == nil {
			out = append(out, x)
		}
	}
	middleware.JSON(w, http.StatusOK, out)
}

// GET /api/v1/admin/teacher-stats?ids=a,b — authoring and participation per teacher.
func (h *Handler) TeacherStats(w http.ResponseWriter, r *http.Request) {
	ids := splitIDs(r.URL.Query().Get("ids"), 50)
	type stat struct {
		ID              string     `json:"id"`
		QuizzesAuthored int        `json:"quizzes_authored"`
		Published       int        `json:"quizzes_published"`
		LastAuthoredAt  *time.Time `json:"last_authored_at"`
		Classes         int        `json:"active_classes"`
		Participants30  int        `json:"students_participating_30d"`
	}
	out := []stat{}
	if len(ids) == 0 {
		middleware.JSON(w, http.StatusOK, out)
		return
	}
	rows, err := h.db.Query(r.Context(), `
		SELECT u.id::text,
		       (SELECT COUNT(*) FROM quizzes q WHERE q.created_by=u.id AND q.deleted_at IS NULL),
		       (SELECT COUNT(*) FROM quizzes q WHERE q.created_by=u.id AND q.deleted_at IS NULL AND q.status='published'),
		       (SELECT MAX(q.created_at) FROM quizzes q WHERE q.created_by=u.id AND q.deleted_at IS NULL),
		       (SELECT COUNT(*) FROM group_teachers gt JOIN groups g ON g.id=gt.group_id AND g.archived_at IS NULL
		         WHERE gt.user_id=u.id),
		       (SELECT COUNT(DISTINCT a.user_id) FROM quiz_attempts a JOIN quizzes q ON q.id=a.quiz_id
		         WHERE q.created_by=u.id AND a.started_at > now() - interval '30 days')
		  FROM users u WHERE u.id::text = ANY($1)`, ids)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var s stat
		if rows.Scan(&s.ID, &s.QuizzesAuthored, &s.Published, &s.LastAuthoredAt, &s.Classes, &s.Participants30) == nil {
			out = append(out, s)
		}
	}
	middleware.JSON(w, http.StatusOK, out)
}

func splitIDs(raw string, max int) []string {
	out := []string{}
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" && len(out) < max {
			out = append(out, p)
		}
	}
	return out
}

// ── Reports ──────────────────────────────────────────────────────────────────

// GET /api/v1/admin/reports/{reportId}/evidence
//
// Platform data a moderator needs before recording a finding: how learners
// answered the reported question, and for the quiz, completion times with the
// fastest high scorers (a cheating signal, never proof).
func (h *Handler) ReportEvidence(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "reportId")
	var quizID, questionID *string
	if err := h.db.QueryRow(r.Context(),
		`SELECT quiz_id::text, question_id::text FROM reports WHERE id=$1`, id).Scan(&quizID, &questionID); err != nil {
		middleware.NotFound(w, "report")
		return
	}
	resp := map[string]interface{}{"question": nil, "quiz": nil, "other_reports": 0}

	if questionID != nil {
		var prompt, qType string
		var options, correct json.RawMessage
		var position int
		h.db.QueryRow(r.Context(),
			`SELECT prompt, type, options, correct_answer, position FROM questions WHERE id=$1`, *questionID,
		).Scan(&prompt, &qType, &options, &correct, &position)
		type bucket struct {
			Answer  json.RawMessage `json:"answer"`
			Count   int             `json:"count"`
			Correct bool            `json:"graded_correct"`
		}
		answers := []bucket{}
		var total, right int
		var credited int64
		if rows, err := h.db.Query(r.Context(), `
			SELECT answer, COUNT(*), bool_or(COALESCE(is_correct,false))
			  FROM question_responses WHERE question_id=$1
			 GROUP BY answer ORDER BY COUNT(*) DESC LIMIT 8`, *questionID); err == nil {
			for rows.Next() {
				var b bucket
				if rows.Scan(&b.Answer, &b.Count, &b.Correct) == nil {
					answers = append(answers, b)
				}
			}
			rows.Close()
		}
		h.db.QueryRow(r.Context(), `
			SELECT COUNT(*), COUNT(*) FILTER (WHERE is_correct),
			       COALESCE(SUM(points_earned) FILTER (WHERE is_correct),0)
			  FROM question_responses WHERE question_id=$1`, *questionID).Scan(&total, &right, &credited)
		resp["question"] = map[string]interface{}{
			"id": *questionID, "number": position, "prompt": prompt, "type": qType,
			"options": options, "correct_answer": correct,
			"responses": total, "graded_correct": right, "points_credited": credited,
			"answers": answers,
		}
	}

	if quizID != nil {
		var attempts int
		var median *float64
		h.db.QueryRow(r.Context(), `
			SELECT COUNT(*),
			       percentile_cont(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM completed_at - started_at))
			  FROM quiz_attempts WHERE quiz_id=$1 AND status='completed'`, *quizID).Scan(&attempts, &median)
		type fast struct {
			UserID   string  `json:"user_id"`
			Name     string  `json:"name"`
			Seconds  float64 `json:"seconds"`
			ScorePct float64 `json:"score_pct"`
			Attempts int     `json:"attempts"`
		}
		fastest := []fast{}
		if median != nil && *median > 0 {
			// Well under the median time with a near-perfect score.
			threshold := math.Max(20, *median*0.25)
			if rows, err := h.db.Query(r.Context(), `
				SELECT a.user_id::text, u.display_name,
				       MIN(EXTRACT(EPOCH FROM a.completed_at - a.started_at))::float8,
				       MAX(COALESCE(a.score_pct,0))::float8, COUNT(*)
				  FROM quiz_attempts a JOIN users u ON u.id=a.user_id
				 WHERE a.quiz_id=$1 AND a.status='completed' AND COALESCE(a.score_pct,0) >= 90
				   AND EXTRACT(EPOCH FROM a.completed_at - a.started_at) < $2
				 GROUP BY a.user_id, u.display_name ORDER BY 3 ASC LIMIT 10`, *quizID, threshold); err == nil {
				for rows.Next() {
					var f fast
					if rows.Scan(&f.UserID, &f.Name, &f.Seconds, &f.ScorePct, &f.Attempts) == nil {
						fastest = append(fastest, f)
					}
				}
				rows.Close()
			}
		}
		var others int
		h.db.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM reports WHERE quiz_id=$1 AND id<>$2 AND status IN ('open','reviewing')`, *quizID, id).Scan(&others)
		resp["other_reports"] = others
		resp["quiz"] = map[string]interface{}{
			"id": *quizID, "completed_attempts": attempts, "median_seconds": median, "fast_high_scorers": fastest,
		}
	}
	middleware.JSON(w, http.StatusOK, resp)
}

// ── Contact inbox ────────────────────────────────────────────────────────────

// PATCH /api/v1/admin/contact-submissions/{id} — {status?, assignee_id?, internal_note?}
// assignee_id "" unassigns. Every change is audit logged.
func (h *Handler) UpdateContactSubmission(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Status       *string `json:"status"`
		AssigneeID   *string `json:"assignee_id"`
		InternalNote *string `json:"internal_note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if req.Status != nil {
		switch *req.Status {
		case "new", "in_progress", "resolved", "spam":
		default:
			middleware.BadRequest(w, "status must be new, in_progress, resolved or spam")
			return
		}
	}
	var oldStatus string
	var oldAssignee, oldNote *string
	if err := h.db.QueryRow(r.Context(),
		`SELECT status, assignee_id::text, internal_note FROM contact_submissions WHERE id=$1`, id,
	).Scan(&oldStatus, &oldAssignee, &oldNote); err != nil {
		middleware.NotFound(w, "submission")
		return
	}
	var assignee *string
	setAssignee := req.AssigneeID != nil
	if setAssignee && *req.AssigneeID != "" {
		var ok bool
		h.db.QueryRow(r.Context(),
			`SELECT true FROM admin_accounts WHERE id::text=$1 AND status='active' AND deleted_at IS NULL`, *req.AssigneeID).Scan(&ok)
		if !ok {
			middleware.BadRequest(w, "assignee must be an active admin")
			return
		}
		assignee = req.AssigneeID
	}
	adminID := middleware.GetAdminID(r)
	_, err := h.db.Exec(r.Context(), `
		UPDATE contact_submissions SET
		  status        = COALESCE($2, status),
		  resolved_at   = CASE WHEN $2 IN ('resolved','spam') THEN now() WHEN $2 IS NOT NULL THEN NULL ELSE resolved_at END,
		  resolved_by   = CASE WHEN $2 IN ('resolved','spam') THEN $6::uuid ELSE resolved_by END,
		  assignee_id   = CASE WHEN $3 THEN $4::uuid ELSE assignee_id END,
		  internal_note = CASE WHEN $5::text IS NOT NULL THEN NULLIF($5, '') ELSE internal_note END,
		  updated_at    = now()
		 WHERE id=$1`,
		id, req.Status, setAssignee, assignee, req.InternalNote, nullableAdmin(adminID))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	logAuditChange(r.Context(), h.db, adminID, "update_contact_submission", "contact_submission", id, "",
		map[string]interface{}{"status": oldStatus, "assignee_id": oldAssignee, "note_changed": false},
		map[string]interface{}{"status": req.Status, "assignee_id": req.AssigneeID, "note_changed": req.InternalNote != nil})
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "submission updated"})
}

// GET /api/v1/admin/assignees — active admins a contact submission can be assigned to.
func (h *Handler) Assignees(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `
		SELECT id, name, role FROM admin_accounts
		 WHERE status='active' AND deleted_at IS NULL AND role IN ('super_admin','support_agent','moderator')
		 ORDER BY name`)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type a struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Role string `json:"role"`
	}
	out := []a{}
	for rows.Next() {
		var x a
		if rows.Scan(&x.ID, &x.Name, &x.Role) == nil {
			out = append(out, x)
		}
	}
	middleware.JSON(w, http.StatusOK, map[string]interface{}{"assignees": out, "me": middleware.GetAdminID(r)})
}

// ── Sessions & security policy ───────────────────────────────────────────────

// GET /api/v1/admin/me/sessions
func (h *Handler) MySessions(w http.ResponseWriter, r *http.Request) {
	adminID := middleware.GetAdminID(r)
	current := middleware.GetSessionID(r)
	rows, err := h.db.Query(r.Context(), `
		SELECT session_id, COALESCE(user_agent,''), COALESCE(ip,''), COALESCE(method,''), first_seen, last_seen, revoked_at
		  FROM admin_sessions WHERE admin_id=$1 AND (revoked_at IS NULL OR revoked_at > now() - interval '1 day')
		 ORDER BY last_seen DESC LIMIT 20`, adminID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type s struct {
		// Only the tail of the id leaves the server: enough to tell sessions apart.
		ID        string     `json:"id"`
		Label     string     `json:"label"`
		UserAgent string     `json:"user_agent"`
		IP        string     `json:"ip"`
		Method    string     `json:"method"`
		FirstSeen time.Time  `json:"first_seen"`
		LastSeen  time.Time  `json:"last_seen"`
		RevokedAt *time.Time `json:"revoked_at"`
		Current   bool       `json:"current"`
	}
	out := []s{}
	for rows.Next() {
		var x s
		var sid string
		if rows.Scan(&sid, &x.UserAgent, &x.IP, &x.Method, &x.FirstSeen, &x.LastSeen, &x.RevokedAt) != nil {
			continue
		}
		x.ID = sid
		x.Current = sid == current
		x.Label = "•••• " + sid[max(0, len(sid)-4):]
		out = append(out, x)
	}
	middleware.JSON(w, http.StatusOK, map[string]interface{}{"sessions": out, "tracking": current != ""})
}

// POST /api/v1/admin/me/sessions/{sessionId}/revoke
func (h *Handler) RevokeMySession(w http.ResponseWriter, r *http.Request) {
	adminID := middleware.GetAdminID(r)
	sid := chi.URLParam(r, "sessionId")
	if sid == middleware.GetSessionID(r) {
		middleware.BadRequest(w, "use sign out to end this session")
		return
	}
	tag, err := h.db.Exec(r.Context(),
		`UPDATE admin_sessions SET revoked_at=now(), revoked_by=$2 WHERE session_id=$1 AND admin_id=$2 AND revoked_at IS NULL`, sid, adminID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "session")
		return
	}
	logAudit(r.Context(), h.db, adminID, "revoke_admin_session", "admin", adminID, "")
	middleware.JSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

// POST /api/v1/admin/me/sessions/revoke-others
func (h *Handler) RevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	adminID := middleware.GetAdminID(r)
	tag, err := h.db.Exec(r.Context(),
		`UPDATE admin_sessions SET revoked_at=now(), revoked_by=$1
		  WHERE admin_id=$1 AND session_id<>$2 AND revoked_at IS NULL`, adminID, middleware.GetSessionID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	logAudit(r.Context(), h.db, adminID, "revoke_other_admin_sessions", "admin", adminID, "")
	middleware.JSON(w, http.StatusOK, map[string]int64{"revoked": tag.RowsAffected()})
}

func (h *Handler) setting(ctx context.Context, key string) json.RawMessage {
	var v json.RawMessage
	h.db.QueryRow(ctx, `SELECT value FROM platform_settings WHERE key=$1`, key).Scan(&v)
	return v
}

func (h *Handler) putSetting(ctx context.Context, key string, value interface{}, adminID string) error {
	b, _ := json.Marshal(value)
	_, err := h.db.Exec(ctx, `
		INSERT INTO platform_settings (key, value, updated_by, updated_at) VALUES ($1, $2, $3, now())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		key, string(b), nullableAdmin(adminID))
	return err
}

// GET /api/v1/admin/security-policy
func (h *Handler) GetSecurityPolicy(w http.ResponseWriter, r *http.Request) {
	required := string(h.setting(r.Context(), "require_admin_passkeys")) == "true"
	type a struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	without := []a{}
	if rows, err := h.db.Query(r.Context(), `
		SELECT id, name, email, role FROM admin_accounts x
		 WHERE status='active' AND deleted_at IS NULL
		   AND NOT EXISTS (SELECT 1 FROM webauthn_credentials c WHERE c.admin_id=x.id)
		 ORDER BY name`); err == nil {
		for rows.Next() {
			var x a
			if rows.Scan(&x.ID, &x.Name, &x.Email, &x.Role) == nil {
				without = append(without, x)
			}
		}
		rows.Close()
	}
	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"require_admin_passkeys": required,
		"admins_without_passkey": without,
		// Recovery exists: a super_admin can reset another admin's passkeys
		// (POST /admin/admin-accounts/{id}/reset-passkeys), after which that
		// admin signs in with an emailed code and enrols again.
		"recovery_available": true,
	})
}

// PUT /api/v1/admin/security-policy — {require_admin_passkeys, reason}
func (h *Handler) PutSecurityPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Require *bool  `json:"require_admin_passkeys"`
		Reason  string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Require == nil || strings.TrimSpace(req.Reason) == "" {
		middleware.BadRequest(w, "require_admin_passkeys and a reason are required")
		return
	}
	adminID := middleware.GetAdminID(r)
	before := string(h.setting(r.Context(), "require_admin_passkeys")) == "true"
	if err := h.putSetting(r.Context(), "require_admin_passkeys", *req.Require, adminID); err != nil {
		middleware.InternalError(w)
		return
	}
	logAuditChange(r.Context(), h.db, adminID, "update_security_policy", "platform_settings", "", req.Reason,
		map[string]bool{"require_admin_passkeys": before}, map[string]bool{"require_admin_passkeys": *req.Require})
	middleware.JSON(w, http.StatusOK, map[string]bool{"require_admin_passkeys": *req.Require})
}

// POST /api/v1/admin/admin-accounts/{adminId}/reset-passkeys — {reason}
//
// Recovery for an admin who lost every passkey: removes their credentials and
// ends their sessions, so they sign in with an emailed code and enrol anew.
func (h *Handler) ResetAdminPasskeys(w http.ResponseWriter, r *http.Request) {
	target := chi.URLParam(r, "adminId")
	adminID := middleware.GetAdminID(r)
	if target == adminID {
		middleware.BadRequest(w, "manage your own passkeys from Security")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || strings.TrimSpace(req.Reason) == "" {
		middleware.BadRequest(w, "a reason is required")
		return
	}
	tag, err := h.db.Exec(r.Context(), `DELETE FROM webauthn_credentials WHERE admin_id=$1`, target)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	h.db.Exec(r.Context(), `UPDATE admin_accounts SET token_generation = token_generation + 1 WHERE id=$1`, target)
	h.db.Exec(r.Context(), `UPDATE admin_sessions SET revoked_at=now(), revoked_by=$2 WHERE admin_id=$1 AND revoked_at IS NULL`, target, nullableAdmin(adminID))
	logAudit(r.Context(), h.db, adminID, "reset_admin_passkeys", "admin", target, req.Reason)
	middleware.JSON(w, http.StatusOK, map[string]int64{"passkeys_removed": tag.RowsAffected()})
}

// ── Point reserve ────────────────────────────────────────────────────────────

// GET /api/v1/admin/points-reserve
func (h *Handler) GetPointsReserve(w http.ResponseWriter, r *http.Request) {
	var reserve *float64
	_ = json.Unmarshal(h.setting(r.Context(), "points_reserve"), &reserve)
	warn := 90.0
	_ = json.Unmarshal(h.setting(r.Context(), "points_reserve_warn_pct"), &warn)
	var circulating int64
	h.db.QueryRow(r.Context(),
		`SELECT COALESCE(SUM(total_points),0) FROM users WHERE deleted_at IS NULL AND role='student'`).Scan(&circulating)
	var updatedAt *time.Time
	h.db.QueryRow(r.Context(), `SELECT updated_at FROM platform_settings WHERE key='points_reserve'`).Scan(&updatedAt)
	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"reserve": reserve, "warn_pct": warn, "circulating_balances": circulating, "updated_at": updatedAt,
	})
}

// PUT /api/v1/admin/points-reserve — {reserve (null clears), warn_pct, reason}
func (h *Handler) PutPointsReserve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reserve *float64 `json:"reserve"`
		WarnPct *float64 `json:"warn_pct"`
		Reason  string   `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Reason) == "" {
		middleware.BadRequest(w, "a reason is required")
		return
	}
	if (req.Reserve != nil && *req.Reserve < 0) || (req.WarnPct != nil && (*req.WarnPct <= 0 || *req.WarnPct > 100)) {
		middleware.BadRequest(w, "reserve must be ≥ 0 and warn_pct between 1 and 100")
		return
	}
	adminID := middleware.GetAdminID(r)
	var before *float64
	_ = json.Unmarshal(h.setting(r.Context(), "points_reserve"), &before)
	if err := h.putSetting(r.Context(), "points_reserve", req.Reserve, adminID); err != nil {
		middleware.InternalError(w)
		return
	}
	if req.WarnPct != nil {
		h.putSetting(r.Context(), "points_reserve_warn_pct", *req.WarnPct, adminID)
	}
	logAuditChange(r.Context(), h.db, adminID, "update_points_reserve", "platform_settings", "", req.Reason,
		map[string]interface{}{"reserve": before}, map[string]interface{}{"reserve": req.Reserve, "warn_pct": req.WarnPct})
	middleware.JSON(w, http.StatusOK, map[string]interface{}{"reserve": req.Reserve})
}

// ── Service health ───────────────────────────────────────────────────────────

// GET /api/v1/admin/health — what backs the console's header status.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	type check struct {
		Name   string `json:"name"`
		Status string `json:"status"` // operational | degraded | down | unknown
		Detail string `json:"detail"`
	}
	checks := []check{}

	start := time.Now()
	var one int
	if err := h.db.QueryRow(r.Context(), `SELECT 1`).Scan(&one); err != nil {
		checks = append(checks, check{"Database", "down", "not reachable"})
	} else {
		ms := time.Since(start).Milliseconds()
		st := "operational"
		if ms > 500 {
			st = "degraded"
		}
		checks = append(checks, check{"Database", st, strconv.FormatInt(ms, 10) + " ms round trip"})
	}

	var sent, failed int
	h.db.QueryRow(r.Context(), `
		SELECT COUNT(*) FILTER (WHERE status='sent'), COUNT(*) FILTER (WHERE status='failed')
		  FROM notification_log WHERE created_at > now() - interval '1 hour'`).Scan(&sent, &failed)
	email := check{"Email delivery", "operational", strconv.Itoa(sent) + " sent · " + strconv.Itoa(failed) + " failed in the last hour"}
	if failed > 0 && failed >= sent {
		email.Status = "down"
	} else if failed > 0 {
		email.Status = "degraded"
	}
	if sent == 0 && failed == 0 {
		email.Detail = "No emails in the last hour"
	}
	checks = append(checks, email)

	var lastAttempt *time.Time
	h.db.QueryRow(r.Context(), `SELECT MAX(completed_at) FROM quiz_attempts`).Scan(&lastAttempt)
	activity := check{"Quiz activity", "unknown", "No completed attempts yet"}
	if lastAttempt != nil {
		ago := time.Since(*lastAttempt)
		activity.Status = "operational"
		activity.Detail = "Last completed attempt " + ago.Round(time.Minute).String() + " ago"
		if ago > 6*time.Hour {
			activity.Status = "degraded"
		}
	}
	checks = append(checks, activity)

	var lastLedger *time.Time
	h.db.QueryRow(r.Context(), `SELECT MAX(created_at) FROM points_ledger`).Scan(&lastLedger)
	ledger := check{"Point ledger", "unknown", "No ledger entries yet"}
	if lastLedger != nil {
		ledger.Status = "operational"
		ledger.Detail = "Last entry " + time.Since(*lastLedger).Round(time.Minute).String() + " ago"
	}
	checks = append(checks, ledger)

	overall := "operational"
	for _, c := range checks {
		if c.Status == "down" {
			overall = "down"
			break
		}
		if c.Status == "degraded" {
			overall = "degraded"
		}
	}
	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"status": overall, "checks": checks, "checked_at": time.Now(),
	})
}

// ── Announcements ────────────────────────────────────────────────────────────

// POST /api/v1/admin/announcements/estimate — {audience, institution_ids}
// Recipient count for the composer. "country" has no location data to count
// against, so it returns null rather than a guess.
func (h *Handler) EstimateAnnouncementReach(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Audience       string   `json:"audience"`
		InstitutionIDs []string `json:"institution_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	base := `SELECT COUNT(*), COUNT(*) FILTER (WHERE EXISTS (SELECT 1 FROM device_tokens d WHERE d.user_id=u.id))
	           FROM users u WHERE u.status='active' AND u.deleted_at IS NULL `
	var q string
	args := []interface{}{}
	switch req.Audience {
	case "all":
		q = base + `AND u.role IN ('student','teacher','parent','institution_admin')`
	case "students":
		q = base + `AND u.role='student'`
	case "teachers":
		q = base + `AND u.role='teacher'`
	case "institution":
		if len(req.InstitutionIDs) == 0 {
			middleware.JSON(w, http.StatusOK, map[string]interface{}{"recipients": 0, "push_reachable": 0})
			return
		}
		q = base + `AND u.institution_id::text = ANY($1)`
		args = append(args, req.InstitutionIDs)
	default:
		middleware.JSON(w, http.StatusOK, map[string]interface{}{"recipients": nil, "push_reachable": nil,
			"note": "this audience has no data to count against"})
		return
	}
	var n, push int
	if err := h.db.QueryRow(r.Context(), q, args...).Scan(&n, &push); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]interface{}{"recipients": n, "push_reachable": push})
}
