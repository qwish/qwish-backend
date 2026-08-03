-- =============================================
-- ROW LEVEL SECURITY for the tables added after 016 that the Supabase linter
-- flagged (rls_disabled_in_public). Same model as 004/016: every read and write
-- goes through the Go backend, which connects as the table owner / service_role
-- and therefore bypasses RLS.
--
-- These tables have no direct Supabase-client consumer, so they get RLS enabled
-- with no policy at all: `authenticated` and `anon` see nothing, the backend is
-- unaffected. Add an owner-scoped SELECT policy (see 016 for the shape) only if
-- a frontend ever starts querying one of them through PostgREST.
-- Idempotent: ENABLE RLS repeats safely.
-- =============================================

ALTER TABLE demo_events               ENABLE ROW LEVEL SECURITY;
ALTER TABLE domains                   ENABLE ROW LEVEL SECURITY;
ALTER TABLE subdomains                ENABLE ROW LEVEL SECURITY;
ALTER TABLE webauthn_credentials      ENABLE ROW LEVEL SECURITY;
ALTER TABLE webauthn_challenges       ENABLE ROW LEVEL SECURITY;
ALTER TABLE webauthn_user_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE clue_reveals              ENABLE ROW LEVEL SECURITY;
ALTER TABLE admin_dashboard_layouts   ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_dashboard_layouts    ENABLE ROW LEVEL SECURITY;
