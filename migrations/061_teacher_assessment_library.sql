-- Teacher-owned assessment-library preferences. The composite key makes
-- favorite writes idempotent and the quiz FK removes stale favorites.
CREATE TABLE IF NOT EXISTS teacher_quiz_favorites (
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  quiz_id UUID NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, quiz_id)
);

CREATE INDEX IF NOT EXISTS teacher_quiz_favorites_user_created
  ON teacher_quiz_favorites (user_id, created_at DESC);

ALTER TABLE teacher_quiz_favorites ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS teacher_quiz_favorites_select ON teacher_quiz_favorites;
CREATE POLICY teacher_quiz_favorites_select ON teacher_quiz_favorites
  FOR SELECT TO authenticated
  USING (is_admin() OR user_id = auth_user_id());
