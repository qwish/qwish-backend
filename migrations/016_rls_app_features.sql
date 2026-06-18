-- =============================================
-- ROW LEVEL SECURITY for tables added after 004 (010/011/012) + schema_migrations.
-- Same model as 004: all writes go through the Go backend (service_role bypasses
-- RLS). Policies here restrict SELECT for the `authenticated` Supabase role.
-- Idempotent: ENABLE RLS repeats safely; policies use DROP IF EXISTS + CREATE.
-- =============================================

-- SECURITY DEFINER membership check. Runs as owner, so it bypasses RLS and
-- avoids infinite recursion when study_group_members' own policy needs to know
-- which groups the caller belongs to.
CREATE OR REPLACE FUNCTION is_study_group_member(gid UUID) RETURNS BOOLEAN
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
  AS $$
    SELECT EXISTS (
      SELECT 1 FROM study_group_members
      WHERE group_id = gid AND user_id = auth_user_id()
    )
  $$;

ALTER TABLE device_tokens            ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_preferences  ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_follows              ENABLE ROW LEVEL SECURITY;
ALTER TABLE study_groups              ENABLE ROW LEVEL SECURITY;
ALTER TABLE study_group_members       ENABLE ROW LEVEL SECURITY;
ALTER TABLE practice_sessions         ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_notifications        ENABLE ROW LEVEL SECURITY;

-- device_tokens — owner only.
DROP POLICY IF EXISTS device_tokens_select ON device_tokens;
CREATE POLICY device_tokens_select ON device_tokens
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR user_id = auth_user_id()
  );

-- notification_preferences — owner only.
DROP POLICY IF EXISTS notification_preferences_select ON notification_preferences;
CREATE POLICY notification_preferences_select ON notification_preferences
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR user_id = auth_user_id()
  );

-- user_follows — either side of the edge.
DROP POLICY IF EXISTS user_follows_select ON user_follows;
CREATE POLICY user_follows_select ON user_follows
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR follower_id = auth_user_id()
    OR followee_id = auth_user_id()
  );

-- study_groups — owner or member.
DROP POLICY IF EXISTS study_groups_select ON study_groups;
CREATE POLICY study_groups_select ON study_groups
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR owner_id = auth_user_id()
    OR is_study_group_member(id)
  );

-- study_group_members — own membership, or any member of a group you belong to.
DROP POLICY IF EXISTS study_group_members_select ON study_group_members;
CREATE POLICY study_group_members_select ON study_group_members
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR user_id = auth_user_id()
    OR is_study_group_member(group_id)
  );

-- practice_sessions — owner only.
DROP POLICY IF EXISTS practice_sessions_select ON practice_sessions;
CREATE POLICY practice_sessions_select ON practice_sessions
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR user_id = auth_user_id()
  );

-- user_notifications — owner only.
DROP POLICY IF EXISTS user_notifications_select ON user_notifications;
CREATE POLICY user_notifications_select ON user_notifications
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR user_id = auth_user_id()
  );

-- schema_migrations — migrator-internal (managed by the Go RunMigrations). No
-- user data; only service_role touches it. Enable RLS with no policy so the
-- `authenticated` role sees nothing and the Supabase linter is satisfied.
-- Guarded: the table may not exist at first-boot ordering on a fresh DB.
DO $$
BEGIN
  IF to_regclass('public.schema_migrations') IS NOT NULL THEN
    EXECUTE 'ALTER TABLE schema_migrations ENABLE ROW LEVEL SECURITY';
  END IF;
END $$;
