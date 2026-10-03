package user

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Report metrics use the first completed attempt per assessment. Retakes never
// increase coverage or replace a lower first score with a best score.
const reportAttempts = `WITH first_attempts AS (
 SELECT DISTINCT ON (qa.user_id,qa.quiz_id) qa.user_id,qa.quiz_id,qa.completed_at,
 qa.total_correct,qa.total_questions,100.0*qa.total_correct/qa.total_questions AS score_pct,q.institution_id,COALESCE(NULLIF(q.domain,''),'Unclassified') AS domain
 FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id
 WHERE qa.status='completed' AND qa.completed_at IS NOT NULL AND qa.total_questions>0
 AND qa.total_correct IS NOT NULL
 ORDER BY qa.user_id,qa.quiz_id,qa.completed_at,qa.id
) `

type LearningReport struct {
	Name                                        string
	Generated                                   time.Time
	MemberSince                                 time.Time
	Streak, LongestStreak                       int
	Assessments, Questions, Correct, ActiveDays int
	RecentCount, PreviousCount                  int
	RecentAccuracy, PreviousAccuracy            *float64
	FirstEvidence, LastEvidence                 *time.Time
	Stages                                      []ReportStage
	Education                                   []Education
	Domains                                     []ReportDomain
	Assigned, Submitted, Overdue                int
}

type ReportStage struct {
	Institution, Grade, Status      string
	Start                           time.Time
	End                             *time.Time
	Assessments, Questions, Correct int
}

type ReportDomain struct {
	Name                            string
	Assessments, Questions, Correct int
	PeerAssessments                 int
	PeerCount                       int
	PeerStanding                    *float64
}

func (s *Service) GetLearningReport(ctx context.Context, userID string) (*LearningReport, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	r := &LearningReport{}
	err = tx.QueryRow(ctx, `SELECT display_name,member_since,current_streak,longest_streak,now() FROM users WHERE id=$1 AND deleted_at IS NULL`, userID).Scan(&r.Name, &r.MemberSince, &r.Streak, &r.LongestStreak, &r.Generated)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, reportAttempts+`SELECT COUNT(*),COALESCE(SUM(total_questions),0),COALESCE(SUM(total_correct),0),
 COUNT(DISTINCT (completed_at AT TIME ZONE 'UTC')::date),MIN(completed_at),MAX(completed_at),
 COUNT(*) FILTER(WHERE completed_at>=now()-interval '30 days'),
 COUNT(*) FILTER(WHERE completed_at>=now()-interval '60 days' AND completed_at<now()-interval '30 days'),
 100.0*SUM(total_correct) FILTER(WHERE completed_at>=now()-interval '30 days')/NULLIF(SUM(total_questions) FILTER(WHERE completed_at>=now()-interval '30 days'),0),
 100.0*SUM(total_correct) FILTER(WHERE completed_at>=now()-interval '60 days' AND completed_at<now()-interval '30 days')/NULLIF(SUM(total_questions) FILTER(WHERE completed_at>=now()-interval '60 days' AND completed_at<now()-interval '30 days'),0)
 FROM first_attempts WHERE user_id=$1`, userID).Scan(&r.Assessments, &r.Questions, &r.Correct, &r.ActiveDays, &r.FirstEvidence, &r.LastEvidence, &r.RecentCount, &r.PreviousCount, &r.RecentAccuracy, &r.PreviousAccuracy)
	if err != nil {
		return nil, err
	}

	// Compare each assessment only with other active students on that exact quiz.
	// Show aggregates only; five peers per quiz and three quizzes per domain are
	// minimum reporting thresholds, not a claim of statistical significance.
	rows, err := tx.Query(ctx, reportAttempts+`, mine AS (SELECT * FROM first_attempts WHERE user_id=$1),
 compared AS (
 SELECT m.*,p.n,p.standing FROM mine m LEFT JOIN LATERAL (
 SELECT COUNT(*) AS n,100.0*COUNT(*) FILTER(WHERE peer.score_pct<m.score_pct)/NULLIF(COUNT(*),0) AS standing
 FROM first_attempts peer JOIN users u ON u.id=peer.user_id
 WHERE peer.quiz_id=m.quiz_id AND peer.user_id<>$1 AND u.role='student' AND u.status='active' AND u.deleted_at IS NULL
 ) p ON true)
 SELECT domain,COUNT(*),SUM(total_questions),SUM(total_correct),COUNT(*) FILTER(WHERE n>=5),
 COALESCE(MIN(n) FILTER(WHERE n>=5),0),AVG(standing) FILTER(WHERE n>=5)
 FROM compared GROUP BY domain ORDER BY 100.0*SUM(total_correct)/SUM(total_questions) DESC,domain`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d ReportDomain
		if err = rows.Scan(&d.Name, &d.Assessments, &d.Questions, &d.Correct, &d.PeerAssessments, &d.PeerCount, &d.PeerStanding); err != nil {
			rows.Close()
			return nil, err
		}
		if d.PeerAssessments < 3 {
			d.PeerStanding = nil
		}
		r.Domains = append(r.Domains, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	// Grade stages come from main classes with a grade: live, ended (archived
	// but still listed) and left (history), clipped to the enrollment. Time no
	// graded class covers is its own stage: the enrollment's current grade after
	// the last class (or with none at all), "Not recorded" before or between.
	// ponytail: two simultaneous graded main classes double-count attempts; pick a primary class if that ever happens.
	rows, err = tx.Query(ctx, reportAttempts+`, memberships AS (
 SELECT g.institution_id, g.grade, gs.joined_at AS start, g.archived_at AS finish
 FROM group_students gs JOIN groups g ON g.id=gs.group_id
 WHERE gs.user_id=$1 AND g.kind='class' AND g.grade IS NOT NULL
 UNION ALL
 SELECT g.institution_id, h.grade, h.joined_at, h.left_at
 FROM group_student_history h JOIN groups g ON g.id=h.group_id
 WHERE h.user_id=$1 AND g.kind='class' AND h.grade IS NOT NULL
 ), enr AS (
 SELECT id, institution_id, status, COALESCE(NULLIF(grade,''),'Not recorded') AS grade, COALESCE(joined_at, created_at) AS es, ended_at AS ee
 FROM enrollments WHERE user_id=$1
 ), clipped AS (
 -- GREATEST/LEAST skip NULLs, so an open end stays open only if both are.
 SELECT e.id, m.grade, GREATEST(m.start, e.es) AS start, LEAST(m.finish, e.ee) AS finish
 FROM enr e JOIN memberships m ON m.institution_id=e.institution_id
  AND m.start < COALESCE(e.ee,'infinity') AND COALESCE(m.finish,'infinity') > e.es
 ), gaps AS (
 SELECT c.id, 'Not recorded' AS grade,
        CASE WHEN ROW_NUMBER() OVER w = 1 THEN e.es ELSE LAG(c.finish) OVER w END AS start, c.start AS finish
 FROM clipped c JOIN enr e ON e.id=c.id WINDOW w AS (PARTITION BY c.id ORDER BY c.start)
 UNION ALL
 SELECT c.id, e.grade, MAX(c.finish), e.ee FROM clipped c JOIN enr e ON e.id=c.id
 GROUP BY c.id, e.grade, e.ee HAVING bool_and(c.finish IS NOT NULL)
 UNION ALL
 SELECT e.id, e.grade, e.es, e.ee FROM enr e WHERE NOT EXISTS (SELECT 1 FROM clipped c WHERE c.id=e.id)
 ), stages AS (
 SELECT e.id, e.institution_id, e.status, p.grade, p.start, p.finish
 FROM (SELECT * FROM clipped UNION ALL SELECT * FROM gaps WHERE start IS NOT NULL AND (finish IS NULL OR start < finish)) p
 JOIN enr e ON e.id=p.id
 ) SELECT i.name,s.grade,s.status,s.start,s.finish,COUNT(a.quiz_id),COALESCE(SUM(a.total_questions),0),COALESCE(SUM(a.total_correct),0)
 FROM stages s JOIN institutions i ON i.id=s.institution_id LEFT JOIN first_attempts a
 ON a.user_id=$1 AND a.institution_id=s.institution_id AND a.completed_at>=s.start AND (s.finish IS NULL OR a.completed_at<s.finish)
 GROUP BY s.id,i.name,s.grade,s.status,s.start,s.finish ORDER BY s.start`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var st ReportStage
		if err = rows.Scan(&st.Institution, &st.Grade, &st.Status, &st.Start, &st.End, &st.Assessments, &st.Questions, &st.Correct); err != nil {
			rows.Close()
			return nil, err
		}
		r.Stages = append(r.Stages, st)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `SELECT id,institution_name,COALESCE(degree,''),COALESCE(field,''),start_year,end_year,is_current FROM user_education WHERE user_id=$1 ORDER BY start_year NULLS LAST,id`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e Education
		if err = rows.Scan(&e.ID, &e.InstitutionName, &e.Degree, &e.Field, &e.StartYear, &e.EndYear, &e.IsCurrent); err != nil {
			rows.Close()
			return nil, err
		}
		r.Education = append(r.Education, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, `SELECT COUNT(*),COUNT(*) FILTER(WHERE ar.status='submitted'),COUNT(*) FILTER(WHERE ar.status IN ('assigned','started','overdue') AND COALESCE(ar.due_at_override,a.due_at)<now())
 FROM learning_assignment_recipients ar JOIN learning_assignments a ON a.id=ar.assignment_id
 WHERE ar.student_id=$1 AND a.status IN ('published','closed') AND ar.status<>'excused' AND (a.available_at IS NULL OR a.available_at<=now())`, userID).Scan(&r.Assigned, &r.Submitted, &r.Overdue)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

func reportAccuracy(correct, questions int) string {
	if questions == 0 {
		return "Not enough recorded evidence"
	}
	return fmt.Sprintf("%.1f%% accuracy (%d / %d correct)", 100*float64(correct)/float64(questions), correct, questions)
}
