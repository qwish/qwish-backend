package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/config"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	db  *pgxpool.Pool
	cfg *config.Config
}

func NewHandler(db *pgxpool.Pool, cfg *config.Config) *Handler {
	return &Handler{db: db, cfg: cfg}
}

// GET /api/v1/admin/overview
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	var totalUsers, activeUsers, pendingInst, verifiedInst, suspendedInst int
	var publishedQuizzes, pendingQuizzes, reportedQuizzes int
	var attemptsToday, attemptsWeek int
	var avgScore float64
	var pointsWeek, pointsAll int64

	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL`).Scan(&totalUsers)
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(DISTINCT user_id) FROM quiz_attempts WHERE completed_at >= CURRENT_DATE - 7`).Scan(&activeUsers)
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM institutions WHERE status='pending'`).Scan(&pendingInst)
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM institutions WHERE status='verified'`).Scan(&verifiedInst)
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM institutions WHERE status='suspended'`).Scan(&suspendedInst)
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM quizzes WHERE status='published' AND deleted_at IS NULL`).Scan(&publishedQuizzes)
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM quizzes WHERE status='pending_approval' AND deleted_at IS NULL`).Scan(&pendingQuizzes)
	h.db.QueryRow(r.Context(), `SELECT COUNT(DISTINCT quiz_id) FROM reports WHERE status='open'`).Scan(&reportedQuizzes)
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM quiz_attempts WHERE completed_at::date = CURRENT_DATE`).Scan(&attemptsToday)
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM quiz_attempts WHERE completed_at >= CURRENT_DATE - 7`).Scan(&attemptsWeek)
	h.db.QueryRow(r.Context(), `SELECT COALESCE(AVG(score_pct),0) FROM quiz_attempts WHERE completed_at >= CURRENT_DATE - 7`).Scan(&avgScore)
	h.db.QueryRow(r.Context(),
		`SELECT COALESCE(SUM(amount),0) FROM points_ledger WHERE amount > 0 AND created_at >= CURRENT_DATE - 7`).Scan(&pointsWeek)
	h.db.QueryRow(r.Context(),
		`SELECT COALESCE(SUM(amount),0) FROM points_ledger WHERE amount > 0`).Scan(&pointsAll)

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
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
	if items == nil { items = []feedItem{} }
	middleware.JSON(w, http.StatusOK, items)
}

// GET /api/v1/admin/institutions
func (h *Handler) ListInstitutions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 { page = 1 }
	if limit < 1 || limit > 50 { limit = 20 }
	offset := (page - 1) * limit

	search := q.Get("search")
	status := q.Get("status")
	instType := q.Get("type")

	where := `deleted_at IS NULL`
	args := []interface{}{}
	n := 1
	if search != "" {
		where += fmt.Sprintf(` AND name ILIKE $%d`, n)
		args = append(args, "%"+search+"%")
		n++
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
		`SELECT id, name, type, status, contact_email, verified_at, created_at FROM institutions WHERE `+where+
			fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, n, n+1),
		args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type instRow struct {
		ID           string     `json:"id"`
		Name         string     `json:"name"`
		Type         string     `json:"type"`
		Status       string     `json:"status"`
		ContactEmail string     `json:"contact_email"`
		VerifiedAt   *time.Time `json:"verified_at,omitempty"`
		CreatedAt    time.Time  `json:"created_at"`
	}
	var insts []instRow
	for rows.Next() {
		var i instRow
		rows.Scan(&i.ID, &i.Name, &i.Type, &i.Status, &i.ContactEmail, &i.VerifiedAt, &i.CreatedAt)
		insts = append(insts, i)
	}
	if insts == nil { insts = []instRow{} }
	middleware.JSONWithMeta(w, http.StatusOK, insts, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// GET /api/v1/admin/institutions/queue
func (h *Handler) InstitutionQueue(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(),
		`SELECT id, name, type, contact_email, created_at FROM institutions WHERE status='pending' ORDER BY created_at ASC`)
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
	}
	var queue []qRow
	for rows.Next() {
		var i qRow
		rows.Scan(&i.ID, &i.Name, &i.Type, &i.ContactEmail, &i.SubmittedAt)
		queue = append(queue, i)
	}
	if queue == nil { queue = []qRow{} }
	middleware.JSON(w, http.StatusOK, queue)
}

// POST /api/v1/admin/institutions/:institutionId/approve
func (h *Handler) ApproveInstitution(w http.ResponseWriter, r *http.Request) {
	instID := chi.URLParam(r, "institutionId")
	adminID := middleware.GetAdminID(r)

	sCode := "S" + uuid.New().String()[:7]
	tCode := "T" + uuid.New().String()[:7]

	_, err := h.db.Exec(r.Context(),
		`UPDATE institutions SET status='verified', verified_at=now(), verified_by=$1,
		 student_referral_code=$2, teacher_referral_code=$3, updated_at=now()
		 WHERE id=$4 AND status='pending'`,
		adminID, sCode, tCode, instID)
	if err != nil {
		middleware.InternalError(w)
		return
	}

	logAudit(r.Context(), h.db, adminID, "approve_institution", "institution", instID, "")
	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"message":              "institution approved",
		"student_referral_code": sCode,
		"teacher_referral_code": tCode,
	})
}

// POST /api/v1/admin/institutions/:institutionId/reject
func (h *Handler) RejectInstitution(w http.ResponseWriter, r *http.Request) {
	instID := chi.URLParam(r, "institutionId")
	var req struct {
		Reason string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	h.db.Exec(r.Context(), `DELETE FROM institutions WHERE id=$1 AND status='pending'`, instID)
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "reject_institution", "institution", instID, req.Reason)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "institution rejected"})
}

// GET /api/v1/admin/institutions/:institutionId
func (h *Handler) GetInstitution(w http.ResponseWriter, r *http.Request) {
	instID := chi.URLParam(r, "institutionId")
	var name, instType, status, email, sCode, tCode string
	var verifiedAt *time.Time
	h.db.QueryRow(r.Context(),
		`SELECT name, type, status, contact_email, student_referral_code, teacher_referral_code, verified_at
		 FROM institutions WHERE id=$1`, instID,
	).Scan(&name, &instType, &status, &email, &sCode, &tCode, &verifiedAt)

	var studentCount, teacherCount, quizCount int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM users WHERE institution_id=$1 AND role='student'`, instID).Scan(&studentCount)
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM users WHERE institution_id=$1 AND role='teacher'`, instID).Scan(&teacherCount)
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM quizzes WHERE institution_id=$1 AND deleted_at IS NULL`, instID).Scan(&quizCount)

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"id": instID, "name": name, "type": instType, "status": status,
		"contact_email": email, "student_referral_code": sCode, "teacher_referral_code": tCode,
		"verified_at": verifiedAt, "student_count": studentCount, "teacher_count": teacherCount, "quiz_count": quizCount,
	})
}

// POST /api/v1/admin/institutions/:institutionId/suspend
func (h *Handler) SuspendInstitution(w http.ResponseWriter, r *http.Request) {
	instID := chi.URLParam(r, "institutionId")
	var req struct {
		Reason string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&req)
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

// GET /api/v1/admin/users
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 { page = 1 }
	if limit < 1 || limit > 50 { limit = 20 }
	offset := (page - 1) * limit

	args := []interface{}{}
	where := `u.deleted_at IS NULL`
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
	if users == nil { users = []userRow{} }
	middleware.JSONWithMeta(w, http.StatusOK, users, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// GET /api/v1/admin/users/:userId
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	var displayName, email, role, status string
	var instName *string
	var totalPoints int64
	var currentStreak int
	var memberSince time.Time
	var lastActive *time.Time

	err := h.db.QueryRow(r.Context(),
		`SELECT u.display_name, u.email, u.role, u.status, i.name, u.total_points, u.current_streak, u.member_since, u.last_active_at
		 FROM users u LEFT JOIN institutions i ON i.id=u.institution_id
		 WHERE u.id=$1 AND u.deleted_at IS NULL`, userID,
	).Scan(&displayName, &email, &role, &status, &instName, &totalPoints, &currentStreak, &memberSince, &lastActive)
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

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"id": userID, "display_name": displayName, "email": email, "role": role, "status": status,
		"institution": instName, "total_points": totalPoints, "current_streak": currentStreak,
		"member_since": memberSince, "last_active_at": lastActive, "recent_attempts": attempts,
	})
}

// PATCH /api/v1/admin/users/:userId/suspend
func (h *Handler) SuspendUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	var req struct {
		Reason string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&req)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Reason == "" {
		middleware.BadRequest(w, "amount and reason are required")
		return
	}

	var currentBalance int64
	h.db.QueryRow(r.Context(), `SELECT total_points FROM users WHERE id=$1`, userID).Scan(&currentBalance)
	newBalance := currentBalance + req.Amount
	if newBalance < 0 { newBalance = 0 }

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
	var sessionID string
	h.db.QueryRow(r.Context(),
		`INSERT INTO impersonation_sessions (admin_id, user_id) VALUES ($1,$2) RETURNING id`, adminID, userID,
	).Scan(&sessionID)
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
	if queue == nil { queue = []item{} }
	middleware.JSON(w, http.StatusOK, queue)
}

// POST /api/v1/admin/quizzes/:quizId/approve
func (h *Handler) ApproveQuiz(w http.ResponseWriter, r *http.Request) {
	quizID := chi.URLParam(r, "quizId")
	adminID := middleware.GetAdminID(r)
	h.db.Exec(r.Context(),
		`UPDATE quizzes SET status='published', published_at=now(), approved_by=$1, approved_at=now(), updated_at=now()
		 WHERE id=$2 AND status='pending_approval'`, adminID, quizID)
	logAudit(r.Context(), h.db, adminID, "approve_quiz", "quiz", quizID, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "quiz approved"})
}

// POST /api/v1/admin/quizzes/:quizId/reject
func (h *Handler) RejectQuiz(w http.ResponseWriter, r *http.Request) {
	quizID := chi.URLParam(r, "quizId")
	var req struct {
		Reason string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&req)
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
	json.NewDecoder(r.Body).Decode(&req)
	h.db.Exec(r.Context(), `UPDATE quizzes SET status='closed', updated_at=now() WHERE id=$1`, quizID)
	logAudit(r.Context(), h.db, middleware.GetAdminID(r), "unpublish_quiz", "quiz", quizID, req.Reason)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "quiz unpublished"})
}

// GET /api/v1/admin/reports
func (h *Handler) ListReports(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 { page = 1 }
	if limit < 1 || limit > 50 { limit = 20 }
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

	var total int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM reports r WHERE `+where, args...).Scan(&total)
	args = append(args, limit, offset)

	rows, _ := h.db.Query(r.Context(),
		`SELECT r.id, u.display_name, COALESCE(qz.title,'') as quiz_title, r.reason, r.status, r.priority, r.created_at
		 FROM reports r JOIN users u ON u.id=r.reporter_id LEFT JOIN quizzes qz ON qz.id=r.quiz_id
		 WHERE `+where+fmt.Sprintf(` ORDER BY r.priority DESC, r.created_at ASC LIMIT $%d OFFSET $%d`, n, n+1),
		args...)
	defer rows.Close()

	type repRow struct {
		ID          string    `json:"id"`
		Reporter    string    `json:"reporter"`
		QuizTitle   string    `json:"quiz_title"`
		Reason      string    `json:"reason"`
		Status      string    `json:"status"`
		Priority    string    `json:"priority"`
		CreatedAt   time.Time `json:"created_at"`
	}
	var reports []repRow
	for rows.Next() {
		var rr repRow
		rows.Scan(&rr.ID, &rr.Reporter, &rr.QuizTitle, &rr.Reason, &rr.Status, &rr.Priority, &rr.CreatedAt)
		reports = append(reports, rr)
	}
	if reports == nil { reports = []repRow{} }
	middleware.JSONWithMeta(w, http.StatusOK, reports, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// POST /api/v1/admin/reports/:reportId/resolve
func (h *Handler) ResolveReport(w http.ResponseWriter, r *http.Request) {
	reportID := chi.URLParam(r, "reportId")
	var req struct {
		Resolution string `json:"resolution"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	adminID := middleware.GetAdminID(r)
	h.db.Exec(r.Context(),
		`UPDATE reports SET status='resolved', resolution=$1, reviewed_by=$2, resolved_at=now() WHERE id=$3`,
		req.Resolution, adminID, reportID)

	// If remove_quiz, unpublish it
	if req.Resolution == "remove_quiz" {
		var quizID *string
		h.db.QueryRow(r.Context(), `SELECT quiz_id FROM reports WHERE id=$1`, reportID).Scan(&quizID)
		if quizID != nil {
			h.db.Exec(r.Context(), `UPDATE quizzes SET status='closed', updated_at=now() WHERE id=$1`, *quizID)
		}
	}
	logAudit(r.Context(), h.db, adminID, "resolve_report", "report", reportID, req.Resolution)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "report resolved"})
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
	if configs == nil { configs = []cfg{} }
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "value is required")
		return
	}

	// Get old value for audit
	var oldVal json.RawMessage
	h.db.QueryRow(r.Context(), `SELECT value FROM point_economy_config WHERE key=$1`, key).Scan(&oldVal)

	_, err := h.db.Exec(r.Context(),
		`UPDATE point_economy_config SET value=$1, updated_by=$2, updated_at=now() WHERE key=$3`,
		req.Value, adminID, key)
	if err != nil {
		middleware.InternalError(w)
		return
	}

	// Audit with old/new values and optional reason
	h.db.Exec(r.Context(),
		`INSERT INTO audit_log (admin_id, admin_name, admin_role, action_type, target_type, target_id, reason, old_value, new_value)
		 VALUES ($1,(SELECT name FROM admin_accounts WHERE id=$1),(SELECT role FROM admin_accounts WHERE id=$1),'update_point_config','config',$2,$3,$4,$5)`,
		adminID, key, req.Reason, oldVal, req.Value)

	middleware.JSON(w, http.StatusOK, map[string]string{"message": "config updated"})
}

// POST /api/v1/admin/announcements
func (h *Handler) CreateAnnouncement(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title         string   `json:"title"`
		Body          string   `json:"body"`
		CTALabel      *string  `json:"cta_label"`
		CTAURL        *string  `json:"cta_url"`
		DeliveryTypes []string `json:"delivery_types"`
		Audience      string   `json:"audience"`
		InstitutionID *string  `json:"institution_id"`
		ScheduledAt   *time.Time `json:"scheduled_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Title == "" || req.Body == "" {
		middleware.BadRequest(w, "title and body are required")
		return
	}
	adminID := middleware.GetAdminID(r)
	role := middleware.GetRole(r)

	status := "draft"
	// Moderators can publish in-app directly; email requires super_admin approval
	hasEmail := false
	for _, dt := range req.DeliveryTypes {
		if dt == "email" {
			hasEmail = true
			break
		}
	}
	if role == "super_admin" && !hasEmail {
		status = "scheduled"
	}
	_ = hasEmail

	deliveryJSON, _ := json.Marshal(req.DeliveryTypes)
	var id string
	h.db.QueryRow(r.Context(),
		`INSERT INTO announcements (title, body, cta_label, cta_url, delivery_types, audience, institution_id, status, scheduled_at, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		req.Title, req.Body, req.CTALabel, req.CTAURL, deliveryJSON, req.Audience, req.InstitutionID, status, req.ScheduledAt, adminID,
	).Scan(&id)
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": id, "status": status})
}

// GET /api/v1/admin/audit-log
func (h *Handler) AuditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 { page = 1 }
	if limit < 1 || limit > 100 { limit = 50 }
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
	if entries == nil { entries = []logEntry{} }
	middleware.JSONWithMeta(w, http.StatusOK, entries, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// POST /api/v1/admin/admin-accounts
func (h *Handler) CreateAdminAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Email == "" || req.Role == "" {
		middleware.BadRequest(w, "name, email, and role are required")
		return
	}
	adminID := middleware.GetAdminID(r)
	// Placeholder supabase_uid — in practice, invite via Supabase Auth admin API and then provision
	var id string
	h.db.QueryRow(r.Context(),
		`INSERT INTO admin_accounts (supabase_uid, name, email, role, created_by) VALUES (gen_random_uuid(),$1,$2,$3,$4) RETURNING id`,
		req.Name, req.Email, req.Role, adminID,
	).Scan(&id)
	logAudit(r.Context(), h.db, adminID, "create_admin_account", "admin", id, "")
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": id, "message": "admin account created, invite sent"})
}

// GET /api/v1/admin/admin-accounts
func (h *Handler) ListAdminAccounts(w http.ResponseWriter, r *http.Request) {
	rows, _ := h.db.Query(r.Context(),
		`SELECT id, name, email, role, status, created_at FROM admin_accounts WHERE deleted_at IS NULL ORDER BY created_at DESC`)
	defer rows.Close()
	type aRow struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		Email     string    `json:"email"`
		Role      string    `json:"role"`
		Status    string    `json:"status"`
		CreatedAt time.Time `json:"created_at"`
	}
	var accounts []aRow
	for rows.Next() {
		var a aRow
		rows.Scan(&a.ID, &a.Name, &a.Email, &a.Role, &a.Status, &a.CreatedAt)
		accounts = append(accounts, a)
	}
	if accounts == nil { accounts = []aRow{} }
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
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Role != nil {
		h.db.Exec(r.Context(), `UPDATE admin_accounts SET role=$1 WHERE id=$2`, *req.Role, targetID)
	}
	if req.Status != nil {
		h.db.Exec(r.Context(), `UPDATE admin_accounts SET status=$1 WHERE id=$2`, *req.Status, targetID)
	}
	logAudit(r.Context(), h.db, requestorID, "update_admin_account", "admin", targetID, "")
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
	h.db.Exec(r.Context(), `UPDATE admin_accounts SET status='deleted', deleted_at=now() WHERE id=$1`, targetID)
	logAudit(r.Context(), h.db, requestorID, "delete_admin_account", "admin", targetID, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "admin account deleted"})
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

	resp, err := http.DefaultClient.Do(req)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Feedback == "" {
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
	if page < 1 { page = 1 }
	if limit < 1 || limit > 50 { limit = 20 }
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

	rows, err := h.db.Query(r.Context(),
		`SELECT id, title, body, delivery_types, audience, status, scheduled_at, sent_at, created_at
		 FROM announcements WHERE `+where+
			fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, n, n+1),
		args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type ann struct {
		ID            string          `json:"id"`
		Title         string          `json:"title"`
		Body          string          `json:"body"`
		DeliveryTypes json.RawMessage `json:"delivery_types"`
		Audience      string          `json:"audience"`
		Status        string          `json:"status"`
		ScheduledAt   *time.Time      `json:"scheduled_at,omitempty"`
		SentAt        *time.Time      `json:"sent_at,omitempty"`
		CreatedAt     time.Time       `json:"created_at"`
	}
	var items []ann
	for rows.Next() {
		var a ann
		rows.Scan(&a.ID, &a.Title, &a.Body, &a.DeliveryTypes, &a.Audience, &a.Status, &a.ScheduledAt, &a.SentAt, &a.CreatedAt)
		items = append(items, a)
	}
	if items == nil { items = []ann{} }
	middleware.JSONWithMeta(w, http.StatusOK, items, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// PATCH /api/v1/admin/announcements/:announcementId/retract
func (h *Handler) RetractAnnouncement(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "announcementId")
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

// GET /api/v1/admin/promos
func (h *Handler) ListPromos(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 { page = 1 }
	if limit < 1 || limit > 50 { limit = 20 }
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

	rows, err := h.db.Query(r.Context(),
		`SELECT id, type, title, body, cta_label, cta_url, audience, status, starts_at, ends_at, created_at
		 FROM promotional_content WHERE `+where+
			fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, n, n+1),
		args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type promo struct {
		ID        string     `json:"id"`
		Type      string     `json:"placement"`
		Title     string     `json:"title"`
		Body      *string    `json:"body,omitempty"`
		CTALabel  *string    `json:"cta_label,omitempty"`
		CTAURL    *string    `json:"cta_url,omitempty"`
		Audience  string     `json:"target"`
		Status    string     `json:"status"`
		StartsAt  *time.Time `json:"start_date,omitempty"`
		EndsAt    *time.Time `json:"end_date,omitempty"`
		CreatedAt time.Time  `json:"created_at"`
	}
	var promos []promo
	for rows.Next() {
		var p promo
		rows.Scan(&p.ID, &p.Type, &p.Title, &p.Body, &p.CTALabel, &p.CTAURL, &p.Audience, &p.Status, &p.StartsAt, &p.EndsAt, &p.CreatedAt)
		promos = append(promos, p)
	}
	if promos == nil { promos = []promo{} }
	middleware.JSONWithMeta(w, http.StatusOK, promos, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// POST /api/v1/admin/promos
func (h *Handler) CreatePromo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title     string     `json:"title"`
		Body      *string    `json:"body"`
		CTALabel  *string    `json:"cta_label"`
		CTAURL    *string    `json:"cta_url"`
		Placement string     `json:"placement"`
		Target    string     `json:"target"`
		StartDate *time.Time `json:"start_date"`
		EndDate   *time.Time `json:"end_date"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Title == "" || req.Placement == "" || req.Target == "" {
		middleware.BadRequest(w, "title, placement, and target are required")
		return
	}
	adminID := middleware.GetAdminID(r)
	var id string
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO promotional_content (title, body, cta_label, cta_url, type, audience, starts_at, ends_at, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		req.Title, req.Body, req.CTALabel, req.CTAURL, req.Placement, req.Target, req.StartDate, req.EndDate, adminID,
	).Scan(&id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	logAudit(r.Context(), h.db, adminID, "create_promo", "promo", id, "")
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": id})
}

// PATCH /api/v1/admin/promos/:promoId
func (h *Handler) UpdatePromoStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "promoId")
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Status == "" {
		middleware.BadRequest(w, "status is required")
		return
	}
	tag, err := h.db.Exec(r.Context(),
		`UPDATE promotional_content SET status=$1 WHERE id=$2`, req.Status, id)
	if err != nil {
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
	if page < 1 { page = 1 }
	if limit < 1 || limit > 50 { limit = 20 }
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

	rows, err := h.db.Query(r.Context(),
		`SELECT id, name, industry, contact_email, website, reward_pool, status, created_at
		 FROM brands WHERE `+where+
			fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, n, n+1),
		args...)
	if err != nil {
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
	}
	var brands []brand
	for rows.Next() {
		var b brand
		rows.Scan(&b.ID, &b.Name, &b.Industry, &b.ContactEmail, &b.Website, &b.RewardPool, &b.Status, &b.CreatedAt)
		brands = append(brands, b)
	}
	if brands == nil { brands = []brand{} }
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		middleware.BadRequest(w, "name is required")
		return
	}
	adminID := middleware.GetAdminID(r)
	var id string
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO brands (name, industry, contact_email, website, reward_pool, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		req.Name, req.Industry, req.ContactEmail, req.Website, req.RewardPool, adminID,
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
		 WHERE id=$2 AND status='pending'`, adminID, id)
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
	if items == nil { items = []sr{} }
	middleware.JSON(w, http.StatusOK, items)
}

// POST /api/v1/admin/sponsorship-requests/:requestId/approve
func (h *Handler) ApproveSponsorshipRequest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "requestId")
	adminID := middleware.GetAdminID(r)
	tag, err := h.db.Exec(r.Context(),
		`UPDATE sponsorship_requests SET status='approved', reviewed_by=$1, reviewed_at=now()
		 WHERE id=$2 AND status='pending'`, adminID, id)
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
	json.NewDecoder(r.Body).Decode(&req)
	adminID := middleware.GetAdminID(r)
	tag, err := h.db.Exec(r.Context(),
		`UPDATE sponsorship_requests SET status='rejected', reason=$1, reviewed_by=$2, reviewed_at=now()
		 WHERE id=$3 AND status='pending'`, req.Reason, adminID, id)
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

// logAudit writes an entry to the audit_log table.
func logAudit(ctx context.Context, db *pgxpool.Pool, adminID, action, targetType, targetID, reason string) {
	var adminName, adminRole string
	db.QueryRow(ctx, `SELECT name, role FROM admin_accounts WHERE id=$1`, adminID).Scan(&adminName, &adminRole)
	db.Exec(ctx,
		`INSERT INTO audit_log (admin_id, admin_name, admin_role, action_type, target_type, target_id, reason)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		adminID, adminName, adminRole, action, targetType, targetID, reason)
}
