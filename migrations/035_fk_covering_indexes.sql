-- =============================================
-- Supabase linter INFO 0001 unindexed_foreign_keys.
-- Covering index for every FK that lacked one. Mostly audit columns
-- (created_by / approved_by / reviewed_by) — the index earns its keep on
-- parent-row delete/update, which otherwise seq-scans the child table.
-- Idempotent.
-- =============================================

CREATE INDEX IF NOT EXISTS idx_admin_accounts_created_by        ON admin_accounts(created_by);

CREATE INDEX IF NOT EXISTS idx_announcements_approved_by         ON announcements(approved_by);
CREATE INDEX IF NOT EXISTS idx_announcements_created_by          ON announcements(created_by);
CREATE INDEX IF NOT EXISTS idx_announcements_institution         ON announcements(institution_id);

CREATE INDEX IF NOT EXISTS idx_brands_approved_by                ON brands(approved_by);
CREATE INDEX IF NOT EXISTS idx_brands_created_by                 ON brands(created_by);

CREATE INDEX IF NOT EXISTS idx_clue_reveals_question             ON clue_reveals(question_id);

CREATE INDEX IF NOT EXISTS idx_contact_resolved_by               ON contact_submissions(resolved_by);

CREATE INDEX IF NOT EXISTS idx_impersonation_admin               ON impersonation_sessions(admin_id);
CREATE INDEX IF NOT EXISTS idx_impersonation_user                ON impersonation_sessions(user_id);

CREATE INDEX IF NOT EXISTS idx_institutions_verified_by          ON institutions(verified_by);

CREATE INDEX IF NOT EXISTS idx_lb_snapshots_institution          ON leaderboard_snapshots(institution_id);

CREATE INDEX IF NOT EXISTS idx_point_economy_updated_by          ON point_economy_config(updated_by);

CREATE INDEX IF NOT EXISTS idx_practice_sessions_quiz            ON practice_sessions(quiz_id);

CREATE INDEX IF NOT EXISTS idx_profile_views_viewer              ON profile_views(viewer_id);

CREATE INDEX IF NOT EXISTS idx_promo_approved_by                 ON promotional_content(approved_by);
CREATE INDEX IF NOT EXISTS idx_promo_created_by                  ON promotional_content(created_by);
CREATE INDEX IF NOT EXISTS idx_promo_institution                 ON promotional_content(institution_id);

CREATE INDEX IF NOT EXISTS idx_quizzes_approved_by               ON quizzes(approved_by);
CREATE INDEX IF NOT EXISTS idx_quizzes_group                     ON quizzes(group_id);
CREATE INDEX IF NOT EXISTS idx_quizzes_subdomain                 ON quizzes(subdomain);

CREATE INDEX IF NOT EXISTS idx_reports_question                  ON reports(question_id);
CREATE INDEX IF NOT EXISTS idx_reports_reporter                  ON reports(reporter_id);
CREATE INDEX IF NOT EXISTS idx_reports_reviewed_by               ON reports(reviewed_by);

CREATE INDEX IF NOT EXISTS idx_sponsorship_quiz                  ON sponsorship_requests(quiz_id);
CREATE INDEX IF NOT EXISTS idx_sponsorship_reviewed_by           ON sponsorship_requests(reviewed_by);

CREATE INDEX IF NOT EXISTS idx_student_edit_requested_by         ON student_edit_requests(requested_by);
CREATE INDEX IF NOT EXISTS idx_student_edit_reviewed_by          ON student_edit_requests(reviewed_by);

CREATE INDEX IF NOT EXISTS idx_teacher_invites_invited_by        ON teacher_invites(invited_by);

CREATE INDEX IF NOT EXISTS idx_topic_requests_assigned_to        ON topic_requests(assigned_to);
CREATE INDEX IF NOT EXISTS idx_topic_requests_student            ON topic_requests(student_id);
