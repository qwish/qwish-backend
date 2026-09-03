-- Keep achievement evaluation bounded to one user's ordered history.
CREATE INDEX IF NOT EXISTS idx_qresponses_attempt_sequence
  ON question_responses (attempt_id, submitted_at, id) INCLUDE (is_correct);

-- Covers score streaks, daily counts, score predicates, and domain joins.
CREATE INDEX IF NOT EXISTS idx_attempts_achievement_history
  ON quiz_attempts (user_id, completed_at, id)
  INCLUDE (quiz_id, score_pct, total_correct, total_questions)
  WHERE status = 'completed';
