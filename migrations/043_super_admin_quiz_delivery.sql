-- Super-admin authored quizzes can be scheduled and can deliver a random
-- subset of their question bank. The attempt snapshot fixes that subset for a
-- learner, so a client cannot answer questions it was not served.

ALTER TABLE quizzes
  ADD COLUMN IF NOT EXISTS starts_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS question_limit INT,
  ADD COLUMN IF NOT EXISTS shuffle_questions BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE quizzes
  ADD CONSTRAINT quizzes_question_limit_check
  CHECK (question_limit IS NULL OR question_limit > 0) NOT VALID;

CREATE TABLE IF NOT EXISTS quiz_attempt_questions (
  attempt_id UUID NOT NULL REFERENCES quiz_attempts(id) ON DELETE CASCADE,
  question_id UUID NOT NULL REFERENCES questions(id) ON DELETE RESTRICT,
  position INT NOT NULL,
  PRIMARY KEY (attempt_id, question_id),
  UNIQUE (attempt_id, position)
);

CREATE INDEX IF NOT EXISTS idx_quiz_attempt_questions_question
  ON quiz_attempt_questions(question_id);

CREATE INDEX IF NOT EXISTS idx_quizzes_availability
  ON quizzes(starts_at, ends_at)
  WHERE status = 'published' AND deleted_at IS NULL;
