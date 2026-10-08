package db

// ClassAttemptSQL counts every completed retake once in the rolling 30-day
// window. Modern context requires the immutable assignment and its recipient.
// Legacy fallback is deliberately conservative: an institution-owned quiz with
// an explicit class and no modern assignments. Present membership is irrelevant.
func ClassAttemptSQL(group, institution string) string {
	return `qa.status='completed' AND qa.completed_at >= now()-interval '30 days' AND (
 (qa.assignment_id IS NOT NULL AND EXISTS (
 SELECT 1 FROM learning_assignments a JOIN learning_assignment_recipients ar ON ar.assignment_id=a.id
 WHERE a.id=qa.assignment_id AND a.group_id=` + group + ` AND a.institution_id=` + institution + `
 AND a.quiz_id=qa.quiz_id AND ar.student_id=qa.user_id))
 OR (qa.assignment_id IS NULL AND qa.legacy_group_id=` + group + ` AND EXISTS (
 SELECT 1 FROM groups legacy WHERE legacy.id=qa.legacy_group_id AND legacy.institution_id=` + institution + `)))`
}
