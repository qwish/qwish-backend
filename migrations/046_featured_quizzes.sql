-- A small, explicitly ordered platform-curated fallback for learners whose
-- personalised recommendation pool is empty.
CREATE TABLE IF NOT EXISTS featured_quizzes (
  quiz_id UUID PRIMARY KEY REFERENCES quizzes(id) ON DELETE CASCADE,
  position SMALLINT NOT NULL CHECK (position >= 0 AND position < 12),
  featured_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS featured_quizzes_position_key
  ON featured_quizzes (position);
