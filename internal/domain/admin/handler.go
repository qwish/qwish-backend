package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"

	"github.com/qwish/backend/internal/httpx"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/config"
	"github.com/qwish/backend/internal/domain/auth"
	"github.com/qwish/backend/internal/domain/notification"
	"github.com/qwish/backend/internal/domain/scoring"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
	"github.com/qwish/backend/internal/supabase"
)

type Handler struct {
	db     *pgxpool.Pool
	cfg    *config.Config
	notif  *notification.Service
	invite *supabase.InviteClient
}

func NewHandler(db *pgxpool.Pool, cfg *config.Config, notif *notification.Service) *Handler {
	return &Handler{db: db, cfg: cfg, notif: notif, invite: supabase.NewInviteClient(db, cfg)}
}

func validContentURL(raw *string) bool {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(*raw))
	if err != nil {
		return false
	}
	if u.IsAbs() {
		return u.Scheme == "https" && u.User == nil && u.Host != ""
	}
	if u.Host != "" || u.User != nil {
		return false
	}
	return strings.HasPrefix(u.Path, "/") && !strings.HasPrefix(u.Path, "//")
}

func allIn(values []string, allowed map[string]bool) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if !allowed[value] {
			return false
		}
	}
	return true
}

func validUUIDs(values []string) bool {
	for _, value := range values {
		if _, err := uuid.Parse(value); err != nil {
			return false
		}
	}
	return true
}

// GET /api/v1/admin/overview
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	var totalUsers, activeUsers, pendingInst, verifiedInst, suspendedInst int
	var publishedQuizzes, pendingQuizzes, reportedQuizzes int
	var attemptsToday, attemptsWeek int
	var avgScore float64
	var pointsWeek, pointsAll int64

	// One round-trip instead of 14 sequential ones — each of the metrics is an
	// independent scalar aggregate, so they compose into a single SELECT. Against
	// a remote database this turns ~14×RTT into 1×RTT.
	if err := h.db.QueryRow(r.Context(), `SELECT
		(SELECT COUNT(*) FROM users WHERE deleted_at IS NULL),
		(SELECT COUNT(DISTINCT user_id) FROM quiz_attempts WHERE completed_at >= CURRENT_DATE - 7),
		(SELECT COUNT(*) FROM institutions WHERE status='pending'),
		(SELECT COUNT(*) FROM institutions WHERE status='verified'),
		(SELECT COUNT(*) FROM institutions WHERE status='suspended'),
		(SELECT COUNT(*) FROM quizzes WHERE status='published' AND deleted_at IS NULL),
		(SELECT COUNT(*) FROM quizzes WHERE status='pending_approval' AND deleted_at IS NULL),
		(SELECT COUNT(DISTINCT quiz_id) FROM reports WHERE status='open'),
		(SELECT COUNT(*) FROM quiz_attempts WHERE completed_at::date = CURRENT_DATE),
		(SELECT COUNT(*) FROM quiz_attempts WHERE completed_at >= CURRENT_DATE - 7),
		(SELECT COALESCE(AVG(score_pct),0) FROM quiz_attempts WHERE completed_at >= CURRENT_DATE - 7),
		(SELECT COALESCE(SUM(amount),0) FROM points_ledger WHERE amount > 0 AND created_at >= CURRENT_DATE - 7),
		(SELECT COALESCE(SUM(amount),0) FROM points_ledger WHERE amount > 0)`,
	).Scan(&totalUsers, &activeUsers, &pendingInst, &verifiedInst, &suspendedInst,
		&publishedQuizzes, &pendingQuizzes, &reportedQuizzes, &attemptsToday,
		&attemptsWeek, &avgScore, &pointsWeek, &pointsAll); err != nil {
		middleware.InternalError(w)
		return
	}

	// Queue depth and age for the dashboard's "needs attention" list.
	var reportsOpen, reportsHigh, contactNew, instOver48h, expiringLearners int
	var expiring14d int64
	var oldestReport, oldestInst, oldestQuiz *time.Time
	var activePrevWeek, attemptsYesterdaySoFar int
	// Comparisons for the metric strip: the previous 7 days, and yesterday up
	// to the same time of day (so a partial today isn't compared to a full day).
	h.db.QueryRow(r.Context(), `SELECT
		(SELECT COUNT(DISTINCT user_id) FROM quiz_attempts
		  WHERE completed_at >= CURRENT_DATE - 14 AND completed_at < CURRENT_DATE - 7),
		(SELECT COUNT(*) FROM quiz_attempts
		  WHERE completed_at >= CURRENT_DATE - 1 AND completed_at < now() - interval '1 day')`,
	).Scan(&activePrevWeek, &attemptsYesterdaySoFar)
	h.db.QueryRow(r.Context(), `SELECT
		(SELECT COUNT(*) FROM reports WHERE status IN ('open','reviewing')),
		(SELECT COUNT(*) FROM reports WHERE status IN ('open','reviewing') AND priority='high'),
		(SELECT MIN(created_at) FROM reports WHERE status IN ('open','reviewing')),
		(SELECT COUNT(*) FROM contact_submissions WHERE status='new'),
		(SELECT COUNT(*) FROM institutions WHERE status='pending' AND deleted_at IS NULL AND created_at < now() - interval '48 hours'),
		(SELECT MIN(created_at) FROM institutions WHERE status='pending' AND deleted_at IS NULL),
		(SELECT MIN(COALESCE(updated_at, created_at)) FROM quizzes WHERE status='pending_approval' AND deleted_at IS NULL),
		(SELECT COALESCE(SUM(amount),0) FROM points_ledger WHERE amount > 0 AND expires_at > now() AND expires_at <= now() + interval '14 days'),
		(SELECT COUNT(DISTINCT user_id) FROM points_ledger WHERE amount > 0 AND expires_at > now() AND expires_at <= now() + interval '14 days')`,
	).Scan(&reportsOpen, &reportsHigh, &oldestReport, &contactNew, &instOver48h, &oldestInst, &oldestQuiz,
		&expiring14d, &expiringLearners)

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"reports":           map[string]interface{}{"open": reportsOpen, "high": reportsHigh, "oldest_at": oldestReport},
		"contact_new":       contactNew,
		"queue_age":         map[string]interface{}{"institutions_over_48h": instOver48h, "oldest_institution_at": oldestInst, "oldest_quiz_at": oldestQuiz},
		"points_expiring":   map[string]interface{}{"points_14d": expiring14d, "learners_14d": expiringLearners},
		"previous":          map[string]int{"active_users_week": activePrevWeek, "attempts_yesterday_so_far": attemptsYesterdaySoFar},
		"total_users":       totalUsers,
		"active_users_week": activeUsers,
		"institutions":      map[string]int{"pending": pendingInst, "verified": verifiedInst, "suspended": suspendedInst},
		"quizzes":           map[string]int{"published": publishedQuizzes, "pending": pendingQuizzes, "reported": reportedQuizzes},
		"attempts_today":    attemptsToday,
		"attempts_week":     attemptsWeek,
		"avg_score_week":    avgScore,
		"points_week":       pointsWeek,
		"points_all_time":   pointsAll,
	})
}

// GET /api/v1/admin/activity-feed
func (h *Handler) ActivityFeed(w http.ResponseWriter, r *http.Request) {
	eventType := r.URL.Query().Get("type")
	limit := 50

	where := ""
	args := []interface{}{}
	if eventType != "" {
		where = `WHERE action_type=$1`
		args = append(args, eventType)
		args = append(args, limit)
	} else {
		args = append(args, limit)
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, timestamp, admin_name, action_type, target_type, target_id
		 FROM audit_log `+where+` ORDER BY timestamp DESC LIMIT $`+strconv.Itoa(len(args)),
		args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type feedItem struct {
		ID         string    `json:"id"`
		Timestamp  time.Time `json:"timestamp"`
		AdminName  string    `json:"admin_name"`
		ActionType string    `json:"action_type"`
		TargetType string    `json:"target_type"`
		TargetID   *string   `json:"target_id,omitempty"`
	}
	var items []feedItem
	for rows.Next() {
		var item feedItem
		rows.Scan(&item.ID, &item.Timestamp, &item.AdminName, &item.ActionType, &item.TargetType, &item.TargetID)
		items = append(items, item)
	}
	if items == nil {
		items = []feedItem{}
	}
	middleware.JSON(w, http.StatusOK, items)
}

// GET /api/v1/admin/institutions
func (h *Handler) ListInstitutions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	offset := (page - 1) * limit

	search := q.Get("search")
	status := q.Get("status")
	instType := q.Get("type")

	where := `deleted_at IS NULL`
	args := []interface{}{}
	n := 1
	if search != "" {
		where += fmt.Sprintf(` AND (name ILIKE $%d OR contact_email ILIKE $%d OR onboarding_city ILIKE $%d)`, n, n, n)
		args = append(args, "%"+search+"%")
		n++
	}
	if v := q.Get("city"); v != "" {
		where += fmt.Sprintf(` AND onboarding_city ILIKE $%d`, n)
		args = append(args, v)
		n++
	}
	if q.Get("non_default_multiplier") == "1" {
		where += ` AND point_multiplier <> 1.0`
	}
	if status != "" {
		where += fmt.Sprintf(` AND status=$%d`, n)
		args = append(args, status)
		n++
	}
	if instType != "" {
		where += fmt.Sprintf(` AND type=$%d`, n)
		args = append(args, instType)
		n++
	}

	var total int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM institutions WHERE `+where, args...).Scan(&total)
	args = append(args, limit, offset)

	rows, err := h.db.Query(r.Context(),
		`SELECT id, name, type, status, contact_email, verified_at, created_at,
			(SELECT COUNT(*) FROM enrollments e
			 WHERE e.institution_id = i.id
			   AND e.status IN ('pending_claim','active','suspended')) AS student_count,
			(SELECT COUNT(*) FROM users u WHERE u.institution_id = i.id AND u.role='teacher') AS teacher_count,
			(SELECT COUNT(*) FROM quizzes q WHERE q.institution_id = i.id AND q.deleted_at IS NULL) AS quiz_count,
			(SELECT COUNT(*) FROM quizzes q WHERE q.institution_id = i.id AND q.deleted_at IS NULL AND q.status='published') AS active_quizzes,
			(SELECT AVG(u.current_streak) FROM users u WHERE u.institution_id = i.id AND u.role='student' AND u.status='active') AS avg_streak,
			onboarding_city, point_multiplier::float8,
			(SELECT MAX(h.created_at) FROM institution_multiplier_history h WHERE h.institution_id = i.id) AS multiplier_set_at
		 FROM institutions i WHERE `+where+
			fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, n, n+1),
		args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type instRow struct {
		ID            string     `json:"id"`
		Name          string     `json:"name"`
		Type          string     `json:"type"`
		Status        string     `json:"status"`
		ContactEmail  string     `json:"contact_email"`
		VerifiedAt    *time.Time `json:"verified_at,omitempty"`
		CreatedAt     time.Time  `json:"created_at"`
		StudentCount  int        `json:"student_count"`
		TeacherCount  int        `json:"teacher_count"`
		QuizCount     int        `json:"quiz_count"`
		ActiveQuizzes int        `json:"active_quizzes"`
		AvgStreak     *float64   `json:"avg_streak"`
		City          *string    `json:"city"`
		Multiplier    float64    `json:"point_multiplier"`
		MultiplierSet *time.Time `json:"multiplier_set_at"`
	}
	var insts []instRow
	for rows.Next() {
		var i instRow
		rows.Scan(&i.ID, &i.Name, &i.Type, &i.Status, &i.ContactEmail, &i.VerifiedAt, &i.CreatedAt,
			&i.StudentCount, &i.TeacherCount, &i.QuizCount, &i.ActiveQuizzes, &i.AvgStreak, &i.City, &i.Multiplier, &i.MultiplierSet)
		insts = append(insts, i)
	}
	if insts == nil {
		insts = []instRow{}
	}
	middleware.JSONWithMeta(w, http.StatusOK, insts, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// GET /api/v1/admin/institutions/queue
func (h *Handler) InstitutionQueue(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(),
		`SELECT id, name, type, contact_email, created_at,
		        onboarding_admin_name, onboarding_phone, onboarding_website,
		        onboarding_city, onboarding_state, onboarding_country, timezone
		   FROM institutions WHERE status='pending' AND deleted_at IS NULL ORDER BY created_at ASC`)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type qRow struct {
		ID           string    `json:"id"`
		Name         string    `json:"name"`
		Type         string    `json:"type"`
		ContactEmail string    `json:"contact_email"`
		SubmittedAt  time.Time `json:"submitted_at"`
		ContactName  *string   `json:"contact_name"`
		Phone        *string   `json:"phone"`
		Website      *string   `json:"website"`
		City         *string   `json:"city"`
		State        *string   `json:"state"`
		Country      *string   `json:"country"`
		Timezone     string    `json:"timezone"`
	}
	var queue []qRow
	for rows.Next() {
		var i qRow
		rows.Scan(&i.ID, &i.Name, &i.Type, &i.ContactEmail, &i.SubmittedAt,
			&i.ContactName, &i.Phone, &i.Website, &i.City, &i.State, &i.Country, &i.Timezone)
		queue = append(queue, i)
	}
	if queue == nil {
		queue = []qRow{}
	}
	middleware.JSON(w, http.StatusOK, queue)
}

// POST /api/v1/admin/institutions/:institutionId/approve
func (h *Handler) ApproveInstitution(w http.ResponseWriter, r *http.Request) {
	instID := chi.URLParam(r, "institutionId")
	adminID := middleware.GetAdminID(r)

	sCode := "S" + uuid.New().String()[:7]
	tCode := "T" + uuid.New().String()[:7]

	// verified_by is a nullable FK to admin_accounts.id. A super_admin resolved
	// via the users table (not admin_accounts) has an empty GetAdminID; pass NULL
	// rather than "" which fails uuid parsing (22P02), and rather than the users.id
	// which would violate the admin_accounts FK (23503).
	var verifiedBy *string
	if adminID != "" {
		verifiedBy = &adminID
	}

	_, err := h.db.Exec(r.Context(),
		`UPDATE institutions SET status='verified', verified_at=now(), verified_by=$1,
		 student_referral_code=$2, teacher_referral_code=$3, updated_at=now()
		 WHERE id=$4 AND status='pending'`,
		verifiedBy, sCode, tCode, instID)
	if err != nil {
		middleware.InternalError(w)
		return
	}

	logAudit(r.Context(), h.db, adminID, "approve_institution", "institution", instID, "")

	// Provision the institution admin + send the login email in the same step, so
	// approval alone delivers credentials (the separate Provision-admin button is
	// disabled in the dashboard). Best-effort: approval already succeeded, so a
	// provisioning failure is reported in the response, not rolled back.
	admin, provErr := h.provisionInstitutionAdmin(r.Context(), instID, "", "")
	resp := map[string]interface{}{
		"message":               "institution approved",
		"student_referral_code": sCode,
		"teacher_referral_code": tCode,
	}
	if provErr != nil {
		fmt.Printf("[admin] auto-provision on approve failed for %s: %v\n", instID, provErr)
		resp["admin_provisioned"] = false
		resp["admin_error"] = "credentials email could not be sent; use resend-credentials"
	} else {
		resp["admin_provisioned"] = true
		resp["admin_email"] = admin.AdminEmail
	}
	middleware.JSON(w, http.StatusOK, resp)
}

// POST /api/v1/admin/institutions/:institutionId/reject
func (h *Handler) RejectInstitution(w http.ResponseWriter, r *http.Request) {
	instID := chi.URLParam(r, "institutionId")
	var req struct {
		Reason string `json:"reason"`
	}
	jsonx.NewDecoder(r.Body).Decode(&req)
	h.db.Exec(r.Context(), `DELETE FROM institutions WHERE id=$1 AND status='pending'`, instID)
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "reject_institution", "institution", instID, req.Reason)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "institution rejected"})
}

// GET /api/v1/admin/institutions/:institutionId
func (h *Handler) GetInstitution(w http.ResponseWriter, r *http.Request) {
	instID := chi.URLParam(r, "institutionId")
	var name, instType, status, email, sCode, tCode, tz string
	var verifiedAt *time.Time
	var createdAt time.Time
	var multiplier float64
	var contactName, phone, website, city, state, country *string
	if err := h.db.QueryRow(r.Context(),
		`SELECT name, type, status, contact_email, student_referral_code, teacher_referral_code, verified_at,
		        created_at, point_multiplier::float8, timezone,
		        onboarding_admin_name, onboarding_phone, onboarding_website,
		        onboarding_city, onboarding_state, onboarding_country
		 FROM institutions WHERE id=$1 AND deleted_at IS NULL`, instID,
	).Scan(&name, &instType, &status, &email, &sCode, &tCode, &verifiedAt, &createdAt, &multiplier, &tz,
		&contactName, &phone, &website, &city, &state, &country); err != nil {
		middleware.NotFound(w, "institution")
		return
	}

	var studentCount, teacherCount, quizCount, activeQuizzes, sJoined, tJoined int
	var avgStreak *float64
	h.db.QueryRow(r.Context(), `SELECT
		(SELECT COUNT(*) FROM users WHERE institution_id=$1 AND role='student' AND deleted_at IS NULL),
		(SELECT COUNT(*) FROM users WHERE institution_id=$1 AND role='teacher' AND deleted_at IS NULL),
		(SELECT COUNT(*) FROM quizzes WHERE institution_id=$1 AND deleted_at IS NULL),
		(SELECT COUNT(*) FROM quizzes WHERE institution_id=$1 AND deleted_at IS NULL AND status='published'),
		(SELECT AVG(current_streak) FROM users WHERE institution_id=$1 AND role='student' AND status='active'),
		(SELECT COUNT(*) FROM users WHERE institution_id=$1 AND role='student'),
		(SELECT COUNT(*) FROM users WHERE institution_id=$1 AND role='teacher')`, instID,
	).Scan(&studentCount, &teacherCount, &quizCount, &activeQuizzes, &avgStreak, &sJoined, &tJoined)

	// The primary institution admin, if provisioned: users row with role
	// institution_admin. last_active_at set means they have signed in.
	var admin map[string]interface{}
	var aName, aEmail, aStatus string
	var aCreated time.Time
	var aActive *time.Time
	if h.db.QueryRow(r.Context(),
		`SELECT display_name, email, status, created_at, last_active_at FROM users
		  WHERE institution_id=$1 AND role='institution_admin' AND deleted_at IS NULL
		  ORDER BY created_at ASC LIMIT 1`, instID,
	).Scan(&aName, &aEmail, &aStatus, &aCreated, &aActive) == nil {
		admin = map[string]interface{}{
			"name": aName, "email": aEmail, "status": aStatus,
			"invited_at": aCreated, "last_active_at": aActive, "accepted": aActive != nil,
		}
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"id": instID, "name": name, "type": instType, "status": status,
		"contact_email": email, "student_referral_code": sCode, "teacher_referral_code": tCode,
		"verified_at": verifiedAt, "created_at": createdAt, "timezone": tz,
		"student_count": studentCount, "teacher_count": teacherCount, "quiz_count": quizCount,
		"active_quizzes": activeQuizzes, "avg_streak": avgStreak,
		"students_joined": sJoined, "teachers_joined": tJoined,
		"point_multiplier": multiplier,
		"contact_name":     contactName, "phone": phone, "website": website,
		"city": city, "state": state, "country": country,
		"primary_admin": admin,
	})
}

// POST /api/v1/admin/institutions/:institutionId/suspend
func (h *Handler) SuspendInstitution(w http.ResponseWriter, r *http.Request) {
	instID := chi.URLParam(r, "institutionId")
	var req struct {
		Reason string `json:"reason"`
	}
	jsonx.NewDecoder(r.Body).Decode(&req)
	h.db.Exec(r.Context(), `UPDATE institutions SET status='suspended', updated_at=now() WHERE id=$1`, instID)
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "suspend_institution", "institution", instID, req.Reason)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "institution suspended"})
}

// POST /api/v1/admin/institutions/:institutionId/reactivate
func (h *Handler) ReactivateInstitution(w http.ResponseWriter, r *http.Request) {
	instID := chi.URLParam(r, "institutionId")
	h.db.Exec(r.Context(), `UPDATE institutions SET status='verified', updated_at=now() WHERE id=$1`, instID)
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "reactivate_institution", "institution", instID, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "institution reactivated"})
}

// POST /api/v1/admin/institutions/:institutionId/reset-referral-codes
func (h *Handler) ResetReferralCodes(w http.ResponseWriter, r *http.Request) {
	instID := chi.URLParam(r, "institutionId")
	sCode := "S" + uuid.New().String()[:7]
	tCode := "T" + uuid.New().String()[:7]
	h.db.Exec(r.Context(),
		`UPDATE institutions SET student_referral_code=$1, teacher_referral_code=$2, updated_at=now() WHERE id=$3`,
		sCode, tCode, instID)
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "reset_referral_codes", "institution", instID, "")
	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"student_referral_code": sCode, "teacher_referral_code": tCode,
	})
}

// POST /api/v1/admin/institutions/:institutionId/resend-credentials
// Resets the institution admin's Supabase password to a fresh temporary one and
// emails the institution's contact with the full credentials (admin login + the
// current referral codes). The previous password stops working.
func (h *Handler) ResendInstitutionCredentials(w http.ResponseWriter, r *http.Request) {
	instID := chi.URLParam(r, "institutionId")

	var instName, contactEmail, status, sCode, tCode string
	err := h.db.QueryRow(r.Context(),
		`SELECT name, contact_email, status,
			COALESCE(student_referral_code,''), COALESCE(teacher_referral_code,'')
		 FROM institutions WHERE id=$1 AND deleted_at IS NULL`,
		instID,
	).Scan(&instName, &contactEmail, &status, &sCode, &tCode)
	if err != nil {
		middleware.NotFound(w, "institution")
		return
	}
	if status != "verified" {
		middleware.Error(w, http.StatusUnprocessableEntity, "NOT_VERIFIED",
			"institution must be approved (status=verified) before resending credentials")
		return
	}

	// Locate the provisioned institution admin (holds the Supabase login we reset).
	var adminUID, adminEmail string
	err = h.db.QueryRow(r.Context(),
		`SELECT supabase_uid, email FROM users
		 WHERE institution_id=$1 AND role='institution_admin' AND deleted_at IS NULL
		 ORDER BY created_at LIMIT 1`,
		instID,
	).Scan(&adminUID, &adminEmail)
	if err != nil {
		middleware.Error(w, http.StatusUnprocessableEntity, "NO_ADMIN",
			"no institution admin is provisioned yet; provision an admin before resending credentials")
		return
	}

	// Fresh temporary password (meets Supabase complexity: length + mixed chars).
	tempPassword := "Qw" + uuid.New().String()[:8] + "#7"
	if err := h.invite.SetPassword(r.Context(), adminUID, tempPassword); err != nil {
		fmt.Printf("[admin] resend-credentials password reset failed for %s: %v\n", adminEmail, err)
		middleware.Error(w, http.StatusBadGateway, "PASSWORD_RESET_FAILED",
			"failed to reset the institution admin password; credentials were not sent")
		return
	}

	if h.notif == nil {
		middleware.Error(w, http.StatusServiceUnavailable, "EMAIL_UNAVAILABLE",
			"email service is not configured")
		return
	}
	if err := h.notif.SendInstitutionApproval(r.Context(), contactEmail, instName, adminEmail, tempPassword, sCode, tCode); err != nil {
		fmt.Printf("[admin] resend-credentials email to %s failed: %v\n", contactEmail, err)
		middleware.Error(w, http.StatusBadGateway, "EMAIL_FAILED",
			"password was reset but the credentials email failed to send")
		return
	}

	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "resend_institution_credentials", "institution", instID,
		fmt.Sprintf("admin_email=%s", adminEmail))
	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"message":     "Credentials resent to " + contactEmail,
		"admin_email": adminEmail,
	})
}

// GET /api/v1/admin/users
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	offset := (page - 1) * limit

	args := []interface{}{}
	// Only end-user roles are managed here; platform staff (moderator,
	// support_agent, super_admin) are administered on the Admin Accounts page.
	where := `u.deleted_at IS NULL AND u.role IN ('student','teacher','parent','institution_admin')`
	n := 1
	if s := q.Get("search"); s != "" {
		where += fmt.Sprintf(` AND (u.display_name ILIKE $%d OR u.email ILIKE $%d)`, n, n)
		args = append(args, "%"+s+"%")
		n++
	}
	if v := q.Get("role"); v != "" {
		where += fmt.Sprintf(` AND u.role=$%d`, n)
		args = append(args, v)
		n++
	}
	if v := q.Get("status"); v != "" {
		where += fmt.Sprintf(` AND u.status=$%d`, n)
		args = append(args, v)
		n++
	}
	if v := q.Get("institution_id"); v != "" {
		where += fmt.Sprintf(` AND u.institution_id=$%d`, n)
		args = append(args, v)
		n++
	}
	// The institution-scoped student view must agree with the institute
	// roster, which is enrollment-backed. Keep global user management broad,
	// but exclude stale student accounts from a scoped student roster.
	if q.Get("role") == "student" && q.Get("institution_id") != "" {
		where += ` AND EXISTS (
			SELECT 1 FROM enrollments e
			 WHERE e.user_id=u.id
			   AND e.institution_id=u.institution_id
			   AND e.status IN ('pending_claim','active','suspended')
		)`
	}

	var total int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM users u WHERE `+where, args...).Scan(&total)
	args = append(args, limit, offset)

	rows, _ := h.db.Query(r.Context(),
		`SELECT u.id, u.display_name, u.email, u.role, COALESCE(i.name,'') as inst, u.status, u.last_active_at, u.total_points, u.current_streak
		 FROM users u LEFT JOIN institutions i ON i.id=u.institution_id
		 WHERE `+where+fmt.Sprintf(` ORDER BY u.created_at DESC LIMIT $%d OFFSET $%d`, n, n+1),
		args...)
	defer rows.Close()

	type userRow struct {
		ID            string     `json:"id"`
		DisplayName   string     `json:"display_name"`
		Email         string     `json:"email"`
		Role          string     `json:"role"`
		Institution   string     `json:"institution"`
		Status        string     `json:"status"`
		LastActiveAt  *time.Time `json:"last_active_at,omitempty"`
		TotalPoints   int64      `json:"total_points"`
		CurrentStreak int        `json:"current_streak"`
	}
	var users []userRow
	for rows.Next() {
		var u userRow
		rows.Scan(&u.ID, &u.DisplayName, &u.Email, &u.Role, &u.Institution, &u.Status, &u.LastActiveAt, &u.TotalPoints, &u.CurrentStreak)
		users = append(users, u)
	}
	if users == nil {
		users = []userRow{}
	}
	middleware.JSONWithMeta(w, http.StatusOK, users, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// GET /api/v1/admin/users/:userId
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	var displayName, email, role, status string
	var instName *string
	var totalPoints int64
	var currentStreak, longestStreak int
	var memberSince time.Time
	var lastActive *time.Time
	var instID, suspensionReason *string
	var recruiterVisible bool

	err := h.db.QueryRow(r.Context(),
		`SELECT u.display_name, u.email, u.role, u.status, i.name, u.total_points, u.current_streak, u.member_since, u.last_active_at,
		        u.longest_streak, u.recruiter_visible, u.suspension_reason, u.institution_id::text
		 FROM users u LEFT JOIN institutions i ON i.id=u.institution_id
		 WHERE u.id=$1 AND u.deleted_at IS NULL
		   AND u.role IN ('student','teacher','parent','institution_admin')`, userID,
	).Scan(&displayName, &email, &role, &status, &instName, &totalPoints, &currentStreak, &memberSince, &lastActive,
		&longestStreak, &recruiterVisible, &suspensionReason, &instID)
	if err != nil {
		middleware.NotFound(w, "user")
		return
	}

	// Attempt history (last 10)
	aRows, _ := h.db.Query(r.Context(),
		`SELECT qa.id, q.title, COALESCE(qa.score_pct,0), qa.completed_at FROM quiz_attempts qa
		 JOIN quizzes q ON q.id=qa.quiz_id WHERE qa.user_id=$1 AND qa.status='completed'
		 ORDER BY qa.completed_at DESC LIMIT 10`, userID)
	defer aRows.Close()
	type aRow struct {
		ID          string     `json:"id"`
		QuizTitle   string     `json:"quiz_title"`
		ScorePct    float64    `json:"score_pct"`
		CompletedAt *time.Time `json:"completed_at"`
	}
	var attempts []aRow
	for aRows.Next() {
		var a aRow
		aRows.Scan(&a.ID, &a.QuizTitle, &a.ScorePct, &a.CompletedAt)
		attempts = append(attempts, a)
	}

	// Registered push devices. Tokens themselves are secrets and never leave
	// the server; only the platform, version and dates are shown.
	type dRow struct {
		Platform   string    `json:"platform"`
		AppVersion *string   `json:"app_version"`
		Locale     *string   `json:"locale"`
		CreatedAt  time.Time `json:"registered_at"`
		LastSeen   time.Time `json:"last_seen"`
	}
	devices := []dRow{}
	if dRows, derr := h.db.Query(r.Context(),
		`SELECT platform, app_version, locale, created_at, last_seen FROM device_tokens
		  WHERE user_id=$1 ORDER BY last_seen DESC LIMIT 10`, userID); derr == nil {
		for dRows.Next() {
			var d dRow
			if dRows.Scan(&d.Platform, &d.AppVersion, &d.Locale, &d.CreatedAt, &d.LastSeen) == nil {
				devices = append(devices, d)
			}
		}
		dRows.Close()
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"id": userID, "display_name": displayName, "email": email, "role": role, "status": status,
		"institution": instName, "institution_id": instID, "total_points": totalPoints, "current_streak": currentStreak,
		"longest_streak": longestStreak, "recruiter_visible": recruiterVisible, "suspension_reason": suspensionReason,
		"member_since": memberSince, "last_active_at": lastActive, "recent_attempts": attempts,
		"devices": devices,
	})
}

// PATCH /api/v1/admin/users/:userId/suspend
func (h *Handler) SuspendUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	var req struct {
		Reason string `json:"reason"`
	}
	jsonx.NewDecoder(r.Body).Decode(&req)
	h.db.Exec(r.Context(), `UPDATE users SET status='suspended', suspension_reason=$1, updated_at=now() WHERE id=$2`, req.Reason, userID)
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "suspend_user", "user", userID, req.Reason)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "user suspended"})
}

// PATCH /api/v1/admin/users/:userId/reactivate
func (h *Handler) ReactivateUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	h.db.Exec(r.Context(), `UPDATE users SET status='active', suspension_reason=NULL, updated_at=now() WHERE id=$1`, userID)
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "reactivate_user", "user", userID, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "user reactivated"})
}

// DELETE /api/v1/admin/users/:userId
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	h.db.Exec(r.Context(),
		`UPDATE users SET status='deleted', deleted_at=now(), full_name='[Deleted User]',
		 display_name='[Deleted]', email='deleted-'||id||'@deleted.invalid', updated_at=now() WHERE id=$1`, userID)
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "delete_user", "user", userID, "GDPR delete")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "user deleted"})
}

// POST /api/v1/admin/users/:userId/points
func (h *Handler) AdjustPoints(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	var req struct {
		Amount int64  `json:"amount"`
		Reason string `json:"reason"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || req.Reason == "" {
		middleware.BadRequest(w, "amount and reason are required")
		return
	}

	var currentBalance int64
	h.db.QueryRow(r.Context(), `SELECT total_points FROM users WHERE id=$1`, userID).Scan(&currentBalance)
	newBalance := currentBalance + req.Amount
	if newBalance < 0 {
		newBalance = 0
	}

	h.db.Exec(r.Context(), `UPDATE users SET total_points=$1, updated_at=now() WHERE id=$2`, newBalance, userID)
	h.db.Exec(r.Context(),
		`INSERT INTO points_ledger (user_id, amount, reason, balance_after) VALUES ($1,$2,'manual_adjustment',$3)`,
		userID, req.Amount, newBalance)
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "adjust_points", "user", userID, req.Reason)
	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"new_balance": newBalance, "adjustment": req.Amount,
	})
}

// POST /api/v1/admin/users/:userId/impersonate
func (h *Handler) Impersonate(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	adminID := middleware.GetAdminID(r)
	// impersonation_sessions.admin_id is NOT NULL and FK-constrained to
	// admin_accounts. A super_admin authenticated via the users table has no such
	// row, so the session can't be attributed — reject clearly rather than 500.
	if adminID == "" {
		middleware.Error(w, http.StatusConflict, "NO_ADMIN_ACCOUNT",
			"impersonation requires an admin_accounts profile for the acting admin")
		return
	}
	var sessionID string
	if err := h.db.QueryRow(r.Context(),
		`INSERT INTO impersonation_sessions (admin_id, user_id) VALUES ($1,$2) RETURNING id`, adminID, userID,
	).Scan(&sessionID); err != nil {
		middleware.InternalError(w)
		return
	}
	logAudit(r.Context(), h.db, adminID, "impersonate_user", "user", userID, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"session_id": sessionID, "message": "impersonation session started"})
}

// POST /api/v1/admin/impersonation/:sessionId/end
func (h *Handler) EndImpersonation(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionId")
	h.db.Exec(r.Context(), `UPDATE impersonation_sessions SET ended_at=now() WHERE id=$1`, sessionID)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "impersonation ended"})
}

// GET /api/v1/admin/quizzes/moderation-queue
// GET /api/v1/admin/quizzes?institution_id=&status=&search=&page=&limit=
//
// The console's own quiz list. The console previously read the learner-facing
// /quizzes for this, which answers only what a learner may browse — so drafts,
// rejected and institution-private quizzes were invisible to an admin looking
// at an institution's catalogue.
//
// attempt_count is counted from quiz_attempts rather than read off the quizzes
// row: there is no denormalised counter on the table.
func (h *Handler) ListQuizzes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	offset := (page - 1) * limit

	args := []interface{}{}
	where := `qz.deleted_at IS NULL`
	n := 1
	if v := q.Get("institution_id"); v != "" {
		where += fmt.Sprintf(` AND qz.institution_id=$%d`, n)
		args = append(args, v)
		n++
	}
	if v := q.Get("status"); v != "" {
		where += fmt.Sprintf(` AND qz.status=$%d`, n)
		args = append(args, v)
		n++
	}
	if s := q.Get("search"); s != "" {
		where += fmt.Sprintf(` AND (qz.title ILIKE $%d OR u.display_name ILIKE $%d)`, n, n)
		args = append(args, "%"+s+"%")
		n++
	}

	var total int
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM quizzes qz JOIN users u ON u.id=qz.created_by WHERE `+where,
		args...).Scan(&total)
	args = append(args, limit, offset)

	rows, err := h.db.Query(r.Context(),
		`SELECT qz.id, qz.title, u.display_name, COALESCE(i.name,''), qz.status, qz.visibility,
		        qz.question_count,
		        (SELECT COUNT(*) FROM quiz_attempts a WHERE a.quiz_id=qz.id),
		        qz.published_at, qz.created_at,
		        qz.created_by='00000000-0000-0000-0000-000000000001'
		   FROM quizzes qz
		   JOIN users u ON u.id=qz.created_by
		   LEFT JOIN institutions i ON i.id=qz.institution_id
		  WHERE `+where+fmt.Sprintf(` ORDER BY qz.created_at DESC LIMIT $%d OFFSET $%d`, n, n+1),
		args...)
	if err != nil {
		log.Printf("ListQuizzes: %v", err)
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type quizRow struct {
		ID               string     `json:"id"`
		Title            string     `json:"title"`
		Author           string     `json:"author"`
		Institution      string     `json:"institution"`
		Status           string     `json:"status"`
		Visibility       string     `json:"visibility"`
		QuestionCount    int        `json:"question_count"`
		AttemptCount     int64      `json:"attempt_count"`
		PublishedAt      *time.Time `json:"published_at,omitempty"`
		CreatedAt        time.Time  `json:"created_at"`
		PlatformAuthored bool       `json:"platform_authored"`
	}
	quizzes := []quizRow{}
	for rows.Next() {
		var x quizRow
		rows.Scan(&x.ID, &x.Title, &x.Author, &x.Institution, &x.Status, &x.Visibility,
			&x.QuestionCount, &x.AttemptCount, &x.PublishedAt, &x.CreatedAt, &x.PlatformAuthored)
		quizzes = append(quizzes, x)
	}
	middleware.JSONWithMeta(w, http.StatusOK, quizzes, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

func (h *Handler) ModerationQueue(w http.ResponseWriter, r *http.Request) {
	rows, _ := h.db.Query(r.Context(),
		`SELECT q.id, q.title, u.display_name, i.name, q.question_count, q.created_at
		 FROM quizzes q JOIN users u ON u.id=q.created_by LEFT JOIN institutions i ON i.id=q.institution_id
		 WHERE q.status='pending_approval' AND q.deleted_at IS NULL ORDER BY q.created_at ASC`)
	defer rows.Close()
	type item struct {
		ID            string    `json:"id"`
		Title         string    `json:"title"`
		Teacher       string    `json:"teacher"`
		Institution   string    `json:"institution"`
		QuestionCount int       `json:"question_count"`
		SubmittedAt   time.Time `json:"submitted_at"`
	}
	var queue []item
	for rows.Next() {
		var i item
		rows.Scan(&i.ID, &i.Title, &i.Teacher, &i.Institution, &i.QuestionCount, &i.SubmittedAt)
		queue = append(queue, i)
	}
	if queue == nil {
		queue = []item{}
	}
	middleware.JSON(w, http.StatusOK, queue)
}

// POST /api/v1/admin/quizzes/:quizId/approve
func (h *Handler) ApproveQuiz(w http.ResponseWriter, r *http.Request) {
	quizID := chi.URLParam(r, "quizId")
	adminID := middleware.GetAdminID(r)
	h.db.Exec(r.Context(),
		`UPDATE quizzes SET status='published', published_at=now(), approved_by=$1, approved_at=now(), updated_at=now()
		 WHERE id=$2 AND status='pending_approval'`, nullableAdmin(adminID), quizID)
	logAudit(r.Context(), h.db, adminID, "approve_quiz", "quiz", quizID, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "quiz approved"})
}

// POST /api/v1/admin/quizzes/:quizId/reject
func (h *Handler) RejectQuiz(w http.ResponseWriter, r *http.Request) {
	quizID := chi.URLParam(r, "quizId")
	var req struct {
		Reason string `json:"reason"`
	}
	jsonx.NewDecoder(r.Body).Decode(&req)
	h.db.Exec(r.Context(),
		`UPDATE quizzes SET status='rejected', rejection_reason=$1, updated_at=now() WHERE id=$2`, req.Reason, quizID)
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "reject_quiz", "quiz", quizID, req.Reason)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "quiz rejected"})
}

// POST /api/v1/admin/quizzes/:quizId/unpublish
func (h *Handler) UnpublishQuiz(w http.ResponseWriter, r *http.Request) {
	quizID := chi.URLParam(r, "quizId")
	var req struct {
		Reason string `json:"reason"`
	}
	jsonx.NewDecoder(r.Body).Decode(&req)
	h.db.Exec(r.Context(), `UPDATE quizzes SET status='closed', updated_at=now() WHERE id=$1`, quizID)
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "unpublish_quiz", "quiz", quizID, req.Reason)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "quiz unpublished"})
}

// GET /api/v1/admin/reports
func (h *Handler) ListReports(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	offset := (page - 1) * limit

	where := "1=1"
	args := []interface{}{}
	n := 1
	if v := q.Get("status"); v != "" {
		where += fmt.Sprintf(` AND r.status=$%d`, n)
		args = append(args, v)
		n++
	}
	if v := q.Get("priority"); v != "" {
		where += fmt.Sprintf(` AND r.priority=$%d`, n)
		args = append(args, v)
		n++
	}
	if v := q.Get("reason"); v != "" {
		where += fmt.Sprintf(` AND r.reason=$%d`, n)
		args = append(args, v)
		n++
	}
	if v := q.Get("quiz_id"); v != "" {
		where += fmt.Sprintf(` AND r.quiz_id::text=$%d`, n)
		args = append(args, v)
		n++
	}
	if v := q.Get("search"); v != "" {
		where += fmt.Sprintf(` AND (qz.title ILIKE $%d OR r.description ILIKE $%d)`, n, n)
		args = append(args, "%"+v+"%")
		n++
	}

	var total int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM reports r LEFT JOIN quizzes qz ON qz.id=r.quiz_id WHERE `+where, args...).Scan(&total)
	args = append(args, limit, offset)

	rows, err := h.db.Query(r.Context(),
		`SELECT r.id, u.display_name, COALESCE(qz.title,'') as quiz_title, r.reason, r.status, r.priority, r.created_at,
		        r.description, r.quiz_id::text, r.question_id::text, qn.prompt, qn.position,
		        r.resolution, r.resolution_note, r.resolved_at, COALESCE(qz.status,''),
		        COALESCE(au.display_name,'')
		 FROM reports r JOIN users u ON u.id=r.reporter_id
		 LEFT JOIN quizzes qz ON qz.id=r.quiz_id
		 LEFT JOIN users au ON au.id=qz.created_by
		 LEFT JOIN questions qn ON qn.id=r.question_id
		 WHERE `+where+fmt.Sprintf(` ORDER BY r.priority DESC, r.created_at ASC LIMIT $%d OFFSET $%d`, n, n+1),
		args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type repRow struct {
		ID             string     `json:"id"`
		Reporter       string     `json:"reporter"`
		QuizTitle      string     `json:"quiz_title"`
		Reason         string     `json:"reason"`
		Status         string     `json:"status"`
		Priority       string     `json:"priority"`
		CreatedAt      time.Time  `json:"created_at"`
		Description    *string    `json:"description"`
		QuizID         *string    `json:"quiz_id"`
		QuestionID     *string    `json:"question_id"`
		QuestionPrompt *string    `json:"question_prompt"`
		QuestionNumber *int       `json:"question_number"`
		Resolution     *string    `json:"resolution"`
		ResolutionNote *string    `json:"resolution_note"`
		ResolvedAt     *time.Time `json:"resolved_at"`
		QuizStatus     string     `json:"quiz_status"`
		QuizAuthor     string     `json:"quiz_author"`
	}
	reports := []repRow{}
	for rows.Next() {
		var rr repRow
		if rows.Scan(&rr.ID, &rr.Reporter, &rr.QuizTitle, &rr.Reason, &rr.Status, &rr.Priority, &rr.CreatedAt,
			&rr.Description, &rr.QuizID, &rr.QuestionID, &rr.QuestionPrompt, &rr.QuestionNumber,
			&rr.Resolution, &rr.ResolutionNote, &rr.ResolvedAt, &rr.QuizStatus, &rr.QuizAuthor) == nil {
			reports = append(reports, rr)
		}
	}
	middleware.JSONWithMeta(w, http.StatusOK, reports, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// POST /api/v1/admin/reports/:reportId/resolve
//
// Body: {resolution, note}. resolution is one of no_action | edit_required |
// author_warned | escalated | remove_quiz ("escalate" is accepted as an alias).
// remove_quiz unpublishes the quiz; author_warned records a warning against the
// quiz author in the audit log. A note is required except for remove_quiz.
func (h *Handler) ResolveReport(w http.ResponseWriter, r *http.Request) {
	reportID := chi.URLParam(r, "reportId")
	var req struct {
		Resolution string `json:"resolution"`
		Note       string `json:"note"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if req.Resolution == "escalate" {
		req.Resolution = "escalated"
	}
	switch req.Resolution {
	case "no_action", "edit_required", "author_warned", "escalated", "remove_quiz":
	default:
		middleware.BadRequest(w, "resolution must be no_action, edit_required, author_warned, escalated or remove_quiz")
		return
	}
	note := strings.TrimSpace(req.Note)
	if note == "" && req.Resolution != "remove_quiz" {
		middleware.BadRequest(w, "a resolution note is required")
		return
	}
	adminID := middleware.GetAdminID(r)
	var quizID, authorID *string
	tag, err := h.db.Exec(r.Context(),
		`UPDATE reports SET status='resolved', resolution=$1, resolution_note=NULLIF($2,''), reviewed_by=$3, resolved_at=now()
		  WHERE id=$4`,
		req.Resolution, note, nullableAdmin(adminID), reportID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "report")
		return
	}
	h.db.QueryRow(r.Context(),
		`SELECT r.quiz_id::text, q.created_by::text FROM reports r LEFT JOIN quizzes q ON q.id=r.quiz_id WHERE r.id=$1`,
		reportID).Scan(&quizID, &authorID)

	if req.Resolution == "remove_quiz" && quizID != nil {
		h.db.Exec(r.Context(), `UPDATE quizzes SET status='closed', updated_at=now() WHERE id=$1`, *quizID)
		logAudit(r.Context(), h.db, adminID, "unpublish_quiz", "quiz", *quizID, "report "+reportID+": "+note)
	}
	if req.Resolution == "author_warned" && authorID != nil {
		logAudit(r.Context(), h.db, adminID, "warn_author", "user", *authorID, note)
	}
	logAuditChange(r.Context(), h.db, adminID, "resolve_report", "report", reportID, note,
		map[string]string{"status": "open"}, map[string]string{"status": "resolved", "resolution": req.Resolution})
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "report resolved", "resolution": req.Resolution})
}

// GET /api/v1/admin/point-economy
func (h *Handler) GetPointEconomy(w http.ResponseWriter, r *http.Request) {
	rows, _ := h.db.Query(r.Context(),
		`SELECT key, value, description, updated_at FROM point_economy_config ORDER BY key`)
	defer rows.Close()
	type cfg struct {
		Key         string          `json:"key"`
		Value       json.RawMessage `json:"value"`
		Description *string         `json:"description,omitempty"`
		UpdatedAt   time.Time       `json:"updated_at"`
	}
	var configs []cfg
	for rows.Next() {
		var c cfg
		rows.Scan(&c.Key, &c.Value, &c.Description, &c.UpdatedAt)
		configs = append(configs, c)
	}
	if configs == nil {
		configs = []cfg{}
	}
	middleware.JSON(w, http.StatusOK, configs)
}

// PATCH /api/v1/admin/point-economy/:key
func (h *Handler) UpdatePointEconomy(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	adminID := middleware.GetAdminID(r)

	var req struct {
		Value  json.RawMessage `json:"value"`
		Reason string          `json:"reason"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "value is required")
		return
	}

	// Get old value for audit
	var oldVal json.RawMessage
	h.db.QueryRow(r.Context(), `SELECT value FROM point_economy_config WHERE key=$1`, key).Scan(&oldVal)

	_, err := h.db.Exec(r.Context(),
		`UPDATE point_economy_config SET value=$1, updated_by=$2, updated_at=now() WHERE key=$3`,
		req.Value, nullableAdmin(adminID), key)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	scoring.InvalidateConfigCache()

	// Audit with old/new values and optional reason. audit_log.admin_id is NOT
	// NULL, so only record when the actor maps to an admin_accounts row.
	if adminID != "" {
		h.db.Exec(r.Context(),
			`INSERT INTO audit_log (admin_id, admin_name, admin_role, action_type, target_type, target_id, reason, old_value, new_value)
			 VALUES ($1,(SELECT name FROM admin_accounts WHERE id=$1),(SELECT role FROM admin_accounts WHERE id=$1),'update_point_config','config',$2,$3,$4,$5)`,
			adminID, key, req.Reason, oldVal, req.Value)
	}

	middleware.JSON(w, http.StatusOK, map[string]string{"message": "config updated"})
}

// POST /api/v1/admin/announcements
func (h *Handler) CreateAnnouncement(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title          string     `json:"title"`
		Body           string     `json:"body"`
		CTALabel       *string    `json:"cta_label"`
		CTAURL         *string    `json:"cta_url"`
		DeliveryTypes  []string   `json:"delivery_types"`
		Audience       string     `json:"audience"`
		InstitutionID  *string    `json:"institution_id"`
		InstitutionIDs []string   `json:"institution_ids"`
		ScheduledAt    *time.Time `json:"scheduled_at"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Body) == "" {
		middleware.BadRequest(w, "title and body are required")
		return
	}
	if len(req.Title) > 80 || len(req.Body) > 600 || !validContentURL(req.CTAURL) ||
		!allIn(req.DeliveryTypes, map[string]bool{"in_app_banner": true, "in_app_notification": true, "email": true}) ||
		!map[string]bool{"all": true, "students": true, "teachers": true, "institution": true, "country": true}[req.Audience] {
		middleware.BadRequest(w, "invalid announcement fields")
		return
	}
	if req.Audience == "institution" {
		if len(req.InstitutionIDs) == 0 && req.InstitutionID != nil {
			req.InstitutionIDs = []string{*req.InstitutionID}
		}
		if len(req.InstitutionIDs) == 0 || !validUUIDs(req.InstitutionIDs) {
			middleware.BadRequest(w, "institution targets are required")
			return
		}
		req.InstitutionID = &req.InstitutionIDs[0]
	} else {
		req.InstitutionID = nil
		req.InstitutionIDs = nil
	}
	if req.ScheduledAt != nil && req.ScheduledAt.Before(time.Now()) {
		middleware.BadRequest(w, "scheduled_at must be in the future")
		return
	}
	adminID := middleware.GetAdminID(r)
	role := middleware.GetRole(r)

	status := "scheduled"
	// Moderators can publish in-app directly; email requires super_admin approval
	hasEmail := false
	for _, dt := range req.DeliveryTypes {
		if dt == "email" {
			hasEmail = true
			break
		}
	}
	if role != "super_admin" && hasEmail {
		status = "draft"
	}

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(r.Context())
	var id string
	err = tx.QueryRow(r.Context(),
		`INSERT INTO announcements (title, body, cta_label, cta_url, delivery_types, audience, institution_id, status, scheduled_at, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		strings.TrimSpace(req.Title), strings.TrimSpace(req.Body), req.CTALabel, req.CTAURL, req.DeliveryTypes, req.Audience, req.InstitutionID, status, req.ScheduledAt, nullableAdmin(adminID),
	).Scan(&id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	for _, instID := range req.InstitutionIDs {
		if _, err = tx.Exec(r.Context(), `INSERT INTO announcement_institutions(announcement_id,institution_id) VALUES($1,$2)`, id, instID); err != nil {
			middleware.BadRequest(w, "invalid institution target")
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		middleware.InternalError(w)
		return
	}
	logAudit(r.Context(), h.db, adminID, "create_announcement", "announcement", id, status)
	middleware.JSON(w, http.StatusCreated, map[string]interface{}{"id": id, "title": strings.TrimSpace(req.Title), "body": strings.TrimSpace(req.Body), "cta_label": req.CTALabel, "cta_url": req.CTAURL, "delivery_types": req.DeliveryTypes, "audience": req.Audience, "institution_ids": req.InstitutionIDs, "status": status, "scheduled_at": req.ScheduledAt, "sent_at": nil, "sent_by": "", "reach": 0})
}

// GET /api/v1/admin/audit-log
func (h *Handler) AuditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	where := "1=1"
	args := []interface{}{}
	n := 1
	if v := q.Get("admin_name"); v != "" {
		where += fmt.Sprintf(` AND admin_name ILIKE $%d`, n)
		args = append(args, "%"+v+"%")
		n++
	}
	if v := q.Get("action_type"); v != "" {
		where += fmt.Sprintf(` AND action_type=$%d`, n)
		args = append(args, v)
		n++
	}
	if v := q.Get("target_type"); v != "" {
		where += fmt.Sprintf(` AND target_type=$%d`, n)
		args = append(args, v)
		n++
	}
	if v := q.Get("target_id"); v != "" {
		where += fmt.Sprintf(` AND target_id::text=$%d`, n)
		args = append(args, v)
		n++
	}
	if v := q.Get("from"); v != "" {
		where += fmt.Sprintf(` AND timestamp >= $%d::date`, n)
		args = append(args, v)
		n++
	}
	if v := q.Get("to"); v != "" {
		where += fmt.Sprintf(` AND timestamp < $%d::date + 1`, n)
		args = append(args, v)
		n++
	}

	var total int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM audit_log WHERE `+where, args...).Scan(&total)
	args = append(args, limit, offset)

	rows, _ := h.db.Query(r.Context(),
		`SELECT id, timestamp, admin_name, admin_role, action_type, target_type, target_id, reason, old_value, new_value
		 FROM audit_log WHERE `+where+fmt.Sprintf(` ORDER BY timestamp DESC LIMIT $%d OFFSET $%d`, n, n+1),
		args...)
	defer rows.Close()

	type logEntry struct {
		ID         string          `json:"id"`
		Timestamp  time.Time       `json:"timestamp"`
		AdminName  string          `json:"admin_name"`
		AdminRole  string          `json:"admin_role"`
		ActionType string          `json:"action_type"`
		TargetType string          `json:"target_type"`
		TargetID   *string         `json:"target_id,omitempty"`
		Reason     *string         `json:"reason,omitempty"`
		OldValue   json.RawMessage `json:"old_value,omitempty"`
		NewValue   json.RawMessage `json:"new_value,omitempty"`
	}
	var entries []logEntry
	for rows.Next() {
		var e logEntry
		rows.Scan(&e.ID, &e.Timestamp, &e.AdminName, &e.AdminRole, &e.ActionType, &e.TargetType, &e.TargetID, &e.Reason, &e.OldValue, &e.NewValue)
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []logEntry{}
	}
	middleware.JSONWithMeta(w, http.StatusOK, entries, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// POST /api/v1/admin/admin-accounts
func (h *Handler) CreateAdminAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Email == "" || req.Role == "" {
		middleware.BadRequest(w, "name, email, and role are required")
		return
	}

	// Validate role
	if req.Role != "super_admin" && req.Role != "moderator" && req.Role != "support_agent" {
		middleware.BadRequest(w, "invalid role: must be super_admin, moderator, or support_agent")
		return
	}

	req.Email = auth.NormalizeEmail(req.Email)

	// One address is one Qwish account. Checked before the Supabase invite so a
	// doomed admin account never gets an auth user, and before the
	// admin_accounts lookup below, which only ever saw its own table.
	if taken := auth.EmailIdentityIn(r.Context(), h.db, req.Email); taken != nil {
		middleware.Error(w, http.StatusConflict, "EMAIL_ALREADY_REGISTERED", taken.Human())
		return
	}

	adminID := middleware.GetAdminID(r)
	// created_by is a nullable FK to admin_accounts.id. When the requester is
	// authenticated via the users table (not admin_accounts), GetAdminID is empty;
	// pass NULL rather than "" which fails uuid parsing (22P02).
	var createdBy *string
	if adminID != "" {
		createdBy = &adminID
	}

	// Provision the Supabase auth user via the shared invite client. On failure
	// it returns an error rather than a placeholder UID, so we never create an
	// unauthenticatable orphan admin_accounts row.
	inv, err := h.invite.Invite(r.Context(), req.Email, h.cfg.SuperAdminURL,
		map[string]string{"role": req.Role, "name": req.Name})
	if err != nil {
		fmt.Printf("[admin] Supabase invite failed for %s: %v\n", req.Email, err)
		middleware.Error(w, http.StatusBadGateway, "INVITE_FAILED",
			"failed to create Supabase invite for this email; admin account was not created")
		return
	}
	supabaseUID := inv.UID

	// An email may already exist in admin_accounts from a prior invite or a
	// soft-deleted account (DeleteAdminAccount only flags status='deleted').
	// Revive soft-deleted rows; reject genuine active duplicates with a clear message.
	// A user who already had a Supabase account can authenticate immediately, so
	// mark them active; a fresh invite stays 'pending' until first sign-in.
	initialStatus := "pending"
	var acceptedAt *time.Time
	if inv.AlreadyExisted {
		initialStatus = "active"
		now := time.Now()
		acceptedAt = &now
	}

	var id, existingStatus string
	err = h.db.QueryRow(r.Context(),
		`SELECT id, status FROM admin_accounts WHERE lower(btrim(email))=$1`, req.Email,
	).Scan(&id, &existingStatus)
	switch {
	case err == nil && existingStatus != "deleted":
		middleware.Error(w, http.StatusConflict, "DUPLICATE_EMAIL", "an admin account with this email already exists")
		return
	case err == nil:
		// Revive soft-deleted account as a fresh invite.
		_, err = h.db.Exec(r.Context(),
			`UPDATE admin_accounts SET supabase_uid=$1, name=$2, role=$3, status=$4, accepted_at=$5,
			 deleted_at=NULL, created_by=$6 WHERE id=$7`,
			supabaseUID, req.Name, req.Role, initialStatus, acceptedAt, createdBy, id)
	default:
		err = h.db.QueryRow(r.Context(),
			`INSERT INTO admin_accounts (supabase_uid, name, email, role, status, accepted_at, created_by)
			 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
			supabaseUID, req.Name, req.Email, req.Role, initialStatus, acceptedAt, createdBy,
		).Scan(&id)
	}
	if err != nil {
		fmt.Printf("[admin] DB write failed: %v\n", err)
		middleware.Error(w, http.StatusConflict, "DB_ERROR", fmt.Sprintf("failed to create admin account record: %v", err))
		return
	}

	// Send email via Resend. A delivery failure on a fresh invite flips the row
	// to 'invite_failed' so the dashboard can surface it and offer a resend.
	var mailErr error
	if h.notif != nil {
		if inv.ActionLink != "" {
			mailErr = h.notif.SendAdminInvite(r.Context(), req.Email, req.Name, req.Role, inv.ActionLink)
		} else if inv.AlreadyExisted {
			mailErr = h.notif.SendAdminWelcome(r.Context(), req.Email, req.Name, req.Role)
		}
		if mailErr != nil {
			fmt.Printf("[admin] invite email to %s failed: %v\n", req.Email, mailErr)
		}
	}
	status := initialStatus
	if mailErr != nil && initialStatus == "pending" {
		status = "invite_failed"
		h.db.Exec(r.Context(), `UPDATE admin_accounts SET status='invite_failed' WHERE id=$1`, id)
	}

	logAudit(r.Context(), h.db, adminID, "create_admin_account", "admin", id, "")
	msg := "admin account created, invite sent"
	if status == "invite_failed" {
		msg = "admin account created, but the invite email failed to send"
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": id, "status": status, "message": msg})
}

// GET /api/v1/admin/admin-accounts
func (h *Handler) ListAdminAccounts(w http.ResponseWriter, r *http.Request) {
	rows, _ := h.db.Query(r.Context(),
		`SELECT a.id, a.name, a.email, a.role, a.status, a.created_at, a.accepted_at,
		        (SELECT COUNT(*) FROM webauthn_credentials c WHERE c.admin_id=a.id),
		        (SELECT MAX(s.last_seen) FROM admin_sessions s WHERE s.admin_id=a.id)
		   FROM admin_accounts a WHERE a.deleted_at IS NULL ORDER BY a.created_at DESC`)
	defer rows.Close()
	type aRow struct {
		ID         string     `json:"id"`
		Name       string     `json:"name"`
		Email      string     `json:"email"`
		Role       string     `json:"role"`
		Status     string     `json:"status"`
		CreatedAt  time.Time  `json:"created_at"`
		AcceptedAt *time.Time `json:"accepted_at,omitempty"`
		Passkeys   int        `json:"passkey_count"`
		LastSeen   *time.Time `json:"last_seen_at"`
	}
	var accounts []aRow
	for rows.Next() {
		var a aRow
		rows.Scan(&a.ID, &a.Name, &a.Email, &a.Role, &a.Status, &a.CreatedAt, &a.AcceptedAt, &a.Passkeys, &a.LastSeen)
		accounts = append(accounts, a)
	}
	if accounts == nil {
		accounts = []aRow{}
	}
	middleware.JSON(w, http.StatusOK, accounts)
}

// PATCH /api/v1/admin/admin-accounts/:adminId
func (h *Handler) UpdateAdminAccount(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "adminId")
	requestorID := middleware.GetAdminID(r)
	if targetID == requestorID {
		middleware.BadRequest(w, "cannot modify your own account")
		return
	}
	var req struct {
		Role   *string `json:"role"`
		Status *string `json:"status"`
		Reason string  `json:"reason"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || (req.Role == nil && req.Status == nil) {
		middleware.BadRequest(w, "role or status is required")
		return
	}
	if req.Role != nil && *req.Role != "super_admin" && *req.Role != "moderator" && *req.Role != "support_agent" {
		middleware.BadRequest(w, "role must be super_admin, moderator or support_agent")
		return
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "suspended" {
		middleware.BadRequest(w, "status must be active or suspended")
		return
	}
	var oldRole, oldStatus string
	if err := h.db.QueryRow(r.Context(),
		`SELECT role, status FROM admin_accounts WHERE id=$1 AND deleted_at IS NULL`, targetID).Scan(&oldRole, &oldStatus); err != nil {
		middleware.NotFound(w, "admin account")
		return
	}
	removesSuper := oldRole == "super_admin" && oldStatus == "active" &&
		((req.Role != nil && *req.Role != "super_admin") || (req.Status != nil && *req.Status != "active"))
	if removesSuper && h.lastActiveSuperAdmin(r.Context(), targetID) {
		middleware.Error(w, http.StatusConflict, "LAST_SUPER_ADMIN",
			"this is the last active super_admin; promote another admin first")
		return
	}
	if req.Role != nil {
		if _, err := h.db.Exec(r.Context(), `UPDATE admin_accounts SET role=$1 WHERE id=$2`, *req.Role, targetID); err != nil {
			middleware.InternalError(w)
			return
		}
	}
	if req.Status != nil {
		if _, err := h.db.Exec(r.Context(), `UPDATE admin_accounts SET status=$1 WHERE id=$2`, *req.Status, targetID); err != nil {
			middleware.InternalError(w)
			return
		}
	}
	action := "update_admin_account"
	switch {
	case req.Role != nil:
		action = "change_admin_role"
	case req.Status != nil && *req.Status == "suspended":
		action = "suspend_admin_account"
	case req.Status != nil:
		action = "reactivate_admin_account"
	}
	logAuditChange(r.Context(), h.db, requestorID, action, "admin", targetID, req.Reason,
		map[string]string{"role": oldRole, "status": oldStatus},
		map[string]interface{}{"role": req.Role, "status": req.Status})
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "admin account updated"})
}

// DELETE /api/v1/admin/admin-accounts/:adminId
func (h *Handler) DeleteAdminAccount(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "adminId")
	requestorID := middleware.GetAdminID(r)
	if targetID == requestorID {
		middleware.BadRequest(w, "cannot delete your own account")
		return
	}
	var role, status string
	if err := h.db.QueryRow(r.Context(),
		`SELECT role, status FROM admin_accounts WHERE id=$1 AND deleted_at IS NULL`, targetID).Scan(&role, &status); err != nil {
		middleware.NotFound(w, "admin account")
		return
	}
	if role == "super_admin" && status == "active" && h.lastActiveSuperAdmin(r.Context(), targetID) {
		middleware.Error(w, http.StatusConflict, "LAST_SUPER_ADMIN",
			"this is the last active super_admin; promote another admin first")
		return
	}
	h.db.Exec(r.Context(), `UPDATE admin_accounts SET status='deleted', deleted_at=now() WHERE id=$1`, targetID)
	h.db.Exec(r.Context(), `UPDATE admin_sessions SET revoked_at=now() WHERE admin_id=$1 AND revoked_at IS NULL`, targetID)
	logAudit(r.Context(), h.db, requestorID, "delete_admin_account", "admin", targetID, r.URL.Query().Get("reason"))
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "admin account deleted"})
}

// lastActiveSuperAdmin reports whether excluding targetID leaves no active super_admin.
func (h *Handler) lastActiveSuperAdmin(ctx context.Context, targetID string) bool {
	var others int
	h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM admin_accounts
		  WHERE role='super_admin' AND status='active' AND deleted_at IS NULL AND id<>$1`, targetID).Scan(&others)
	return others == 0
}

// POST /api/v1/admin/admin-accounts/:adminId/resend
// Re-issues the Supabase invite + email for a pending or failed admin invite.
func (h *Handler) ResendAdminInvite(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "adminId")

	var name, email, role, status string
	err := h.db.QueryRow(r.Context(),
		`SELECT name, email, role, status FROM admin_accounts WHERE id=$1 AND deleted_at IS NULL`,
		targetID,
	).Scan(&name, &email, &role, &status)
	if err != nil {
		middleware.NotFound(w, "admin account")
		return
	}
	if status != "pending" && status != "invite_failed" {
		middleware.BadRequest(w, "only pending or failed invites can be resent")
		return
	}

	inv, err := h.invite.Invite(r.Context(), email, h.cfg.SuperAdminURL,
		map[string]string{"role": role, "name": name})
	if err != nil {
		h.db.Exec(r.Context(), `UPDATE admin_accounts SET status='invite_failed' WHERE id=$1`, targetID)
		middleware.Error(w, http.StatusBadGateway, "INVITE_FAILED", "failed to create Supabase invite for this email")
		return
	}
	// Keep supabase_uid in sync in case Supabase issued a fresh identity.
	h.db.Exec(r.Context(), `UPDATE admin_accounts SET supabase_uid=$1, status='pending' WHERE id=$2`, inv.UID, targetID)

	var mailErr error
	if h.notif != nil {
		if inv.ActionLink != "" {
			mailErr = h.notif.SendAdminInvite(r.Context(), email, name, role, inv.ActionLink)
		} else if inv.AlreadyExisted {
			mailErr = h.notif.SendAdminWelcome(r.Context(), email, name, role)
		}
	}
	newStatus := "pending"
	if mailErr != nil {
		newStatus = "invite_failed"
		h.db.Exec(r.Context(), `UPDATE admin_accounts SET status='invite_failed' WHERE id=$1`, targetID)
		fmt.Printf("[admin] resend invite email to %s failed: %v\n", email, mailErr)
	}

	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "resend_admin_invite", "admin", targetID, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"status": newStatus, "message": "invite resent"})
}

// POST /api/v1/admin/users/:userId/reset-password
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")

	var email, supabaseUID string
	err := h.db.QueryRow(r.Context(),
		`SELECT email, supabase_uid FROM users WHERE id=$1 AND deleted_at IS NULL`, userID,
	).Scan(&email, &supabaseUID)
	if err != nil {
		middleware.NotFound(w, "user")
		return
	}

	body, _ := json.Marshal(map[string]string{"type": "recovery", "email": email})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		h.cfg.SupabaseURL+"/auth/v1/admin/generate_link", bytes.NewReader(body))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", h.cfg.SupabaseServiceKey)
	req.Header.Set("Authorization", "Bearer "+h.cfg.SupabaseServiceKey)

	resp, err := httpx.Client.Do(req)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		middleware.JSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("supabase error: %s", string(raw))})
		return
	}

	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "reset_password", "user", userID, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "password reset email sent"})
}

// POST /api/v1/admin/quizzes/:quizId/request-edits
func (h *Handler) RequestEdits(w http.ResponseWriter, r *http.Request) {
	quizID := chi.URLParam(r, "quizId")
	var req struct {
		Feedback string `json:"feedback"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || req.Feedback == "" {
		middleware.BadRequest(w, "feedback is required")
		return
	}

	tag, err := h.db.Exec(r.Context(),
		`UPDATE quizzes SET status='needs_edits', edit_feedback=$1, updated_at=now()
		 WHERE id=$2 AND status='pending_approval' AND deleted_at IS NULL`,
		req.Feedback, quizID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "quiz (must be pending_approval)")
		return
	}

	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "request_quiz_edits", "quiz", quizID, req.Feedback)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "edit request sent to teacher"})
}

// GET /api/v1/admin/announcements
func (h *Handler) ListAnnouncements(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	offset := (page - 1) * limit

	where := "1=1"
	args := []interface{}{}
	n := 1
	if s := q.Get("status"); s != "" {
		where += fmt.Sprintf(` AND status=$%d`, n)
		args = append(args, s)
		n++
	}

	var total int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM announcements WHERE `+where, args...).Scan(&total)
	args = append(args, limit, offset)

	// cta_*, the author and the reach estimate are stored on create but were
	// never selected here, so the console rendered them as blank.
	rows, err := h.db.Query(r.Context(),
		`SELECT a.id, a.title, a.body, a.cta_label, a.cta_url, a.delivery_types, a.audience,
		        a.status, a.scheduled_at, a.sent_at, a.created_at,
		        COALESCE(ac.name,''),
		        COALESCE((SELECT COUNT(*) FROM content_delivery_events e WHERE e.content_kind='announcement' AND e.content_id=a.id AND e.event_type='impression'),0),
		        COALESCE((SELECT array_agg(ai.institution_id::text) FROM announcement_institutions ai WHERE ai.announcement_id=a.id), ARRAY[]::text[])
		 FROM announcements a
		 LEFT JOIN admin_accounts ac ON ac.id = a.created_by
		 WHERE `+where+
			fmt.Sprintf(` ORDER BY a.created_at DESC LIMIT $%d OFFSET $%d`, n, n+1),
		args...)
	if err != nil {
		log.Printf("ListAnnouncements: %v", err)
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type ann struct {
		ID             string     `json:"id"`
		Title          string     `json:"title"`
		Body           string     `json:"body"`
		CTALabel       *string    `json:"cta_label,omitempty"`
		CTAURL         *string    `json:"cta_url,omitempty"`
		DeliveryTypes  []string   `json:"delivery_types"`
		Audience       string     `json:"audience"`
		Status         string     `json:"status"`
		ScheduledAt    *time.Time `json:"scheduled_at,omitempty"`
		SentAt         *time.Time `json:"sent_at,omitempty"`
		CreatedAt      time.Time  `json:"created_at"`
		SentBy         string     `json:"sent_by"`
		Reach          int        `json:"reach"`
		InstitutionIDs []string   `json:"institution_ids"`
	}
	var items []ann
	for rows.Next() {
		var a ann
		rows.Scan(&a.ID, &a.Title, &a.Body, &a.CTALabel, &a.CTAURL, &a.DeliveryTypes, &a.Audience,
			&a.Status, &a.ScheduledAt, &a.SentAt, &a.CreatedAt, &a.SentBy, &a.Reach, &a.InstitutionIDs)
		items = append(items, a)
	}
	if items == nil {
		items = []ann{}
	}
	middleware.JSONWithMeta(w, http.StatusOK, items, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// PATCH /api/v1/admin/announcements/:announcementId/retract
func (h *Handler) RetractAnnouncement(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "announcementId")
	if _, err := uuid.Parse(id); err != nil {
		middleware.BadRequest(w, "invalid announcement id")
		return
	}
	tag, err := h.db.Exec(r.Context(),
		`UPDATE announcements SET status='retracted' WHERE id=$1 AND status IN ('scheduled','sent')`, id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "announcement (must be scheduled or sent)")
		return
	}
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "retract_announcement", "announcement", id, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "announcement retracted"})
}

// POST /api/v1/admin/announcements/:announcementId/publish
func (h *Handler) PublishAnnouncement(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "announcementId")
	if _, err := uuid.Parse(id); err != nil {
		middleware.BadRequest(w, "invalid announcement id")
		return
	}
	tag, err := h.db.Exec(r.Context(), `UPDATE announcements SET status='scheduled' WHERE id=$1 AND status='draft'`, id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "draft announcement")
		return
	}
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "publish_announcement", "announcement", id, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "scheduled", "message": "announcement queued"})
}

// GET /api/v1/admin/promos
func (h *Handler) ListPromos(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	offset := (page - 1) * limit

	where := "1=1"
	args := []interface{}{}
	n := 1
	if s := q.Get("status"); s != "" {
		where += fmt.Sprintf(` AND status=$%d`, n)
		args = append(args, s)
		n++
	}

	var total int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM promotional_content WHERE `+where, args...).Scan(&total)
	args = append(args, limit, offset)

	// The author was stored but never selected, so the console rendered
	// "by undefined". There is no impression counter on this table — see
	// the note on the response type.
	rows, err := h.db.Query(r.Context(),
		`SELECT p.id, p.type, p.title, p.body, p.cta_label, p.cta_url, p.image_url, p.audience, p.status,
		        p.starts_at, p.ends_at, p.created_at, COALESCE(ac.name,''),
		        COALESCE((SELECT COUNT(*) FROM content_delivery_events e WHERE e.content_kind='promo' AND e.content_id=p.id AND e.event_type='impression'),0),
		        COALESCE((SELECT array_agg(pi.institution_id::text) FROM promo_institutions pi WHERE pi.promo_id=p.id), ARRAY[]::text[])
		 FROM promotional_content p
		 LEFT JOIN admin_accounts ac ON ac.id = p.created_by
		 WHERE `+where+
			fmt.Sprintf(` ORDER BY p.created_at DESC LIMIT $%d OFFSET $%d`, n, n+1),
		args...)
	if err != nil {
		log.Printf("ListPromos: %v", err)
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	// No Impressions field: nothing records promo impressions yet, and a
	// hardcoded zero dressed as a metric is worse than an absent one. The
	// console treats it as unavailable.
	type promo struct {
		ID             string     `json:"id"`
		Type           string     `json:"placement"`
		Title          string     `json:"title"`
		Body           *string    `json:"body,omitempty"`
		CTALabel       *string    `json:"cta_label,omitempty"`
		CTAURL         *string    `json:"cta_url,omitempty"`
		ImageURL       *string    `json:"image_url,omitempty"`
		Audience       string     `json:"target"`
		Status         string     `json:"status"`
		StartsAt       *time.Time `json:"start_date,omitempty"`
		EndsAt         *time.Time `json:"end_date,omitempty"`
		CreatedAt      time.Time  `json:"created_at"`
		CreatedBy      string     `json:"created_by"`
		Impressions    int64      `json:"impressions"`
		InstitutionIDs []string   `json:"institution_ids"`
	}
	var promos []promo
	for rows.Next() {
		var p promo
		rows.Scan(&p.ID, &p.Type, &p.Title, &p.Body, &p.CTALabel, &p.CTAURL, &p.ImageURL, &p.Audience, &p.Status,
			&p.StartsAt, &p.EndsAt, &p.CreatedAt, &p.CreatedBy, &p.Impressions, &p.InstitutionIDs)
		promos = append(promos, p)
	}
	if promos == nil {
		promos = []promo{}
	}
	middleware.JSONWithMeta(w, http.StatusOK, promos, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// POST /api/v1/admin/promos
func (h *Handler) CreatePromo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title          string     `json:"title"`
		Body           *string    `json:"body"`
		CTALabel       *string    `json:"cta_label"`
		CTAURL         *string    `json:"cta_url"`
		ImageURL       *string    `json:"image_url"`
		Placement      string     `json:"placement"`
		Target         string     `json:"target"`
		StartDate      *time.Time `json:"start_date"`
		EndDate        *time.Time `json:"end_date"`
		InstitutionIDs []string   `json:"institution_ids"`
		Status         string     `json:"status"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Title) == "" || req.Placement == "" || req.Target == "" {
		middleware.BadRequest(w, "title, placement, and target are required")
		return
	}
	if len(req.Title) > 80 || (req.Body != nil && len(*req.Body) > 600) || !validContentURL(req.CTAURL) || !validContentURL(req.ImageURL) ||
		!map[string]bool{"home_banner": true, "quiz_browser_banner": true, "splash_interstitial": true, "achievement_prompt": true}[req.Placement] ||
		!map[string]bool{"all": true, "students": true, "institution": true, "lapsed": true}[req.Target] {
		middleware.BadRequest(w, "invalid promo fields")
		return
	}
	if req.Status == "" {
		req.Status = "active"
	}
	if req.Status != "active" && req.Status != "draft" {
		middleware.BadRequest(w, "status must be active or draft")
		return
	}
	if req.StartDate != nil && req.EndDate != nil && !req.EndDate.After(*req.StartDate) {
		middleware.BadRequest(w, "end_date must be after start_date")
		return
	}
	var instID *string
	if req.Target == "institution" {
		if len(req.InstitutionIDs) == 0 || !validUUIDs(req.InstitutionIDs) {
			middleware.BadRequest(w, "institution targets are required")
			return
		}
		instID = &req.InstitutionIDs[0]
	} else {
		req.InstitutionIDs = nil
	}
	adminID := middleware.GetAdminID(r)
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(r.Context())
	if req.Status == "active" && req.Placement == "home_banner" && (req.StartDate == nil || !req.StartDate.After(time.Now())) {
		if _, err = tx.Exec(r.Context(), `UPDATE promotional_content SET status='inactive' WHERE type='home_banner' AND status='active'`); err != nil {
			middleware.InternalError(w)
			return
		}
	}
	var id string
	err = tx.QueryRow(r.Context(),
		`INSERT INTO promotional_content (title, body, cta_label, cta_url, type, audience, institution_id, status, starts_at, ends_at, created_by, image_url)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
		strings.TrimSpace(req.Title), req.Body, req.CTALabel, req.CTAURL, req.Placement, req.Target, instID, req.Status, req.StartDate, req.EndDate, nullableAdmin(adminID), req.ImageURL,
	).Scan(&id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	for _, targetID := range req.InstitutionIDs {
		if _, err = tx.Exec(r.Context(), `INSERT INTO promo_institutions(promo_id,institution_id) VALUES($1,$2)`, id, targetID); err != nil {
			middleware.BadRequest(w, "invalid institution target")
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		middleware.InternalError(w)
		return
	}
	logAudit(r.Context(), h.db, adminID, "create_promo", "promo", id, "")
	middleware.JSON(w, http.StatusCreated, map[string]interface{}{"id": id, "title": strings.TrimSpace(req.Title), "body": req.Body, "cta_label": req.CTALabel, "cta_url": req.CTAURL, "placement": req.Placement, "target": req.Target, "status": req.Status, "start_date": req.StartDate, "end_date": req.EndDate, "institution_ids": req.InstitutionIDs, "impressions": 0, "created_by": ""})
}

// PATCH /api/v1/admin/promos/:promoId
func (h *Handler) UpdatePromoStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "promoId")
	if _, err := uuid.Parse(id); err != nil {
		middleware.BadRequest(w, "invalid promo id")
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || (req.Status != "active" && req.Status != "inactive" && req.Status != "draft") {
		middleware.BadRequest(w, "status is required")
		return
	}
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(r.Context())
	var placement string
	if err = tx.QueryRow(r.Context(), `SELECT type FROM promotional_content WHERE id=$1 FOR UPDATE`, id).Scan(&placement); err != nil {
		middleware.NotFound(w, "promo")
		return
	}
	if req.Status == "active" && placement == "home_banner" {
		if _, err = tx.Exec(r.Context(), `UPDATE promotional_content SET status='inactive' WHERE type='home_banner' AND status='active' AND id<>$1`, id); err != nil {
			middleware.InternalError(w)
			return
		}
	}
	tag, err := tx.Exec(r.Context(), `UPDATE promotional_content SET status=$1 WHERE id=$2`, req.Status, id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "promo")
		return
	}
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "update_promo_status", "promo", id, req.Status)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "promo updated"})
}

// DELETE /api/v1/admin/promos/:promoId
func (h *Handler) DeletePromo(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "promoId")
	if _, err := uuid.Parse(id); err != nil {
		middleware.BadRequest(w, "invalid promo id")
		return
	}
	tag, err := h.db.Exec(r.Context(), `DELETE FROM promotional_content WHERE id=$1`, id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "promo")
		return
	}
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "delete_promo", "promo", id, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "promo deleted"})
}

// GET /api/v1/admin/brands
func (h *Handler) ListBrands(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	offset := (page - 1) * limit

	where := "1=1"
	args := []interface{}{}
	n := 1
	if s := q.Get("status"); s != "" {
		where += fmt.Sprintf(` AND status=$%d`, n)
		args = append(args, s)
		n++
	}
	if s := q.Get("industry"); s != "" {
		where += fmt.Sprintf(` AND industry=$%d`, n)
		args = append(args, s)
		n++
	}

	var total int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM brands WHERE `+where, args...).Scan(&total)
	args = append(args, limit, offset)

	// joined_at and active_sponsors were expected by the console but never
	// sent. Nothing tracks reward-pool spend anywhere in the schema, so no
	// reward_pool_used is invented here.
	rows, err := h.db.Query(r.Context(),
		`SELECT b.id, b.name, b.industry, b.contact_email, b.website, b.reward_pool, b.status,
		        b.created_at,
		        (SELECT COUNT(*) FROM sponsorship_requests s
		          WHERE s.brand_id = b.id AND s.status = 'approved')
		 FROM brands b WHERE `+where+
			fmt.Sprintf(` ORDER BY b.created_at DESC LIMIT $%d OFFSET $%d`, n, n+1),
		args...)
	if err != nil {
		log.Printf("ListBrands: %v", err)
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type brand struct {
		ID           string    `json:"id"`
		Name         string    `json:"name"`
		Industry     *string   `json:"industry,omitempty"`
		ContactEmail *string   `json:"contact_email,omitempty"`
		Website      *string   `json:"website,omitempty"`
		RewardPool   float64   `json:"reward_pool"`
		Status       string    `json:"status"`
		CreatedAt    time.Time `json:"created_at"`
		// Alias of CreatedAt: the console labels this "Joined".
		JoinedAt       time.Time `json:"joined_at"`
		ActiveSponsors int       `json:"active_sponsors"`
	}
	var brands []brand
	for rows.Next() {
		var b brand
		rows.Scan(&b.ID, &b.Name, &b.Industry, &b.ContactEmail, &b.Website, &b.RewardPool, &b.Status,
			&b.CreatedAt, &b.ActiveSponsors)
		b.JoinedAt = b.CreatedAt
		brands = append(brands, b)
	}
	if brands == nil {
		brands = []brand{}
	}
	middleware.JSONWithMeta(w, http.StatusOK, brands, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// POST /api/v1/admin/brands
func (h *Handler) CreateBrand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string  `json:"name"`
		Industry     *string `json:"industry"`
		ContactEmail *string `json:"contact_email"`
		Website      *string `json:"website"`
		RewardPool   float64 `json:"reward_pool"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		middleware.BadRequest(w, "name is required")
		return
	}
	adminID := middleware.GetAdminID(r)
	var id string
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO brands (name, industry, contact_email, website, reward_pool, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		req.Name, req.Industry, req.ContactEmail, req.Website, req.RewardPool, nullableAdmin(adminID),
	).Scan(&id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	logAudit(r.Context(), h.db, adminID, "create_brand", "brand", id, "")
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": id, "status": "pending"})
}

// POST /api/v1/admin/brands/:brandId/approve
func (h *Handler) ApproveBrand(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "brandId")
	adminID := middleware.GetAdminID(r)
	tag, err := h.db.Exec(r.Context(),
		`UPDATE brands SET status='active', approved_by=$1, approved_at=now(), updated_at=now()
		 WHERE id=$2 AND status='pending'`, nullableAdmin(adminID), id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "brand (must be pending)")
		return
	}
	logAudit(r.Context(), h.db, adminID, "approve_brand", "brand", id, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "brand approved"})
}

// POST /api/v1/admin/brands/:brandId/suspend
func (h *Handler) SuspendBrand(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "brandId")
	h.db.Exec(r.Context(), `UPDATE brands SET status='suspended', updated_at=now() WHERE id=$1`, id)
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "suspend_brand", "brand", id, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "brand suspended"})
}

// POST /api/v1/admin/brands/:brandId/reactivate
func (h *Handler) ReactivateBrand(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "brandId")
	h.db.Exec(r.Context(), `UPDATE brands SET status='active', updated_at=now() WHERE id=$1`, id)
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "reactivate_brand", "brand", id, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "brand reactivated"})
}

// GET /api/v1/admin/brands/:brandId/sponsorship-requests
func (h *Handler) ListSponsorshipRequests(w http.ResponseWriter, r *http.Request) {
	brandID := chi.URLParam(r, "brandId")
	rows, err := h.db.Query(r.Context(),
		`SELECT sr.id, sr.quiz_id, COALESCE(q.title,'') as quiz_title, sr.status, sr.reason, sr.requested_at, sr.reviewed_at
		 FROM sponsorship_requests sr LEFT JOIN quizzes q ON q.id=sr.quiz_id
		 WHERE sr.brand_id=$1 ORDER BY sr.requested_at DESC`, brandID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type sr struct {
		ID          string     `json:"id"`
		QuizID      *string    `json:"quiz_id,omitempty"`
		QuizTitle   string     `json:"quiz_title"`
		Status      string     `json:"status"`
		Reason      *string    `json:"reason,omitempty"`
		RequestedAt time.Time  `json:"requested_at"`
		ReviewedAt  *time.Time `json:"reviewed_at,omitempty"`
	}
	var items []sr
	for rows.Next() {
		var s sr
		rows.Scan(&s.ID, &s.QuizID, &s.QuizTitle, &s.Status, &s.Reason, &s.RequestedAt, &s.ReviewedAt)
		items = append(items, s)
	}
	if items == nil {
		items = []sr{}
	}
	middleware.JSON(w, http.StatusOK, items)
}

// POST /api/v1/admin/sponsorship-requests/:requestId/approve
func (h *Handler) ApproveSponsorshipRequest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "requestId")
	adminID := middleware.GetAdminID(r)
	tag, err := h.db.Exec(r.Context(),
		`UPDATE sponsorship_requests SET status='approved', reviewed_by=$1, reviewed_at=now()
		 WHERE id=$2 AND status='pending'`, nullableAdmin(adminID), id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "sponsorship request (must be pending)")
		return
	}
	logAudit(r.Context(), h.db, adminID, "approve_sponsorship_request", "sponsorship_request", id, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "sponsorship request approved"})
}

// POST /api/v1/admin/sponsorship-requests/:requestId/reject
func (h *Handler) RejectSponsorshipRequest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "requestId")
	var req struct {
		Reason string `json:"reason"`
	}
	jsonx.NewDecoder(r.Body).Decode(&req)
	adminID := middleware.GetAdminID(r)
	tag, err := h.db.Exec(r.Context(),
		`UPDATE sponsorship_requests SET status='rejected', reason=$1, reviewed_by=$2, reviewed_at=now()
		 WHERE id=$3 AND status='pending'`, req.Reason, nullableAdmin(adminID), id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "sponsorship request (must be pending)")
		return
	}
	logAudit(r.Context(), h.db, adminID, "reject_sponsorship_request", "sponsorship_request", id, req.Reason)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "sponsorship request rejected"})
}

// POST /api/v1/admin/institutions/:institutionId/provision-admin
// provisionResult reports the outcome of provisionInstitutionAdmin.
type provisionResult struct {
	UserID        string
	AdminEmail    string
	AdminName     string
	Institution   string
	AlreadyExists bool // an admin was already provisioned; no email was sent
}

// provisionInstitutionAdmin creates the institution_admin Supabase user + local
// record and sends the login email. Safe to call more than once: if an admin
// already exists it returns it with AlreadyExists=true and sends nothing. The
// override args replace the institution's contact_email / onboarding_admin_name
// when non-empty. Shared by ProvisionAdmin (button) and ApproveInstitution
// (auto-provision on approval).
func (h *Handler) provisionInstitutionAdmin(ctx context.Context, instID, adminNameOverride, adminEmailOverride string) (*provisionResult, error) {
	var instName, contactEmail, onboardingAdminName string
	if err := h.db.QueryRow(ctx,
		`SELECT name, contact_email, COALESCE(onboarding_admin_name,'') FROM institutions WHERE id=$1 AND deleted_at IS NULL`,
		instID,
	).Scan(&instName, &contactEmail, &onboardingAdminName); err != nil {
		return nil, err
	}

	adminEmail := contactEmail
	if adminEmailOverride != "" {
		adminEmail = adminEmailOverride
	}
	adminName := onboardingAdminName
	if adminNameOverride != "" {
		adminName = adminNameOverride
	}
	if adminName == "" {
		adminName = instName + " Admin"
	}

	// Already provisioned? Return the existing admin, send nothing.
	var existingID, existingEmail string
	if err := h.db.QueryRow(ctx,
		`SELECT id, email FROM users WHERE institution_id=$1 AND role='institution_admin' AND deleted_at IS NULL LIMIT 1`,
		instID,
	).Scan(&existingID, &existingEmail); err == nil {
		return &provisionResult{UserID: existingID, AdminEmail: existingEmail, AdminName: adminName, Institution: instName, AlreadyExists: true}, nil
	}

	adminEmail = auth.NormalizeEmail(adminEmail)

	// One address is one Qwish account. Refused here rather than at the INSERT
	// so an institution admin is never given a Supabase auth user for a row the
	// one_identity_per_email trigger will reject.
	if taken := auth.EmailIdentityIn(ctx, h.db, adminEmail); taken != nil {
		return nil, fmt.Errorf("cannot provision %s: %w", adminEmail, taken)
	}

	// Provision the Supabase auth user via the shared invite client — same path
	// internal admin invites use, so UID resolution and duplicate handling match.
	inv, err := h.invite.Invite(ctx, adminEmail, h.cfg.InstituteURL, map[string]string{
		"role":           "institution_admin",
		"institution_id": instID,
		"full_name":      adminName,
	})
	if err != nil {
		return nil, fmt.Errorf("supabase invite failed for %s: %w", adminEmail, err)
	}

	var userID string
	if err := h.db.QueryRow(ctx,
		`INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
		 VALUES ($1,$2,$2,$3,'institution_admin',$4)
		 RETURNING id`,
		inv.UID, adminName, adminEmail, instID,
	).Scan(&userID); err != nil {
		return nil, err
	}

	h.db.Exec(ctx, `INSERT INTO streaks (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, userID)

	// Send the login email via Resend (generate_link does not send mail itself).
	// A fresh invite carries a set-password action link; when Supabase returns no
	// link (email already had an account) we send the welcome so the admin always
	// gets *something* — never a silent no-op, which was the original bug.
	// ponytail: welcome has no set-password link; if a brand-new user ever comes
	// back with an empty ActionLink (Supabase config), reset+SendInstitutionApproval instead.
	if h.notif != nil {
		var mailErr error
		if inv.ActionLink != "" {
			mailErr = h.notif.SendAdminInvite(ctx, adminEmail, adminName, "institution_admin", inv.ActionLink)
		} else {
			mailErr = h.notif.SendAdminWelcome(ctx, adminEmail, adminName, "institution_admin")
		}
		if mailErr != nil {
			fmt.Printf("[admin] institution-admin login email to %s failed: %v\n", adminEmail, mailErr)
		}
	}

	return &provisionResult{UserID: userID, AdminEmail: adminEmail, AdminName: adminName, Institution: instName}, nil
}

// Creates an institution_admin user record and sends a Supabase email invite
// so the institution admin can set up their password and log in to the dashboard.
// Only valid for 'verified' institutions that do not yet have an admin user.
func (h *Handler) ProvisionAdmin(w http.ResponseWriter, r *http.Request) {
	instID := chi.URLParam(r, "institutionId")
	requesterAdminID := middleware.GetAdminID(r)

	var status string
	if err := h.db.QueryRow(r.Context(),
		`SELECT status FROM institutions WHERE id=$1 AND deleted_at IS NULL`, instID,
	).Scan(&status); err != nil {
		middleware.NotFound(w, "institution")
		return
	}
	if status != "verified" {
		middleware.Error(w, http.StatusUnprocessableEntity, "NOT_VERIFIED",
			"institution must be approved (status=verified) before provisioning admin credentials")
		return
	}

	// Parse optional override body
	var req struct {
		AdminName  string `json:"admin_name"`  // overrides onboarding_admin_name if provided
		AdminEmail string `json:"admin_email"` // overrides contact_email if provided
	}
	jsonx.NewDecoder(r.Body).Decode(&req)

	res, err := h.provisionInstitutionAdmin(r.Context(), instID, req.AdminName, req.AdminEmail)
	if err != nil {
		fmt.Printf("[admin] provision institution admin failed for %s: %v\n", instID, err)
		middleware.Error(w, http.StatusBadGateway, "INVITE_FAILED",
			"failed to provision the institution admin; no admin was created")
		return
	}
	if res.AlreadyExists {
		middleware.JSON(w, http.StatusOK, map[string]interface{}{
			"message":        "institution admin already provisioned",
			"user_id":        res.UserID,
			"admin_email":    res.AdminEmail,
			"institution_id": instID,
			"already_exists": true,
		})
		return
	}

	logAudit(r.Context(), h.db, requesterAdminID, "provision_institution_admin", "institution", instID,
		fmt.Sprintf("admin_user_id=%s email=%s", res.UserID, res.AdminEmail))

	middleware.JSON(w, http.StatusCreated, map[string]interface{}{
		"message":        "Institution admin provisioned. An invite email has been sent to " + res.AdminEmail + " with login instructions.",
		"user_id":        res.UserID,
		"admin_email":    res.AdminEmail,
		"admin_name":     res.AdminName,
		"institution_id": instID,
		"institution":    res.Institution,
	})
}

// GET /api/v1/admin/notification-log
// Optional query params: to_email, status (sent|failed), date_from (YYYY-MM-DD), date_to (YYYY-MM-DD)
func (h *Handler) ListNotificationLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	args := []interface{}{}
	where := "1=1"
	n := 1

	if toEmail := q.Get("to_email"); toEmail != "" {
		where += fmt.Sprintf(" AND to_email ILIKE $%d", n)
		args = append(args, "%"+toEmail+"%")
		n++
	}
	if status := q.Get("status"); status == "sent" || status == "failed" {
		where += fmt.Sprintf(" AND status=$%d", n)
		args = append(args, status)
		n++
	}
	if dateFrom := q.Get("date_from"); dateFrom != "" {
		where += fmt.Sprintf(" AND created_at >= $%d", n)
		args = append(args, dateFrom)
		n++
	}
	if dateTo := q.Get("date_to"); dateTo != "" {
		where += fmt.Sprintf(" AND created_at < ($%d::date + INTERVAL '1 day')", n)
		args = append(args, dateTo)
		n++
	}

	var total int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM notification_log WHERE `+where, args...).Scan(&total)

	args = append(args, limit, offset)
	rows, err := h.db.Query(r.Context(),
		`SELECT id, to_email, subject, status, error, reference, created_at
		 FROM notification_log
		 WHERE `+where+
			fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, n, n+1),
		args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type entry struct {
		ID        string    `json:"id"`
		ToEmail   string    `json:"to_email"`
		Subject   string    `json:"subject"`
		Status    string    `json:"status"`
		Error     *string   `json:"error,omitempty"`
		Reference *string   `json:"reference,omitempty"`
		CreatedAt time.Time `json:"created_at"`
	}

	var entries []entry
	for rows.Next() {
		var e entry
		rows.Scan(&e.ID, &e.ToEmail, &e.Subject, &e.Status, &e.Error, &e.Reference, &e.CreatedAt)
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []entry{}
	}
	middleware.JSONWithMeta(w, http.StatusOK, entries, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// logAudit writes an entry to the audit_log table.
// nullableAdmin maps an actor id to a nullable admin_accounts FK value. A
// super_admin/moderator resolved via the users table (not admin_accounts) has an
// empty GetAdminID; persist NULL rather than "" (which fails uuid parsing, 22P02)
// or a users.id (which would violate the admin_accounts FK, 23503).
func nullableAdmin(adminID string) *string {
	if adminID == "" {
		return nil
	}
	return &adminID
}

// logAuditChange is logAudit with before/after values, which the console's
// audit drawer renders as a field-by-field diff.
func logAuditChange(ctx context.Context, db *pgxpool.Pool, adminID, action, targetType, targetID, reason string, before, after interface{}) {
	if adminID == "" {
		return
	}
	oldJSON, _ := json.Marshal(before)
	newJSON, _ := json.Marshal(after)
	var adminName, adminRole string
	db.QueryRow(ctx, `SELECT name, role FROM admin_accounts WHERE id=$1`, adminID).Scan(&adminName, &adminRole)
	db.Exec(ctx,
		`INSERT INTO audit_log (admin_id, admin_name, admin_role, action_type, target_type, target_id, reason, old_value, new_value)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		adminID, adminName, adminRole, action, targetType, nullableAdmin(targetID), reason, string(oldJSON), string(newJSON))
}

func logAudit(ctx context.Context, db *pgxpool.Pool, adminID, action, targetType, targetID, reason string) {
	// audit_log.admin_id is NOT NULL. When the actor isn't an admin_accounts row
	// (e.g. a super_admin on the users table) there's no id to attribute the
	// entry to, so skip rather than fail the insert with 22P02.
	if adminID == "" {
		return
	}
	var adminName, adminRole string
	db.QueryRow(ctx, `SELECT name, role FROM admin_accounts WHERE id=$1`, adminID).Scan(&adminName, &adminRole)
	db.Exec(ctx,
		`INSERT INTO audit_log (admin_id, admin_name, admin_role, action_type, target_type, target_id, reason)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		adminID, adminName, adminRole, action, targetType, targetID, reason)
}
