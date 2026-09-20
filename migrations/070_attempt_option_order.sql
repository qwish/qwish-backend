-- Each attempt owns the order in which its answer options were delivered.
-- This makes randomisation server-side while keeping a resumed attempt stable.
ALTER TABLE quiz_attempt_questions
  ADD COLUMN IF NOT EXISTS option_order JSONB;
