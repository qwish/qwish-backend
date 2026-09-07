package learning

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct{ db *pgxpool.Pool }

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

func insightStatus(mapped bool, distinctQuestions, highConfidenceErrors int) string {
	if mapped && distinctQuestions >= 2 {
		return "possible_misconception"
	}
	if mapped && highConfidenceErrors > 0 {
		return "review_flag"
	}
	return "insufficient_evidence"
}

type misconceptionInput struct {
	ConceptID   string `json:"concept_id"`
	Code        string `json:"code"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (h *Handler) CreateMisconception(w http.ResponseWriter, r *http.Request) {
	var in misconceptionInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || in.ConceptID == "" || strings.TrimSpace(in.Code) == "" || strings.TrimSpace(in.Title) == "" || len(in.Code) > 80 || len(in.Title) > 200 || len(in.Description) > 2000 {
		middleware.BadRequest(w, "concept_id, code and title are required")
		return
	}
	var id string
	err := h.db.QueryRow(r.Context(), `
		INSERT INTO misconceptions (institution_id,concept_id,code,title,description,created_by)
		SELECT $1,c.id,$2,$3,NULLIF($4,''),$5 FROM curriculum_concepts c
		JOIN curriculum_chapters ch ON ch.id=c.chapter_id
		JOIN curriculum_versions cv ON cv.id=ch.version_id
		JOIN curricula cu ON cu.id=cv.curriculum_id
		WHERE c.id=$6 AND cu.institution_id=$1
		ON CONFLICT (institution_id,code) DO UPDATE SET title=EXCLUDED.title,description=EXCLUDED.description,active=true
		RETURNING id`, middleware.GetInstitutionID(r), strings.TrimSpace(in.Code), strings.TrimSpace(in.Title), strings.TrimSpace(in.Description), middleware.GetUserID(r), in.ConceptID).Scan(&id)
	if err != nil {
		middleware.BadRequest(w, "concept is not available to your institution")
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": id})
}

type mapInput struct {
	ConceptID string  `json:"concept_id"`
	Weight    float64 `json:"weight"`
	Options   []struct {
		OptionID        string `json:"option_id"`
		MisconceptionID string `json:"misconception_id"`
	} `json:"options"`
}

func (h *Handler) MapQuestion(w http.ResponseWriter, r *http.Request) {
	var in mapInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || in.ConceptID == "" || len(in.Options) > 20 {
		middleware.BadRequest(w, "invalid learning map")
		return
	}
	if in.Weight == 0 {
		in.Weight = 1
	}
	if in.Weight <= 0 || in.Weight > 1 {
		middleware.BadRequest(w, "weight must be greater than 0 and at most 1")
		return
	}
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(r.Context())
	questionID := chi.URLParam(r, "questionId")
	var allowed bool
	err = tx.QueryRow(r.Context(), `SELECT EXISTS(
		SELECT 1 FROM questions q JOIN quizzes z ON z.id=q.quiz_id
		JOIN curriculum_concepts c ON c.id=$4 JOIN curriculum_chapters ch ON ch.id=c.chapter_id
		JOIN curriculum_versions cv ON cv.id=ch.version_id JOIN curricula cu ON cu.id=cv.curriculum_id
		WHERE q.id=$1 AND z.created_by=$2 AND cu.institution_id=$3)`, questionID, middleware.GetUserID(r), middleware.GetInstitutionID(r), in.ConceptID).Scan(&allowed)
	if err != nil || !allowed {
		middleware.NotFound(w, "question or concept")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO question_concepts(question_id,concept_id,weight,mapped_by) VALUES($1,$2,$3,$4)
		ON CONFLICT(question_id,concept_id) DO UPDATE SET weight=EXCLUDED.weight,mapped_by=EXCLUDED.mapped_by,mapped_at=now()`, questionID, in.ConceptID, in.Weight, middleware.GetUserID(r)); err != nil {
		middleware.BadRequest(w, "could not map concept")
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM question_misconception_options WHERE question_id=$1`, questionID); err != nil {
		middleware.InternalError(w)
		return
	}
	for _, option := range in.Options {
		ct, execErr := tx.Exec(r.Context(), `INSERT INTO question_misconception_options(question_id,option_id,misconception_id,reviewed_by)
			SELECT $1,qo.id,m.id,$4 FROM question_options qo JOIN misconceptions m ON m.id=$3
			WHERE qo.id=$2 AND qo.question_id=$1 AND qo.active AND m.concept_id=$5 AND m.institution_id=$6`, questionID, option.OptionID, option.MisconceptionID, middleware.GetUserID(r), in.ConceptID, middleware.GetInstitutionID(r))
		if execErr != nil || ct.RowsAffected() != 1 {
			middleware.BadRequest(w, "an option or misconception is outside this learning map")
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "mapped"})
}

// GetQuestionMap supplies the teacher authoring surface with stable option,
// concept, and misconception IDs. All rows are scoped to the teacher's quiz
// and institution so the client never has to guess identifiers.
func (h *Handler) GetQuestionMap(w http.ResponseWriter, r *http.Request) {
	questionID := chi.URLParam(r, "questionId")
	var owns bool
	if err := h.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM questions q JOIN quizzes z ON z.id=q.quiz_id WHERE q.id=$1 AND z.created_by=$2)`, questionID, middleware.GetUserID(r)).Scan(&owns); err != nil || !owns {
		middleware.NotFound(w, "question")
		return
	}
	options := []map[string]interface{}{}
	rows, err := h.db.Query(r.Context(), `SELECT id,label,position FROM question_options WHERE question_id=$1 AND active ORDER BY position`, questionID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, label string
		var position int
		if rows.Scan(&id, &label, &position) != nil {
			middleware.InternalError(w)
			return
		}
		options = append(options, map[string]interface{}{"id": id, "label": label, "position": position})
	}
	var conceptID *string
	var weight float64
	_ = h.db.QueryRow(r.Context(), `SELECT concept_id,weight FROM question_concepts WHERE question_id=$1 ORDER BY mapped_at DESC LIMIT 1`, questionID).Scan(&conceptID, &weight)
	misconceptions := []map[string]interface{}{}
	mrows, err := h.db.Query(r.Context(), `SELECT id,code,title,description FROM misconceptions WHERE institution_id=$1 AND active ORDER BY title`, middleware.GetInstitutionID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer mrows.Close()
	for mrows.Next() {
		var id, code, title string
		var description *string
		if mrows.Scan(&id, &code, &title, &description) != nil {
			middleware.InternalError(w)
			return
		}
		misconceptions = append(misconceptions, map[string]interface{}{"id": id, "code": code, "title": title, "description": description})
	}
	middleware.JSON(w, http.StatusOK, map[string]interface{}{"question_id": questionID, "options": options, "concept_id": conceptID, "weight": weight, "misconceptions": misconceptions})
}

func (h *Handler) QuizInsights(w http.ResponseWriter, r *http.Request) {
	quizID := chi.URLParam(r, "quizId")
	classID := strings.TrimSpace(r.URL.Query().Get("class_id"))
	rows, err := h.db.Query(r.Context(), `
		SELECT le.user_id,u.display_name,le.concept_id,c.code,c.title,le.misconception_id,m.title,
		 COUNT(*) FILTER (WHERE NOT le.is_correct),COUNT(DISTINCT le.question_id) FILTER (WHERE NOT le.is_correct),
		 COUNT(*) FILTER (WHERE le.is_correct),
		 COUNT(*) FILTER (WHERE NOT le.is_correct AND le.confidence_level='very_confident'),
		 COUNT(*) FILTER (WHERE NOT le.is_correct AND le.confidence_level='pretty_sure'),
		 COUNT(*) FILTER (WHERE NOT le.is_correct AND le.confidence_level='not_sure'),
		 COUNT(*) FILTER (WHERE NOT le.is_correct AND le.confidence_level IS NULL),MAX(le.occurred_at)
		FROM learning_evidence le JOIN users u ON u.id=le.user_id JOIN curriculum_concepts c ON c.id=le.concept_id
		LEFT JOIN misconceptions m ON m.id=le.misconception_id JOIN quiz_attempts qa ON qa.id=le.attempt_id
		JOIN quizzes q ON q.id=qa.quiz_id
		WHERE q.id=$1 AND q.created_by=$2 AND le.institution_id=$3
		  AND ($4='' OR (
		    EXISTS(SELECT 1 FROM group_teachers gt WHERE gt.group_id::text=$4 AND gt.user_id=$2)
		    AND EXISTS(SELECT 1 FROM group_students gs WHERE gs.group_id::text=$4 AND gs.user_id=le.user_id)
		  ))
		GROUP BY le.user_id,u.display_name,le.concept_id,c.code,c.title,le.misconception_id,m.title
		ORDER BY MAX(le.occurred_at) DESC LIMIT 500`, quizID, middleware.GetUserID(r), middleware.GetInstitutionID(r), classID)
	if err != nil {
		middleware.BadRequest(w, "quiz insights unavailable")
		return
	}
	defer rows.Close()
	result := []map[string]interface{}{}
	for rows.Next() {
		var studentID, studentName, conceptID, conceptCode, conceptTitle string
		var misconceptionID, misconceptionTitle *string
		var errors, distinct, correct, high, middle, low, unknown int
		var latest interface{}
		if err := rows.Scan(&studentID, &studentName, &conceptID, &conceptCode, &conceptTitle, &misconceptionID, &misconceptionTitle, &errors, &distinct, &correct, &high, &middle, &low, &unknown, &latest); err != nil {
			middleware.InternalError(w)
			return
		}
		status := insightStatus(misconceptionID != nil, distinct, high)
		result = append(result, map[string]interface{}{"student_id": studentID, "student_name": studentName, "concept_id": conceptID, "concept_code": conceptCode, "concept_title": conceptTitle, "misconception_id": misconceptionID, "misconception_title": misconceptionTitle, "status": status, "error_count": errors, "distinct_questions": distinct, "contradictory_correct": correct, "confidence": map[string]int{"very_confident": high, "pretty_sure": middle, "not_sure": low, "unknown": unknown}, "latest_evidence_at": latest})
	}
	middleware.JSON(w, http.StatusOK, result)
}

func (h *Handler) StudentSummary(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `SELECT c.id,c.code,c.title,
		COUNT(*) FILTER(WHERE le.is_correct),COUNT(*) FILTER(WHERE NOT le.is_correct),COUNT(DISTINCT le.question_id),MAX(le.occurred_at)
		FROM learning_evidence le JOIN curriculum_concepts c ON c.id=le.concept_id
		WHERE le.user_id=$1 GROUP BY c.id,c.code,c.title ORDER BY MAX(le.occurred_at) DESC LIMIT 50`, middleware.GetUserID(r))
	if err != nil {
		middleware.BadRequest(w, "learning summary unavailable")
		return
	}
	defer rows.Close()
	result := []map[string]interface{}{}
	for rows.Next() {
		var id, code, title string
		var correct, errors, distinct int
		var latest interface{}
		if rows.Scan(&id, &code, &title, &correct, &errors, &distinct, &latest) != nil {
			middleware.InternalError(w)
			return
		}
		state := "building_evidence"
		if distinct >= 2 && errors > correct {
			state = "needs_another_look"
		} else if distinct >= 2 && correct >= errors {
			state = "demonstrated"
		}
		result = append(result, map[string]interface{}{"concept_id": id, "concept_code": code, "concept_title": title, "state": state, "correct_count": correct, "error_count": errors, "distinct_questions": distinct, "latest_evidence_at": latest})
	}
	middleware.JSON(w, http.StatusOK, result)
}

func (h *Handler) InstitutionSummary(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `SELECT c.id,c.code,c.title,COUNT(DISTINCT le.user_id),COUNT(*) FILTER(WHERE le.is_correct),COUNT(*) FILTER(WHERE NOT le.is_correct),COUNT(*) FILTER(WHERE le.confidence_level IS NULL),MAX(le.occurred_at)
		FROM learning_evidence le JOIN curriculum_concepts c ON c.id=le.concept_id WHERE le.institution_id=$1 GROUP BY c.id,c.code,c.title ORDER BY COUNT(*) FILTER(WHERE NOT le.is_correct) DESC LIMIT 200`, middleware.GetInstitutionID(r))
	if err != nil {
		middleware.BadRequest(w, "learning summary unavailable")
		return
	}
	defer rows.Close()
	result := []map[string]interface{}{}
	for rows.Next() {
		var id, code, title string
		var students, correct, errors, unknown int
		var latest interface{}
		if rows.Scan(&id, &code, &title, &students, &correct, &errors, &unknown, &latest) != nil {
			middleware.InternalError(w)
			return
		}
		result = append(result, map[string]interface{}{"concept_id": id, "concept_code": code, "concept_title": title, "students_assessed": students, "correct_evidence": correct, "error_evidence": errors, "unknown_confidence": unknown, "latest_evidence_at": latest})
	}
	middleware.JSON(w, http.StatusOK, result)
}

// TeacherClassSummary returns concept evidence only for students in classes
// assigned to the authenticated teacher. A supplied class_id narrows that
// scope; it never expands it.
func (h *Handler) TeacherClassSummary(w http.ResponseWriter, r *http.Request) {
	classID := strings.TrimSpace(r.URL.Query().Get("class_id"))
	rows, err := h.db.Query(r.Context(), `WITH per_student AS (
		SELECT le.user_id,le.concept_id,c.code,c.title,
		       COUNT(*) FILTER(WHERE le.is_correct) AS correct_count,
		       COUNT(*) FILTER(WHERE NOT le.is_correct) AS error_count,
		       COUNT(DISTINCT le.question_id) AS distinct_questions,
		       MAX(le.occurred_at) AS latest_evidence_at
		FROM learning_evidence le
		JOIN curriculum_concepts c ON c.id=le.concept_id
		WHERE le.institution_id=$1
		  AND EXISTS (
		    SELECT 1 FROM group_students gs JOIN group_teachers gt ON gt.group_id=gs.group_id
		    WHERE gs.user_id=le.user_id AND gt.user_id=$2 AND ($3='' OR gs.group_id::text=$3)
		  )
		GROUP BY le.user_id,le.concept_id,c.code,c.title
	)
	SELECT concept_id,code,title,COUNT(*) AS students_assessed,
	       SUM(correct_count),SUM(error_count),SUM(distinct_questions),
	       COUNT(*) FILTER(WHERE distinct_questions>=2 AND error_count>correct_count) AS students_needing_support,
	       MAX(latest_evidence_at)
	FROM per_student
	GROUP BY concept_id,code,title
	ORDER BY students_needing_support DESC,error_count DESC,latest_evidence_at DESC
	LIMIT 100`, middleware.GetInstitutionID(r), middleware.GetUserID(r), classID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	result := []map[string]interface{}{}
	for rows.Next() {
		var conceptID, code, title string
		var students, correct, errors, questions, needsSupport int
		var latest interface{}
		if err := rows.Scan(&conceptID, &code, &title, &students, &correct, &errors, &questions, &needsSupport, &latest); err != nil {
			middleware.InternalError(w)
			return
		}
		result = append(result, map[string]interface{}{
			"concept_id": conceptID, "concept_code": code, "concept_title": title,
			"students_assessed": students, "correct_evidence": correct, "error_evidence": errors,
			"distinct_questions": questions, "students_needing_support": needsSupport,
			"latest_evidence_at": latest,
		})
	}
	if rows.Err() != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, result)
}

func (h *Handler) StudentAssignments(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `SELECT a.id,a.quiz_id,q.title,a.purpose,a.due_at,ar.status,ar.attempt_id
		FROM learning_assignment_recipients ar JOIN learning_assignments a ON a.id=ar.assignment_id JOIN quizzes q ON q.id=a.quiz_id
		WHERE ar.student_id=$1 AND a.status='published' AND (a.due_at IS NULL OR a.due_at > now() OR ar.status IN ('submitted','started')) ORDER BY a.due_at NULLS LAST,a.created_at DESC LIMIT 100`, middleware.GetUserID(r))
	if err != nil {
		middleware.BadRequest(w, "assignments unavailable")
		return
	}
	defer rows.Close()
	result := []map[string]interface{}{}
	for rows.Next() {
		var id, quizID, title, purpose, status string
		var due, attempt interface{}
		if rows.Scan(&id, &quizID, &title, &purpose, &due, &status, &attempt) != nil {
			middleware.InternalError(w)
			return
		}
		result = append(result, map[string]interface{}{"id": id, "quiz_id": quizID, "title": title, "purpose": purpose, "due_at": due, "status": status, "attempt_id": attempt})
	}
	middleware.JSON(w, http.StatusOK, result)
}

type assignmentInput struct {
	GroupID   string  `json:"group_id"`
	QuizID    string  `json:"quiz_id"`
	Purpose   string  `json:"purpose"`
	DueAt     *string `json:"due_at"`
	ConceptID *string `json:"concept_id"`
}

type TeacherAssignment struct {
	ID             string     `json:"id"`
	GroupID        string     `json:"group_id"`
	GroupName      string     `json:"group_name"`
	QuizID         string     `json:"quiz_id"`
	QuizTitle      string     `json:"quiz_title"`
	Purpose        string     `json:"purpose"`
	Status         string     `json:"status"`
	DueAt          *time.Time `json:"due_at"`
	AssignedCount  int        `json:"assigned_count"`
	StartedCount   int        `json:"started_count"`
	SubmittedCount int        `json:"submitted_count"`
	OverdueCount   int        `json:"overdue_count"`
	ExcusedCount   int        `json:"excused_count"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (h *Handler) ListAssignments(w http.ResponseWriter, r *http.Request) {
	groupID := strings.TrimSpace(r.URL.Query().Get("group_id"))
	rows, err := h.db.Query(r.Context(), `
		SELECT a.id,a.group_id,g.name,a.quiz_id,q.title,a.purpose,a.status,a.due_at,
		       COUNT(ar.student_id),
		       COUNT(*) FILTER (WHERE ar.status='started'),
		       COUNT(*) FILTER (WHERE ar.status='submitted'),
		       COUNT(*) FILTER (WHERE ar.status='overdue' OR (ar.status='assigned' AND a.due_at < now())),
		       COUNT(*) FILTER (WHERE ar.status='excused'),a.created_at
		FROM learning_assignments a
		JOIN groups g ON g.id=a.group_id AND g.institution_id=a.institution_id
		JOIN group_teachers gt ON gt.group_id=a.group_id AND gt.user_id=$1
		JOIN quizzes q ON q.id=a.quiz_id
		LEFT JOIN learning_assignment_recipients ar ON ar.assignment_id=a.id
		WHERE a.institution_id=$2 AND ($3='' OR a.group_id::text=$3)
		GROUP BY a.id,g.name,q.title
		ORDER BY CASE WHEN a.status='published' THEN 0 ELSE 1 END,a.due_at NULLS LAST,a.created_at DESC
		LIMIT 200`, middleware.GetUserID(r), middleware.GetInstitutionID(r), groupID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	list := []TeacherAssignment{}
	for rows.Next() {
		var item TeacherAssignment
		if err := rows.Scan(&item.ID, &item.GroupID, &item.GroupName, &item.QuizID, &item.QuizTitle, &item.Purpose, &item.Status, &item.DueAt, &item.AssignedCount, &item.StartedCount, &item.SubmittedCount, &item.OverdueCount, &item.ExcusedCount, &item.CreatedAt); err != nil {
			middleware.InternalError(w)
			return
		}
		list = append(list, item)
	}
	if rows.Err() != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, list)
}

func (h *Handler) CreateAssignment(w http.ResponseWriter, r *http.Request) {
	var in assignmentInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || in.GroupID == "" || in.QuizID == "" || (in.Purpose != "baseline" && in.Purpose != "practice" && in.Purpose != "follow_up" && in.Purpose != "diagnostic") {
		middleware.BadRequest(w, "group_id, quiz_id and a valid purpose are required")
		return
	}
	var dueAt interface{}
	if in.DueAt != nil && strings.TrimSpace(*in.DueAt) != "" {
		parsed, parseErr := time.Parse(time.RFC3339, *in.DueAt)
		if parseErr != nil || parsed.Before(time.Now()) {
			middleware.BadRequest(w, "due_at must be a future RFC3339 timestamp")
			return
		}
		dueAt = parsed
	}
	conceptID := ""
	if in.ConceptID != nil {
		conceptID = strings.TrimSpace(*in.ConceptID)
	}
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(r.Context())
	var id string
	err = tx.QueryRow(r.Context(), `INSERT INTO learning_assignments(institution_id,group_id,quiz_id,purpose,due_at,created_by,source_concept_id)
		SELECT $1,g.id,q.id,$3,$4,$5,NULLIF($7,'')::uuid
		FROM groups g JOIN quizzes q ON q.id=$2
		WHERE g.id=$6 AND g.institution_id=$1 AND q.created_by=$5 AND q.status='published'
		AND EXISTS(SELECT 1 FROM group_teachers gt WHERE gt.group_id=g.id AND gt.user_id=$5)
		AND ($7='' OR EXISTS(
			SELECT 1 FROM curriculum_concepts c
			JOIN curriculum_chapters ch ON ch.id=c.chapter_id
			JOIN curriculum_versions cv ON cv.id=ch.version_id
			WHERE c.id::text=$7 AND cv.institution_id=$1
		)) RETURNING id`, middleware.GetInstitutionID(r), in.QuizID, in.Purpose, dueAt, middleware.GetUserID(r), in.GroupID, conceptID).Scan(&id)
	if err != nil {
		middleware.BadRequest(w, "quiz or class is unavailable to this teacher")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO learning_assignment_recipients(assignment_id,student_id) SELECT $1,gs.user_id FROM group_students gs WHERE gs.group_id=$2 ON CONFLICT DO NOTHING`, id, in.GroupID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": id})
}

type FollowUpOutcome struct {
	AssignmentID       string     `json:"assignment_id"`
	QuizID             string     `json:"quiz_id"`
	QuizTitle          string     `json:"quiz_title"`
	GroupID            string     `json:"group_id"`
	GroupName          string     `json:"group_name"`
	ConceptID          string     `json:"concept_id"`
	ConceptCode        string     `json:"concept_code"`
	ConceptTitle       string     `json:"concept_title"`
	Status             string     `json:"status"`
	CreatedAt          time.Time  `json:"created_at"`
	DueAt              *time.Time `json:"due_at"`
	Recipients         int        `json:"recipients"`
	Submitted          int        `json:"submitted"`
	BeforeCorrect      int        `json:"before_correct"`
	BeforeTotal        int        `json:"before_total"`
	BeforeStudents     int        `json:"before_students"`
	BeforeQuestions    int        `json:"before_questions"`
	AfterCorrect       int        `json:"after_correct"`
	AfterTotal         int        `json:"after_total"`
	AfterStudents      int        `json:"after_students"`
	AfterQuestions     int        `json:"after_questions"`
	ComparableStudents int        `json:"comparable_students"`
	ComparisonStatus   string     `json:"comparison_status"`
	ComparisonEndsAt   time.Time  `json:"comparison_ends_at"`
	ReviewStatus       string     `json:"review_status"`
	ReviewNote         *string    `json:"review_note"`
	ReviewedAt         *time.Time `json:"reviewed_at"`
}

func (h *Handler) FollowUpOutcomes(w http.ResponseWriter, r *http.Request) {
	groupID := strings.TrimSpace(r.URL.Query().Get("class_id"))
	rows, err := h.db.Query(r.Context(), `
		WITH scoped AS (
		  SELECT a.*,LEAST(
		    a.created_at+interval '90 days',
		    COALESCE(LEAD(a.created_at) OVER (PARTITION BY a.group_id,a.source_concept_id ORDER BY a.created_at),a.created_at+interval '90 days')
		  ) AS comparison_ends_at
		  FROM learning_assignments a
		  WHERE a.institution_id=$2 AND a.purpose='follow_up' AND a.source_concept_id IS NOT NULL
		), evidence AS (
		  SELECT a.id AS assignment_id,ar.student_id,le.id,le.question_id,le.is_correct,le.occurred_at
		  FROM scoped a
		  JOIN learning_assignment_recipients ar ON ar.assignment_id=a.id
		  LEFT JOIN learning_evidence le ON le.institution_id=a.institution_id
		    AND le.user_id=ar.student_id AND le.concept_id=a.source_concept_id
		    AND le.occurred_at>=a.created_at-interval '90 days' AND le.occurred_at<a.comparison_ends_at
		), paired AS (
		  SELECT assignment_id,student_id
		  FROM evidence e JOIN scoped a ON a.id=e.assignment_id
		  GROUP BY assignment_id,student_id,a.created_at
		  HAVING COUNT(e.id) FILTER (WHERE e.occurred_at<a.created_at)>0
		     AND COUNT(e.id) FILTER (WHERE e.occurred_at>=a.created_at)>0
		)
		SELECT a.id,a.quiz_id,q.title,a.group_id,g.name,c.id,c.code,c.title,a.status,a.created_at,a.due_at,
		       COUNT(DISTINCT ar.student_id),
		       COUNT(DISTINCT ar.student_id) FILTER (WHERE ar.status='submitted'),
		       COUNT(e.id) FILTER (WHERE e.occurred_at<a.created_at AND e.is_correct),
		       COUNT(e.id) FILTER (WHERE e.occurred_at<a.created_at),
		       COUNT(DISTINCT e.student_id) FILTER (WHERE e.occurred_at<a.created_at),
		       COUNT(DISTINCT e.question_id) FILTER (WHERE e.occurred_at<a.created_at),
		       COUNT(e.id) FILTER (WHERE e.occurred_at>=a.created_at AND e.is_correct),
		       COUNT(e.id) FILTER (WHERE e.occurred_at>=a.created_at),
		       COUNT(DISTINCT e.student_id) FILTER (WHERE e.occurred_at>=a.created_at),
		       COUNT(DISTINCT e.question_id) FILTER (WHERE e.occurred_at>=a.created_at),
		       COUNT(DISTINCT p.student_id),a.comparison_ends_at,
		       a.follow_up_review_status,a.follow_up_note,a.follow_up_reviewed_at
		FROM scoped a
		JOIN groups g ON g.id=a.group_id AND g.institution_id=a.institution_id
		JOIN group_teachers gt ON gt.group_id=a.group_id AND gt.user_id=$1
		JOIN quizzes q ON q.id=a.quiz_id
		JOIN curriculum_concepts c ON c.id=a.source_concept_id
		LEFT JOIN learning_assignment_recipients ar ON ar.assignment_id=a.id
		LEFT JOIN evidence e ON e.assignment_id=a.id AND e.student_id=ar.student_id
		LEFT JOIN paired p ON p.assignment_id=a.id AND p.student_id=ar.student_id
		WHERE ($3='' OR a.group_id::text=$3)
		GROUP BY a.id,a.quiz_id,a.group_id,a.status,a.created_at,a.due_at,a.comparison_ends_at,
		         a.follow_up_review_status,a.follow_up_note,a.follow_up_reviewed_at,
		         q.title,g.name,c.id,c.code,c.title
		ORDER BY a.created_at DESC LIMIT 100`, middleware.GetUserID(r), middleware.GetInstitutionID(r), groupID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	result := []FollowUpOutcome{}
	for rows.Next() {
		var item FollowUpOutcome
		if err := rows.Scan(&item.AssignmentID, &item.QuizID, &item.QuizTitle, &item.GroupID, &item.GroupName, &item.ConceptID, &item.ConceptCode, &item.ConceptTitle, &item.Status, &item.CreatedAt, &item.DueAt, &item.Recipients, &item.Submitted, &item.BeforeCorrect, &item.BeforeTotal, &item.BeforeStudents, &item.BeforeQuestions, &item.AfterCorrect, &item.AfterTotal, &item.AfterStudents, &item.AfterQuestions, &item.ComparableStudents, &item.ComparisonEndsAt, &item.ReviewStatus, &item.ReviewNote, &item.ReviewedAt); err != nil {
			middleware.InternalError(w)
			return
		}
		item.ComparisonStatus = "comparable"
		if item.AfterTotal == 0 {
			item.ComparisonStatus = "awaiting_after_evidence"
		} else if item.ComparableStudents < 3 {
			item.ComparisonStatus = "insufficient_student_overlap"
		} else if item.BeforeQuestions < 2 || item.AfterQuestions < 2 {
			item.ComparisonStatus = "insufficient_question_variety"
		}
		result = append(result, item)
	}
	if rows.Err() != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, result)
}

type followUpReviewInput struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

func (h *Handler) ReviewFollowUp(w http.ResponseWriter, r *http.Request) {
	var in followUpReviewInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || (in.Status != "continue_support" && in.Status != "resolved") {
		middleware.BadRequest(w, "status must be continue_support or resolved")
		return
	}
	in.Note = strings.TrimSpace(in.Note)
	if len(in.Note) > 2000 {
		middleware.BadRequest(w, "note must be 2000 characters or fewer")
		return
	}
	result, err := h.db.Exec(r.Context(), `UPDATE learning_assignments a
		SET follow_up_review_status=$1,follow_up_note=NULLIF($2,''),follow_up_reviewed_by=$3,follow_up_reviewed_at=now(),
		    status=CASE WHEN $1='resolved' THEN 'closed' ELSE 'published' END
		WHERE a.id=$4 AND a.institution_id=$5 AND a.purpose='follow_up' AND a.source_concept_id IS NOT NULL
		AND EXISTS(SELECT 1 FROM group_teachers gt WHERE gt.group_id=a.group_id AND gt.user_id=$3)`,
		in.Status, in.Note, middleware.GetUserID(r), chi.URLParam(r, "assignmentId"), middleware.GetInstitutionID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if result.RowsAffected() == 0 {
		middleware.NotFound(w, "follow-up assignment")
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": in.Status})
}

func (h *Handler) CloseAssignment(w http.ResponseWriter, r *http.Request) {
	result, err := h.db.Exec(r.Context(), `UPDATE learning_assignments a SET status='closed'
		WHERE a.id=$1 AND a.institution_id=$2 AND a.status='published'
		AND EXISTS(SELECT 1 FROM group_teachers gt WHERE gt.group_id=a.group_id AND gt.user_id=$3)`,
		chi.URLParam(r, "assignmentId"), middleware.GetInstitutionID(r), middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if result.RowsAffected() == 0 {
		middleware.NotFound(w, "assignment")
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "closed"})
}

type reviewInput struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func (h *Handler) Review(w http.ResponseWriter, r *http.Request) {
	var in reviewInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || (in.Status != "confirmed" && in.Status != "dismissed") || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 1000 {
		middleware.BadRequest(w, "status and reason are required")
		return
	}
	studentID, misconceptionID := chi.URLParam(r, "studentId"), chi.URLParam(r, "misconceptionId")
	var allowed bool
	err := h.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM learning_evidence le JOIN misconceptions m ON m.id=$2
		WHERE le.user_id=$1 AND le.misconception_id=$2 AND le.institution_id=$3 AND
		(NOT EXISTS(SELECT 1 FROM group_teachers WHERE user_id=$4) OR EXISTS(SELECT 1 FROM group_students gs JOIN group_teachers gt ON gt.group_id=gs.group_id WHERE gs.user_id=$1 AND gt.user_id=$4)))`, studentID, misconceptionID, middleware.GetInstitutionID(r), middleware.GetUserID(r)).Scan(&allowed)
	if err != nil || !allowed {
		middleware.NotFound(w, "insight")
		return
	}
	_, err = h.db.Exec(r.Context(), `INSERT INTO misconception_reviews(institution_id,student_id,misconception_id,status,reason,reviewed_by)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(institution_id,student_id,misconception_id) DO UPDATE SET status=EXCLUDED.status,reason=EXCLUDED.reason,reviewed_by=EXCLUDED.reviewed_by,reviewed_at=now()`, middleware.GetInstitutionID(r), studentID, misconceptionID, in.Status, strings.TrimSpace(in.Reason), middleware.GetUserID(r))
	if err != nil {
		middleware.BadRequest(w, "could not review insight")
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": in.Status})
}
