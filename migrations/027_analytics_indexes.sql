-- ============================================================================
-- Migration 027: Indexes for the admin analytics endpoints
-- ============================================================================
-- date_trunc() over an unindexed TIMESTAMPTZ on a high-volume table is a
-- sequential scan. Each index below backs a specific metric source in
-- internal/domain/admin/metrics_catalog.go.
-- All use IF NOT EXISTS so this file is safe to re-run.
-- ============================================================================

-- Attempt starts and the abandonment sweeper both filter on started_at.
-- 019 only indexes completed_at, and only for status='completed', so nothing
-- today serves a started_at range scan.
CREATE INDEX IF NOT EXISTS idx_attempts_started_at
    ON quiz_attempts (started_at);

-- The sweeper's UPDATE: WHERE status='in_progress' AND started_at < cutoff.
-- Partial keeps it small — in-progress is a tiny slice of the table.
CREATE INDEX IF NOT EXISTS idx_attempts_inprogress_started
    ON quiz_attempts (started_at)
    WHERE status = 'in_progress';

-- Abandoned attempts bucket on started_at (completed_at is NULL for them).
CREATE INDEX IF NOT EXISTS idx_attempts_abandoned_started
    ON quiz_attempts (started_at)
    WHERE status = 'abandoned';

-- Economy metrics scan a global created_at window. 019's index is
-- (user_id, created_at DESC) — wrong leading column for that.
CREATE INDEX IF NOT EXISTS idx_ledger_created_at
    ON points_ledger (created_at);

-- signups
CREATE INDEX IF NOT EXISTS idx_users_created_at
    ON users (created_at);

-- moderation_actions. 019 has (target_type, timestamp DESC) only.
-- "timestamp" is quoted because it is a reserved word in Postgres.
CREATE INDEX IF NOT EXISTS idx_audit_timestamp
    ON audit_log ("timestamp");

-- questions_answered
CREATE INDEX IF NOT EXISTS idx_qresponses_submitted_at
    ON question_responses (submitted_at);

-- reports_opened / reports_resolved / median_time_to_resolve_report
CREATE INDEX IF NOT EXISTS idx_reports_created_at
    ON reports (created_at);
CREATE INDEX IF NOT EXISTS idx_reports_resolved_at
    ON reports (resolved_at)
    WHERE resolved_at IS NOT NULL;

-- Content metrics
CREATE INDEX IF NOT EXISTS idx_quizzes_created_at
    ON quizzes (created_at);
CREATE INDEX IF NOT EXISTS idx_institutions_created_at
    ON institutions (created_at);
