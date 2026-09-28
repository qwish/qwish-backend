package notification

import (
	"context"
	"encoding/json"
)

// Teacher notification topics (R7). Keep in sync with teacher.NotificationTopics.
const (
	TopicOverdueWork         = "overdue_work"
	TopicTopicRequests       = "topic_requests"
	TopicFollowUpEvidence    = "follow_up_evidence"
	TopicSuggestionDecisions = "suggestion_decisions"
	TopicAssessmentDecisions = "assessment_decisions"
)

var teacherDefaults = map[string][2]bool{
	TopicOverdueWork:         {true, true},
	TopicTopicRequests:       {true, false},
	TopicFollowUpEvidence:    {true, true},
	TopicSuggestionDecisions: {true, false},
	TopicAssessmentDecisions: {true, true},
}

// teacherChannels returns (in_app, email) for a teacher and topic, falling back
// to defaults when the teacher never saved preferences. Unknown topics
// (e.g. support reviews) are always in-app only.
func (s *Service) teacherChannels(ctx context.Context, userID, topic string) (bool, bool) {
	d, ok := teacherDefaults[topic]
	if !ok {
		return true, false
	}
	var raw []byte
	if s.db.QueryRow(ctx, `SELECT prefs FROM teacher_notification_preferences WHERE user_id=$1`, userID).Scan(&raw) == nil {
		var stored map[string]struct {
			InApp bool `json:"in_app"`
			Email bool `json:"email"`
		}
		if json.Unmarshal(raw, &stored) == nil {
			if v, ok := stored[topic]; ok {
				return v.InApp, v.Email
			}
		}
	}
	return d[0], d[1]
}

// EmitTeacher sends a teacher-facing notification honouring their channel
// preferences. `reference` is the idempotency key: the in-app row is unique
// per (user, reference), and email is only sent when that row was new.
func (s *Service) EmitTeacher(ctx context.Context, userID, topic, kind, title, body, reference, link string) {
	if s == nil || s.db == nil || userID == "" {
		return
	}
	inApp, email := s.teacherChannels(ctx, userID, topic)
	var inserted bool
	if inApp {
		var before int
		_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM user_notifications WHERE user_id=$1 AND reference=$2`, userID, reference).Scan(&before)
		if before == 0 {
			s.Emit(ctx, userID, kind, title, body, WithReference(reference))
			inserted = true
		}
	} else {
		// Still dedupe email across cron runs without an in-app row.
		var sent int
		_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM notification_log WHERE reference=$1`, "teacher:"+userID+":"+reference).Scan(&sent)
		inserted = sent == 0
	}
	if !email || !inserted {
		return
	}
	var to string
	if s.db.QueryRow(ctx, `SELECT email FROM users WHERE id=$1 AND deleted_at IS NULL`, userID).Scan(&to) != nil || to == "" {
		return
	}
	content := heading(title) + paragraph(body)
	if link != "" {
		content += primaryButton("Open Qwish", link)
	}
	_ = s.SendEmail(ctx, to, title, emailLayout(body, content), "teacher:"+userID+":"+reference)
}
