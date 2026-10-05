package institution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/teacher"
	"github.com/qwish/backend/internal/middleware"
)

// ClassAttentionDefinitionVersion is bumped whenever a definition changes
// (plans/teacher-and-institute-decision-dashboards.md §2). The student-level
// rules match the teacher attention queue (teacher.AttentionDefinitionVersion).
const ClassAttentionDefinitionVersion = "class-attention-2026-10-05"

// Ratio is a metric with its denominator. A zero denominator is
// not_applicable with a null value, never 0%.
type Ratio struct {
	Numerator   int      `json:"numerator"`
	Denominator int      `json:"denominator"`
	Value       *float64 `json:"value"`
	State       string   `json:"state"` // ok | not_applicable
}

func ratio(n, d int) Ratio {
	if d == 0 {
		return Ratio{Numerator: n, State: "not_applicable"}
	}
	v := float64(n) / float64(d)
	return Ratio{Numerator: n, Denominator: d, Value: &v, State: "ok"}
}

type PendingApprovals struct {
	Count             int        `json:"count"`
	AgeUnavailable    int        `json:"age_unavailable"`
	OldestSubmittedAt *time.Time `json:"oldest_submitted_at"`
}

// ClassAttentionTotals are institution-wide distinct counts, never sums of the
// per-class rows: a student in two classes counts once.
type ClassAttentionTotals struct {
	ActiveClasses           int   `json:"active_classes"`
	ClassesNeedingAttention int   `json:"classes_needing_attention"`
	Coverage                Ratio `json:"coverage"`
	SupportReviewsOverdue   int   `json:"support_reviews_overdue"`
	StudentsMissingWork     int   `json:"students_missing_work"`
	OverdueSubmissions      int   `json:"overdue_submissions"`
	StudentsNeedingSupport  int   `json:"students_needing_support"`
	StudentsRepeatingErrors int   `json:"students_repeating_errors"`
	// Null under department scope: approvals are an institution-admin queue.
	PendingApprovals *PendingApprovals `json:"pending_approvals"`
}

type ClassTeacher struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ClassAttentionRow struct {
	ClassID                 string         `json:"class_id"`
	ClassName               string         `json:"class_name"`
	Teachers                []ClassTeacher `json:"teachers"`
	Eligible                int            `json:"eligible_students"`
	Assessed                int            `json:"assessed_students"`
	Coverage                Ratio          `json:"coverage"`
	StudentsMissingWork     int            `json:"students_missing_work"`
	OverdueSubmissions      int            `json:"overdue_submissions"`
	StudentsNeedingSupport  int            `json:"students_needing_support"`
	SupportReviewsOverdue   int            `json:"support_reviews_overdue"`
	StudentsRepeatingErrors int            `json:"students_repeating_errors"`
	Reasons                 []string       `json:"reasons"`
}

type ApprovalItem struct {
	QuizID      string     `json:"quiz_id"`
	Title       string     `json:"title"`
	TeacherID   string     `json:"teacher_id"`
	TeacherName string     `json:"teacher_name"`
	SubmittedAt *time.Time `json:"submitted_at"` // null: pending before ages were recorded
}

type SupportReviewItem struct {
	StudentID   string    `json:"student_id"`
	StudentName string    `json:"student_name"`
	TeacherID   string    `json:"teacher_id"`
	TeacherName string    `json:"teacher_name"`
	Status      string    `json:"status"`
	ReviewOn    string    `json:"review_on"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ClassAttention struct {
	Scope             map[string]any       `json:"scope"`
	From              string               `json:"from"`
	To                string               `json:"to"`
	Timezone          string               `json:"timezone"`
	GeneratedAt       time.Time            `json:"generated_at"`
	DefinitionVersion string               `json:"definition_version"`
	Totals            ClassAttentionTotals `json:"totals"`
	Classes           []ClassAttentionRow  `json:"classes"`
	Approvals         []ApprovalItem       `json:"approvals"`
	SupportReviews    []SupportReviewItem  `json:"support_reviews"`
}

// classAttentionCTE: $1 institution, $2 today (institution zone), $3 window
// start, $4 department ids (NULL: every class; leadership passes its grant). Roster and reason rules are the teacher queue's, at institution scope:
// active, claimed, non-deleted students in active classes; support reviews by
// any teacher; overdue work attributed to the assignment's class; learning and
// repeated-wrong signals attributed to every class the student is in.
const classAttentionCTE = `
WITH classes AS (
  SELECT g.id, g.name FROM groups g
   WHERE g.institution_id=$1 AND g.archived_at IS NULL
     AND ($4::uuid[] IS NULL OR g.department_id = ANY($4::uuid[]))
), members AS (
  SELECT gs.group_id, gs.user_id AS student_id
    FROM group_students gs
    JOIN classes c ON c.id=gs.group_id
    JOIN users u ON u.id=gs.user_id AND u.role='student' AND u.deleted_at IS NULL
    JOIN enrollments e ON e.user_id=gs.user_id AND e.institution_id=$1 AND e.status='active'
), students AS (
  SELECT DISTINCT student_id FROM members
), assessed AS (
  SELECT DISTINCT qa.user_id AS student_id
    FROM quiz_attempts qa
    JOIN students s ON s.student_id=qa.user_id
    JOIN quizzes q ON q.id=qa.quiz_id AND q.institution_id=$1
   WHERE qa.status='completed' AND qa.completed_at >= $3
), support AS (
  SELECT s.teacher_id, s.student_id, s.status, s.review_on, s.updated_at
    FROM teacher_student_support s JOIN students USING (student_id)
   WHERE s.institution_id=$1 AND s.status<>'resolved' AND s.review_on < $2::date
), work AS (
  SELECT a.group_id, ar.student_id
    FROM learning_assignment_recipients ar
    JOIN learning_assignments a ON a.id=ar.assignment_id AND a.institution_id=$1 AND a.status='published'
    JOIN members m ON m.group_id=a.group_id AND m.student_id=ar.student_id
   WHERE ar.status IN ('assigned','started','overdue')
     AND COALESCE(ar.due_at_override, a.due_at) < now()
     AND (a.available_at IS NULL OR a.available_at <= now())
), learning AS (
  SELECT DISTINCT le.user_id AS student_id
    FROM learning_evidence le JOIN students s ON s.student_id=le.user_id
   WHERE le.institution_id=$1 AND NOT le.timed_out
   GROUP BY le.user_id, le.concept_id
  HAVING COUNT(DISTINCT le.question_id) >= 2
     AND COUNT(*) FILTER (WHERE NOT le.is_correct) > COUNT(*) FILTER (WHERE le.is_correct)
), repeats AS (
  SELECT DISTINCT qa.user_id AS student_id
    FROM students s
    JOIN quiz_attempts qa ON qa.user_id=s.student_id
    JOIN quizzes q ON q.id=qa.quiz_id AND q.institution_id=$1
    JOIN question_responses qr ON qr.attempt_id=qa.id AND qr.is_correct IS NOT NULL
   GROUP BY qa.user_id, qr.question_id
  HAVING COUNT(*) FILTER (WHERE NOT qr.is_correct) >= 2
     AND NOT (array_agg(qr.is_correct ORDER BY qr.submitted_at DESC))[1]
), per_class AS (
  SELECT c.id, c.name,
         (SELECT COUNT(*) FROM members m WHERE m.group_id=c.id) AS eligible,
         (SELECT COUNT(*) FROM members m JOIN assessed x USING (student_id) WHERE m.group_id=c.id) AS assessed,
         (SELECT COUNT(DISTINCT w.student_id) FROM work w WHERE w.group_id=c.id) AS missing_work,
         (SELECT COUNT(*) FROM work w WHERE w.group_id=c.id) AS overdue_submissions,
         (SELECT COUNT(*) FROM members m JOIN learning x USING (student_id) WHERE m.group_id=c.id) AS needs_support,
         (SELECT COUNT(DISTINCT m.student_id) FROM members m JOIN support x USING (student_id) WHERE m.group_id=c.id) AS reviews_overdue,
         (SELECT COUNT(*) FROM members m JOIN repeats x USING (student_id) WHERE m.group_id=c.id) AS repeating
    FROM classes c
), flagged AS (
  SELECT * FROM per_class WHERE missing_work + needs_support + reviews_overdue + repeating > 0
)`

const classAttentionTotalsSQL = classAttentionCTE + `
SELECT (SELECT COUNT(*) FROM classes), (SELECT COUNT(*) FROM flagged),
       (SELECT COUNT(*) FROM assessed), (SELECT COUNT(*) FROM students),
       (SELECT COUNT(DISTINCT student_id) FROM support), (SELECT COUNT(DISTINCT student_id) FROM work), (SELECT COUNT(*) FROM work),
       (SELECT COUNT(*) FROM learning), (SELECT COUNT(*) FROM repeats)`

// Classes with an overdue support review first, then overdue work, learning
// signals, repeated wrong answers; then by affected students, then name.
// $5 limit, $6 offset.
const classAttentionRowsSQL = classAttentionCTE + `
SELECT f.id, f.name, f.eligible, f.assessed, f.missing_work, f.overdue_submissions, f.needs_support, f.reviews_overdue, f.repeating,
       (SELECT COALESCE(json_agg(json_build_object('id', u.id, 'name', COALESCE(NULLIF(u.display_name,''), u.full_name)) ORDER BY u.display_name), '[]')
          FROM group_teachers gt JOIN users u ON u.id=gt.user_id WHERE gt.group_id=f.id)
  FROM flagged f
 ORDER BY CASE WHEN f.reviews_overdue>0 THEN 1 WHEN f.missing_work>0 THEN 2 WHEN f.needs_support>0 THEN 3 ELSE 4 END,
          f.missing_work + f.needs_support + f.reviews_overdue + f.repeating DESC, f.name, f.id
 LIMIT $5 OFFSET $6`

// GET /institution/classes/attention?days=7|30|90&page=&limit=
// Which classes need support, and the overview totals beside them. Coverage
// uses the window; due work and reviews count every open item.
func (h *Handler) ClassAttention(w http.ResponseWriter, r *http.Request) {
	days, ok := ClassAttentionDays(r)
	if !ok {
		middleware.BadRequest(w, "days must be 7, 30 or 90")
		return
	}
	page, limit := pageParams(r)
	out, err := QueryClassAttention(r.Context(), h.db, middleware.GetInstitutionID(r), nil, days, limit, (page-1)*limit)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSONWithMeta(w, http.StatusOK, out, &middleware.Meta{Page: page, Limit: limit, Total: out.Totals.ClassesNeedingAttention})
}

// ClassAttentionDays reads the coverage window: 7, 30 or 90 days, default 30.
func ClassAttentionDays(r *http.Request) (int, bool) {
	switch v := r.URL.Query().Get("days"); v {
	case "":
		return 30, true
	case "7", "30", "90":
		n, _ := strconv.Atoi(v)
		return n, true
	}
	return 0, false
}

// PageParams reads page (≥1) and limit (1–100, default 25).
func PageParams(r *http.Request) (page, limit int) { return pageParams(r) }

// QueryClassAttention computes the overview for an institution's active
// classes, or only those in departments (leadership's grant; nil is every
// class). Pending approvals are included only for the whole institution.
func QueryClassAttention(ctx context.Context, db *pgxpool.Pool, instID string, departments []string, days, limit, offset int) (ClassAttention, error) {
	var tz string
	if err := db.QueryRow(ctx, `SELECT timezone FROM institutions WHERE id=$1`, instID).Scan(&tz); err != nil {
		return ClassAttention{}, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return ClassAttention{}, err
	}
	now := time.Now()
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	from := today.AddDate(0, 0, -(days - 1))
	todayStr := today.Format("2006-01-02")
	scope := map[string]any{"kind": "institution", "days": days}
	if departments != nil {
		scope = map[string]any{"kind": "departments", "department_ids": departments, "days": days}
	}
	out := ClassAttention{
		Scope: scope, From: from.Format("2006-01-02"), To: todayStr, Timezone: tz, GeneratedAt: now.UTC(),
		DefinitionVersion: ClassAttentionDefinitionVersion,
	}
	t := &out.Totals
	var assessed, eligible int
	if err := db.QueryRow(ctx, classAttentionTotalsSQL, instID, todayStr, from, departments).Scan(
		&t.ActiveClasses, &t.ClassesNeedingAttention, &assessed, &eligible,
		&t.SupportReviewsOverdue, &t.StudentsMissingWork, &t.OverdueSubmissions,
		&t.StudentsNeedingSupport, &t.StudentsRepeatingErrors); err != nil {
		return out, err
	}
	t.Coverage = ratio(assessed, eligible)
	if out.Classes, err = classAttentionRows(ctx, db, instID, todayStr, from, departments, limit, offset); err != nil {
		return out, err
	}
	if out.SupportReviews, err = overdueSupportReviews(ctx, db, instID, todayStr, departments); err != nil {
		return out, err
	}
	if departments == nil {
		t.PendingApprovals = &PendingApprovals{}
		if err := db.QueryRow(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE submitted_for_approval_at IS NULL), MIN(submitted_for_approval_at)
			FROM quizzes WHERE institution_id=$1 AND status='pending_approval' AND deleted_at IS NULL`, instID).Scan(
			&t.PendingApprovals.Count, &t.PendingApprovals.AgeUnavailable, &t.PendingApprovals.OldestSubmittedAt); err != nil {
			return out, err
		}
		if out.Approvals, err = pendingApprovals(ctx, db, instID); err != nil {
			return out, err
		}
	}
	return out, nil
}

func classAttentionRows(ctx context.Context, db *pgxpool.Pool, instID, today string, from time.Time, departments []string, limit, offset int) ([]ClassAttentionRow, error) {
	rows, err := db.Query(ctx, classAttentionRowsSQL, instID, today, from, departments, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ClassAttentionRow{}
	for rows.Next() {
		var c ClassAttentionRow
		var teachers []byte
		if err := rows.Scan(&c.ClassID, &c.ClassName, &c.Eligible, &c.Assessed, &c.StudentsMissingWork, &c.OverdueSubmissions,
			&c.StudentsNeedingSupport, &c.SupportReviewsOverdue, &c.StudentsRepeatingErrors, &teachers); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(teachers, &c.Teachers); err != nil {
			return nil, err
		}
		c.Coverage = ratio(c.Assessed, c.Eligible)
		c.Reasons = []string{}
		for _, x := range []struct {
			n    int
			kind string
		}{{c.SupportReviewsOverdue, "support_review_overdue"}, {c.StudentsMissingWork, "overdue_work"},
			{c.StudentsNeedingSupport, "needs_support"}, {c.StudentsRepeatingErrors, "repeated_wrong"}} {
			if x.n > 0 {
				c.Reasons = append(c.Reasons, x.kind)
			}
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// pendingApprovals is the oldest ten, by recorded submission time; quizzes
// pending since before ages were recorded sort last and carry no age.
func pendingApprovals(ctx context.Context, db *pgxpool.Pool, instID string) ([]ApprovalItem, error) {
	rows, err := db.Query(ctx, `SELECT q.id, q.title, u.id, COALESCE(NULLIF(u.display_name,''), u.full_name, ''), q.submitted_for_approval_at
		FROM quizzes q JOIN users u ON u.id=q.created_by
		WHERE q.institution_id=$1 AND q.status='pending_approval' AND q.deleted_at IS NULL
		ORDER BY q.submitted_for_approval_at ASC NULLS LAST, q.created_at, q.id LIMIT 10`, instID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ApprovalItem{}
	for rows.Next() {
		var a ApprovalItem
		if err := rows.Scan(&a.QuizID, &a.Title, &a.TeacherID, &a.TeacherName, &a.SubmittedAt); err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// overdueSupportReviews is the oldest ten overdue reviews with the teacher who
// recorded the plan, over the same roster and departments as the totals.
func overdueSupportReviews(ctx context.Context, db *pgxpool.Pool, instID, today string, departments []string) ([]SupportReviewItem, error) {
	rows, err := db.Query(ctx, `SELECT s.student_id, COALESCE(NULLIF(st.display_name,''), st.full_name, ''),
		       s.teacher_id, COALESCE(NULLIF(t.display_name,''), t.full_name, ''), s.status, to_char(s.review_on,'YYYY-MM-DD'), s.updated_at
		  FROM teacher_student_support s
		  JOIN users st ON st.id=s.student_id AND st.role='student' AND st.deleted_at IS NULL
		  JOIN users t ON t.id=s.teacher_id
		 WHERE s.institution_id=$1 AND s.status<>'resolved' AND s.review_on < $2::date
		   AND EXISTS (SELECT 1 FROM enrollments e WHERE e.user_id=s.student_id AND e.institution_id=$1 AND e.status='active')
		   AND EXISTS (SELECT 1 FROM group_students gs JOIN groups g ON g.id=gs.group_id
		                WHERE gs.user_id=s.student_id AND g.institution_id=$1 AND g.archived_at IS NULL
		                  AND ($3::uuid[] IS NULL OR g.department_id = ANY($3::uuid[])))
		 ORDER BY s.review_on, s.student_id LIMIT 10`, instID, today, departments)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []SupportReviewItem{}
	for rows.Next() {
		var s SupportReviewItem
		if err := rows.Scan(&s.StudentID, &s.StudentName, &s.TeacherID, &s.TeacherName, &s.Status, &s.ReviewOn, &s.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

// GET /institution/classes/{classId}/attention?page=&limit=
// The class drill-down: the teacher queue's student rows and definitions, at
// admin scope (every teacher's support plans), for one active class of this
// institution. Anything else is 404.
func (h *Handler) ClassStudentsAttention(w http.ResponseWriter, r *http.Request) {
	page, limit := pageParams(r)
	out, total, err := QueryClassStudentsAttention(r.Context(), h.db, middleware.GetInstitutionID(r), chi.URLParam(r, "classId"), nil, limit, (page-1)*limit)
	if errors.Is(err, ErrClassNotFound) {
		middleware.NotFound(w, "class")
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSONWithMeta(w, http.StatusOK, out, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// ErrClassNotFound: no such active class in this institution and scope.
var ErrClassNotFound = errors.New("class not found")

func pageParams(r *http.Request) (page, limit int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 25
	}
	return page, limit
}

// QueryClassStudentsAttention is the class drill-down for one active class in
// the institution and, when departments is non-nil, in one of them.
func QueryClassStudentsAttention(ctx context.Context, db *pgxpool.Pool, instID, classID string, departments []string, limit, offset int) (map[string]any, int, error) {
	var name, tz string
	var teachers []byte
	err := db.QueryRow(ctx, `SELECT g.name, i.timezone,
		(SELECT COALESCE(json_agg(json_build_object('id', u.id, 'name', COALESCE(NULLIF(u.display_name,''), u.full_name)) ORDER BY u.display_name), '[]')
		   FROM group_teachers gt JOIN users u ON u.id=gt.user_id WHERE gt.group_id=g.id)
		FROM groups g JOIN institutions i ON i.id=g.institution_id
		WHERE g.id::text=$1 AND g.institution_id=$2 AND g.archived_at IS NULL
		  AND ($3::uuid[] IS NULL OR g.department_id = ANY($3::uuid[]))`, classID, instID, departments).Scan(&name, &tz, &teachers)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrClassNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	var classTeachers []ClassTeacher
	if err := json.Unmarshal(teachers, &classTeachers); err != nil {
		return nil, 0, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, 0, err
	}
	now := time.Now()
	today := now.In(loc).Format("2006-01-02")
	totals, students, err := teacher.QueryAttention(ctx, db, instID, []string{classID}, nil, today, tz, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	return map[string]any{
		"scope":              map[string]any{"kind": "class", "class_id": classID, "class_name": name},
		"from":               nil,
		"to":                 today,
		"timezone":           tz,
		"generated_at":       now.UTC(),
		"definition_version": teacher.AttentionDefinitionVersion,
		"teachers":           classTeachers,
		"totals":             totals,
		"students":           students,
	}, totals.StudentsNeedingAttention, nil
}
