package teacher

import (
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

// hasGroupAssignments returns true if the teacher is assigned to at least one group.
// Unassigned teachers see all institution students (per PRD §5.4).
func (h *Handler) hasGroupAssignments(r *http.Request, teacherID string) bool {
	var count int
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM group_teachers WHERE user_id=$1`, teacherID).Scan(&count)
	return count > 0
}

// ─── Overview ────────────────────────────────────────────────────────────────

// GET /api/v1/teacher/overview
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	teacherID := middleware.GetUserID(r)

	var drafts, pending, published, totalAttempts int
	var avgScore float64

	h.db.QueryRow(r.Context(), `
		SELECT
		  COUNT(*) FILTER (WHERE status='draft'),
		  COUNT(*) FILTER (WHERE status='pending_approval'),
		  COUNT(*) FILTER (WHERE status='published')
		FROM quizzes WHERE created_by=$1 AND deleted_at IS NULL`, teacherID,
	).Scan(&drafts, &pending, &published)

	h.db.QueryRow(r.Context(), `
		SELECT COUNT(*), COALESCE(AVG(score_pct),0)
		FROM quiz_attempts qa
		JOIN quizzes q ON q.id=qa.quiz_id
		WHERE q.created_by=$1 AND qa.status='completed'`, teacherID,
	).Scan(&totalAttempts, &avgScore)

	type recentRow struct {
		AttemptID   string     `json:"attempt_id"`
		QuizID      string     `json:"quiz_id"`
		QuizTitle   string     `json:"quiz_title"`
		StudentID   string     `json:"student_id"`
		StudentName string     `json:"student_name"`
		ScorePct    float64    `json:"score_pct"`
		CompletedAt *time.Time `json:"completed_at"`
	}
	rows, _ := h.db.Query(r.Context(), `
		SELECT qa.id, q.id, q.title, u.id, u.display_name, COALESCE(qa.score_pct,0), qa.completed_at
		FROM quiz_attempts qa
		JOIN quizzes q ON q.id=qa.quiz_id
		JOIN users u  ON u.id=qa.user_id
		WHERE q.created_by=$1 AND qa.status='completed'
		ORDER BY qa.completed_at DESC NULLS LAST
		LIMIT 10`, teacherID)
	defer rows.Close()
	recent := []recentRow{}
	for rows.Next() {
		var rr recentRow
		rows.Scan(&rr.AttemptID, &rr.QuizID, &rr.QuizTitle, &rr.StudentID, &rr.StudentName, &rr.ScorePct, &rr.CompletedAt)
		recent = append(recent, rr)
	}

	var openTopicRequests int
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM topic_requests WHERE status='pending' AND (assigned_to=$1 OR assigned_to IS NULL)`,
		teacherID).Scan(&openTopicRequests)

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"drafts":              drafts,
		"pending_review":      pending,
		"published":           published,
		"total_attempts":      totalAttempts,
		"average_score":       avgScore,
		"open_topic_requests": openTopicRequests,
		"recent_attempts":     recent,
	})
}

// ─── Students ────────────────────────────────────────────────────────────────

// scopeStudentsSQL returns extra WHERE clause + args restricting students to
// the teacher's assigned groups (or all institution students if unassigned).
func (h *Handler) scopeStudentsSQL(r *http.Request, teacherID string, startN int, args *[]interface{}) string {
	if !h.hasGroupAssignments(r, teacherID) {
		return ""
	}
	clause := fmt.Sprintf(` AND EXISTS (
		SELECT 1 FROM group_students gs
		JOIN group_teachers gt ON gt.group_id = gs.group_id
		WHERE gs.user_id = u.id AND gt.user_id = $%d
	)`, startN)
	*args = append(*args, teacherID)
	return clause
}

// GET /api/v1/teacher/students
func (h *Handler) ListStudents(w http.ResponseWriter, r *http.Request) {
	teacherID := middleware.GetUserID(r)
	instID := middleware.GetInstitutionID(r)
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

	args := []interface{}{instID}
	where := `u.institution_id=$1 AND u.role='student' AND u.deleted_at IS NULL`
	n := 2

	if s := q.Get("search"); s != "" {
		where += fmt.Sprintf(` AND (u.display_name ILIKE $%d OR u.email ILIKE $%d)`, n, n)
		args = append(args, "%"+s+"%")
		n++
	}
	if cid := q.Get("class_id"); cid != "" {
		// Restrict to a specific group, and verify the teacher is assigned to it.
		where += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM group_students gs WHERE gs.user_id=u.id AND gs.group_id=$%d)
			AND EXISTS (SELECT 1 FROM group_teachers gt WHERE gt.group_id=$%d AND gt.user_id=$%d)`, n, n, n+1)
		args = append(args, cid, teacherID)
		n += 2
	} else {
		where += h.scopeStudentsSQL(r, teacherID, n, &args)
		if h.hasGroupAssignments(r, teacherID) {
			n++
		}
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
		 FROM users u WHERE `+where+` ORDER BY `+sortCol+
			fmt.Sprintf(` LIMIT $%d OFFSET $%d`, n, n+1),
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
	students := []studentRow{}
	for rows.Next() {
		var s studentRow
		rows.Scan(&s.ID, &s.DisplayName, &s.Email, &s.TotalPoints, &s.CurrentStreak, &s.LastActiveAt, &s.Status, &s.AverageScore)
		students = append(students, s)
	}
	middleware.JSONWithMeta(w, http.StatusOK, students, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// GET /api/v1/teacher/students/:userId
func (h *Handler) GetStudent(w http.ResponseWriter, r *http.Request) {
	teacherID := middleware.GetUserID(r)
	instID := middleware.GetInstitutionID(r)
	studentID := chi.URLParam(r, "userId")

	// Visibility check: same institution + either teacher is unassigned or shares a group with the student.
	var visible int
	if h.hasGroupAssignments(r, teacherID) {
		h.db.QueryRow(r.Context(), `
			SELECT 1 FROM users u
			WHERE u.id=$1 AND u.institution_id=$2 AND u.role='student' AND u.deleted_at IS NULL
			  AND EXISTS (
			    SELECT 1 FROM group_students gs
			    JOIN group_teachers gt ON gt.group_id = gs.group_id
			    WHERE gs.user_id=u.id AND gt.user_id=$3
			  )`, studentID, instID, teacherID).Scan(&visible)
	} else {
		h.db.QueryRow(r.Context(),
			`SELECT 1 FROM users WHERE id=$1 AND institution_id=$2 AND role='student' AND deleted_at IS NULL`,
			studentID, instID).Scan(&visible)
	}
	if visible == 0 {
		middleware.NotFound(w, "student")
		return
	}

	var displayName, email, status string
	var points int64
	var streak, longestStreak, quizCount int
	var avgScore float64
	var memberSince time.Time
	h.db.QueryRow(r.Context(),
		`SELECT display_name, email, status, total_points, current_streak, longest_streak, member_since
		 FROM users WHERE id=$1`, studentID,
	).Scan(&displayName, &email, &status, &points, &streak, &longestStreak, &memberSince)

	// Stats limited to this teacher's quizzes.
	h.db.QueryRow(r.Context(), `
		SELECT COUNT(*), COALESCE(AVG(qa.score_pct),0)
		FROM quiz_attempts qa
		JOIN quizzes q ON q.id=qa.quiz_id
		WHERE qa.user_id=$1 AND qa.status='completed' AND q.created_by=$2`,
		studentID, teacherID).Scan(&quizCount, &avgScore)

	// Quiz history on this teacher's quizzes (last 20).
	rows, _ := h.db.Query(r.Context(), `
		SELECT qa.id, q.id, q.title, COALESCE(qa.score_pct,0), COALESCE(qa.points_delta,0), qa.completed_at
		FROM quiz_attempts qa
		JOIN quizzes q ON q.id=qa.quiz_id
		WHERE qa.user_id=$1 AND qa.status='completed' AND q.created_by=$2
		ORDER BY qa.completed_at DESC LIMIT 20`, studentID, teacherID)
	defer rows.Close()
	type attempt struct {
		ID          string     `json:"id"`
		QuizID      string     `json:"quiz_id"`
		QuizTitle   string     `json:"quiz_title"`
		ScorePct    float64    `json:"score_pct"`
		PointsDelta int64      `json:"points_delta"`
		CompletedAt *time.Time `json:"completed_at"`
	}
	attempts := []attempt{}
	for rows.Next() {
		var a attempt
		rows.Scan(&a.ID, &a.QuizID, &a.QuizTitle, &a.ScorePct, &a.PointsDelta, &a.CompletedAt)
		attempts = append(attempts, a)
	}

	// Shared classes between the teacher and this student.
	cRows, _ := h.db.Query(r.Context(), `
		SELECT g.id, g.name FROM groups g
		JOIN group_students gs ON gs.group_id=g.id
		JOIN group_teachers gt ON gt.group_id=g.id
		WHERE gs.user_id=$1 AND gt.user_id=$2`, studentID, teacherID)
	defer cRows.Close()
	type class struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	classes := []class{}
	for cRows.Next() {
		var c class
		cRows.Scan(&c.ID, &c.Name)
		classes = append(classes, c)
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"id":             studentID,
		"display_name":   displayName,
		"email":          email,
		"status":         status,
		"total_points":   points,
		"current_streak": streak,
		"longest_streak": longestStreak,
		"average_score":  avgScore,
		"quizzes_taken":  quizCount,
		"member_since":   memberSince,
		"quiz_history":   attempts,
		"classes":        classes,
	})
}

// ─── Classes (read-only group view) ──────────────────────────────────────────

// GET /api/v1/teacher/classes
func (h *Handler) ListClasses(w http.ResponseWriter, r *http.Request) {
	teacherID := middleware.GetUserID(r)
	rows, err := h.db.Query(r.Context(), `
		SELECT g.id, g.name, g.description, g.invite_code, g.archived_at, g.created_at,
		       (SELECT COUNT(*) FROM group_students gs WHERE gs.group_id=g.id) AS student_count
		FROM groups g
		JOIN group_teachers gt ON gt.group_id=g.id
		WHERE gt.user_id=$1 AND g.archived_at IS NULL
		ORDER BY g.name`, teacherID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type classRow struct {
		ID           string     `json:"id"`
		Name         string     `json:"name"`
		Description  *string    `json:"description,omitempty"`
		InviteCode   string     `json:"invite_code"`
		ArchivedAt   *time.Time `json:"archived_at,omitempty"`
		CreatedAt    time.Time  `json:"created_at"`
		StudentCount int        `json:"student_count"`
	}
	classes := []classRow{}
	for rows.Next() {
		var c classRow
		rows.Scan(&c.ID, &c.Name, &c.Description, &c.InviteCode, &c.ArchivedAt, &c.CreatedAt, &c.StudentCount)
		classes = append(classes, c)
	}
	middleware.JSON(w, http.StatusOK, classes)
}

// GET /api/v1/teacher/classes/:classId
func (h *Handler) GetClass(w http.ResponseWriter, r *http.Request) {
	teacherID := middleware.GetUserID(r)
	classID := chi.URLParam(r, "classId")

	// Membership check.
	var ok int
	h.db.QueryRow(r.Context(),
		`SELECT 1 FROM group_teachers WHERE group_id=$1 AND user_id=$2`, classID, teacherID).Scan(&ok)
	if ok == 0 {
		middleware.NotFound(w, "class")
		return
	}

	var name string
	var description *string
	var inviteCode string
	var createdAt time.Time
	h.db.QueryRow(r.Context(),
		`SELECT name, description, invite_code, created_at FROM groups WHERE id=$1`, classID,
	).Scan(&name, &description, &inviteCode, &createdAt)

	var studentCount int
	var avgScore float64
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM group_students WHERE group_id=$1`, classID).Scan(&studentCount)
	h.db.QueryRow(r.Context(), `
		SELECT COALESCE(AVG(qa.score_pct),0) FROM quiz_attempts qa
		JOIN group_students gs ON gs.user_id=qa.user_id
		WHERE gs.group_id=$1 AND qa.status='completed'`, classID).Scan(&avgScore)

	sRows, _ := h.db.Query(r.Context(), `
		SELECT u.id, u.display_name, u.email, u.total_points, u.current_streak, u.last_active_at, u.status,
		       COALESCE((SELECT AVG(score_pct) FROM quiz_attempts WHERE user_id=u.id AND status='completed'),0) AS avg_score
		FROM users u
		JOIN group_students gs ON gs.user_id=u.id
		WHERE gs.group_id=$1 AND u.deleted_at IS NULL
		ORDER BY u.display_name`, classID)
	defer sRows.Close()
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
	students := []studentRow{}
	for sRows.Next() {
		var s studentRow
		sRows.Scan(&s.ID, &s.DisplayName, &s.Email, &s.TotalPoints, &s.CurrentStreak, &s.LastActiveAt, &s.Status, &s.AverageScore)
		students = append(students, s)
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"id":            classID,
		"name":          name,
		"description":   description,
		"invite_code":   inviteCode,
		"created_at":    createdAt,
		"student_count": studentCount,
		"average_score": avgScore,
		"students":      students,
	})
}

// ─── Reports ─────────────────────────────────────────────────────────────────

// GET /api/v1/teacher/reports/quiz-analytics
func (h *Handler) QuizAnalyticsReport(w http.ResponseWriter, r *http.Request) {
	teacherID := middleware.GetUserID(r)
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	args := []interface{}{teacherID}
	dateClause := ""
	n := 2
	if df := q.Get("date_from"); df != "" {
		dateClause += fmt.Sprintf(" AND qa.started_at >= $%d", n)
		args = append(args, df)
		n++
	}
	if dt := q.Get("date_to"); dt != "" {
		dateClause += fmt.Sprintf(" AND qa.started_at <= $%d", n)
		args = append(args, dt)
		n++
	}

	var total int
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM quizzes WHERE created_by=$1 AND deleted_at IS NULL`, teacherID).Scan(&total)

	args = append(args, limit, offset)
	sql := `SELECT q.id, q.title,
	        COUNT(qa.id) AS started_count,
	        COUNT(qa.id) FILTER (WHERE qa.status='completed') AS completed_count,
	        COUNT(*) FILTER (WHERE qa.status='completed' AND qa.score_pct >= 80) AS high_band,
	        COUNT(*) FILTER (WHERE qa.status='completed' AND qa.score_pct >= 60 AND qa.score_pct < 80) AS mid_band,
	        COUNT(*) FILTER (WHERE qa.status='completed' AND qa.score_pct < 60) AS low_band
	 FROM quizzes q
	 LEFT JOIN quiz_attempts qa ON qa.quiz_id=q.id` + dateClause + `
	 WHERE q.created_by=$1 AND q.deleted_at IS NULL
	 GROUP BY q.id, q.title
	 ORDER BY completed_count DESC, q.title
	 LIMIT $` + strconv.Itoa(n) + ` OFFSET $` + strconv.Itoa(n+1)

	rows, err := h.db.Query(r.Context(), sql, args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type row struct {
		QuizID         string  `json:"quiz_id"`
		Title          string  `json:"title"`
		CompletionRate float64 `json:"completion_rate"`
		ScoreDistHigh  int     `json:"score_dist_high"`
		ScoreDistMid   int     `json:"score_dist_mid"`
		ScoreDistLow   int     `json:"score_dist_low"`
	}
	out := []row{}
	for rows.Next() {
		var qid, title string
		var started, completed, hi, mid, lo int
		rows.Scan(&qid, &title, &started, &completed, &hi, &mid, &lo)
		cr := 0.0
		if started > 0 {
			cr = float64(completed) / float64(started) * 100
		}
		out = append(out, row{qid, title, cr, hi, mid, lo})
	}
	middleware.JSONWithMeta(w, http.StatusOK, out, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// GET /api/v1/teacher/reports/student-performance
func (h *Handler) StudentPerformanceReport(w http.ResponseWriter, r *http.Request) {
	teacherID := middleware.GetUserID(r)
	instID := middleware.GetInstitutionID(r)
	q := r.URL.Query()
	classID := q.Get("class_id")

	args := []interface{}{instID, teacherID}
	where := `u.institution_id=$1 AND u.role='student' AND u.deleted_at IS NULL`
	n := 3

	if classID != "" {
		// Verify teacher is assigned to that class.
		where += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM group_students gs WHERE gs.user_id=u.id AND gs.group_id=$%d)
			AND EXISTS (SELECT 1 FROM group_teachers gt WHERE gt.group_id=$%d AND gt.user_id=$2)`, n, n)
		args = append(args, classID)
		n++
	} else if h.hasGroupAssignments(r, teacherID) {
		where += ` AND EXISTS (
			SELECT 1 FROM group_students gs
			JOIN group_teachers gt ON gt.group_id = gs.group_id
			WHERE gs.user_id=u.id AND gt.user_id=$2
		)`
	}

	dateClause := ""
	if df := q.Get("date_from"); df != "" {
		dateClause += fmt.Sprintf(" AND qa.completed_at >= $%d", n)
		args = append(args, df)
		n++
	}
	if dt := q.Get("date_to"); dt != "" {
		dateClause += fmt.Sprintf(" AND qa.completed_at <= $%d", n)
		args = append(args, dt)
		n++
	}

	sql := `SELECT u.id, u.display_name, u.total_points, u.current_streak,
	        COUNT(qa.id) FILTER (WHERE qa.status='completed'` + dateClause + `) AS quizzes_taken,
	        COALESCE(AVG(qa.score_pct) FILTER (WHERE qa.status='completed'` + dateClause + `),0) AS average_score
	 FROM users u
	 LEFT JOIN quiz_attempts qa ON qa.user_id=u.id
	 LEFT JOIN quizzes qz ON qz.id=qa.quiz_id AND qz.created_by=$2
	 WHERE ` + where + `
	 GROUP BY u.id, u.display_name, u.total_points, u.current_streak
	 ORDER BY average_score DESC, u.display_name`

	rows, err := h.db.Query(r.Context(), sql, args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type row struct {
		ID            string  `json:"id"`
		DisplayName   string  `json:"display_name"`
		TotalPoints   int64   `json:"total_points"`
		CurrentStreak int     `json:"current_streak"`
		QuizzesTaken  int     `json:"quizzes_taken"`
		AverageScore  float64 `json:"average_score"`
	}
	out := []row{}
	for rows.Next() {
		var rr row
		rows.Scan(&rr.ID, &rr.DisplayName, &rr.TotalPoints, &rr.CurrentStreak, &rr.QuizzesTaken, &rr.AverageScore)
		out = append(out, rr)
	}
	middleware.JSON(w, http.StatusOK, out)
}
