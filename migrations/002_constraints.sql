-- Additional constraints applied after initial schema (idempotent).

-- Ensure each user can only submit one response per question per attempt.
DO $$
BEGIN
  ALTER TABLE question_responses
    ADD CONSTRAINT uq_response_attempt_question UNIQUE (attempt_id, question_id);
EXCEPTION
  WHEN duplicate_table THEN NULL;  -- constraint exists
  WHEN duplicate_object THEN NULL;
END $$;

-- Auto-update updated_at via shared trigger function.
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_institutions_updated_at ON institutions;
CREATE TRIGGER trg_institutions_updated_at
  BEFORE UPDATE ON institutions
  FOR EACH ROW EXECUTE FUNCTION update_updated_at();

DROP TRIGGER IF EXISTS trg_users_updated_at ON users;
CREATE TRIGGER trg_users_updated_at
  BEFORE UPDATE ON users
  FOR EACH ROW EXECUTE FUNCTION update_updated_at();

DROP TRIGGER IF EXISTS trg_quizzes_updated_at ON quizzes;
CREATE TRIGGER trg_quizzes_updated_at
  BEFORE UPDATE ON quizzes
  FOR EACH ROW EXECUTE FUNCTION update_updated_at();
