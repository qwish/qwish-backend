package scheduler

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/qwish/backend/internal/domain/notification"
)

// SendTeacherNotifications (R7) is hourly and idempotent: every alert's
// reference is stable, and EmitTeacher skips references already delivered.
//   - assignment_overdue  once per assignment after it passes due with non-submitters
//   - follow_up_evidence  once per follow-up when its comparison becomes comparable
//   - support_review      once per plan on the morning of review_on
func (s *Scheduler) SendTeacherNotifications(ctx context.Context, teacherURL string) error {
	log.Println("[cron] running teacher-notifications")
	if s.notifSvc == nil {
		return nil
	}
	base := strings.TrimRight(teacherURL, "/")
	sent := 0

	type alert struct{ userID, topic, kind, title, body, ref, link string }
	alerts := []alert{}

	// Overdue work — every teacher of the class hears about it once.
	rows, err := s.db.Query(ctx, `
		SELECT gt.user_id, a.id, a.group_id, q.title, g.name,
		       COUNT(*) FILTER (WHERE ar.status IN ('assigned','started','overdue') AND COALESCE(ar.due_at_override,a.due_at)<now())
		  FROM learning_assignments a
		  JOIN quizzes q ON q.id=a.quiz_id
		  JOIN groups g ON g.id=a.group_id
		  JOIN group_teachers gt ON gt.group_id=a.group_id
		  JOIN learning_assignment_recipients ar ON ar.assignment_id=a.id
		 WHERE a.status='published' AND a.due_at IS NOT NULL AND a.due_at<now() AND a.due_at>now()-interval '3 days'
		 GROUP BY gt.user_id, a.id, a.group_id, q.title, g.name
		HAVING COUNT(*) FILTER (WHERE ar.status IN ('assigned','started','overdue') AND COALESCE(ar.due_at_override,a.due_at)<now()) > 0`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var uid, aid, gid, title, class string
		var n int
		if rows.Scan(&uid, &aid, &gid, &title, &class, &n) == nil {
			alerts = append(alerts, alert{uid, notification.TopicOverdueWork, "assignment_overdue", "Assignment",
				fmt.Sprintf("%s is past due and %d student%s in %s haven’t submitted.", title, n, plural(n), class),
				"assignment:" + aid + ":" + gid, base + "/classes/detail?id=" + gid + "&tab=assignments&assignment=" + aid})
		}
	}
	rows.Close()

	// Follow-up evidence ready — same rule as the Follow-up outcomes report:
	// ≥3 students with evidence in both windows and ≥2 distinct questions each.
	rows, err = s.db.Query(ctx, `
		WITH f AS (
		  SELECT a.id, a.created_by, a.institution_id, a.source_concept_id, a.created_at, g.name AS class, c.title AS concept
		    FROM learning_assignments a JOIN groups g ON g.id=a.group_id JOIN curriculum_concepts c ON c.id=a.source_concept_id
		   WHERE a.purpose='follow_up' AND a.source_concept_id IS NOT NULL
		     AND a.created_at>now()-interval '90 days' AND a.follow_up_review_status='unreviewed'
		), e AS (
		  SELECT f.id, ar.student_id, le.question_id, le.occurred_at>=f.created_at AS after
		    FROM f JOIN learning_assignment_recipients ar ON ar.assignment_id=f.id
		    JOIN learning_evidence le ON le.institution_id=f.institution_id AND le.user_id=ar.student_id
		     AND le.concept_id=f.source_concept_id AND NOT le.timed_out
		     AND le.occurred_at>=f.created_at-interval '90 days'
		)
		SELECT f.id, f.created_by, f.class, f.concept FROM f
		 WHERE (SELECT COUNT(DISTINCT question_id) FROM e WHERE e.id=f.id AND NOT after) >= 2
		   AND (SELECT COUNT(DISTINCT question_id) FROM e WHERE e.id=f.id AND after) >= 2
		   AND (SELECT COUNT(*) FROM (SELECT student_id FROM e WHERE e.id=f.id GROUP BY student_id
		        HAVING bool_or(after) AND bool_or(NOT after)) p) >= 3`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var aid, uid, class, concept string
		if rows.Scan(&aid, &uid, &class, &concept) == nil {
			alerts = append(alerts, alert{uid, notification.TopicFollowUpEvidence, "follow_up_evidence", "Follow-up outcome",
				"Follow-up on " + concept + " in " + class + " now has after-evidence to compare.",
				"follow_up:" + aid, base + "/insights/reports?view=followups"})
		}
	}
	rows.Close()

	// Support reviews — the morning they fall due (06:00+ in the institution's timezone).
	rows, err = s.db.Query(ctx, `
		SELECT s.teacher_id, s.student_id, COALESCE(NULLIF(u.display_name,''),u.full_name,'a student'), to_char(s.review_on,'YYYY-MM-DD')
		  FROM teacher_student_support s
		  JOIN users u ON u.id=s.student_id
		  JOIN institutions i ON i.id=s.institution_id
		 WHERE s.status<>'resolved' AND s.review_on IS NOT NULL
		   AND s.review_on <= (now() AT TIME ZONE COALESCE(NULLIF(i.timezone,''),'Asia/Kolkata'))::date
		   AND EXTRACT(HOUR FROM now() AT TIME ZONE COALESCE(NULLIF(i.timezone,''),'Asia/Kolkata')) >= 6`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var tid, sid, name, day string
		if rows.Scan(&tid, &sid, &name, &day) == nil {
			alerts = append(alerts, alert{tid, "support_review", "support_review", "Support plan",
				"Support review for " + name + " is due today.", "support_review:" + sid + ":" + day, base + "/students/detail?id=" + sid})
		}
	}
	rows.Close()

	for _, a := range alerts {
		s.notifSvc.EmitTeacher(ctx, a.userID, a.topic, a.kind, a.title, a.body, a.ref, a.link)
		sent++
	}
	log.Printf("[cron] teacher-notifications done (%d candidates) at %s", sent, time.Now().UTC().Format(time.RFC3339))
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
