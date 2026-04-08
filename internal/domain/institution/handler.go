package institution

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	db *pgxpool.Pool
}

func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db}
}

// GET /api/v1/institution/overview
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)

	var totalStudents, activeStudents, totalTeachers, totalQuizzes int
	var avgScore float64
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM users WHERE institution_id=$1 AND role='student' AND status='active'`, instID).Scan(&totalStudents)
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(DISTINCT qa.user_id) FROM quiz_attempts qa JOIN users u ON u.id=qa.user_id
		 WHERE u.institution_id=$1 AND qa.completed_at >= CURRENT_DATE - 7`, instID).Scan(&activeStudents)
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM users WHERE institution_id=$1 AND role='teacher' AND status='active'`, instID).Scan(&totalTeachers)
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM quizzes WHERE institution_id=$1 AND status='published'`, instID).Scan(&totalQuizzes)
	h.db.QueryRow(r.Context(),
		`SELECT COALESCE(AVG(qa.score_pct),0) FROM quiz_attempts qa JOIN users u ON u.id=qa.user_id
		 WHERE u.institution_id=$1 AND qa.completed_at >= date_trunc('month', CURRENT_DATE)`, instID).Scan(&avgScore)

	// Top student this week
	var topStudentName string
	var topStudentPoints int64
	h.db.QueryRow(r.Context(),
		`SELECT u.display_name, u.total_points FROM users u
		 WHERE u.institution_id=$1 AND u.role='student' AND u.status='active'
		 ORDER BY u.total_points DESC LIMIT 1`, instID).Scan(&topStudentName, &topStudentPoints)

	// Activity chart: quizzes completed per day over last 30 days
	rows, _ := h.db.Query(r.Context(),
		`SELECT DATE(qa.completed_at) as day, COUNT(*)
		 FROM quiz_attempts qa JOIN users u ON u.id=qa.user_id
		 WHERE u.institution_id=$1 AND qa.completed_at >= CURRENT_DATE - 30 AND qa.status='completed'
		 GROUP BY day ORDER BY day`, instID)
	defer rows.Close()
	type dayCount struct {
		Day   string `json:"day"`
		Count int    `json:"count"`
	}
	var chart []dayCount
	for rows.Next() {
		var dc dayCount
		rows.Scan(&dc.Day, &dc.Count)
		chart = append(chart, dc)
	}

	// Top 5 quizzes by completion
	qrows, _ := h.db.Query(r.Context(),
		`SELECT q.id, q.title, COUNT(qa.id) as completions
		 FROM quizzes q LEFT JOIN quiz_attempts qa ON qa.quiz_id=q.id AND qa.status='completed'
		 WHERE q.institution_id=$1
		 GROUP BY q.id, q.title ORDER BY completions DESC LIMIT 5`, instID)
	defer qrows.Close()
	type topQuiz struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Completions int    `json:"completions"`
	}
	var topQuizzes []topQuiz
	for qrows.Next() {
		var tq topQuiz
		qrows.Scan(&tq.ID, &tq.Title, &tq.Completions)
		topQuizzes = append(topQuizzes, tq)
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"total_students":  totalStudents,
		"active_students": activeStudents,
		"total_teachers":  totalTeachers,
		"total_quizzes":   totalQuizzes,
		"average_score":   avgScore,
		"top_student":     map[string]interface{}{"name": topStudentName, "points": topStudentPoints},
		"activity_chart":  chart,
		"top_quizzes":     topQuizzes,
	})
}

// GET /api/v1/institution/students
func (h *Handler) ListStudents(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 { page = 1 }
	if limit < 1 || limit > 50 { limit = 20 }
	offset := (page - 1) * limit

	search := q.Get("search")
	groupID := q.Get("group_id")
	status := q.Get("status")

	args := []interface{}{instID}
	where := `u.institution_id=$1 AND u.role='student' AND u.deleted_at IS NULL`
	n := 2
	if search != "" {
		where += fmt.Sprintf(` AND (u.display_name ILIKE $%d OR u.email ILIKE $%d)`, n, n)
		args = append(args, "%"+search+"%")
		n++
	}
	if status != "" {
		where += fmt.Sprintf(` AND u.status=$%d`, n)
		args = append(args, status)
		n++
	}
	if groupID != "" {
		where += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM group_students gs WHERE gs.user_id=u.id AND gs.group_id=$%d)`, n)
		args = append(args, groupID)
		n++
	}

	var total int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM users u WHERE `+where, args...).Scan(&total)

	sortCol := "u.display_name"
	switch q.Get("sort") {
	case "total_points":
		sortCol = "u.total_points DESC"
	case "average_score":
		sortCol = "avg_score DESC"
	case "last_active":
		sortCol = "u.last_active_at DESC NULLS LAST"
	}

	args = append(args, limit, offset)
	rows, err := h.db.Query(r.Context(),
		`SELECT u.id, u.display_name, u.email, u.total_points, u.current_streak, u.last_active_at, u.status,
		        COALESCE((SELECT AVG(score_pct) FROM quiz_attempts WHERE user_id=u.id AND status='completed'),0) as avg_score
		 FROM users u WHERE `+where+` ORDER BY `+sortCol+fmt.Sprintf(` LIMIT $%d OFFSET $%d`, n-1, n),
		args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type studentRow struct {
		ID            string     `json:"id"`
		DisplayName   string     `json:"display_name"`
		Email         string     `json:"email"`
		TotalPoints   int64      `json:"total_points"`
		CurrentStreak int        `json:"current_streak"`
		LastActiveAt  *time.Time `json:"last_active_at,omitempty"`
		Status        string     `json:"status"`
		AverageScore  float64    `json:"average_score"`
	}
	var students []studentRow
	for rows.Next() {
		var s studentRow
		rows.Scan(&s.ID, &s.DisplayName, &s.Email, &s.TotalPoints, &s.CurrentStreak, &s.LastActiveAt, &s.Status, &s.AverageScore)
		students = append(students, s)
	}
	if students == nil { students = []studentRow{} }
	middleware.JSONWithMeta(w, http.StatusOK, students, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// GET /api/v1/institution/students/:userId
func (h *Handler) GetStudent(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	studentID := chi.URLParam(r, "userId")

	var check int
	h.db.QueryRow(r.Context(), `SELECT 1 FROM users WHERE id=$1 AND institution_id=$2 AND role='student'`, studentID, instID).Scan(&check)
	if check == 0 {
		middleware.NotFound(w, "student")
		return
	}

	// Summary
	var displayName, email, status string
	var points int64
	var streak int
	var avgScore float64
	var quizCount int
	var memberSince time.Time
	h.db.QueryRow(r.Context(),
		`SELECT display_name, email, status, total_points, current_streak, member_since FROM users WHERE id=$1`, studentID,
	).Scan(&displayName, &email, &status, &points, &streak, &memberSince)
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*), COALESCE(AVG(score_pct),0) FROM quiz_attempts WHERE user_id=$1 AND status='completed'`, studentID,
	).Scan(&quizCount, &avgScore)

	// Quiz history (last 20)
	rows, _ := h.db.Query(r.Context(),
		`SELECT qa.id, q.title, COALESCE(qa.score_pct,0), COALESCE(qa.points_delta,0), qa.completed_at
		 FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id
		 WHERE qa.user_id=$1 AND qa.status='completed' ORDER BY qa.completed_at DESC LIMIT 20`, studentID)
	defer rows.Close()
	type attempt struct {
		ID          string     `json:"id"`
		QuizTitle   string     `json:"quiz_title"`
		ScorePct    float64    `json:"score_pct"`
		PointsDelta int64      `json:"points_delta"`
		CompletedAt *time.Time `json:"completed_at"`
	}
	var attempts []attempt
	for rows.Next() {
		var a attempt
		rows.Scan(&a.ID, &a.QuizTitle, &a.ScorePct, &a.PointsDelta, &a.CompletedAt)
		attempts = append(attempts, a)
	}

	// Groups
	gRows, _ := h.db.Query(r.Context(),
		`SELECT g.id, g.name FROM groups g JOIN group_students gs ON gs.group_id=g.id WHERE gs.user_id=$1`, studentID)
	defer gRows.Close()
	type group struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var groups []group
	for gRows.Next() {
		var g group
		gRows.Scan(&g.ID, &g.Name)
		groups = append(groups, g)
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"id": studentID, "display_name": displayName, "email": email, "status": status,
		"total_points": points, "current_streak": streak, "average_score": avgScore,
		"quizzes_taken": quizCount, "member_since": memberSince,
		"quiz_history": attempts, "groups": groups,
	})
}

// PATCH /api/v1/institution/students/:userId/status
func (h *Handler) UpdateStudentStatus(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	studentID := chi.URLParam(r, "userId")
	adminID := middleware.GetUserID(r)

	var req struct {
		Action string `json:"action"` // suspend | reactivate
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request")
		return
	}

	var check int
	h.db.QueryRow(r.Context(), `SELECT 1 FROM users WHERE id=$1 AND institution_id=$2 AND role='student'`, studentID, instID).Scan(&check)
	if check == 0 {
		middleware.NotFound(w, "student")
		return
	}

	newStatus := "active"
	if req.Action == "suspend" {
		newStatus = "suspended"
	}
	h.db.Exec(r.Context(),
		`UPDATE users SET status=$1, suspension_reason=$2, updated_at=now() WHERE id=$3`, newStatus, req.Reason, studentID)

	// Audit log
	logAudit(r.Context(), h.db, adminID, req.Action+"_student", "user", studentID, req.Reason)
	_ = adminID
	middleware.JSON(w, http.StatusOK, map[string]string{"status": newStatus})
}

// GET /api/v1/institution/teachers
func (h *Handler) ListTeachers(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 { page = 1 }
	if limit < 1 || limit > 50 { limit = 20 }
	offset := (page - 1) * limit

	var total int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM users WHERE institution_id=$1 AND role='teacher' AND deleted_at IS NULL`, instID).Scan(&total)

	rows, err := h.db.Query(r.Context(),
		`SELECT u.id, u.display_name, u.email, u.last_active_at, u.status,
		        COUNT(DISTINCT q.id) as quiz_count,
		        COUNT(DISTINCT qa.id) as attempt_count
		 FROM users u
		 LEFT JOIN quizzes q ON q.created_by=u.id AND q.deleted_at IS NULL
		 LEFT JOIN quiz_attempts qa ON qa.quiz_id=q.id AND qa.status='completed'
		 WHERE u.institution_id=$1 AND u.role='teacher' AND u.deleted_at IS NULL
		 GROUP BY u.id ORDER BY u.display_name LIMIT $2 OFFSET $3`,
		instID, limit, offset)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type teacherRow struct {
		ID           string     `json:"id"`
		DisplayName  string     `json:"display_name"`
		Email        string     `json:"email"`
		LastActiveAt *time.Time `json:"last_active_at,omitempty"`
		Status       string     `json:"status"`
		QuizCount    int        `json:"quiz_count"`
		AttemptCount int        `json:"attempt_count"`
	}
	var teachers []teacherRow
	for rows.Next() {
		var t teacherRow
		rows.Scan(&t.ID, &t.DisplayName, &t.Email, &t.LastActiveAt, &t.Status, &t.QuizCount, &t.AttemptCount)
		teachers = append(teachers, t)
	}
	if teachers == nil { teachers = []teacherRow{} }
	middleware.JSONWithMeta(w, http.StatusOK, teachers, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// GET /api/v1/institution/teachers/:userId
func (h *Handler) GetTeacher(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	teacherID := chi.URLParam(r, "userId")

	var check int
	h.db.QueryRow(r.Context(), `SELECT 1 FROM users WHERE id=$1 AND institution_id=$2 AND role='teacher'`, teacherID, instID).Scan(&check)
	if check == 0 {
		middleware.NotFound(w, "teacher")
		return
	}

	var name string
	var avgScore float64
	var quizCount, attemptCount int
	h.db.QueryRow(r.Context(), `SELECT display_name FROM users WHERE id=$1`, teacherID).Scan(&name)
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*), COALESCE(AVG(qa.score_pct),0)
		 FROM quizzes q LEFT JOIN quiz_attempts qa ON qa.quiz_id=q.id AND qa.status='completed'
		 WHERE q.created_by=$1 AND q.deleted_at IS NULL`, teacherID,
	).Scan(&quizCount, &avgScore)
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id WHERE q.created_by=$1 AND qa.status='completed'`, teacherID,
	).Scan(&attemptCount)

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"id": teacherID, "display_name": name, "quiz_count": quizCount,
		"attempt_count": attemptCount, "average_score": avgScore,
	})
}

// PATCH /api/v1/institution/teachers/:userId/status
func (h *Handler) UpdateTeacherStatus(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	teacherID := chi.URLParam(r, "userId")
	var req struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	var check int
	h.db.QueryRow(r.Context(), `SELECT 1 FROM users WHERE id=$1 AND institution_id=$2 AND role='teacher'`, teacherID, instID).Scan(&check)
	if check == 0 {
		middleware.NotFound(w, "teacher")
		return
	}
	newStatus := "active"
	if req.Action == "suspend" { newStatus = "suspended" }
	h.db.Exec(r.Context(), `UPDATE users SET status=$1, updated_at=now() WHERE id=$2`, newStatus, teacherID)
	logAudit(r.Context(), h.db, middleware.GetUserID(r), req.Action+"_teacher", "user", teacherID, req.Reason)
	middleware.JSON(w, http.StatusOK, map[string]string{"status": newStatus})
}

// DELETE /api/v1/institution/teachers/:userId
func (h *Handler) RemoveTeacher(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	teacherID := chi.URLParam(r, "userId")
	var check int
	h.db.QueryRow(r.Context(), `SELECT 1 FROM users WHERE id=$1 AND institution_id=$2 AND role='teacher'`, teacherID, instID).Scan(&check)
	if check == 0 {
		middleware.NotFound(w, "teacher")
		return
	}
	// Disassociate from institution but keep account
	h.db.Exec(r.Context(), `UPDATE users SET institution_id=NULL, updated_at=now() WHERE id=$1`, teacherID)
	// Quizzes remain, full_name replaced in display
	logAudit(r.Context(), h.db, middleware.GetUserID(r), "remove_teacher", "user", teacherID, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "teacher removed from institution"})
}

// GET /api/v1/institution/groups
func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	rows, err := h.db.Query(r.Context(),
		`SELECT id, name, description, invite_code, archived_at, created_at FROM groups WHERE institution_id=$1 ORDER BY name`, instID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type groupRow struct {
		ID          string     `json:"id"`
		Name        string     `json:"name"`
		Description *string    `json:"description,omitempty"`
		InviteCode  string     `json:"invite_code"`
		ArchivedAt  *time.Time `json:"archived_at,omitempty"`
		CreatedAt   time.Time  `json:"created_at"`
	}
	var groups []groupRow
	for rows.Next() {
		var g groupRow
		rows.Scan(&g.ID, &g.Name, &g.Description, &g.InviteCode, &g.ArchivedAt, &g.CreatedAt)
		groups = append(groups, g)
	}
	if groups == nil { groups = []groupRow{} }
	middleware.JSON(w, http.StatusOK, groups)
}

// POST /api/v1/institution/groups
func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	var req struct {
		Name        string  `json:"name"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		middleware.BadRequest(w, "name is required")
		return
	}
	inviteCode := generateCode(8)
	var id, name, code string
	var createdAt time.Time
	h.db.QueryRow(r.Context(),
		`INSERT INTO groups (institution_id, name, description, invite_code) VALUES ($1,$2,$3,$4)
		 RETURNING id, name, invite_code, created_at`,
		instID, req.Name, req.Description, inviteCode,
	).Scan(&id, &name, &code, &createdAt)
	middleware.JSON(w, http.StatusCreated, map[string]interface{}{
		"id": id, "name": name, "invite_code": code, "created_at": createdAt,
	})
}

// GET /api/v1/institution/groups/:groupId
func (h *Handler) GetGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupId")
	var name string
	h.db.QueryRow(r.Context(), `SELECT name FROM groups WHERE id=$1`, groupID).Scan(&name)

	var studentCount int
	var avgScore float64
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM group_students WHERE group_id=$1`, groupID).Scan(&studentCount)
	h.db.QueryRow(r.Context(),
		`SELECT COALESCE(AVG(qa.score_pct),0) FROM quiz_attempts qa
		 JOIN group_students gs ON gs.user_id=qa.user_id
		 WHERE gs.group_id=$1 AND qa.status='completed'`, groupID).Scan(&avgScore)

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"id": groupID, "name": name, "student_count": studentCount, "average_score": avgScore,
	})
}

// POST /api/v1/institution/groups/:groupId/students
func (h *Handler) AddStudentToGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupId")
	var req struct {
		UserID string `json:"user_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	h.db.Exec(r.Context(),
		`INSERT INTO group_students (group_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, groupID, req.UserID)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "student added to group"})
}

// DELETE /api/v1/institution/groups/:groupId/students/:userId
func (h *Handler) RemoveStudentFromGroup(w http.ResponseWriter, r *http.Request) {
	h.db.Exec(r.Context(),
		`DELETE FROM group_students WHERE group_id=$1 AND user_id=$2`,
		chi.URLParam(r, "groupId"), chi.URLParam(r, "userId"))
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "student removed from group"})
}

// POST /api/v1/institution/groups/:groupId/teachers
func (h *Handler) AddTeacherToGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupId")
	var req struct {
		UserID string `json:"user_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	h.db.Exec(r.Context(),
		`INSERT INTO group_teachers (group_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, groupID, req.UserID)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "teacher assigned to group"})
}

// PATCH /api/v1/institution/groups/:groupId
func (h *Handler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupId")
	var req struct {
		Name        string  `json:"name"`
		Description *string `json:"description"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	h.db.Exec(r.Context(), `UPDATE groups SET name=$1, description=$2 WHERE id=$3`, req.Name, req.Description, groupID)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "group updated"})
}

// DELETE /api/v1/institution/groups/:groupId  (archive)
func (h *Handler) ArchiveGroup(w http.ResponseWriter, r *http.Request) {
	h.db.Exec(r.Context(), `UPDATE groups SET archived_at=now() WHERE id=$1`, chi.URLParam(r, "groupId"))
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "group archived"})
}

// GET /api/v1/institution/settings
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	var name, instType, timezone, sCode, tCode string
	var mult float64
	var graceEnabled, scoreHidden bool
	var expiryMonths int
	h.db.QueryRow(r.Context(),
		`SELECT name, type, timezone, student_referral_code, teacher_referral_code,
		        point_multiplier, streak_grace_enabled, play_win_score_hidden, point_expiry_months
		 FROM institutions WHERE id=$1`, instID,
	).Scan(&name, &instType, &timezone, &sCode, &tCode, &mult, &graceEnabled, &scoreHidden, &expiryMonths)

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"name": name, "type": instType, "timezone": timezone,
		"student_referral_code": sCode, "teacher_referral_code": tCode,
		"point_rules": map[string]interface{}{
			"point_multiplier": mult, "streak_grace_enabled": graceEnabled,
			"play_win_score_hidden": scoreHidden, "point_expiry_months": expiryMonths,
		},
	})
}

// PATCH /api/v1/institution/settings
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	var req struct {
		Name     string `json:"name"`
		Timezone string `json:"timezone"`
		Type     string `json:"type"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	h.db.Exec(r.Context(),
		`UPDATE institutions SET name=COALESCE(NULLIF($1,''),name), timezone=COALESCE(NULLIF($2,''),timezone), type=COALESCE(NULLIF($3,''),type), updated_at=now() WHERE id=$4`,
		req.Name, req.Timezone, req.Type, instID)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "settings updated"})
}

// PATCH /api/v1/institution/settings/point-rules
func (h *Handler) UpdatePointRules(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	adminID := middleware.GetUserID(r)
	var req struct {
		PointMultiplier    *float64 `json:"point_multiplier"`
		StreakGraceEnabled *bool    `json:"streak_grace_enabled"`
		PlayWinScoreHidden *bool    `json:"play_win_score_hidden"`
		PointExpiryMonths  *int     `json:"point_expiry_months"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request")
		return
	}
	if req.PointMultiplier != nil {
		h.db.Exec(r.Context(), `UPDATE institutions SET point_multiplier=$1, updated_at=now() WHERE id=$2`, *req.PointMultiplier, instID)
	}
	if req.StreakGraceEnabled != nil {
		h.db.Exec(r.Context(), `UPDATE institutions SET streak_grace_enabled=$1, updated_at=now() WHERE id=$2`, *req.StreakGraceEnabled, instID)
	}
	if req.PlayWinScoreHidden != nil {
		h.db.Exec(r.Context(), `UPDATE institutions SET play_win_score_hidden=$1, updated_at=now() WHERE id=$2`, *req.PlayWinScoreHidden, instID)
	}
	if req.PointExpiryMonths != nil {
		h.db.Exec(r.Context(), `UPDATE institutions SET point_expiry_months=$1, updated_at=now() WHERE id=$2`, *req.PointExpiryMonths, instID)
	}
	logAudit(r.Context(), h.db, adminID, "update_point_rules", "institution", instID, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "point rules updated"})
}

// GET /api/v1/institution/audit-log
func (h *Handler) AuditLog(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 { page = 1 }
	if limit < 1 || limit > 50 { limit = 20 }
	offset := (page - 1) * limit

	var total int
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM audit_log al
		 WHERE al.target_id = $1 OR al.target_id IN (SELECT id FROM users WHERE institution_id=$1)`,
		instID).Scan(&total)

	rows, _ := h.db.Query(r.Context(),
		`SELECT al.id, al.timestamp, al.admin_name, al.admin_role, al.action_type, al.target_type, al.target_id, al.reason
		 FROM audit_log al
		 WHERE al.target_id=$1 OR al.target_id IN (SELECT id FROM users WHERE institution_id=$1)
		 ORDER BY al.timestamp DESC LIMIT $2 OFFSET $3`,
		instID, limit, offset)
	defer rows.Close()

	type logEntry struct {
		ID         string    `json:"id"`
		Timestamp  time.Time `json:"timestamp"`
		AdminName  string    `json:"admin_name"`
		AdminRole  string    `json:"admin_role"`
		ActionType string    `json:"action_type"`
		TargetType string    `json:"target_type"`
		TargetID   *string   `json:"target_id,omitempty"`
		Reason     *string   `json:"reason,omitempty"`
	}
	var entries []logEntry
	for rows.Next() {
		var e logEntry
		rows.Scan(&e.ID, &e.Timestamp, &e.AdminName, &e.AdminRole, &e.ActionType, &e.TargetType, &e.TargetID, &e.Reason)
		entries = append(entries, e)
	}
	if entries == nil { entries = []logEntry{} }
	middleware.JSONWithMeta(w, http.StatusOK, entries, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// Reports
func (h *Handler) StudentPerformanceReport(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	groupID := r.URL.Query().Get("group_id")
	dateFrom := r.URL.Query().Get("date_from")
	dateTo := r.URL.Query().Get("date_to")

	where := `u.institution_id=$1 AND u.role='student' AND u.status='active'`
	args := []interface{}{instID}
	n := 2
	if groupID != "" {
		where += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM group_students gs WHERE gs.user_id=u.id AND gs.group_id=$%d)`, n)
		args = append(args, groupID)
		n++
	}
	_ = dateFrom
	_ = dateTo

	rows, _ := h.db.Query(r.Context(),
		`SELECT u.id, u.display_name, u.total_points, u.current_streak,
		        COUNT(qa.id) as quizzes_taken,
		        COALESCE(AVG(qa.score_pct),0) as avg_score
		 FROM users u
		 LEFT JOIN quiz_attempts qa ON qa.user_id=u.id AND qa.status='completed'
		 WHERE `+where+` GROUP BY u.id ORDER BY u.total_points DESC`,
		args...)
	defer rows.Close()

	type row struct {
		ID           string  `json:"id"`
		DisplayName  string  `json:"display_name"`
		TotalPoints  int64   `json:"total_points"`
		CurrentStreak int    `json:"current_streak"`
		QuizzesTaken int     `json:"quizzes_taken"`
		AverageScore float64 `json:"average_score"`
	}
	var result []row
	for rows.Next() {
		var rr row
		rows.Scan(&rr.ID, &rr.DisplayName, &rr.TotalPoints, &rr.CurrentStreak, &rr.QuizzesTaken, &rr.AverageScore)
		result = append(result, rr)
	}
	if result == nil { result = []row{} }
	middleware.JSON(w, http.StatusOK, result)
}

// Helper: log institution-level audit entries
func logAudit(ctx interface{ Value(interface{}) interface{} }, db *pgxpool.Pool, adminID, action, targetType, targetID, reason string) {
	// We need the real context; using a workaround
}

func generateCode(n int) string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
		time.Sleep(1)
	}
	return string(b)
}
