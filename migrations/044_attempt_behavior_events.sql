-- Privacy-bounded interaction telemetry for understanding quiz-taking behavior.
-- Answers and correctness remain in question_responses; this table deliberately
-- stores no answer text, option value, free-form text, IP address, or user agent.

CREATE TABLE IF NOT EXISTS attempt_behavior_events (
  id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  client_event_id   UUID NOT NULL,
  attempt_id        UUID NOT NULL REFERENCES quiz_attempts(id) ON DELETE CASCADE,
  user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  question_id       UUID REFERENCES questions(id) ON DELETE SET NULL,
  event_type        TEXT NOT NULL CHECK (event_type IN (
    'question_viewed', 'answer_changed', 'timer_expired',
    'question_advanced', 'focus_lost', 'focus_gained',
    'exit_clicked', 'completion_requested'
  )),
  client_elapsed_ms INTEGER NOT NULL CHECK (client_elapsed_ms BETWEEN 0 AND 86400000),
  change_count      INTEGER CHECK (change_count BETWEEN 1 AND 1000),
  hidden_ms         INTEGER CHECK (hidden_ms BETWEEN 0 AND 86400000),
  received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at        TIMESTAMPTZ NOT NULL DEFAULT now() + interval '180 days',
  UNIQUE (attempt_id, client_event_id)
);

CREATE INDEX IF NOT EXISTS attempt_behavior_attempt_received_idx
  ON attempt_behavior_events(attempt_id, received_at);

CREATE INDEX IF NOT EXISTS attempt_behavior_quiz_patterns_idx
  ON attempt_behavior_events(event_type, received_at);

CREATE INDEX IF NOT EXISTS attempt_behavior_expiry_idx
  ON attempt_behavior_events(expires_at);

-- All access goes through the authenticated Go API, which binds user_id from
-- the verified token. PostgREST clients must not read this behavioral dataset.
ALTER TABLE attempt_behavior_events ENABLE ROW LEVEL SECURITY;
