-- Anti-cheat: make scoring inputs server-authoritative.
-- Combo level, per-question elapsed time, and clue reveals were all
-- self-reported by the client and fed straight into scoring.

ALTER TABLE quiz_attempts
  ADD COLUMN IF NOT EXISTS combo_level INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS last_answer_at TIMESTAMPTZ;

-- Existing in-flight attempts get their clock started from the attempt start.
UPDATE quiz_attempts SET last_answer_at = started_at WHERE last_answer_at IS NULL;

-- One row per clue the server actually handed out. Replaces the client's
-- self-reported question_responses.clues_used as the scoring input.
CREATE TABLE IF NOT EXISTS clue_reveals (
  attempt_id  UUID NOT NULL REFERENCES quiz_attempts(id) ON DELETE CASCADE,
  question_id UUID NOT NULL REFERENCES questions(id),
  clue_index  INT  NOT NULL,
  revealed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (attempt_id, question_id, clue_index)
);
