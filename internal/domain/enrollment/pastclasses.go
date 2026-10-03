package enrollment

import (
	"context"
	"time"
)

type PastClass struct {
	GroupID         string    `json:"group_id"`
	ClassName       string    `json:"class_name"`
	InstitutionName string    `json:"institution_name"`
	From            time.Time `json:"from"`
	To              time.Time `json:"to"`
	Assessments     int       `json:"assessments"`
	Questions       int       `json:"questions"`
	Correct         int       `json:"correct"`
}

type ConceptStat struct {
	ConceptID string `json:"concept_id"`
	Title     string `json:"title"`
	Correct   int    `json:"correct"`
	Errors    int    `json:"errors"`
}

// pastWindowsSQL: every finished stay in a main class. $1 = user.
const pastWindowsSQL = `windows AS (
	SELECT g.id AS group_id, g.name, g.institution_id, gs.joined_at AS from_at, g.archived_at AS to_at
	  FROM group_students gs JOIN groups g ON g.id=gs.group_id
	 WHERE gs.user_id=$1 AND g.kind='class' AND g.archived_at IS NOT NULL
	UNION ALL
	SELECT g.id, g.name, g.institution_id, h.joined_at, h.left_at
	  FROM group_student_history h JOIN groups g ON g.id=h.group_id
	 WHERE h.user_id=$1 AND g.kind='class')`

func (s *Service) PastClasses(ctx context.Context, userID string) ([]PastClass, error) {
	rows, err := s.db.Query(ctx, `WITH `+pastWindowsSQL+`,
	first_attempts AS (
		SELECT DISTINCT ON (qa.quiz_id) qa.quiz_id, qa.completed_at, qa.total_correct, qa.total_questions, q.institution_id
		  FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id
		 WHERE qa.user_id=$1 AND qa.status='completed' AND qa.total_questions>0
		 ORDER BY qa.quiz_id, qa.completed_at)
	SELECT w.group_id::text, w.name, i.name, w.from_at, w.to_at,
	       count(a.quiz_id), COALESCE(sum(a.total_questions),0), COALESCE(sum(a.total_correct),0)
	  FROM windows w JOIN institutions i ON i.id=w.institution_id
	  LEFT JOIN first_attempts a ON a.institution_id=w.institution_id AND a.completed_at>=w.from_at AND a.completed_at<w.to_at
	 GROUP BY w.group_id, w.name, i.name, w.from_at, w.to_at
	 ORDER BY w.to_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PastClass{}
	for rows.Next() {
		var p PastClass
		if err := rows.Scan(&p.GroupID, &p.ClassName, &p.InstitutionName, &p.From, &p.To, &p.Assessments, &p.Questions, &p.Correct); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) PastClassConcepts(ctx context.Context, userID, groupID string) ([]ConceptStat, error) {
	rows, err := s.db.Query(ctx, `WITH `+pastWindowsSQL+`
	SELECT c.id::text, c.title, count(*) FILTER (WHERE le.is_correct), count(*) FILTER (WHERE NOT le.is_correct)
	  FROM windows w
	  JOIN learning_evidence le ON le.user_id=$1 AND le.institution_id=w.institution_id
	                           AND le.occurred_at>=w.from_at AND le.occurred_at<w.to_at AND NOT le.timed_out
	  JOIN curriculum_concepts c ON c.id=le.concept_id
	 WHERE w.group_id=$2
	 GROUP BY c.id, c.title ORDER BY count(*) FILTER (WHERE NOT le.is_correct) DESC, c.title`, userID, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConceptStat{}
	for rows.Next() {
		var c ConceptStat
		if err := rows.Scan(&c.ConceptID, &c.Title, &c.Correct, &c.Errors); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
