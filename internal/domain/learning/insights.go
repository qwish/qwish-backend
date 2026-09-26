package learning

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/middleware"
)

const defaultInsightWindowDays = 30

// The selected quiz anchors the students/concepts shown. Evidence for those
// pairs spans this teacher's quizzes, within one institution and bounded window.
// Tags are joined only after concept totals so multi-tag answers cannot inflate
// the contradictory-correct count. A review overrides the automatic status.
const quizInsightsSQL = `WITH eligible AS MATERIALIZED (
 SELECT le.*,qa.quiz_id FROM learning_evidence le
 JOIN quiz_attempts qa ON qa.id=le.attempt_id
 JOIN quizzes q ON q.id=qa.quiz_id
 WHERE le.institution_id=$3 AND q.created_by=$2
   AND NOT le.timed_out AND le.occurred_at>=now()-($5::int*interval '1 day')
   AND ($4='' OR (
     EXISTS(SELECT 1 FROM group_teachers gt WHERE gt.group_id::text=$4 AND gt.user_id=$2)
     AND EXISTS(SELECT 1 FROM group_students gs WHERE gs.group_id::text=$4 AND gs.user_id=le.user_id)
   ))
), anchors AS (
 SELECT DISTINCT user_id,concept_id FROM eligible WHERE quiz_id=$1
), scoped AS MATERIALIZED (
 SELECT e.* FROM eligible e JOIN anchors a USING(user_id,concept_id)
), concept_totals AS (
 SELECT user_id,concept_id,COUNT(*) FILTER(WHERE is_correct) AS correct FROM scoped GROUP BY user_id,concept_id
), tagged AS (
 SELECT e.*,em.misconception_id AS tag_id FROM scoped e
 LEFT JOIN learning_evidence_misconceptions em ON em.evidence_id=e.id
), grouped AS (
 SELECT user_id,concept_id,tag_id,
 COUNT(*) FILTER(WHERE NOT is_correct) AS errors,
 COUNT(DISTINCT question_id) FILTER(WHERE NOT is_correct) AS distinct_errors,
 COUNT(*) FILTER(WHERE NOT is_correct AND confidence_level='very_confident') AS high,
 COUNT(*) FILTER(WHERE NOT is_correct AND confidence_level='pretty_sure') AS middle,
 COUNT(*) FILTER(WHERE NOT is_correct AND confidence_level='not_sure') AS low,
 COUNT(*) FILTER(WHERE NOT is_correct AND confidence_level IS NULL) AS unknown,
 MAX(occurred_at) AS latest,
 array_agg(DISTINCT question_id::text) FILTER(WHERE NOT is_correct) AS evidence_question_ids
 FROM tagged GROUP BY user_id,concept_id,tag_id
)
 SELECT g.user_id,u.display_name,g.concept_id,c.code,c.title,g.tag_id,m.title,
 g.errors,g.distinct_errors,t.correct,g.high,g.middle,g.low,g.unknown,g.latest,
 r.status,r.reason,r.reviewed_at,r.reviewed_by,COALESCE(g.evidence_question_ids,ARRAY[]::text[])
 FROM grouped g JOIN concept_totals t USING(user_id,concept_id)
 JOIN users u ON u.id=g.user_id JOIN curriculum_concepts c ON c.id=g.concept_id
 LEFT JOIN misconceptions m ON m.id=g.tag_id
 LEFT JOIN misconception_reviews r ON r.institution_id=$3 AND r.student_id=g.user_id AND r.misconception_id=g.tag_id
 ORDER BY g.latest DESC,g.user_id,g.concept_id,g.tag_id NULLS LAST LIMIT 500`

func (h *Handler) QuizInsights(w http.ResponseWriter, r *http.Request) {
	days := defaultInsightWindowDays
	if value := r.URL.Query().Get("window_days"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 365 {
			middleware.BadRequest(w, "window_days must be between 1 and 365")
			return
		}
		days = n
	}
	rows, err := h.db.Query(r.Context(), quizInsightsSQL, chi.URLParam(r, "quizId"), middleware.GetUserID(r), middleware.GetInstitutionID(r), r.URL.Query().Get("class_id"), days)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	result := []map[string]interface{}{}
	for rows.Next() {
		var studentID, studentName, conceptID, conceptCode, conceptTitle string
		var misconceptionID, misconceptionTitle, reviewStatus, reviewReason, reviewer *string
		var errors, distinct, correct, high, middle, low, unknown int
		var latest, reviewedAt interface{}
		var questions []string
		if err = rows.Scan(&studentID, &studentName, &conceptID, &conceptCode, &conceptTitle, &misconceptionID, &misconceptionTitle, &errors, &distinct, &correct, &high, &middle, &low, &unknown, &latest, &reviewStatus, &reviewReason, &reviewedAt, &reviewer, &questions); err != nil {
			middleware.InternalError(w)
			return
		}
		automatic := insightStatus(misconceptionID != nil, distinct, high)
		status := automatic
		if reviewStatus != nil {
			status = *reviewStatus
		}
		result = append(result, map[string]interface{}{
			"student_id": studentID, "student_name": studentName, "concept_id": conceptID, "concept_code": conceptCode, "concept_title": conceptTitle,
			"misconception_id": misconceptionID, "misconception_title": misconceptionTitle, "status": status, "automatic_status": automatic,
			"error_count": errors, "distinct_questions": distinct, "contradictory_correct": correct,
			"confidence":         map[string]int{"very_confident": high, "pretty_sure": middle, "not_sure": low, "unknown": unknown},
			"latest_evidence_at": latest, "window_days": days, "evidence_question_ids": questions,
			"review_status": reviewStatus, "review_reason": reviewReason, "reviewed_at": reviewedAt, "reviewed_by": reviewer,
		})
	}
	if err = rows.Err(); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, result)
}
