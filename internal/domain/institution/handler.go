package institution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/auth"
	"github.com/qwish/backend/internal/domain/enrollment"
	"github.com/qwish/backend/internal/domain/notification"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	db         *pgxpool.Pool
	notif      *notification.Service
	enrollment *enrollment.Service
	appURL     string
	teacherURL string // teacher panel base; used in teacher-verified emails
}

func NewHandler(db *pgxpool.Pool, notif *notification.Service, enr *enrollment.Service, appURL, teacherURL string) *Handler {
	return &Handler{db: db, notif: notif, enrollment: enr, appURL: appURL, teacherURL: teacherURL}
}

// GET /api/v1/institution/overview
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)

	var totalStudents, activeStudents, totalTeachers, totalQuizzes int
	var avgScore float64
	var topStudentName string
	var topStudentPoints int64
	var topStudentID *string
	// Six independent aggregates folded into one round-trip.
	h.db.QueryRow(r.Context(), `SELECT
		(SELECT COUNT(*) FROM enrollments e LEFT JOIN users u ON u.id=e.user_id WHERE e.institution_id=$1 AND e.status IN ('pending_claim','active','suspended') AND (e.user_id IS NULL OR (u.role='student' AND u.deleted_at IS NULL))),
		(SELECT COUNT(DISTINCT qa.user_id) FROM quiz_attempts qa JOIN users u ON u.id=qa.user_id
		 WHERE u.institution_id=$1 AND u.role='student' AND u.deleted_at IS NULL AND qa.completed_at >= CURRENT_DATE - 7),
		(SELECT COUNT(*) FROM users WHERE institution_id=$1 AND role='teacher' AND status='active'),
		(SELECT COUNT(*) FROM quizzes WHERE institution_id=$1 AND status='published'),
		(SELECT COALESCE(AVG(qa.score_pct),0) FROM quiz_attempts qa JOIN users u ON u.id=qa.user_id
		 WHERE u.institution_id=$1 AND u.role='student' AND u.deleted_at IS NULL AND qa.status='completed' AND qa.completed_at >= CURRENT_DATE - 30),
		COALESCE((SELECT display_name FROM users WHERE institution_id=$1 AND role='student' AND status='active' ORDER BY total_points DESC LIMIT 1), ''),
		COALESCE((SELECT total_points FROM users WHERE institution_id=$1 AND role='student' AND status='active' ORDER BY total_points DESC LIMIT 1), 0),
		(SELECT id::text FROM users WHERE institution_id=$1 AND role='student' AND status='active' ORDER BY total_points DESC LIMIT 1)`,
		instID,
	).Scan(&totalStudents, &activeStudents, &totalTeachers, &totalQuizzes, &avgScore, &topStudentName, &topStudentPoints, &topStudentID)

	// The top student's last 30 days here, and the class to show beside them.
	topStudent := map[string]interface{}{"name": topStudentName, "points": topStudentPoints}
	if topStudentID != nil {
		var tsAvg *float64
		var tsQuizzes int
		var tsClass *string
		h.db.QueryRow(r.Context(), `SELECT
			(SELECT AVG(qa.score_pct) FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id
			  WHERE qa.user_id=$1 AND q.institution_id=$2 AND qa.status='completed' AND qa.completed_at >= CURRENT_DATE - 30),
			(SELECT COUNT(*) FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id
			  WHERE qa.user_id=$1 AND q.institution_id=$2 AND qa.status='completed' AND qa.completed_at >= CURRENT_DATE - 30),
			(SELECT g.name FROM groups g JOIN group_students gs ON gs.group_id=g.id
			  WHERE gs.user_id=$1 AND g.institution_id=$2 AND g.archived_at IS NULL ORDER BY g.name LIMIT 1)`,
			*topStudentID, instID).Scan(&tsAvg, &tsQuizzes, &tsClass)
		topStudent["id"] = *topStudentID
		topStudent["average_score_30d"] = tsAvg
		topStudent["quizzes_30d"] = tsQuizzes
		topStudent["class_name"] = tsClass
	}

	// Work waiting on the institution: drives the overview strip and sidebar counts.
	var pendingAdmissions, pendingEdits, unclaimed int
	var oldestAdmission *time.Time
	h.db.QueryRow(r.Context(), `SELECT
		(SELECT COUNT(*) FROM admission_requests WHERE institution_id=$1 AND status='pending'),
		(SELECT MIN(created_at) FROM admission_requests WHERE institution_id=$1 AND status='pending'),
		(SELECT COUNT(*) FROM student_edit_requests sr JOIN enrollments e ON e.id=sr.enrollment_id WHERE e.institution_id=$1 AND sr.status='pending'),
		(SELECT COUNT(*) FROM enrollments WHERE institution_id=$1 AND status='pending_claim')`,
		instID,
	).Scan(&pendingAdmissions, &oldestAdmission, &pendingEdits, &unclaimed)

	// Activity chart: quizzes completed per day over last 30 days
	rows, _ := h.db.Query(r.Context(),
		`SELECT DATE(qa.completed_at) as day, COUNT(*)
		 FROM quiz_attempts qa JOIN users u ON u.id=qa.user_id
		 WHERE u.institution_id=$1 AND u.role='student' AND u.deleted_at IS NULL AND qa.completed_at >= CURRENT_DATE - 30 AND qa.status='completed'
		 GROUP BY day ORDER BY day`, instID)
	defer rows.Close()
	type dayCount struct {
		Day   string `json:"day"`
		Count int    `json:"count"`
	}
	chart := []dayCount{}
	for rows.Next() {
		var dc dayCount
		rows.Scan(&dc.Day, &dc.Count)
		chart = append(chart, dc)
	}

	// Top 5 quizzes by completion
	qrows, _ := h.db.Query(r.Context(),
		`SELECT q.id, q.title, q.type, COALESCE(NULLIF(t.display_name,''), t.full_name, ''), COUNT(qa.id) as completions
		 FROM quizzes q LEFT JOIN quiz_attempts qa ON qa.quiz_id=q.id AND qa.status='completed'
		 LEFT JOIN users t ON t.id=q.created_by
		 WHERE q.institution_id=$1
		 GROUP BY q.id, q.title, q.type, t.display_name, t.full_name ORDER BY completions DESC LIMIT 5`, instID)
	defer qrows.Close()
	type topQuiz struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Type        string `json:"type"`
		TeacherName string `json:"teacher_name"`
		Completions int    `json:"completions"`
	}
	topQuizzes := []topQuiz{}
	for qrows.Next() {
		var tq topQuiz
		qrows.Scan(&tq.ID, &tq.Title, &tq.Type, &tq.TeacherName, &tq.Completions)
		topQuizzes = append(topQuizzes, tq)
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"total_students":  totalStudents,
		"active_students": activeStudents,
		"total_teachers":  totalTeachers,
		"total_quizzes":   totalQuizzes,
		"average_score":   avgScore,
		"top_student":     topStudent,
		// Completed attempts in the last 30 days, the window the dashboard labels.
		"average_score_window_days": 30,
		"activity_chart":            chart,
		"top_quizzes":               topQuizzes,
		"pending": map[string]interface{}{
			"admissions":            pendingAdmissions,
			"oldest_admission_at":   oldestAdmission,
			"edit_requests":         pendingEdits,
			"unclaimed_enrollments": unclaimed,
		},
	})
}

// GET /api/v1/institution/students
// avgScoreExpr is a student's mean completed-quiz score inside this
// institution. Shared between the SELECT list and the score filters so a
// threshold can never disagree with the number shown next to it.
//
// The completed_at >= joined_at clause is what keeps a transferred-in student's
// previous school's attempts out of this institution's numbers.
const avgScoreExpr = `COALESCE((SELECT AVG(qa.score_pct) FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id
 WHERE qa.user_id=e.user_id AND qa.status='completed' AND q.institution_id=e.institution_id
 AND qa.completed_at >= COALESCE(e.joined_at, '-infinity'::timestamptz)
 AND (e.ended_at IS NULL OR qa.completed_at<=e.ended_at)),0)`

// attemptsExpr counts the same attempts avgScoreExpr averages, so a client can
// tell "no attempts" from a real 0% average.
const attemptsExpr = `(SELECT COUNT(*) FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id
 WHERE qa.user_id=e.user_id AND qa.status='completed' AND q.institution_id=e.institution_id
 AND qa.completed_at >= COALESCE(e.joined_at, '-infinity'::timestamptz)
 AND (e.ended_at IS NULL OR qa.completed_at<=e.ended_at))`

func (h *Handler) ListStudents(w http.ResponseWriter, r *http.Request) {
	h.listStudents(w, r, middleware.GetInstitutionID(r))
}

// ListStudentsForAdmin serves the same enrollment roster to the platform console.
// This handler is registered only inside the platform-admin route group.
func (h *Handler) ListStudentsForAdmin(w http.ResponseWriter, r *http.Request) {
	instID := chi.URLParam(r, "institutionId")
	if _, err := uuid.Parse(instID); err != nil {
		middleware.BadRequest(w, "invalid institution id")
		return
	}
	h.listStudents(w, r, instID)
}

// studentListWhere is the roster filter shared by the paged list and the
// "select all matching" ids endpoint, so both always agree on who matches.
func studentListWhere(instID string, q url.Values) (string, []interface{}) {
	search := q.Get("search")
	groupID := q.Get("group_id")
	status := q.Get("status")

	// Students are listed through their live enrollment: unclaimed roster rows
	// appear (user_id IS NULL). Explicit status filters can include ended rows.
	args := []interface{}{instID}
	where := `e.institution_id=$1 AND (e.user_id IS NULL OR (u.role='student' AND u.deleted_at IS NULL))`
	if status == "" {
		where += ` AND e.status IN ('pending_claim','active','suspended')`
	}
	n := 2
	if search != "" {
		where += fmt.Sprintf(` AND (COALESCE(NULLIF(u.full_name,''), NULLIF(u.display_name,''), e.full_name) ILIKE $%d OR COALESCE(u.email, e.email) ILIKE $%d)`, n, n)
		args = append(args, "%"+search+"%")
		n++
	}
	if status != "" {
		where += fmt.Sprintf(` AND e.status=$%d`, n)
		args = append(args, status)
		n++
	}
	if groupID != "" {
		where += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM group_students gs WHERE gs.user_id=e.user_id AND gs.group_id=$%d)`, n)
		args = append(args, groupID)
		n++
	}
	// Grade and section filter here rather than in the browser because the
	// endpoint paginates: a client-side filter would narrow one page, not the
	// roster.
	if grade := q.Get("grade"); grade != "" {
		where += fmt.Sprintf(` AND e.grade=$%d`, n)
		args = append(args, grade)
		n++
	}
	if section := q.Get("section"); section != "" {
		where += fmt.Sprintf(` AND e.section=$%d`, n)
		args = append(args, section)
		n++
	}
	// Score and activity thresholds. The promotion flow needs to ask "who in
	// this class is under 40%", which sorting cannot answer across pages.
	if v := q.Get("min_score"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			where += fmt.Sprintf(` AND %s >= $%d`, avgScoreExpr, n)
			args = append(args, f)
			n++
		}
	}
	if v := q.Get("max_score"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			where += fmt.Sprintf(` AND %s <= $%d`, avgScoreExpr, n)
			args = append(args, f)
			n++
		}
	}
	// "Nothing for 30 days" must include a student who has never been active at
	// all, which a bare last_active_at comparison would drop.
	if v := q.Get("inactive_days"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 {
			where += fmt.Sprintf(` AND (u.last_active_at IS NULL OR u.last_active_at < now() - ($%d || ' days')::interval)`, n)
			args = append(args, strconv.Itoa(d))
			n++
		}
	}

	_ = n
	return where, args
}

func (h *Handler) listStudents(w http.ResponseWriter, r *http.Request, instID string) {
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

	where, args := studentListWhere(instID, q)
	n := len(args) + 1

	var total int
	if err := h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM enrollments e LEFT JOIN users u ON u.id = e.user_id WHERE `+where,
		args...).Scan(&total); err != nil {
		middleware.InternalError(w)
		return
	}

	sortCol := "COALESCE(NULLIF(u.full_name,''), NULLIF(u.display_name,''), e.full_name)"
	switch q.Get("sort") {
	case "total_points":
		sortCol = "u.total_points DESC"
	case "average_score":
		sortCol = "avg_score DESC"
	case "last_active":
		sortCol = "u.last_active_at DESC NULLS LAST"
	}

	args = append(args, limit, offset)
	// The completed_at >= joined_at clause is what keeps a transferred-in
	// student's previous school's attempts out of this institution's numbers.
	rows, err := h.db.Query(r.Context(),
		`SELECT e.id, e.user_id, COALESCE(NULLIF(u.full_name,''), NULLIF(u.display_name,''), e.full_name), COALESCE(u.email, e.email, ''),
		        e.roll_number, e.grade, e.section, e.status,
		        COALESCE(u.total_points,0), COALESCE(u.current_streak,0), u.last_active_at,
		        `+avgScoreExpr+` AS avg_score,
		        `+attemptsExpr+` AS attempts_count,
		        CASE WHEN e.status='pending_claim' THEN e.claim_code END,
		        COALESCE((SELECT json_agg(json_build_object('id', g.id, 'name', g.name))
		                    FROM group_students gs JOIN groups g ON g.id = gs.group_id
		                   WHERE gs.user_id = e.user_id AND g.institution_id=e.institution_id AND g.archived_at IS NULL), '[]'::json) AS groups
		   FROM enrollments e LEFT JOIN users u ON u.id = e.user_id
		  WHERE `+where+` ORDER BY `+sortCol+`, e.id`+fmt.Sprintf(` LIMIT $%d OFFSET $%d`, n, n+1),
		args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type groupRef struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type studentRow struct {
		EnrollmentID  string     `json:"enrollment_id"`
		ID            *string    `json:"id"` // null until the roster row is claimed
		DisplayName   string     `json:"display_name"`
		Email         string     `json:"email"`
		RollNumber    *string    `json:"roll_number,omitempty"`
		Grade         *string    `json:"grade,omitempty"`
		Section       *string    `json:"section,omitempty"`
		Status        string     `json:"status"`
		TotalPoints   int64      `json:"total_points"`
		CurrentStreak int        `json:"current_streak"`
		LastActiveAt  *time.Time `json:"last_active_at,omitempty"`
		AverageScore  float64    `json:"average_score"`
		// Completed attempts behind average_score; 0 means "no attempts", not 0%.
		AttemptsCount int `json:"attempts_count"`
		// Only a pending_claim row has a live code. Claimed rows carry NULL,
		// which is what stops the roster screen offering a code to copy.
		ClaimCode *string    `json:"claim_code"`
		Groups    []groupRef `json:"groups"`
	}
	var students []studentRow
	for rows.Next() {
		var s studentRow
		if err := rows.Scan(&s.EnrollmentID, &s.ID, &s.DisplayName, &s.Email, &s.RollNumber, &s.Grade,
			&s.Section, &s.Status, &s.TotalPoints, &s.CurrentStreak, &s.LastActiveAt, &s.AverageScore,
			&s.AttemptsCount, &s.ClaimCode, &s.Groups); err != nil {
			middleware.InternalError(w)
			return
		}
		students = append(students, s)
	}
	if err := rows.Err(); err != nil {
		middleware.InternalError(w)
		return
	}
	if students == nil {
		students = []studentRow{}
	}
	middleware.JSONWithMeta(w, http.StatusOK, students, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// GET /api/v1/institution/students/ids — every enrollment matching the roster
// filters (same query params as the list), for "select all matching" across
// pages. Capped: a selection larger than this is a job for an import.
func (h *Handler) ListStudentIDs(w http.ResponseWriter, r *http.Request) {
	const max = 5000
	where, args := studentListWhere(middleware.GetInstitutionID(r), r.URL.Query())
	rows, err := h.db.Query(r.Context(),
		`SELECT e.id, e.user_id, COALESCE(NULLIF(u.full_name,''), NULLIF(u.display_name,''), e.full_name), e.status
		   FROM enrollments e LEFT JOIN users u ON u.id = e.user_id
		  WHERE `+where+fmt.Sprintf(` ORDER BY e.id LIMIT %d`, max+1), args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type ref struct {
		EnrollmentID string  `json:"enrollment_id"`
		UserID       *string `json:"user_id"`
		DisplayName  string  `json:"display_name"`
		Status       string  `json:"status"`
	}
	out := []ref{}
	for rows.Next() {
		var x ref
		rows.Scan(&x.EnrollmentID, &x.UserID, &x.DisplayName, &x.Status)
		out = append(out, x)
	}
	truncated := len(out) > max
	if truncated {
		out = out[:max]
	}
	middleware.JSON(w, http.StatusOK, map[string]interface{}{"students": out, "truncated": truncated})
}

// GET /api/v1/institution/students/:userId
func (h *Handler) GetStudent(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	studentID := chi.URLParam(r, "userId")

	var check int
	h.db.QueryRow(r.Context(), `SELECT 1 FROM users u JOIN enrollments e ON e.user_id=u.id WHERE u.id=$1 AND e.institution_id=$2 AND u.role='student' AND u.deleted_at IS NULL AND e.status IN ('active','suspended')`, studentID, instID).Scan(&check)
	if check == 0 {
		middleware.NotFound(w, "student")
		return
	}

	// Summary
	var displayName, email, status string
	var points int64
	var streak, longestStreak int
	var avgScore float64
	var quizCount int
	var memberSince time.Time
	var guardianName, guardianPhone *string
	h.db.QueryRow(r.Context(),
		`SELECT display_name, email, status, total_points, current_streak, COALESCE(longest_streak,0), member_since,
		        NULLIF(guardian_name,''), NULLIF(guardian_phone,'')
		   FROM users WHERE id=$1`, studentID,
	).Scan(&displayName, &email, &status, &points, &streak, &longestStreak, &memberSince, &guardianName, &guardianPhone)
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*), COALESCE(AVG(qa.score_pct),0) FROM quiz_attempts qa
 JOIN quizzes q ON q.id=qa.quiz_id JOIN enrollments e ON e.user_id=qa.user_id AND e.institution_id=$2 AND e.status IN ('active','suspended')
 WHERE qa.user_id=$1 AND qa.status='completed' AND q.institution_id=$2
 AND qa.completed_at>=COALESCE(e.joined_at,'-infinity'::timestamptz)`, studentID, instID,
	).Scan(&quizCount, &avgScore)

	// Quiz history, paged. The same window as the summary above so the two agree.
	histLimit, _ := strconv.Atoi(r.URL.Query().Get("history_limit"))
	if histLimit < 1 || histLimit > 50 {
		histLimit = 20
	}
	histOffset, _ := strconv.Atoi(r.URL.Query().Get("history_offset"))
	if histOffset < 0 {
		histOffset = 0
	}
	rows, _ := h.db.Query(r.Context(),
		`SELECT qa.id, q.id, q.title, q.type, COALESCE(qa.score_pct,0), COALESCE(qa.points_delta,0), qa.completed_at,
		        CASE WHEN qa.started_at IS NOT NULL THEN (EXTRACT(EPOCH FROM (qa.completed_at - qa.started_at))*1000)::BIGINT END
		 FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id
		 WHERE qa.user_id=$1 AND qa.status='completed' AND q.institution_id=$2
 AND qa.completed_at>=(SELECT COALESCE(e.joined_at,'-infinity'::timestamptz) FROM enrollments e WHERE e.user_id=$1 AND e.institution_id=$2 AND e.status IN ('active','suspended'))
 ORDER BY qa.completed_at DESC LIMIT $3 OFFSET $4`, studentID, instID, histLimit, histOffset)
	defer rows.Close()
	type attempt struct {
		ID          string     `json:"id"`
		QuizID      string     `json:"quiz_id"`
		QuizTitle   string     `json:"quiz_title"`
		QuizType    string     `json:"quiz_type"`
		ScorePct    float64    `json:"score_pct"`
		PointsDelta int64      `json:"points_delta"`
		CompletedAt *time.Time `json:"completed_at"`
		TimeTakenMs *int64     `json:"time_taken_ms"`
	}
	attempts := []attempt{}
	for rows.Next() {
		var a attempt
		rows.Scan(&a.ID, &a.QuizID, &a.QuizTitle, &a.QuizType, &a.ScorePct, &a.PointsDelta, &a.CompletedAt, &a.TimeTakenMs)
		attempts = append(attempts, a)
	}

	// Points ledger, most recent first. Points are platform-wide, so this is
	// the student's whole ledger, not an institution slice.
	ledger := []map[string]interface{}{}
	lRows, _ := h.db.Query(r.Context(),
		`SELECT id, amount, reason, reference_id, balance_after, expires_at, created_at
		   FROM points_ledger WHERE user_id=$1 ORDER BY created_at DESC LIMIT 50`, studentID)
	defer lRows.Close()
	for lRows.Next() {
		var id, reason string
		var ref *string
		var amount, balance int64
		var expires *time.Time
		var created time.Time
		if lRows.Scan(&id, &amount, &reason, &ref, &balance, &expires, &created) == nil {
			ledger = append(ledger, map[string]interface{}{
				"id": id, "amount": amount, "reason": reason, "reference_id": ref,
				"balance_after": balance, "expires_at": expires, "created_at": created,
			})
		}
	}

	// Groups, with who teaches each.
	gRows, _ := h.db.Query(r.Context(),
		`SELECT g.id, g.name,
		        COALESCE((SELECT array_agg(COALESCE(NULLIF(t.display_name,''), t.full_name) ORDER BY t.display_name)
		                    FROM group_teachers gt JOIN users t ON t.id=gt.user_id
		                   WHERE gt.group_id=g.id AND t.deleted_at IS NULL), '{}')
		   FROM groups g JOIN group_students gs ON gs.group_id=g.id
		  WHERE gs.user_id=$1 AND g.institution_id=$2 AND g.archived_at IS NULL`, studentID, instID)
	defer gRows.Close()
	type group struct {
		ID           string   `json:"id"`
		Name         string   `json:"name"`
		TeacherNames []string `json:"teacher_names"`
	}
	groups := []group{}
	for gRows.Next() {
		var g group
		gRows.Scan(&g.ID, &g.Name, &g.TeacherNames)
		groups = append(groups, g)
	}

	// The live enrollment. Without it this page cannot address the enrollment
	// at all, and PATCH /enrollments/{id} replaces every column it is sent —
	// so admission_date has to come back here or an edit would blank it.
	var enrollmentID, enrollmentStatus string
	var rollNumber, grade, section *string
	var admissionDate *time.Time
	h.db.QueryRow(r.Context(),
		`SELECT id, status, roll_number, grade, section, admission_date FROM enrollments
		  WHERE user_id=$1 AND institution_id=$2 AND status IN ('active','suspended')`,
		studentID, instID).Scan(&enrollmentID, &enrollmentStatus, &rollNumber, &grade, &section, &admissionDate)

	var admissionDateStr *string
	if admissionDate != nil {
		s := admissionDate.Format("2006-01-02")
		admissionDateStr = &s
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"id": studentID, "display_name": displayName, "email": email, "status": status,
		"total_points": points, "current_streak": streak, "longest_streak": longestStreak,
		"average_score": avgScore, "quizzes_taken": quizCount, "member_since": memberSince,
		"quiz_history": attempts, "history_limit": histLimit, "history_offset": histOffset,
		"points_ledger": ledger, "groups": groups,
		"guardian_name": guardianName, "guardian_phone": maskPhone(guardianPhone),
		"enrollment_id": enrollmentID, "enrollment_status": enrollmentStatus,
		"roll_number": rollNumber, "grade": grade, "section": section,
		"admission_date": admissionDateStr,
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
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request")
		return
	}

	newStatus := "active"
	if req.Action == "suspend" {
		newStatus = "suspended"
	}

	// The enrollment is the source of truth; the service mirrors users.status,
	// which is what actually blocks login.
	var enrollmentID string
	h.db.QueryRow(r.Context(),
		`SELECT id FROM enrollments
		  WHERE user_id=$1 AND institution_id=$2 AND status IN ('active','suspended')`,
		studentID, instID).Scan(&enrollmentID)
	if enrollmentID == "" {
		middleware.NotFound(w, "student")
		return
	}
	if err := h.enrollment.SetStatus(r.Context(), instID, enrollmentID, newStatus); err != nil {
		middleware.InternalError(w)
		return
	}
	if req.Reason != "" {
		h.db.Exec(r.Context(),
			`UPDATE users SET suspension_reason=$1 WHERE id=$2`, req.Reason, studentID)
	}

	// Audit log
	logAuditInst(r.Context(), h.db, adminID, middleware.GetInstitutionID(r), req.Action+"_student", "user", studentID, req.Reason)
	_ = adminID
	middleware.JSON(w, http.StatusOK, map[string]string{"status": newStatus})
}

// GET /api/v1/institution/teachers
func (h *Handler) ListTeachers(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
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

	where := `u.institution_id=$1 AND u.role='teacher' AND u.deleted_at IS NULL`
	args := []interface{}{instID}
	switch status := q.Get("status"); status {
	case "":
	case "active", "pending", "suspended":
		where += fmt.Sprintf(` AND u.status=$%d`, len(args)+1)
		args = append(args, status)
	case "needs_action":
		where += ` AND u.status IN ('pending','suspended')`
	default:
		middleware.BadRequest(w, "status must be active, pending, suspended or needs_action")
		return
	}
	if search := strings.TrimSpace(q.Get("search")); search != "" {
		where += fmt.Sprintf(` AND (u.display_name ILIKE $%[1]d OR u.email ILIKE $%[1]d)`, len(args)+1)
		args = append(args, "%"+search+"%")
	}

	var total int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM users u WHERE `+where, args...).Scan(&total)

	n := len(args)
	rows, err := h.db.Query(r.Context(),
		`SELECT u.id, u.display_name, u.email, u.last_active_at, u.status, u.verified_at,
		        (SELECT COUNT(*) FROM quizzes q WHERE q.created_by=u.id AND q.deleted_at IS NULL) AS quiz_count,
		        (SELECT COUNT(*) FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id
		          WHERE q.created_by=u.id AND q.deleted_at IS NULL AND qa.status='completed') AS attempt_count,
		        COALESCE((SELECT json_agg(json_build_object('id', g.id, 'name', g.name) ORDER BY g.name)
		                    FROM group_teachers gt JOIN groups g ON g.id=gt.group_id
		                   WHERE gt.user_id=u.id AND g.archived_at IS NULL), '[]'::json)
		 FROM users u
		 WHERE `+where+fmt.Sprintf(` ORDER BY u.display_name, u.id LIMIT $%d OFFSET $%d`, n+1, n+2),
		append(args, limit, offset)...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type groupRef struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type teacherRow struct {
		ID           string     `json:"id"`
		DisplayName  string     `json:"display_name"`
		Email        string     `json:"email"`
		LastActiveAt *time.Time `json:"last_active_at,omitempty"`
		Status       string     `json:"status"`
		VerifiedAt   *time.Time `json:"verified_at"`
		QuizCount    int        `json:"quiz_count"`
		AttemptCount int        `json:"attempt_count"`
		Groups       []groupRef `json:"groups"`
	}
	teachers := []teacherRow{}
	for rows.Next() {
		var t teacherRow
		rows.Scan(&t.ID, &t.DisplayName, &t.Email, &t.LastActiveAt, &t.Status, &t.VerifiedAt, &t.QuizCount, &t.AttemptCount, &t.Groups)
		teachers = append(teachers, t)
	}
	middleware.JSONWithMeta(w, http.StatusOK, teachers, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// GET /api/v1/institution/teachers/counts — totals for the filter pills.
// Pending invitations are counted separately: they aren't accounts yet.
func (h *Handler) TeacherCounts(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	var all, active, pending, suspended, invited int
	h.db.QueryRow(r.Context(), `SELECT
		COUNT(*), COUNT(*) FILTER (WHERE status='active'), COUNT(*) FILTER (WHERE status='pending'),
		COUNT(*) FILTER (WHERE status='suspended'),
		(SELECT COUNT(*) FROM teacher_invites WHERE institution_id=$1 AND status='pending' AND expires_at > now())
		FROM users WHERE institution_id=$1 AND role='teacher' AND deleted_at IS NULL`, instID).
		Scan(&all, &active, &pending, &suspended, &invited)
	middleware.JSON(w, http.StatusOK, map[string]int{
		"all": all, "active": active, "pending": pending, "suspended": suspended,
		"invited": invited, "needs_action": pending + suspended,
	})
}

// GET /api/v1/institution/teachers/invites — open invitations, newest first.
func (h *Handler) ListTeacherInvites(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(),
		`SELECT id, email, COALESCE(name,''), created_at, expires_at FROM teacher_invites
		  WHERE institution_id=$1 AND status='pending' AND expires_at > now()
		  ORDER BY created_at DESC LIMIT 200`, middleware.GetInstitutionID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type invite struct {
		ID        string    `json:"id"`
		Email     string    `json:"email"`
		Name      string    `json:"name"`
		InvitedAt time.Time `json:"invited_at"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	out := []invite{}
	for rows.Next() {
		var i invite
		rows.Scan(&i.ID, &i.Email, &i.Name, &i.InvitedAt, &i.ExpiresAt)
		out = append(out, i)
	}
	middleware.JSON(w, http.StatusOK, out)
}

// POST /api/v1/institution/teachers/invites/{inviteId}/resend
// Sends the same invitation again and restarts its 7-day window. The token is
// kept, so a link from the first email still works.
func (h *Handler) ResendTeacherInvite(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	inviteID := chi.URLParam(r, "inviteId")
	var email, name, token string
	var expires time.Time
	err := h.db.QueryRow(r.Context(),
		`UPDATE teacher_invites SET expires_at = now() + INTERVAL '7 days'
		  WHERE id=$1 AND institution_id=$2 AND status='pending'
		  RETURNING email, COALESCE(name,''), token, expires_at`, inviteID, instID).Scan(&email, &name, &token, &expires)
	if err != nil {
		middleware.NotFound(w, "pending invitation")
		return
	}
	var instName string
	h.db.QueryRow(r.Context(), `SELECT name FROM institutions WHERE id=$1`, instID).Scan(&instName)
	if h.notif != nil {
		if err := h.notif.SendTeacherInvite(r.Context(), email, name, instName, token, h.appURL, inviteID); err != nil {
			middleware.Error(w, http.StatusBadGateway, "EMAIL_FAILED", "the invitation couldn't be emailed; try again")
			return
		}
	}
	logAuditInst(r.Context(), h.db, middleware.GetUserID(r), instID, "invite_teacher", "teacher_invite", inviteID, "resent to "+email)
	middleware.JSON(w, http.StatusOK, map[string]string{"email": email, "expires_at": expires.UTC().Format(time.RFC3339)})
}

// GET /api/v1/institution/teachers/:userId
func (h *Handler) GetTeacher(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	teacherID := chi.URLParam(r, "userId")

	var name, email, status string
	var lastActive, verifiedAt *time.Time
	err := h.db.QueryRow(r.Context(),
		`SELECT COALESCE(NULLIF(display_name,''), full_name), email, status, last_active_at, verified_at
		   FROM users WHERE id=$1 AND institution_id=$2 AND role='teacher' AND deleted_at IS NULL`,
		teacherID, instID).Scan(&name, &email, &status, &lastActive, &verifiedAt)
	if err != nil {
		middleware.NotFound(w, "teacher")
		return
	}

	// Their quizzes with per-quiz completions and averages.
	qrows, _ := h.db.Query(r.Context(),
		`SELECT q.id, q.title, q.type, q.status, q.question_count,
		        COUNT(qa.id) FILTER (WHERE qa.status='completed'),
		        COALESCE(AVG(qa.score_pct) FILTER (WHERE qa.status='completed'),0),
		        q.published_at, q.created_at
		   FROM quizzes q LEFT JOIN quiz_attempts qa ON qa.quiz_id=q.id
		  WHERE q.created_by=$1 AND q.deleted_at IS NULL
		  GROUP BY q.id ORDER BY COALESCE(q.published_at, q.created_at) DESC LIMIT 100`, teacherID)
	defer qrows.Close()
	type quizRow struct {
		ID              string     `json:"id"`
		Title           string     `json:"title"`
		Type            string     `json:"type"`
		Status          string     `json:"status"`
		QuestionCount   int        `json:"question_count"`
		CompletionCount int        `json:"completion_count"`
		AverageScore    float64    `json:"average_score"`
		PublishedAt     *time.Time `json:"published_at"`
		CreatedAt       time.Time  `json:"created_at"`
	}
	quizzes := []quizRow{}
	attempts, weighted := 0, 0.0
	for qrows.Next() {
		var q quizRow
		qrows.Scan(&q.ID, &q.Title, &q.Type, &q.Status, &q.QuestionCount, &q.CompletionCount, &q.AverageScore, &q.PublishedAt, &q.CreatedAt)
		attempts += q.CompletionCount
		weighted += q.AverageScore * float64(q.CompletionCount)
		quizzes = append(quizzes, q)
	}
	var avg *float64
	if attempts > 0 {
		v := weighted / float64(attempts)
		avg = &v
	}

	grows, _ := h.db.Query(r.Context(),
		`SELECT g.id, g.name, (SELECT COUNT(*) FROM group_students gs WHERE gs.group_id=g.id)
		   FROM group_teachers gt JOIN groups g ON g.id=gt.group_id
		  WHERE gt.user_id=$1 AND g.institution_id=$2 AND g.archived_at IS NULL ORDER BY g.name`, teacherID, instID)
	defer grows.Close()
	type groupRow struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		StudentCount int    `json:"student_count"`
	}
	groups := []groupRow{}
	for grows.Next() {
		var g groupRow
		grows.Scan(&g.ID, &g.Name, &g.StudentCount)
		groups = append(groups, g)
	}

	// Recent activity: what they published and which topic requests they took on.
	arows, _ := h.db.Query(r.Context(),
		`(SELECT 'Published “' || title || '”', published_at FROM quizzes
		   WHERE created_by=$1 AND deleted_at IS NULL AND published_at IS NOT NULL)
		 UNION ALL
		 (SELECT 'Picked up topic request “' || topic || '”', created_at FROM topic_requests WHERE assigned_to=$1)
		 ORDER BY 2 DESC LIMIT 5`, teacherID)
	defer arows.Close()
	type activity struct {
		Text string    `json:"text"`
		At   time.Time `json:"at"`
	}
	recent := []activity{}
	for arows.Next() {
		var a activity
		arows.Scan(&a.Text, &a.At)
		recent = append(recent, a)
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"id": teacherID, "display_name": name, "full_name": name, "email": email, "status": status,
		"last_active_at": lastActive, "verified_at": verifiedAt,
		"quiz_count": len(quizzes), "attempt_count": attempts, "average_score": avg,
		"quizzes": quizzes, "groups": groups, "recent_activity": recent,
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
	jsonx.NewDecoder(r.Body).Decode(&req)

	var teacherName, teacherEmail, curStatus string
	err := h.db.QueryRow(r.Context(),
		`SELECT display_name, email, status FROM users WHERE id=$1 AND institution_id=$2 AND role='teacher'`,
		teacherID, instID).Scan(&teacherName, &teacherEmail, &curStatus)
	if err != nil {
		middleware.NotFound(w, "teacher")
		return
	}

	newStatus := "active"
	switch req.Action {
	case "suspend":
		newStatus = "suspended"
	case "verify":
		// Only pending teachers can be verified.
		if curStatus != "pending" {
			middleware.Error(w, http.StatusUnprocessableEntity, "NOT_PENDING",
				"only a teacher awaiting verification can be verified")
			return
		}
		newStatus = "active"
	}

	h.db.Exec(r.Context(), `UPDATE users SET status=$1, updated_at=now(),
		verified_at = CASE WHEN $3 THEN now() ELSE verified_at END WHERE id=$2`, newStatus, teacherID, req.Action == "verify")
	logAuditInst(r.Context(), h.db, middleware.GetUserID(r), middleware.GetInstitutionID(r), req.Action+"_teacher", "user", teacherID, req.Reason)

	// On verification, email the teacher that they can now sign in.
	if req.Action == "verify" && h.notif != nil {
		var instName string
		h.db.QueryRow(r.Context(), `SELECT name FROM institutions WHERE id=$1`, instID).Scan(&instName)
		if mailErr := h.notif.SendTeacherVerified(r.Context(), teacherEmail, teacherName, instName, h.teacherURL+"/login"); mailErr != nil {
			fmt.Printf("[institution] teacher-verified email to %s failed: %v\n", teacherEmail, mailErr)
		}
	}

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
	logAuditInst(r.Context(), h.db, middleware.GetUserID(r), middleware.GetInstitutionID(r), "remove_teacher", "user", teacherID, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "teacher removed from institution"})
}

// POST /api/v1/institution/teachers/invite
func (h *Handler) InviteTeacher(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	loggedInUser := middleware.GetUserID(r)

	var req struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		middleware.BadRequest(w, "email is required")
		return
	}

	req.Email = auth.NormalizeEmail(req.Email)

	// One address is one Qwish account. The old check here only looked for a
	// teacher inside this institution, so an address already registered as a
	// student, as a teacher elsewhere, or as a super admin still got an invite
	// — one that could only dead-end when they tried to accept it.
	if taken := auth.EmailIdentityIn(r.Context(), h.db, req.Email); taken != nil {
		middleware.Error(w, http.StatusConflict, "EMAIL_ALREADY_REGISTERED", taken.Human())
		return
	}

	// Reject if there is already a pending invite for this email + institution
	var pendingID string
	h.db.QueryRow(r.Context(),
		`SELECT id FROM teacher_invites
		 WHERE lower(btrim(email))=$1 AND institution_id=$2 AND status='pending' AND expires_at > now()`,
		req.Email, instID).Scan(&pendingID)
	if pendingID != "" {
		middleware.Error(w, http.StatusConflict, "DUPLICATE_INVITE", "a pending invite for this email already exists")
		return
	}

	// Generate a cryptographically random 32-byte hex token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		middleware.InternalError(w)
		return
	}
	token := hex.EncodeToString(tokenBytes)

	// Fetch institution name for the email
	var instName string
	h.db.QueryRow(r.Context(), `SELECT name FROM institutions WHERE id=$1`, instID).Scan(&instName)

	// Insert the invite record
	var inviteID string
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO teacher_invites (institution_id, invited_by, email, name, token)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		instID, loggedInUser, req.Email, nullableString(req.Name), token,
	).Scan(&inviteID)
	if err != nil {
		middleware.InternalError(w)
		return
	}

	// Send invite email (non-blocking on error — invite is already created)
	if h.notif != nil {
		if err := h.notif.SendTeacherInvite(r.Context(), req.Email, req.Name, instName, token, h.appURL, inviteID); err != nil {
			fmt.Printf("[institution] teacher invite email to %s failed: %v\n", req.Email, err)
		}
	}

	logAuditInst(r.Context(), h.db, loggedInUser, middleware.GetInstitutionID(r), "invite_teacher", "teacher_invite", inviteID, req.Email)
	middleware.JSON(w, http.StatusCreated, map[string]string{
		"message":    "invite sent",
		"invite_id":  inviteID,
		"email":      req.Email,
		"expires_at": time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339),
	})
}

// nullableString returns a *string pointer for DB nullable TEXT columns.
// maskPhone keeps the country prefix, two leading and three trailing digits:
// "+91 98••• ••412". An admin can recognise the number without it being
// exposed on every screen that renders a profile.
func maskPhone(p *string) *string {
	if p == nil {
		return nil
	}
	digits := make([]rune, 0, len(*p))
	for _, c := range *p {
		if c >= '0' && c <= '9' {
			digits = append(digits, c)
		}
	}
	if len(digits) < 6 {
		m := "•••"
		return &m
	}
	prefix := ""
	if len(digits) > 10 {
		prefix = "+" + string(digits[:len(digits)-10]) + " "
		digits = digits[len(digits)-10:]
	}
	n := len(digits)
	out := prefix + string(digits[:2]) + "••• ••" + string(digits[n-3:])
	return &out
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// GET /api/v1/institution/groups
func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	// The 30-day average covers attempts by the class's current students on
	// any of this institution's quizzes; null when there are none.
	rows, err := h.db.Query(r.Context(),
		`SELECT g.id, g.name, g.description, g.invite_code, g.archived_at, g.created_at,
		        (SELECT COUNT(*) FROM group_students gs WHERE gs.group_id=g.id),
		        (SELECT COUNT(*) FROM group_teachers gt WHERE gt.group_id=g.id),
		        COALESCE((SELECT array_agg(COALESCE(NULLIF(t.display_name,''), t.full_name) ORDER BY t.display_name)
		                    FROM group_teachers gt JOIN users t ON t.id=gt.user_id
		                   WHERE gt.group_id=g.id AND t.deleted_at IS NULL), '{}'),
		        (SELECT AVG(qa.score_pct) FROM quiz_attempts qa
		           JOIN group_students gs ON gs.user_id=qa.user_id AND gs.group_id=g.id
		           JOIN quizzes q ON q.id=qa.quiz_id AND q.institution_id=g.institution_id
		          WHERE qa.status='completed' AND qa.completed_at >= CURRENT_DATE - 30),
		        (SELECT json_build_object('version_id', v.id, 'name', c.name, 'label', v.label,
		                                  'subject', v.subject, 'grade', v.grade, 'revision', v.revision,
		                                  'academic_year_name', y.name)
		           FROM class_curricula cc
		           JOIN curriculum_versions v ON v.id=cc.version_id JOIN curricula c ON c.id=v.curriculum_id
		           JOIN academic_years y ON y.id=cc.academic_year_id
		          WHERE cc.group_id=g.id AND cc.ended_at IS NULL AND CURRENT_DATE BETWEEN y.starts_on AND y.ends_on
		          ORDER BY cc.assigned_at DESC LIMIT 1),
		        g.department_id::text
		   FROM groups g WHERE g.institution_id=$1 ORDER BY g.name`, instID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type groupRow struct {
		ID              string                 `json:"id"`
		Name            string                 `json:"name"`
		Description     *string                `json:"description"`
		InviteCode      string                 `json:"invite_code"`
		ArchivedAt      *time.Time             `json:"archived_at"`
		CreatedAt       time.Time              `json:"created_at"`
		StudentCount    int                    `json:"student_count"`
		TeacherCount    int                    `json:"teacher_count"`
		TeacherNames    []string               `json:"teacher_names"`
		AverageScore30d *float64               `json:"average_score_30d"`
		CurrentCurric   map[string]interface{} `json:"current_curriculum"`
		DepartmentID    *string                `json:"department_id"`
	}
	var groups []groupRow
	for rows.Next() {
		var g groupRow
		rows.Scan(&g.ID, &g.Name, &g.Description, &g.InviteCode, &g.ArchivedAt, &g.CreatedAt,
			&g.StudentCount, &g.TeacherCount, &g.TeacherNames, &g.AverageScore30d, &g.CurrentCurric, &g.DepartmentID)
		groups = append(groups, g)
	}
	if groups == nil {
		groups = []groupRow{}
	}
	middleware.JSON(w, http.StatusOK, groups)
}

// POST /api/v1/institution/groups
func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	var req struct {
		Name        string  `json:"name"`
		Description *string `json:"description"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		middleware.BadRequest(w, "name is required")
		return
	}
	inviteCode := generateCode(8)
	var id, name, code string
	var createdAt time.Time
	if err := h.db.QueryRow(r.Context(),
		`INSERT INTO groups (institution_id, name, description, invite_code) VALUES ($1,$2,$3,$4)
		 RETURNING id, name, invite_code, created_at`,
		instID, req.Name, req.Description, inviteCode,
	).Scan(&id, &name, &code, &createdAt); err != nil {
		middleware.InternalError(w)
		return
	}
	logAuditInst(r.Context(), h.db, middleware.GetUserID(r), instID, "create_group", "group", id, req.Name)
	middleware.JSON(w, http.StatusCreated, map[string]interface{}{
		"id": id, "name": name, "invite_code": code, "created_at": createdAt,
	})
}

// GET /api/v1/institution/groups/:groupId
func (h *Handler) GetGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupId")
	instID := middleware.GetInstitutionID(r)

	var name, inviteCode string
	var description *string
	if err := h.db.QueryRow(r.Context(),
		`SELECT name, description, invite_code FROM groups WHERE id=$1 AND institution_id=$2`,
		groupID, instID).Scan(&name, &description, &inviteCode); err != nil {
		middleware.NotFound(w, "group not found")
		return
	}

	var studentCount int
	var avgScore float64
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM group_students WHERE group_id=$1`, groupID).Scan(&studentCount)
	h.db.QueryRow(r.Context(),
		`SELECT COALESCE(AVG(qa.score_pct),0) FROM quiz_attempts qa
		 JOIN group_students gs ON gs.user_id=qa.user_id
		 WHERE gs.group_id=$1 AND qa.status='completed'`, groupID).Scan(&avgScore)

	type studentRow struct {
		EnrollmentID  *string    `json:"enrollment_id"`
		ID            string     `json:"id"`
		DisplayName   string     `json:"display_name"`
		Email         string     `json:"email"`
		Status        string     `json:"status"`
		RollNumber    *string    `json:"roll_number"`
		TotalPoints   int64      `json:"total_points"`
		CurrentStreak int        `json:"current_streak"`
		LastActiveAt  *time.Time `json:"last_active_at"`
		AverageScore  float64    `json:"average_score"`
		// Class-scoped: attempts on quizzes set for this class. Null with no
		// attempts, which is not the same as 0%.
		ClassAverageScore *float64  `json:"class_average_score"`
		ClassAttempts     int       `json:"class_attempts"`
		JoinedAt          time.Time `json:"joined_at"`
	}
	students := []studentRow{}
	srows, err := h.db.Query(r.Context(),
		`SELECT e.id, u.id, COALESCE(u.display_name, ''), COALESCE(u.email, ''),
		        COALESCE(e.status, 'active'), e.roll_number,
		        COALESCE(u.total_points,0), COALESCE(u.current_streak,0), u.last_active_at,
		        COALESCE((SELECT AVG(score_pct) FROM quiz_attempts
		                   WHERE user_id=u.id AND status='completed'),0),
		        (SELECT AVG(qa.score_pct) FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id
		          WHERE qa.user_id=u.id AND qa.status='completed' AND q.group_id=gs.group_id),
		        (SELECT COUNT(*) FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id
		          WHERE qa.user_id=u.id AND qa.status='completed' AND q.group_id=gs.group_id),
		        gs.joined_at
		   FROM group_students gs
		   JOIN users u ON u.id = gs.user_id
		   LEFT JOIN enrollments e ON e.user_id = u.id AND e.institution_id = $2
		  WHERE gs.group_id=$1
		  ORDER BY u.display_name`, groupID, instID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	for srows.Next() {
		var s studentRow
		srows.Scan(&s.EnrollmentID, &s.ID, &s.DisplayName, &s.Email, &s.Status, &s.RollNumber,
			&s.TotalPoints, &s.CurrentStreak, &s.LastActiveAt, &s.AverageScore,
			&s.ClassAverageScore, &s.ClassAttempts, &s.JoinedAt)
		students = append(students, s)
	}
	srows.Close()

	type teacherRow struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Status      string `json:"status"`
	}
	teachers := []teacherRow{}
	trows, err := h.db.Query(r.Context(),
		`SELECT u.id, COALESCE(u.display_name,''), COALESCE(u.email,''), u.status
		   FROM group_teachers gt JOIN users u ON u.id = gt.user_id
		  WHERE gt.group_id=$1 ORDER BY u.display_name`, groupID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	for trows.Next() {
		var t teacherRow
		trows.Scan(&t.ID, &t.DisplayName, &t.Email, &t.Status)
		teachers = append(teachers, t)
	}
	trows.Close()

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"id": groupID, "name": name, "description": description, "invite_code": inviteCode,
		"student_count": studentCount, "average_score": avgScore,
		"students": students, "teachers": teachers,
	})
}

// POST /api/v1/institution/groups/:groupId/students
func (h *Handler) AddStudentToGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupId")
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "user_id is required")
		return
	}
	if _, err := uuid.Parse(req.UserID); err != nil {
		middleware.BadRequest(w, "invalid user_id")
		return
	}
	tag, err := h.db.Exec(r.Context(), `INSERT INTO group_students(group_id,user_id)
 SELECT g.id,u.id FROM groups g JOIN enrollments e ON e.institution_id=g.institution_id
 JOIN users u ON u.id=e.user_id
 WHERE g.id=$1 AND u.id=$2 AND g.institution_id=$3 AND g.archived_at IS NULL
 AND e.status='active' AND u.role='student' AND u.status='active' AND u.deleted_at IS NULL
 ON CONFLICT(group_id,user_id) DO UPDATE SET user_id=EXCLUDED.user_id`, groupID, req.UserID, middleware.GetInstitutionID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.BadRequest(w, "Choose an active student enrolled in this institute and an active class.")
		return
	}
	logAuditInst(r.Context(), h.db, middleware.GetUserID(r), middleware.GetInstitutionID(r), "add_student_to_group", "group", groupID, req.UserID)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "student added to group"})
}

// DELETE /api/v1/institution/groups/:groupId/students/:userId
func (h *Handler) RemoveStudentFromGroup(w http.ResponseWriter, r *http.Request) {
	groupID, userID := chi.URLParam(r, "groupId"), chi.URLParam(r, "userId")
	if _, err := h.db.Exec(r.Context(),
		`DELETE FROM group_students WHERE group_id=$1 AND user_id=$2`, groupID, userID); err != nil {
		middleware.InternalError(w)
		return
	}
	logAuditInst(r.Context(), h.db, middleware.GetUserID(r), middleware.GetInstitutionID(r), "remove_student_from_group", "group", groupID, userID)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "student removed from group"})
}

// POST /api/v1/institution/groups/:groupId/teachers
func (h *Handler) AddTeacherToGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupId")
	var req struct {
		UserID string `json:"user_id"`
	}
	jsonx.NewDecoder(r.Body).Decode(&req)
	if _, err := h.db.Exec(r.Context(),
		`INSERT INTO group_teachers (group_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, groupID, req.UserID); err != nil {
		middleware.InternalError(w)
		return
	}
	logAuditInst(r.Context(), h.db, middleware.GetUserID(r), middleware.GetInstitutionID(r), "add_teacher_to_group", "group", groupID, req.UserID)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "teacher assigned to group"})
}

// DELETE /api/v1/institution/groups/:groupId/teachers/:userId
func (h *Handler) RemoveTeacherFromGroup(w http.ResponseWriter, r *http.Request) {
	groupID, userID := chi.URLParam(r, "groupId"), chi.URLParam(r, "userId")
	if _, err := h.db.Exec(r.Context(),
		`DELETE FROM group_teachers WHERE group_id=$1 AND user_id=$2`, groupID, userID); err != nil {
		middleware.InternalError(w)
		return
	}
	logAuditInst(r.Context(), h.db, middleware.GetUserID(r), middleware.GetInstitutionID(r), "remove_teacher_from_group", "group", groupID, userID)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "teacher removed from group"})
}

// PATCH /api/v1/institution/groups/:groupId
func (h *Handler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupId")
	var req struct {
		Name        string  `json:"name"`
		Description *string `json:"description"`
	}
	jsonx.NewDecoder(r.Body).Decode(&req)
	if _, err := h.db.Exec(r.Context(),
		`UPDATE groups SET name=$1, description=$2 WHERE id=$3`, req.Name, req.Description, groupID); err != nil {
		middleware.InternalError(w)
		return
	}
	logAuditInst(r.Context(), h.db, middleware.GetUserID(r), middleware.GetInstitutionID(r), "update_group", "group", groupID, req.Name)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "group updated"})
}

// DELETE /api/v1/institution/groups/:groupId  (archive)
func (h *Handler) ArchiveGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupId")
	if _, err := h.db.Exec(r.Context(), `UPDATE groups SET archived_at=now() WHERE id=$1`, groupID); err != nil {
		middleware.InternalError(w)
		return
	}
	logAuditInst(r.Context(), h.db, middleware.GetUserID(r), middleware.GetInstitutionID(r), "archive_group", "group", groupID, "")
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "group archived"})
}

// GET /api/v1/institution/settings
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	var name, instType, timezone, sCode, tCode string
	var mult float64
	var graceEnabled, scoreHidden bool
	var expiryMonths int
	// A failed scan used to fall through and serve an all-zero institution:
	// blank name, 0.0x multiplier, empty referral codes. The dashboard would
	// then render those zeros as the institution's real configuration.
	if err := h.db.QueryRow(r.Context(),
		`SELECT name, type, timezone, student_referral_code, teacher_referral_code,
		        point_multiplier, streak_grace_enabled, play_win_score_hidden, point_expiry_months
		 FROM institutions WHERE id=$1`, instID,
	).Scan(&name, &instType, &timezone, &sCode, &tCode, &mult, &graceEnabled, &scoreHidden, &expiryMonths); err != nil {
		middleware.InternalError(w)
		return
	}

	// The open reset request travels with the settings: the referral panel has
	// to know whether this admin has already asked, or it offers a button that
	// the unique index would reject.
	var pending *pendingResetRequest
	var pr pendingResetRequest
	if err := h.db.QueryRow(r.Context(),
		`SELECT id, code_type, reason, created_at
		   FROM referral_code_reset_requests
		  WHERE institution_id=$1 AND status='pending'`, instID,
	).Scan(&pr.ID, &pr.CodeType, &pr.Reason, &pr.CreatedAt); err == nil {
		pending = &pr
	}

	// Verification state for the "Qwish is verifying your institution" banner,
	// and the open Play & Win count a point-rule change should mention.
	var verification string
	var submittedAt time.Time
	var openPlayWin int
	h.db.QueryRow(r.Context(), `SELECT status, created_at,
		(SELECT COUNT(*) FROM quizzes q WHERE q.institution_id=$1 AND q.deleted_at IS NULL AND q.type='play_and_win'
		   AND q.status='published' AND (q.ends_at IS NULL OR q.ends_at > now()))
		FROM institutions WHERE id=$1`, instID).Scan(&verification, &submittedAt, &openPlayWin)
	// A short, stable reference derived from the id, for support conversations.
	reference := "INST-" + strings.ToUpper(strings.ReplaceAll(instID, "-", "")[:6])

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"name": name, "type": instType, "timezone": timezone,
		"verification_status": verification, "submitted_at": submittedAt, "reference": reference,
		"open_play_win_quizzes": openPlayWin,
		"student_referral_code": sCode, "teacher_referral_code": tCode,
		"point_rules": map[string]interface{}{
			"point_multiplier": mult, "streak_grace_enabled": graceEnabled,
			"play_win_score_hidden": scoreHidden, "point_expiry_months": expiryMonths,
		},
		"pending_code_reset": pending,
	})
}

type pendingResetRequest struct {
	ID        string    `json:"id"`
	CodeType  string    `json:"code_type"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// validateResetRequest returns an empty string when the request is fileable, or
// the message to send back. A super admin triaging this queue needs to know
// which code and why; a blank reason makes the request unactionable, so it is
// refused here rather than filed empty.
func validateResetRequest(codeType, reason string) string {
	switch codeType {
	case "student", "teacher", "both":
	default:
		return "code_type must be student, teacher or both"
	}
	if len([]rune(strings.TrimSpace(reason))) < 10 {
		return "tell us why the code needs resetting (at least 10 characters)"
	}
	return ""
}

// POST /api/v1/institution/referral-code-reset-request
//
// Resetting a code is a super-admin action by design, so the institution files
// a request instead of holding the power. One open request at a time.
func (h *Handler) RequestReferralCodeReset(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	adminID := middleware.GetUserID(r)

	var req struct {
		CodeType string `json:"code_type"`
		Reason   string `json:"reason"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request")
		return
	}
	if msg := validateResetRequest(req.CodeType, req.Reason); msg != "" {
		middleware.BadRequest(w, msg)
		return
	}

	var id string
	var createdAt time.Time
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO referral_code_reset_requests (institution_id, requested_by, code_type, reason)
		 VALUES ($1,$2,$3,$4) RETURNING id, created_at`,
		instID, adminID, req.CodeType, strings.TrimSpace(req.Reason),
	).Scan(&id, &createdAt)
	if err != nil {
		// The partial unique index is the only realistic conflict here.
		middleware.BadRequest(w, "a reset request is already open for your institution")
		return
	}

	var instName string
	h.db.QueryRow(r.Context(), `SELECT name FROM institutions WHERE id=$1`, instID).Scan(&instName)

	// Notify the people who can actually action it. Best-effort: the request row
	// is the record of truth, and the super-admin queue reads that table.
	rows, qErr := h.db.Query(r.Context(),
		`SELECT id FROM users WHERE role='super_admin' AND status='active'`)
	if qErr == nil {
		defer rows.Close()
		for rows.Next() {
			var uid string
			if rows.Scan(&uid) == nil {
				h.notif.Emit(r.Context(), uid, "referral_code_reset_requested",
					"Referral code reset requested",
					fmt.Sprintf("%s asked to reset their %s referral code.", instName, req.CodeType))
			}
		}
	}

	logAuditInst(r.Context(), h.db, adminID, instID, "request_referral_code_reset", "institution", instID, req.Reason)
	middleware.JSON(w, http.StatusCreated, pendingResetRequest{
		ID: id, CodeType: req.CodeType, Reason: strings.TrimSpace(req.Reason), CreatedAt: createdAt,
	})
}

// GET /api/v1/institution/setup-checklist
//
// Every item is derived from something that actually happened, so the meter
// cannot congratulate an admin for work they have not done. Nothing here is a
// client-side "dismissed" flag.
func (h *Handler) SetupChecklist(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	adminID := middleware.GetUserID(r)

	var profileConfirmed, rulesReviewed, hasPasskey bool
	var teachers, students int
	if err := h.db.QueryRow(r.Context(), `SELECT
		 EXISTS(SELECT 1 FROM audit_log WHERE target_id=$1 AND action_type='update_settings'),
		 EXISTS(SELECT 1 FROM audit_log WHERE target_id=$1 AND action_type='update_point_rules'),
		 EXISTS(SELECT 1 FROM webauthn_user_credentials WHERE user_id=$2),
		 (SELECT COUNT(*) FROM users WHERE institution_id=$1 AND role='teacher' AND status='active'),
		 (SELECT COUNT(*) FROM users WHERE institution_id=$1 AND role='student' AND status='active')`,
		instID, adminID,
	).Scan(&profileConfirmed, &rulesReviewed, &hasPasskey, &teachers, &students); err != nil {
		middleware.InternalError(w)
		return
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"profile_confirmed":  profileConfirmed,
		"rules_reviewed":     rulesReviewed,
		"passkey_registered": hasPasskey,
		"teachers_joined":    teachers,
		"students_joined":    students,
	})
}

// PATCH /api/v1/institution/settings
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	adminID := middleware.GetUserID(r)
	var req struct {
		Name     string `json:"name"`
		Timezone string `json:"timezone"`
		Type     string `json:"type"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request")
		return
	}
	var before [3]string
	h.db.QueryRow(r.Context(), `SELECT name, COALESCE(timezone,''), COALESCE(type,'') FROM institutions WHERE id=$1`, instID).
		Scan(&before[0], &before[1], &before[2])
	var after [3]string
	if err := h.db.QueryRow(r.Context(),
		`UPDATE institutions SET name=COALESCE(NULLIF($1,''),name), timezone=COALESCE(NULLIF($2,''),timezone), type=COALESCE(NULLIF($3,''),type), updated_at=now() WHERE id=$4
		 RETURNING name, COALESCE(timezone,''), COALESCE(type,'')`,
		req.Name, req.Timezone, req.Type, instID).Scan(&after[0], &after[1], &after[2]); err != nil {
		middleware.InternalError(w)
		return
	}
	// The institution's display name is what every student sees in the app, and
	// its timezone decides when a streak day ends. Both belong in the log for the
	// same reason a suspension does.
	logAuditChange(r.Context(), h.db, adminID, instID, "update_settings", "institution", instID, "",
		map[string]interface{}{"name": before[0], "timezone": before[1], "type": before[2]},
		map[string]interface{}{"name": after[0], "timezone": after[1], "type": after[2]})
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
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request")
		return
	}
	rulesQuery := `SELECT point_multiplier, streak_grace_enabled, play_win_score_hidden, point_expiry_months FROM institutions WHERE id=$1`
	readRules := func() map[string]interface{} {
		var m float64
		var g, hid bool
		var e int
		h.db.QueryRow(r.Context(), rulesQuery, instID).Scan(&m, &g, &hid, &e)
		return map[string]interface{}{"point_multiplier": m, "streak_grace_enabled": g, "play_win_score_hidden": hid, "point_expiry_months": e}
	}
	before := readRules()
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
	logAuditChange(r.Context(), h.db, adminID, instID, "update_point_rules", "institution", instID, "", before, readRules())
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "point rules updated"})
}

// auditLogWhere builds the WHERE clause and args for the institution audit log.
//
// The institution scope is itself an OR (the target is either the institution
// row or one of its users), so it MUST stay parenthesised: without the parens,
// an added `AND action_type = $n` binds to the right-hand branch only and the
// filter silently leaks every institution row into the result.
//
// Invariant, as elsewhere: the next free placeholder is $(len(args)+1).
func auditLogWhere(institutionID, actionType, dateFrom, dateTo string) (string, []interface{}) {
	// institution_id is the answer for anything written since migration 037 and
	// for all backfilled history. The two target_id branches stay for entries
	// written by handlers outside this package, which have no institution to
	// record — dropping them would hide super-admin actions on this institution.
	where := `(al.institution_id = $1` +
		` OR al.target_id = $1` +
		` OR al.target_id IN (SELECT id FROM users WHERE institution_id = $1))`
	args := []interface{}{institutionID}
	if actionType != "" {
		where += fmt.Sprintf(` AND al.action_type = $%d`, len(args)+1)
		args = append(args, actionType)
	}
	if dateFrom != "" {
		where += fmt.Sprintf(` AND al.timestamp >= $%d`, len(args)+1)
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		where += fmt.Sprintf(` AND al.timestamp <= $%d`, len(args)+1)
		args = append(args, dateTo)
	}
	return where, args
}

// GET /api/v1/institution/audit-log
func (h *Handler) AuditLog(w http.ResponseWriter, r *http.Request) {
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

	where, args := auditLogWhere(instID, q.Get("action_type"), q.Get("date_from"), q.Get("date_to"))
	if g := q.Get("action_group"); g != "" {
		actions, ok := auditActionGroups[g]
		if !ok {
			middleware.BadRequest(w, "action_group must be membership, admissions, academics or settings")
			return
		}
		where += fmt.Sprintf(` AND al.action_type = ANY($%d::text[])`, len(args)+1)
		args = append(args, actions)
	}

	var total int
	if err := h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM audit_log al WHERE `+where, args...).Scan(&total); err != nil {
		middleware.InternalError(w)
		return
	}

	n := len(args)
	rows, err := h.db.Query(r.Context(),
		`SELECT al.id, al.timestamp, al.admin_name, al.admin_role, al.action_type, al.target_type, al.target_id, al.reason,
		        `+auditTargetLabel+`, al.old_value, al.new_value
		 FROM audit_log al
		 WHERE `+where+
			fmt.Sprintf(` ORDER BY al.timestamp DESC LIMIT $%d OFFSET $%d`, n+1, n+2),
		append(args, limit, offset)...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
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
		// TargetLabel names the target when it still exists.
		TargetLabel *string `json:"target_label"`
		// Changes lists field-level before/after for entries that recorded them.
		Changes []auditChange `json:"changes,omitempty"`
	}
	var entries []logEntry
	for rows.Next() {
		var e logEntry
		var oldV, newV []byte
		rows.Scan(&e.ID, &e.Timestamp, &e.AdminName, &e.AdminRole, &e.ActionType, &e.TargetType, &e.TargetID, &e.Reason,
			&e.TargetLabel, &oldV, &newV)
		e.Changes = auditChanges(oldV, newV)
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []logEntry{}
	}
	middleware.JSONWithMeta(w, http.StatusOK, entries, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// Reports

// GET /api/v1/institution/reports/student-performance
// Ranked by points, paged, with the period and class applied to the attempts
// behind quizzes_taken and average_score.
func (h *Handler) StudentPerformanceReport(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}

	where := `u.institution_id=$1 AND u.role='student' AND u.status='active' AND u.deleted_at IS NULL`
	args := []interface{}{instID}
	if groupID := q.Get("group_id"); groupID != "" {
		args = append(args, groupID)
		where += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM group_students gs WHERE gs.user_id=u.id AND gs.group_id=$%d)`, len(args))
	}
	// The count uses only the student filter; attempt dates are appended after.
	studentArgs := append([]interface{}{}, args...)
	attemptFilter := `qa.user_id=u.id AND qa.status='completed'`
	if v := q.Get("date_from"); v != "" {
		args = append(args, v)
		attemptFilter += fmt.Sprintf(` AND qa.completed_at >= $%d`, len(args))
	}
	if v := q.Get("date_to"); v != "" {
		args = append(args, v)
		attemptFilter += fmt.Sprintf(` AND qa.completed_at <= $%d`, len(args))
	}

	var total int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM users u WHERE `+where, studentArgs...).Scan(&total)

	n := len(args)
	rows, err := h.db.Query(r.Context(),
		`SELECT u.id, u.display_name, u.total_points, u.current_streak,
		        (SELECT COUNT(*) FROM quiz_attempts qa WHERE `+attemptFilter+`),
		        (SELECT COALESCE(AVG(qa.score_pct),0) FROM quiz_attempts qa WHERE `+attemptFilter+`),
		        COALESCE((SELECT array_agg(g.name ORDER BY g.name) FROM group_students gs JOIN groups g ON g.id=gs.group_id
		                   WHERE gs.user_id=u.id AND g.archived_at IS NULL), '{}')
		 FROM users u WHERE `+where+fmt.Sprintf(` ORDER BY u.total_points DESC, u.id LIMIT $%d OFFSET $%d`, n+1, n+2),
		append(args, limit, (page-1)*limit)...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type row struct {
		ID            string   `json:"id"`
		DisplayName   string   `json:"display_name"`
		TotalPoints   int64    `json:"total_points"`
		CurrentStreak int      `json:"current_streak"`
		QuizzesTaken  int      `json:"quizzes_taken"`
		AverageScore  float64  `json:"average_score"`
		ClassNames    []string `json:"class_names"`
	}
	result := []row{}
	for rows.Next() {
		var rr row
		rows.Scan(&rr.ID, &rr.DisplayName, &rr.TotalPoints, &rr.CurrentStreak, &rr.QuizzesTaken, &rr.AverageScore, &rr.ClassNames)
		result = append(result, rr)
	}
	middleware.JSONWithMeta(w, http.StatusOK, result, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// GET /api/v1/institution/reports/teacher-activity
func (h *Handler) TeacherActivityReport(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
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
	dateFrom := q.Get("date_from")
	dateTo := q.Get("date_to")

	args := []interface{}{instID}
	dateClause := ""
	n := 2
	if dateFrom != "" {
		dateClause += fmt.Sprintf(" AND qa.completed_at >= $%d", n)
		args = append(args, dateFrom)
		n++
	}
	if dateTo != "" {
		dateClause += fmt.Sprintf(" AND qa.completed_at <= $%d", n)
		args = append(args, dateTo)
		n++
	}

	// Teachers of one class, when a class is chosen.
	scope := func(p int) string {
		return fmt.Sprintf(` AND EXISTS (SELECT 1 FROM group_teachers gt WHERE gt.user_id=u.id AND gt.group_id::text=$%d)`, p)
	}
	teacherScope := ""
	countSQL := `SELECT COUNT(*) FROM users u WHERE u.institution_id=$1 AND u.role='teacher' AND u.deleted_at IS NULL`
	countArgs := []interface{}{instID}
	if groupID := q.Get("group_id"); groupID != "" {
		args = append(args, groupID)
		teacherScope = scope(n)
		n++
		countSQL += scope(2)
		countArgs = append(countArgs, groupID)
	}
	var total int
	h.db.QueryRow(r.Context(), countSQL, countArgs...).Scan(&total)

	args = append(args, limit, offset)
	sql := `SELECT u.id, u.display_name,
	        COUNT(DISTINCT q.id) FILTER (WHERE q.deleted_at IS NULL) AS quizzes_created,
	        COUNT(qa.id) FILTER (WHERE qa.status='completed'` + dateClause + `) AS total_attempts,
	        COALESCE(AVG(qa.score_pct) FILTER (WHERE qa.status='completed'` + dateClause + `),0) AS avg_score
	 FROM users u
	 LEFT JOIN quizzes q ON q.created_by=u.id
	 LEFT JOIN quiz_attempts qa ON qa.quiz_id=q.id
	 WHERE u.institution_id=$1 AND u.role='teacher' AND u.deleted_at IS NULL` + teacherScope + `
	 GROUP BY u.id, u.display_name
	 ORDER BY total_attempts DESC, u.display_name
	 LIMIT $` + strconv.Itoa(n) + ` OFFSET $` + strconv.Itoa(n+1)

	rows, err := h.db.Query(r.Context(), sql, args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type row struct {
		TeacherID      string  `json:"teacher_id"`
		DisplayName    string  `json:"display_name"`
		QuizzesCreated int     `json:"quizzes_created"`
		TotalAttempts  int     `json:"total_attempts"`
		AvgScore       float64 `json:"avg_score"`
	}
	out := []row{}
	for rows.Next() {
		var rr row
		rows.Scan(&rr.TeacherID, &rr.DisplayName, &rr.QuizzesCreated, &rr.TotalAttempts, &rr.AvgScore)
		out = append(out, rr)
	}
	middleware.JSONWithMeta(w, http.StatusOK, out, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// GET /api/v1/institution/reports/quiz-analytics
func (h *Handler) QuizAnalyticsReport(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
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
	dateFrom := q.Get("date_from")
	dateTo := q.Get("date_to")

	args := []interface{}{instID}
	dateClause := ""
	n := 2
	if dateFrom != "" {
		dateClause += fmt.Sprintf(" AND qa.started_at >= $%d", n)
		args = append(args, dateFrom)
		n++
	}
	if dateTo != "" {
		dateClause += fmt.Sprintf(" AND qa.started_at <= $%d", n)
		args = append(args, dateTo)
		n++
	}

	quizScope := ""
	var total int
	if groupID := q.Get("group_id"); groupID != "" {
		args = append(args, groupID)
		quizScope = fmt.Sprintf(` AND q.group_id::text=$%d`, n)
		n++
		h.db.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM quizzes q WHERE q.institution_id=$1 AND q.deleted_at IS NULL AND q.group_id::text=$2`, instID, groupID).Scan(&total)
	} else {
		h.db.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM quizzes WHERE institution_id=$1 AND deleted_at IS NULL`, instID).Scan(&total)
	}

	args = append(args, limit, offset)
	sql := `SELECT q.id, q.title,
	        COUNT(qa.id) AS started_count,
	        COUNT(qa.id) FILTER (WHERE qa.status='completed') AS completed_count,
	        COUNT(*) FILTER (WHERE qa.status='completed' AND qa.score_pct >= 80) AS high_band,
	        COUNT(*) FILTER (WHERE qa.status='completed' AND qa.score_pct >= 60 AND qa.score_pct < 80) AS mid_band,
	        COUNT(*) FILTER (WHERE qa.status='completed' AND qa.score_pct < 60) AS low_band
	 FROM quizzes q
	 LEFT JOIN quiz_attempts qa ON qa.quiz_id=q.id` + dateClause + `
	 WHERE q.institution_id=$1 AND q.deleted_at IS NULL` + quizScope + `
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

// GET /api/v1/institution/reports/streak-health
func (h *Handler) StreakHealthReport(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	groupID := r.URL.Query().Get("group_id")
	var active, atRisk, broken, unclaimed int
	h.db.QueryRow(r.Context(),
		`SELECT
		   COUNT(*) FILTER (WHERE current_streak >= 7),
		   COUNT(*) FILTER (WHERE current_streak BETWEEN 1 AND 6),
		   COUNT(*) FILTER (WHERE current_streak = 0),
		   (SELECT COUNT(*) FROM enrollments WHERE institution_id=$1 AND status='pending_claim' AND $2='')
		 FROM users u
		 WHERE u.institution_id=$1 AND u.role='student' AND u.status='active' AND u.deleted_at IS NULL
		   AND ($2='' OR EXISTS (SELECT 1 FROM group_students gs WHERE gs.user_id=u.id AND gs.group_id::text=$2))`,
		instID, groupID).Scan(&active, &atRisk, &broken, &unclaimed)
	// Unclaimed roster records have no account and so no streak; they're
	// reported beside the bands, not inside "broken".
	middleware.JSON(w, http.StatusOK, map[string]int{
		"active": active, "at_risk": atRisk, "broken": broken, "unclaimed": unclaimed,
	})
}

// GET /api/v1/institution/reports/points-summary
func (h *Handler) PointsSummaryReport(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	q := r.URL.Query()
	dateFrom := q.Get("date_from")
	dateTo := q.Get("date_to")

	// Default rolling 30-day window
	args := []interface{}{instID}
	fromClause := "pl.created_at >= CURRENT_DATE - 30"
	toClause := ""
	n := 2
	if dateFrom != "" {
		fromClause = fmt.Sprintf("pl.created_at >= $%d", n)
		args = append(args, dateFrom)
		n++
	}
	if dateTo != "" {
		toClause = fmt.Sprintf(" AND pl.created_at <= $%d", n)
		args = append(args, dateTo)
		n++
	}

	// A chosen class narrows both the trend and the student list.
	groupID := q.Get("group_id")
	inGroup := func(p int) string {
		return fmt.Sprintf(` AND ($%[1]d='' OR EXISTS (SELECT 1 FROM group_students gs WHERE gs.user_id=u.id AND gs.group_id::text=$%[1]d))`, p)
	}
	args = append(args, groupID)
	groupClause := inGroup(n)

	rows, _ := h.db.Query(r.Context(),
		`SELECT DATE(pl.created_at) AS day, COALESCE(SUM(pl.amount),0) AS points_distributed
		 FROM points_ledger pl
		 JOIN users u ON u.id=pl.user_id
		 WHERE u.institution_id=$1 AND pl.amount > 0 AND `+fromClause+toClause+groupClause+`
		 GROUP BY day ORDER BY day`, args...)
	defer rows.Close()
	type day struct {
		Date              string `json:"date"`
		PointsDistributed int64  `json:"points_distributed"`
	}
	daily := []day{}
	for rows.Next() {
		var d day
		var t time.Time
		rows.Scan(&t, &d.PointsDistributed)
		d.Date = t.Format("2006-01-02")
		daily = append(daily, d)
	}

	// Per-student totals + expiring within next 30 days
	srows, _ := h.db.Query(r.Context(),
		`SELECT u.id, u.display_name, u.total_points,
		        COALESCE((
		          SELECT SUM(amount) FROM points_ledger
		          WHERE user_id=u.id AND amount > 0
		            AND expires_at IS NOT NULL
		            AND expires_at <= now() + INTERVAL '30 days'
		            AND expires_at > now()
		        ),0) AS expiring_soon
		 FROM users u
		 WHERE u.institution_id=$1 AND u.role='student' AND u.deleted_at IS NULL`+inGroup(2)+`
		 ORDER BY u.total_points DESC`, instID, groupID)
	defer srows.Close()
	type stu struct {
		UserID       string `json:"user_id"`
		DisplayName  string `json:"display_name"`
		TotalPoints  int64  `json:"total_points"`
		ExpiringSoon int64  `json:"expiring_soon"`
	}
	students := []stu{}
	for srows.Next() {
		var s stu
		srows.Scan(&s.UserID, &s.DisplayName, &s.TotalPoints, &s.ExpiringSoon)
		students = append(students, s)
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"daily_trend": daily,
		"students":    students,
	})
}

// GET /api/v1/institution/quizzes/{quizId}/results
func (h *Handler) QuizResults(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	quizID := chi.URLParam(r, "quizId")

	var check int
	h.db.QueryRow(r.Context(),
		`SELECT 1 FROM quizzes WHERE id=$1 AND institution_id=$2 AND deleted_at IS NULL`, quizID, instID).Scan(&check)
	if check == 0 {
		middleware.NotFound(w, "quiz")
		return
	}

	var started, completions int
	var avgScore float64
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE status='completed'), COALESCE(AVG(score_pct) FILTER (WHERE status='completed'),0)
		 FROM quiz_attempts WHERE quiz_id=$1`, quizID).Scan(&started, &completions, &avgScore)
	completionRate := 0.0
	if started > 0 {
		completionRate = float64(completions) / float64(started) * 100
	}

	// Per-question accuracy
	qrows, _ := h.db.Query(r.Context(),
		`SELECT q.position,
		        COALESCE(100.0 * SUM(CASE WHEN qr.is_correct THEN 1 ELSE 0 END) / NULLIF(COUNT(*),0), 0) AS accuracy
		 FROM questions q
		 LEFT JOIN question_responses qr ON qr.question_id=q.id
		 LEFT JOIN quiz_attempts qa ON qa.id=qr.attempt_id AND qa.status='completed'
		 WHERE q.quiz_id=$1
		 GROUP BY q.position ORDER BY q.position`, quizID)
	defer qrows.Close()
	type qAcc struct {
		Position    int     `json:"position"`
		AccuracyPct float64 `json:"accuracy_pct"`
	}
	perQ := []qAcc{}
	for qrows.Next() {
		var p qAcc
		qrows.Scan(&p.Position, &p.AccuracyPct)
		perQ = append(perQ, p)
	}

	// Attempts, paged. Unfinished attempts are included with their status: an
	// in-progress or abandoned attempt's time runs to its last answer, not to
	// now, so a week-old abandoned attempt doesn't read as a week spent.
	aLimit, _ := strconv.Atoi(r.URL.Query().Get("attempts_limit"))
	if aLimit < 1 || aLimit > 200 {
		aLimit = 50
	}
	aOffset, _ := strconv.Atoi(r.URL.Query().Get("attempts_offset"))
	if aOffset < 0 {
		aOffset = 0
	}
	arows, _ := h.db.Query(r.Context(),
		`SELECT qa.id, qa.user_id, u.display_name, qa.status,
		        qa.score_pct, COALESCE(qa.points_delta,0),
		        (EXTRACT(EPOCH FROM (COALESCE(qa.completed_at,
		            (SELECT MAX(qr.submitted_at) FROM question_responses qr WHERE qr.attempt_id=qa.id),
		            qa.started_at) - qa.started_at))*1000)::BIGINT AS time_taken_ms,
		        qa.started_at, qa.completed_at
		 FROM quiz_attempts qa JOIN users u ON u.id=qa.user_id
		 WHERE qa.quiz_id=$1
		 ORDER BY COALESCE(qa.completed_at, qa.started_at) DESC, qa.id
		 LIMIT $2 OFFSET $3`, quizID, aLimit, aOffset)
	defer arows.Close()
	type att struct {
		AttemptID    string     `json:"attempt_id"`
		StudentID    string     `json:"student_id"`
		DisplayName  string     `json:"display_name"`
		Status       string     `json:"status"` // completed, in_progress, abandoned
		ScorePct     *float64   `json:"score_pct"`
		PointsEarned int64      `json:"points_earned"`
		TimeTakenMs  int64      `json:"time_taken_ms"`
		StartedAt    time.Time  `json:"started_at"`
		CompletedAt  *time.Time `json:"completed_at"`
	}
	attempts := []att{}
	for arows.Next() {
		var a att
		arows.Scan(&a.AttemptID, &a.StudentID, &a.DisplayName, &a.Status, &a.ScorePct, &a.PointsEarned, &a.TimeTakenMs, &a.StartedAt, &a.CompletedAt)
		attempts = append(attempts, a)
	}

	// The classes the quiz targets: its own class, plus classes holding a
	// curriculum unit it covers.
	var classNames []string
	h.db.QueryRow(r.Context(), `SELECT COALESCE(array_agg(DISTINCT g.name ORDER BY g.name),'{}') FROM groups g
		WHERE g.institution_id=$2 AND g.archived_at IS NULL AND (
		  g.id=(SELECT group_id FROM quizzes WHERE id=$1)
		  OR g.id IN (SELECT cc.group_id FROM quiz_curriculum_units qu
		                JOIN curriculum_chapters ch ON ch.id=qu.unit_id
		                JOIN class_curricula cc ON cc.version_id=ch.version_id AND cc.ended_at IS NULL
		               WHERE qu.quiz_id=$1))`, quizID, instID).Scan(&classNames)

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"started":               started,
		"completions":           completions,
		"completion_rate":       completionRate,
		"class_names":           classNames,
		"attempts_total":        started,
		"attempts_limit":        aLimit,
		"attempts_offset":       aOffset,
		"avg_score":             avgScore,
		"per_question_accuracy": perQ,
		"attempts":              attempts,
	})
}

// logAudit writes an institution-level audit entry.
// logAudit records an action without naming an owning institution. Prefer
// logAuditInst inside this package: an entry with no institution_id is only
// visible to the institution log if its target happens to be the institution
// row or one of its users.
func logAudit(ctx context.Context, db *pgxpool.Pool, adminID, action, targetType, targetID, reason string) {
	logAuditInst(ctx, db, adminID, "", action, targetType, targetID, reason)
}

// logAuditInst records an action and the institution it belongs to, so the
// institution's log can find it by ownership rather than by guessing from the
// target's type.
func logAuditInst(ctx context.Context, db *pgxpool.Pool, adminID, institutionID, action, targetType, targetID, reason string) {
	var adminName, adminRole string
	db.QueryRow(ctx, `SELECT display_name, role FROM users WHERE id=$1`, adminID).Scan(&adminName, &adminRole)

	var inst *string
	if institutionID != "" {
		inst = &institutionID
	}
	db.Exec(ctx,
		`INSERT INTO audit_log (admin_id, admin_name, admin_role, action_type, target_type, target_id, reason, institution_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		adminID, adminName, adminRole, action, targetType, targetID, reason, inst)
}

func generateCode(n int) string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	rb := make([]byte, n)
	rand.Read(rb)
	for i := range b {
		b[i] = letters[int(rb[i])%len(letters)]
	}
	return string(b)
}
