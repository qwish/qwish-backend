-- Migration 041: Onboarding calibration.
--
-- A first-run user picks preferences and plays one quiz before an account
-- exists. onboarding_sessions is where that work lives until signup claims it.
-- The session id IS the credential: it is unguessable, single-use, and expires.

CREATE TABLE IF NOT EXISTS onboarding_sessions (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  language     TEXT NOT NULL DEFAULT 'en',
  topics       TEXT[] NOT NULL DEFAULT '{}',
  quiz_id      UUID REFERENCES quizzes(id),
  responses    JSONB,
  submitted_at TIMESTAMPTZ,
  claimed_by   UUID REFERENCES users(id),
  claimed_at   TIMESTAMPTZ,
  expires_at   TIMESTAMPTZ NOT NULL DEFAULT now() + interval '24 hours',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The purge job's only predicate.
CREATE INDEX IF NOT EXISTS idx_onboarding_sessions_expires
  ON onboarding_sessions(expires_at) WHERE claimed_by IS NULL;

-- Reached only through the service-role pool, like the other tables covered
-- in migration 033. Enabled with no permissive policy: anon and authenticated
-- roles get nothing.
ALTER TABLE onboarding_sessions ENABLE ROW LEVEL SECURITY;

ALTER TABLE users ADD COLUMN IF NOT EXISTS preferred_language TEXT NOT NULL DEFAULT 'en';
ALTER TABLE users ADD COLUMN IF NOT EXISTS interest_domains   TEXT[] NOT NULL DEFAULT '{}';
