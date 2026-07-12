-- ============================================================================
-- Migration 023: Demo quiz authoring + play analytics
-- ============================================================================
-- Super-admin can now author demo quizzes directly (no teacher). quizzes.created_by
-- stays NOT NULL, so we seed one fixed "system" user that owns all demo quizzes.
-- Demo plays are anonymous/pre-login, so we capture lightweight events here to
-- power the super-admin demo dashboard.
-- ============================================================================

-- Fixed system user that owns admin-authored demo quizzes.
INSERT INTO users (id, supabase_uid, full_name, display_name, email, role, status)
VALUES (
  '00000000-0000-0000-0000-000000000001',
  '00000000-0000-0000-0000-000000000001',
  'Qwish System', 'Qwish', 'system@qwish.internal', 'super_admin', 'active'
)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS demo_events (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  quiz_id        UUID NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
  event_type     TEXT NOT NULL CHECK (event_type IN ('start','complete')),
  score_pct      NUMERIC,        -- complete only
  total_correct  INT,            -- complete only
  total_questions INT,           -- complete only
  per_question   JSONB,          -- complete only: [{"question_id":"..","correct":true}]
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_demo_events_quiz ON demo_events(quiz_id, created_at);
