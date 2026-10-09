package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// Teacher notification topics (R7). Keep in sync with teacher.NotificationTopics.
const (
	TopicOverdueWork         = "overdue_work"
	TopicFollowUpEvidence    = "follow_up_evidence"
	TopicSuggestionDecisions = "suggestion_decisions"
	TopicAssessmentDecisions = "assessment_decisions"
)

var teacherDefaults = map[string][2]bool{
	TopicOverdueWork:         {true, true},
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

// EmitTeacher persists the in-app notification and email job atomically.
// A transaction-scoped lock serializes recurring deliveries for this reference.
func (s *Service) EmitTeacher(ctx context.Context, userID, topic, kind, title, body, reference, link string) {
	if s == nil || s.db == nil || userID == "" {
		return
	}
	if err := s.emitTeacher(ctx, userID, topic, kind, title, body, reference, link); err != nil {
		log.Printf("[notification] teacher delivery enqueue: %v", err)
	}
}

func (s *Service) emitTeacher(ctx context.Context, userID, topic, kind, title, body, reference, link string) error {
	inApp, email := s.teacherChannels(ctx, userID, topic)
	if !inApp && !email {
		return nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	ref := "teacher:" + userID + ":" + reference
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,2))`, ref); err != nil {
		return err
	}
	if inApp {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_notifications WHERE user_id=$1 AND reference=$2)`, userID, reference).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if err = s.EmitTx(ctx, tx, userID, kind, title, body, WithReference(reference)); err != nil {
				return err
			}
		}
	}
	if email {
		var sent bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notification_log WHERE reference=$1 AND status='sent')`, ref).Scan(&sent); err != nil {
			return err
		}
		if !sent {
			var to string
			if err = tx.QueryRow(ctx, `SELECT COALESCE(email,'') FROM users WHERE id=$1 AND deleted_at IS NULL`, userID).Scan(&to); err != nil {
				return fmt.Errorf("teacher email recipient: %w", err)
			}
			if to != "" {
				if err = s.QueueEmailTx(ctx, tx, ref+":"+to, to, title, tmplTeacherNotice(title, body, link), ref); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit(ctx)
}
