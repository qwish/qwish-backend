-- =============================================
-- ROW LEVEL SECURITY POLICIES
-- All writes go through the Go backend (service_role bypasses RLS).
-- Policies here restrict SELECT for the `authenticated` Supabase role.
-- =============================================

-- =============================================
-- HELPER FUNCTIONS (security definer = run as owner, not caller)
-- =============================================

CREATE OR REPLACE FUNCTION auth_user_id() RETURNS UUID
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
  AS $$
    SELECT id FROM users
    WHERE supabase_uid = auth.uid() AND deleted_at IS NULL
    LIMIT 1
  $$;

CREATE OR REPLACE FUNCTION auth_user_role() RETURNS TEXT
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
  AS $$
    SELECT role FROM users
    WHERE supabase_uid = auth.uid() AND deleted_at IS NULL
    LIMIT 1
  $$;

CREATE OR REPLACE FUNCTION auth_institution_id() RETURNS UUID
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
  AS $$
    SELECT institution_id FROM users
    WHERE supabase_uid = auth.uid() AND deleted_at IS NULL
    LIMIT 1
  $$;

CREATE OR REPLACE FUNCTION auth_admin_role() RETURNS TEXT
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
  AS $$
    SELECT role FROM admin_accounts
    WHERE supabase_uid = auth.uid() AND deleted_at IS NULL AND status = 'active'
    LIMIT 1
  $$;

CREATE OR REPLACE FUNCTION is_admin() RETURNS BOOLEAN
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
  AS $$
    SELECT EXISTS (
      SELECT 1 FROM admin_accounts
      WHERE supabase_uid = auth.uid() AND deleted_at IS NULL AND status = 'active'
    )
  $$;

CREATE OR REPLACE FUNCTION is_super_admin() RETURNS BOOLEAN
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
  AS $$
    SELECT EXISTS (
      SELECT 1 FROM admin_accounts
      WHERE supabase_uid = auth.uid() AND role = 'super_admin'
        AND deleted_at IS NULL AND status = 'active'
    )
  $$;

-- =============================================
-- ENABLE RLS
-- =============================================

ALTER TABLE admin_accounts        ENABLE ROW LEVEL SECURITY;
ALTER TABLE institutions          ENABLE ROW LEVEL SECURITY;
ALTER TABLE users                 ENABLE ROW LEVEL SECURITY;
ALTER TABLE groups                ENABLE ROW LEVEL SECURITY;
ALTER TABLE group_students        ENABLE ROW LEVEL SECURITY;
ALTER TABLE group_teachers        ENABLE ROW LEVEL SECURITY;
ALTER TABLE quizzes               ENABLE ROW LEVEL SECURITY;
ALTER TABLE questions             ENABLE ROW LEVEL SECURITY;
ALTER TABLE quiz_attempts         ENABLE ROW LEVEL SECURITY;
ALTER TABLE question_responses    ENABLE ROW LEVEL SECURITY;
ALTER TABLE points_ledger         ENABLE ROW LEVEL SECURITY;
ALTER TABLE streaks               ENABLE ROW LEVEL SECURITY;
ALTER TABLE badges                ENABLE ROW LEVEL SECURITY;
ALTER TABLE saved_quizzes         ENABLE ROW LEVEL SECURITY;
ALTER TABLE parent_student_links  ENABLE ROW LEVEL SECURITY;
ALTER TABLE topic_requests        ENABLE ROW LEVEL SECURITY;
ALTER TABLE reports               ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_log             ENABLE ROW LEVEL SECURITY;
ALTER TABLE point_economy_config  ENABLE ROW LEVEL SECURITY;
ALTER TABLE announcements         ENABLE ROW LEVEL SECURITY;
ALTER TABLE promotional_content   ENABLE ROW LEVEL SECURITY;
ALTER TABLE impersonation_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE leaderboard_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE profile_views         ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_education        ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_skills           ENABLE ROW LEVEL SECURITY;

-- =============================================
-- ADMIN ACCOUNTS
-- Super admin sees all. Admins see their own row.
-- =============================================

CREATE POLICY admin_accounts_select ON admin_accounts
  FOR SELECT TO authenticated
  USING (
    is_super_admin()
    OR supabase_uid = auth.uid()
  );

-- =============================================
-- INSTITUTIONS
-- Admins see all. Institution members see their own institution.
-- =============================================

CREATE POLICY institutions_select ON institutions
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR id = auth_institution_id()
  );

-- =============================================
-- USERS
-- Users see themselves. Institution admins/teachers see same institution.
-- Parents see their linked students. Admins see all.
-- =============================================

CREATE POLICY users_select ON users
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR supabase_uid = auth.uid()
    OR (
      institution_id IS NOT NULL
      AND institution_id = auth_institution_id()
      AND auth_user_role() IN ('institution_admin', 'teacher')
    )
    OR id IN (
      SELECT student_id FROM parent_student_links
      WHERE parent_id = auth_user_id() AND status = 'active'
    )
  );

-- =============================================
-- GROUPS
-- Institution members see groups in their institution.
-- =============================================

CREATE POLICY groups_select ON groups
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR institution_id = auth_institution_id()
  );

-- =============================================
-- GROUP_STUDENTS
-- Institution members or the student themselves.
-- =============================================

CREATE POLICY group_students_select ON group_students
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR user_id = auth_user_id()
    OR group_id IN (
      SELECT id FROM groups WHERE institution_id = auth_institution_id()
    )
  );

-- =============================================
-- GROUP_TEACHERS
-- =============================================

CREATE POLICY group_teachers_select ON group_teachers
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR user_id = auth_user_id()
    OR group_id IN (
      SELECT id FROM groups WHERE institution_id = auth_institution_id()
    )
  );

-- =============================================
-- QUIZZES
-- Published quizzes: all authenticated.
-- Drafts/pending: creator and institution_admin of same institution.
-- Admins: all.
-- =============================================

CREATE POLICY quizzes_select ON quizzes
  FOR SELECT TO authenticated
  USING (
    deleted_at IS NULL
    AND (
      is_admin()
      OR status = 'published'
      OR created_by = auth_user_id()
      OR (
        institution_id = auth_institution_id()
        AND auth_user_role() IN ('institution_admin', 'teacher')
      )
    )
  );

-- =============================================
-- QUESTIONS
-- Visible if the parent quiz is visible.
-- =============================================

CREATE POLICY questions_select ON questions
  FOR SELECT TO authenticated
  USING (
    quiz_id IN (
      SELECT id FROM quizzes
      WHERE deleted_at IS NULL
        AND (
          is_admin()
          OR status = 'published'
          OR created_by = auth_user_id()
          OR (
            institution_id = auth_institution_id()
            AND auth_user_role() IN ('institution_admin', 'teacher')
          )
        )
    )
  );

-- =============================================
-- QUIZ ATTEMPTS
-- Users see own. Institution admins/teachers see institution students'.
-- Parents see their children's. Admins see all.
-- =============================================

CREATE POLICY quiz_attempts_select ON quiz_attempts
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR user_id = auth_user_id()
    OR (
      auth_user_role() IN ('institution_admin', 'teacher')
      AND user_id IN (
        SELECT id FROM users WHERE institution_id = auth_institution_id()
      )
    )
    OR user_id IN (
      SELECT student_id FROM parent_student_links
      WHERE parent_id = auth_user_id() AND status = 'active'
    )
  );

-- =============================================
-- QUESTION RESPONSES
-- Visible if the parent attempt is visible.
-- =============================================

CREATE POLICY question_responses_select ON question_responses
  FOR SELECT TO authenticated
  USING (
    attempt_id IN (
      SELECT id FROM quiz_attempts
      WHERE
        is_admin()
        OR user_id = auth_user_id()
        OR (
          auth_user_role() IN ('institution_admin', 'teacher')
          AND user_id IN (
            SELECT id FROM users WHERE institution_id = auth_institution_id()
          )
        )
    )
  );

-- =============================================
-- POINTS LEDGER
-- Users see own. Admins see all.
-- =============================================

CREATE POLICY points_ledger_select ON points_ledger
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR user_id = auth_user_id()
  );

-- =============================================
-- STREAKS
-- Users see own. Institution admins/teachers see institution students'.
-- Parents see children's. Admins see all.
-- =============================================

CREATE POLICY streaks_select ON streaks
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR user_id = auth_user_id()
    OR (
      auth_user_role() IN ('institution_admin', 'teacher')
      AND user_id IN (
        SELECT id FROM users WHERE institution_id = auth_institution_id()
      )
    )
    OR user_id IN (
      SELECT student_id FROM parent_student_links
      WHERE parent_id = auth_user_id() AND status = 'active'
    )
  );

-- =============================================
-- BADGES
-- Public — all authenticated can see (needed for public profiles).
-- =============================================

CREATE POLICY badges_select ON badges
  FOR SELECT TO authenticated
  USING (true);

-- =============================================
-- SAVED QUIZZES
-- Users see their own only.
-- =============================================

CREATE POLICY saved_quizzes_select ON saved_quizzes
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR user_id = auth_user_id()
  );

-- =============================================
-- PARENT STUDENT LINKS
-- Parents and students see their own links.
-- =============================================

CREATE POLICY parent_student_links_select ON parent_student_links
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR parent_id = auth_user_id()
    OR student_id = auth_user_id()
  );

-- =============================================
-- TOPIC REQUESTS
-- Students see own. Institution admins/teachers see institution's.
-- Admins see all.
-- =============================================

CREATE POLICY topic_requests_select ON topic_requests
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR student_id = auth_user_id()
    OR (
      institution_id = auth_institution_id()
      AND auth_user_role() IN ('institution_admin', 'teacher')
    )
  );

-- =============================================
-- REPORTS
-- Reporters see their own. Admins/moderators see all.
-- =============================================

CREATE POLICY reports_select ON reports
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR reporter_id = auth_user_id()
  );

-- =============================================
-- AUDIT LOG
-- Super admins see all. Institution admins see their institution actions.
-- =============================================

CREATE POLICY audit_log_select ON audit_log
  FOR SELECT TO authenticated
  USING (
    is_super_admin()
    OR (
      auth_user_role() = 'institution_admin'
      AND target_id = auth_institution_id()
    )
  );

-- =============================================
-- POINT ECONOMY CONFIG
-- Super admins only.
-- =============================================

CREATE POLICY point_economy_config_select ON point_economy_config
  FOR SELECT TO authenticated
  USING (is_super_admin());

-- =============================================
-- ANNOUNCEMENTS
-- All authenticated see sent announcements. Admins see all.
-- =============================================

CREATE POLICY announcements_select ON announcements
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR status = 'sent'
  );

-- =============================================
-- PROMOTIONAL CONTENT
-- All authenticated see active content. Admins see all.
-- =============================================

CREATE POLICY promotional_content_select ON promotional_content
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR status = 'active'
  );

-- =============================================
-- IMPERSONATION SESSIONS
-- Admins only.
-- =============================================

CREATE POLICY impersonation_sessions_select ON impersonation_sessions
  FOR SELECT TO authenticated
  USING (is_admin());

-- =============================================
-- LEADERBOARD SNAPSHOTS
-- All authenticated users.
-- =============================================

CREATE POLICY leaderboard_snapshots_select ON leaderboard_snapshots
  FOR SELECT TO authenticated
  USING (true);

-- =============================================
-- PROFILE VIEWS
-- Users see views on their own profile (viewed_id = me).
-- Admins see all.
-- =============================================

CREATE POLICY profile_views_select ON profile_views
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR viewed_id = auth_user_id()
  );

-- =============================================
-- USER EDUCATION
-- Public — all authenticated (needed for public profiles).
-- =============================================

CREATE POLICY user_education_select ON user_education
  FOR SELECT TO authenticated
  USING (true);

-- =============================================
-- USER SKILLS
-- Public — all authenticated (needed for public profiles).
-- =============================================

CREATE POLICY user_skills_select ON user_skills
  FOR SELECT TO authenticated
  USING (true);

-- =============================================
-- BRANDS
-- Active brands: all authenticated (students/teachers can see sponsors).
-- Pending/suspended: admins only.
-- =============================================

ALTER TABLE brands ENABLE ROW LEVEL SECURITY;

CREATE POLICY brands_select ON brands
  FOR SELECT TO authenticated
  USING (
    is_admin()
    OR status = 'active'
  );

-- =============================================
-- SPONSORSHIP REQUESTS
-- Admins see all. Regular users have no direct access
-- (requests are initiated and managed solely by admins/brands).
-- =============================================

ALTER TABLE sponsorship_requests ENABLE ROW LEVEL SECURITY;

CREATE POLICY sponsorship_requests_select ON sponsorship_requests
  FOR SELECT TO authenticated
  USING (is_admin());
