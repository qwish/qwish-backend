-- Candidate generation and learner-history lookups used by recommendations.
CREATE INDEX IF NOT EXISTS idx_attempts_user_completed_recent
  ON quiz_attempts (user_id, completed_at DESC, quiz_id)
  WHERE status = 'completed';

CREATE INDEX IF NOT EXISTS idx_quizzes_recommendation_candidates
  ON quizzes (status, visibility, published_at DESC, domain, subdomain)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_questions_quiz_difficulty
  ON questions (quiz_id, difficulty);
