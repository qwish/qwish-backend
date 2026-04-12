-- Add edit_feedback column and expand quiz status to include 'needs_edits'

ALTER TABLE quizzes ADD COLUMN IF NOT EXISTS edit_feedback TEXT;

-- Recreate the status CHECK constraint to include 'needs_edits'.
-- PostgreSQL requires dropping the old constraint first.
ALTER TABLE quizzes DROP CONSTRAINT IF EXISTS quizzes_status_check;
ALTER TABLE quizzes ADD CONSTRAINT quizzes_status_check
  CHECK (status IN ('draft','pending_approval','published','closed','rejected','needs_edits'));
