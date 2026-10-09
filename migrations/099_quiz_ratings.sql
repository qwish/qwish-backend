-- Optional quiz feedback, separate from assessment scores and learner ratings.
CREATE TABLE quiz_ratings (
  quiz_id UUID NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  stars SMALLINT NOT NULL CHECK (stars BETWEEN 1 AND 5),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (quiz_id, user_id)
);
CREATE INDEX idx_quiz_ratings_user ON quiz_ratings(user_id);
ALTER TABLE quiz_ratings ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON TABLE quiz_ratings FROM anon, authenticated;

-- Retire delivery and preferences for topic requests. Keep historical request
-- records and migrations intact; the application no longer exposes them.
DELETE FROM user_notifications WHERE kind='topic_request';
UPDATE teacher_notification_preferences SET prefs=prefs-'topic_requests'
WHERE prefs ? 'topic_requests';
DELETE FROM action_item_owners WHERE item_type='topic_request';
DELETE FROM background_jobs
WHERE kind='email' AND state IN ('pending','failed')
  AND payload->>'reference' ~ '^teacher:[^:]+:topic_request:';

-- Retained legacy rows must not be available through direct client SQL access.
REVOKE ALL ON TABLE topic_requests FROM anon, authenticated;
DROP POLICY IF EXISTS topic_requests_select ON topic_requests;
