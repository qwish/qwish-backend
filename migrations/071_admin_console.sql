-- Super-admin console redesign: the data the console needs to stop guessing.

-- Reports: keep the reviewer's note alongside the finding, and allow an
-- "author warned" finding. Previously a note had nowhere to go and anything
-- outside the enum was rejected by the CHECK (and silently dropped).
ALTER TABLE reports ADD COLUMN IF NOT EXISTS resolution_note TEXT;
ALTER TABLE reports DROP CONSTRAINT IF EXISTS reports_resolution_check;
ALTER TABLE reports ADD CONSTRAINT reports_resolution_check
  CHECK (resolution IN ('no_action','edit_required','remove_quiz','escalated','author_warned'));

-- Contact inbox triage.
ALTER TABLE contact_submissions
  ADD COLUMN IF NOT EXISTS assignee_id   UUID REFERENCES admin_accounts(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS internal_note TEXT,
  ADD COLUMN IF NOT EXISTS updated_at    TIMESTAMPTZ NOT NULL DEFAULT now();

-- Every change to an institution's point multiplier, with who and why.
CREATE TABLE IF NOT EXISTS institution_multiplier_history (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  old_value      NUMERIC(4,2),
  new_value      NUMERIC(4,2) NOT NULL,
  reason         TEXT NOT NULL,
  changed_by     UUID REFERENCES admin_accounts(id) ON DELETE SET NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_inst_multiplier_history
  ON institution_multiplier_history (institution_id, created_at DESC);

-- Admin console sessions, keyed by the token's session_id claim. Revoking a
-- row makes the auth middleware refuse that session on its next request.
CREATE TABLE IF NOT EXISTS admin_sessions (
  session_id TEXT PRIMARY KEY,
  admin_id   UUID NOT NULL REFERENCES admin_accounts(id) ON DELETE CASCADE,
  user_agent TEXT,
  ip         TEXT,
  method     TEXT,
  first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen  TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at TIMESTAMPTZ,
  revoked_by UUID REFERENCES admin_accounts(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_admin_sessions_admin ON admin_sessions (admin_id, last_seen DESC);

-- Small key/value store for platform policy that isn't a point rule.
CREATE TABLE IF NOT EXISTS platform_settings (
  key        TEXT PRIMARY KEY,
  value      JSONB NOT NULL,
  updated_by UUID REFERENCES admin_accounts(id) ON DELETE SET NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO platform_settings (key, value) VALUES
  ('require_admin_passkeys', 'false'),
  ('points_reserve', 'null'),
  ('points_reserve_warn_pct', '90')
ON CONFLICT (key) DO NOTHING;

-- Surveys can be archived (hidden from the active list, results kept).
ALTER TABLE anonymous_surveys DROP CONSTRAINT IF EXISTS anonymous_surveys_status_check;
ALTER TABLE anonymous_surveys ADD CONSTRAINT anonymous_surveys_status_check
  CHECK (status IN ('draft','published','closed','archived'));

-- Demo acquisition: a visitor who registers after playing a demo.
ALTER TABLE demo_events DROP CONSTRAINT IF EXISTS demo_events_event_type_check;
ALTER TABLE demo_events ADD CONSTRAINT demo_events_event_type_check
  CHECK (event_type IN ('start','complete','register'));
ALTER TABLE quizzes ADD COLUMN IF NOT EXISTS demo_live BOOLEAN NOT NULL DEFAULT true;
