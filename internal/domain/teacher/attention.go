package teacher

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/qwish/backend/internal/middleware"
)

// AttentionDefinitionVersion is bumped whenever a definition below changes
// (plans/teacher-and-institute-decision-dashboards.md §2), so a client can tell
// numbers computed under different rules apart.
const AttentionDefinitionVersion = "attention-2026-10-05"

type AttentionTotals struct {
	EligibleStudents         int `json:"eligible_students"`
	StudentsNeedingAttention int `json:"students_needing_attention"`
	SupportReviewsOverdue    int `json:"support_reviews_overdue"`
	StudentsMissingWork      int `json:"students_missing_work"`
	OverdueSubmissions       int `json:"overdue_submissions"`
	StudentsNeedingSupport   int `json:"students_needing_support"`
}

type AttentionClass struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type AttentionWork struct {
	AssignmentID string    `json:"assignment_id"`
	QuizTitle    string    `json:"quiz_title"`
	ClassID      string    `json:"class_id"`
	DueAt        time.Time `json:"due_at"`
}

type AttentionConcept struct {
	ConceptID        string    `json:"concept_id"`
	Code             string    `json:"code"`
	Title            string    `json:"title"`
	LatestEvidenceAt time.Time `json:"latest_evidence_at"`
}

// AttentionReason is one reason a student is listed. Date is what the reason is
// ordered by: the review date, the oldest due date, or the oldest evidence date.
type AttentionReason struct {
	Kind     string             `json:"kind"` // support_review_overdue | overdue_work | needs_support
	Date     string             `json:"date"`
	Status   string             `json:"status,omitempty"`
	Items    []AttentionWork    `json:"items,omitempty"`
	Concepts []AttentionConcept `json:"concepts,omitempty"`
}

type AttentionStudent struct {
	StudentID   string            `json:"student_id"`
	StudentName string            `json:"student_name"`
	Classes     []AttentionClass  `json:"classes"`
	Reasons     []AttentionReason `json:"reasons"`
}

// attentionCTE defines the three reasons over the operational roster: active,
// claimed, non-deleted students in the teacher's active classes. Suspended and
// pending-claim students are roster exceptions, never academic risk.
// $1 institution, $2 class ids, $3 teacher, $4 today in the institution's zone.
const attentionCTE = `
WITH roster AS (
  SELECT DISTINCT gs.user_id AS student_id
    FROM group_students gs
    JOIN users u ON u.id=gs.user_id AND u.role='student' AND u.deleted_at IS NULL
    JOIN enrollments e ON e.user_id=gs.user_id AND e.institution_id=$1 AND e.status='active'
   WHERE gs.group_id = ANY($2::uuid[])
), support AS (
  SELECT s.student_id, s.review_on, s.status
    FROM teacher_student_support s JOIN roster USING (student_id)
   WHERE s.teacher_id=$3 AND s.institution_id=$1 AND s.status<>'resolved' AND s.review_on < $4::date
), work AS (
  -- Recipient deadline overrides and availability apply; excused is never owed.
  SELECT ar.student_id, a.id AS assignment_id, q.title AS quiz_title, a.group_id AS class_id,
         COALESCE(ar.due_at_override, a.due_at) AS due_at
    FROM learning_assignment_recipients ar
    JOIN learning_assignments a ON a.id=ar.assignment_id AND a.institution_id=$1
     AND a.status='published' AND a.group_id = ANY($2::uuid[])
    JOIN group_students gs ON gs.group_id=a.group_id AND gs.user_id=ar.student_id
    JOIN roster r ON r.student_id=ar.student_id
    JOIN quizzes q ON q.id=a.quiz_id
   WHERE ar.status IN ('assigned','started','overdue')
     AND COALESCE(ar.due_at_override, a.due_at) < now()
     AND (a.available_at IS NULL OR a.available_at <= now())
), learning AS (
  -- Same needs-support rule as the class matrix: at least two distinct
  -- questions, more errors than correct answers. Repeat attempts of one
  -- question are not variety.
  SELECT le.user_id AS student_id, le.concept_id, c.code, c.title, MAX(le.occurred_at) AS latest_evidence_at
    FROM learning_evidence le
    JOIN roster r ON r.student_id=le.user_id
    JOIN curriculum_concepts c ON c.id=le.concept_id
   WHERE le.institution_id=$1 AND NOT le.timed_out
   GROUP BY le.user_id, le.concept_id, c.code, c.title
  HAVING COUNT(DISTINCT le.question_id) >= 2
     AND COUNT(*) FILTER (WHERE NOT le.is_correct) > COUNT(*) FILTER (WHERE le.is_correct)
), flagged AS (
  SELECT student_id FROM support UNION SELECT student_id FROM work UNION SELECT student_id FROM learning
)`

const attentionTotalsSQL = attentionCTE + `
SELECT (SELECT COUNT(*) FROM roster), (SELECT COUNT(*) FROM flagged), (SELECT COUNT(*) FROM support),
       (SELECT COUNT(DISTINCT student_id) FROM work), (SELECT COUNT(*) FROM work),
       (SELECT COUNT(DISTINCT student_id) FROM learning)`

// Ordering: overdue support reviews, then overdue work, then learning signals;
// oldest first within each. $5 timezone, $6 limit, $7 offset.
const attentionRowsSQL = attentionCTE + `, ranked AS (
  SELECT f.student_id, COALESCE(NULLIF(u.display_name,''), u.full_name, '') AS name,
         CASE WHEN EXISTS (SELECT 1 FROM support s WHERE s.student_id=f.student_id) THEN 1
              WHEN EXISTS (SELECT 1 FROM work w WHERE w.student_id=f.student_id) THEN 2 ELSE 3 END AS rank,
         COALESCE((SELECT s.review_on::timestamp AT TIME ZONE $5 FROM support s WHERE s.student_id=f.student_id),
                  (SELECT MIN(w.due_at) FROM work w WHERE w.student_id=f.student_id),
                  (SELECT MIN(l.latest_evidence_at) FROM learning l WHERE l.student_id=f.student_id)) AS since
    FROM flagged f JOIN users u ON u.id=f.student_id
)
SELECT r.student_id, r.name,
       (SELECT COALESCE(json_agg(json_build_object('id', g.id, 'name', g.name) ORDER BY g.name), '[]')
          FROM group_students gs JOIN groups g ON g.id=gs.group_id
         WHERE gs.user_id=r.student_id AND gs.group_id = ANY($2::uuid[])),
       (SELECT json_build_object('review_on', to_char(s.review_on,'YYYY-MM-DD'), 'status', s.status)
          FROM support s WHERE s.student_id=r.student_id),
       (SELECT json_agg(json_build_object('assignment_id', w.assignment_id, 'quiz_title', w.quiz_title,
               'class_id', w.class_id, 'due_at', w.due_at) ORDER BY w.due_at)
          FROM work w WHERE w.student_id=r.student_id),
       (SELECT json_agg(json_build_object('concept_id', l.concept_id, 'code', l.code, 'title', l.title,
               'latest_evidence_at', l.latest_evidence_at) ORDER BY l.latest_evidence_at)
          FROM learning l WHERE l.student_id=r.student_id)
  FROM ranked r
 ORDER BY r.rank, r.since, r.name, r.student_id
 LIMIT $6 OFFSET $7`

// Attention answers "who needs help today?" for the teacher's own classes.
// Due work counts every open overdue item regardless of any window, so the
// envelope's `from` is null.
func (h *Handler) Attention(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	teacherID, instID := middleware.GetUserID(r), middleware.GetInstitutionID(r)
	q := r.URL.Query()
	classID := strings.TrimSpace(q.Get("class_id"))
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 25
	}

	var tz string
	if err := h.db.QueryRow(ctx, `SELECT timezone FROM institutions WHERE id=$1`, instID).Scan(&tz); err != nil {
		middleware.NotFound(w, "institution")
		return
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	now := time.Now()
	today := now.In(loc).Format("2006-01-02")

	classes, err := h.activeClassIDs(ctx, teacherID, instID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	scope := map[string]any{"kind": "teacher_classes", "class_id": nil, "state": "ok", "reason": ""}
	if classID != "" {
		if !slices.Contains(classes, classID) {
			middleware.NotFound(w, "class")
			return
		}
		classes = []string{classID}
		scope["class_id"] = classID
	} else if len(classes) == 0 {
		scope["state"], scope["reason"] = "no_assigned_classes", NoClassesReason
	}

	var totals AttentionTotals
	if err := h.db.QueryRow(ctx, attentionTotalsSQL, instID, classes, teacherID, today).Scan(
		&totals.EligibleStudents, &totals.StudentsNeedingAttention, &totals.SupportReviewsOverdue,
		&totals.StudentsMissingWork, &totals.OverdueSubmissions, &totals.StudentsNeedingSupport); err != nil {
		middleware.InternalError(w)
		return
	}
	students, err := h.attentionRows(ctx, instID, classes, teacherID, today, tz, limit, (page-1)*limit)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSONWithMeta(w, http.StatusOK, map[string]any{
		"scope":              scope,
		"from":               nil,
		"to":                 today,
		"timezone":           tz,
		"generated_at":       now.UTC(),
		"definition_version": AttentionDefinitionVersion,
		"totals":             totals,
		"students":           students,
	}, &middleware.Meta{Page: page, Limit: limit, Total: totals.StudentsNeedingAttention})
}

func (h *Handler) activeClassIDs(ctx context.Context, teacherID, instID string) ([]string, error) {
	rows, err := h.db.Query(ctx, `SELECT g.id::text FROM groups g JOIN group_teachers gt ON gt.group_id=g.id
		WHERE gt.user_id=$1 AND g.institution_id=$2 AND g.archived_at IS NULL`, teacherID, instID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (h *Handler) attentionRows(ctx context.Context, instID string, classes []string, teacherID, today, tz string, limit, offset int) ([]AttentionStudent, error) {
	rows, err := h.db.Query(ctx, attentionRowsSQL, instID, classes, teacherID, today, tz, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AttentionStudent{}
	for rows.Next() {
		var st AttentionStudent
		var classesJSON, supportJSON, workJSON, learningJSON []byte
		if err := rows.Scan(&st.StudentID, &st.StudentName, &classesJSON, &supportJSON, &workJSON, &learningJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(classesJSON, &st.Classes); err != nil {
			return nil, err
		}
		if supportJSON != nil {
			var s struct {
				ReviewOn string `json:"review_on"`
				Status   string `json:"status"`
			}
			if err := json.Unmarshal(supportJSON, &s); err != nil {
				return nil, err
			}
			st.Reasons = append(st.Reasons, AttentionReason{Kind: "support_review_overdue", Date: s.ReviewOn, Status: s.Status})
		}
		if workJSON != nil {
			var items []AttentionWork
			if err := json.Unmarshal(workJSON, &items); err != nil {
				return nil, err
			}
			st.Reasons = append(st.Reasons, AttentionReason{Kind: "overdue_work", Date: items[0].DueAt.Format(time.RFC3339), Items: items})
		}
		if learningJSON != nil {
			var concepts []AttentionConcept
			if err := json.Unmarshal(learningJSON, &concepts); err != nil {
				return nil, err
			}
			st.Reasons = append(st.Reasons, AttentionReason{Kind: "needs_support", Date: concepts[0].LatestEvidenceAt.Format(time.RFC3339), Concepts: concepts})
		}
		out = append(out, st)
	}
	return out, rows.Err()
}
