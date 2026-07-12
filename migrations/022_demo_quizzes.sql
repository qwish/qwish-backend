-- ============================================================================
-- Migration 022: Demo quizzes (public, pre-login onboarding demo)
-- ============================================================================
-- Flags a small hand-curated set of quizzes as "demo" so they can be served on
-- public, unauthenticated endpoints during onboarding. No user, no attempt
-- rows — the demo is graded statelessly. Flag chosen quizzes by hand with:
--   UPDATE quizzes SET is_demo = true WHERE id = '...';
-- ============================================================================

ALTER TABLE quizzes ADD COLUMN IF NOT EXISTS is_demo boolean NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_quizzes_demo ON quizzes(is_demo) WHERE is_demo;
