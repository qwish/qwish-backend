-- Adaptive review scheduling and contextual-bandit recommendation state.
CREATE TABLE IF NOT EXISTS learner_topic_mastery (
  user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  topic          TEXT NOT NULL,
  mastery        DOUBLE PRECISION NOT NULL DEFAULT 0.5 CHECK (mastery BETWEEN 0 AND 1),
  ease_factor    DOUBLE PRECISION NOT NULL DEFAULT 2.5 CHECK (ease_factor BETWEEN 1.3 AND 3.0),
  interval_days  INT NOT NULL DEFAULT 1 CHECK (interval_days >= 1),
  review_count   INT NOT NULL DEFAULT 0,
  next_review_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, topic)
);
CREATE INDEX IF NOT EXISTS learner_topic_due
  ON learner_topic_mastery (user_id, next_review_at);
ALTER TABLE learner_topic_mastery ENABLE ROW LEVEL SECURITY;

CREATE TABLE IF NOT EXISTS recommendation_bandit_stats (
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  quiz_id     UUID NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
  impressions BIGINT NOT NULL DEFAULT 0,
  rewards     DOUBLE PRECISION NOT NULL DEFAULT 0,
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, quiz_id)
);
ALTER TABLE recommendation_bandit_stats ENABLE ROW LEVEL SECURITY;
