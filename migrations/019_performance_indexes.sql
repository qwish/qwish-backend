-- ============================================================================
-- Migration 019: Performance indexes for high-volume tables
-- ============================================================================
-- Rationale for every index is documented inline.
-- All indexes use IF NOT EXISTS so this file is safe to re-run.
-- ============================================================================

-- ────────────────────────────────────────────────────────────────────────────
-- quiz_attempts  (highest volume — every user action writes here)
-- ────────────────────────────────────────────────────────────────────────────

-- Composite: repeat-attempt check and taker count subquery both filter on
-- (quiz_id, status). Adding user_id makes the knowledge-check guard in
-- attempt/service.go Complete() index-only for the inner COUNT(*).
CREATE INDEX IF NOT EXISTS idx_attempts_quiz_status_user
    ON quiz_attempts (quiz_id, status, user_id);

-- Partial: "in-progress" attempts are queried on every SubmitAnswer and
-- Complete call. Filtering to only in-progress rows keeps this tiny.
CREATE INDEX IF NOT EXISTS idx_attempts_inprogress
    ON quiz_attempts (user_id, quiz_id)
    WHERE status = 'in_progress';

-- Partial: completed attempts ordered by date — used in profile history,
-- weekly digest, and admin "active users last 7 days" query.
CREATE INDEX IF NOT EXISTS idx_attempts_completed_at
    ON quiz_attempts (user_id, completed_at DESC)
    WHERE status = 'completed';

-- Admin dashboard: COUNT(DISTINCT user_id) WHERE completed_at >= recent date
CREATE INDEX IF NOT EXISTS idx_attempts_completed_date
    ON quiz_attempts (completed_at)
    WHERE status = 'completed';


-- ────────────────────────────────────────────────────────────────────────────
-- question_responses  (one row per question per attempt — very high volume)
-- ────────────────────────────────────────────────────────────────────────────

-- Primary access pattern: fetch all responses for an attempt (SubmitAnswer
-- reads previous response; breakdown reads them all at Complete).
CREATE INDEX IF NOT EXISTS idx_qresponses_attempt
    ON question_responses (attempt_id);

-- Teacher results: JOIN quiz_attempts qa ON qa.id = qr.attempt_id and then
-- GROUP BY qr.question_id — both sides need to be fast.
CREATE INDEX IF NOT EXISTS idx_qresponses_question
    ON question_responses (question_id);


-- ────────────────────────────────────────────────────────────────────────────
-- quizzes  (filtered list queries on every browse screen load)
-- ────────────────────────────────────────────────────────────────────────────

-- ListForStudent: WHERE institution_id=X AND status='published' AND deleted_at IS NULL
-- ORDER BY published_at DESC. Covering index on frequently combined predicates.
CREATE INDEX IF NOT EXISTS idx_quizzes_inst_status_published
    ON quizzes (institution_id, status, published_at DESC)
    WHERE deleted_at IS NULL;

-- Public quiz browse (no institution filter):
-- WHERE visibility='public' AND status='published' AND deleted_at IS NULL
CREATE INDEX IF NOT EXISTS idx_quizzes_public_published
    ON quizzes (status, published_at DESC)
    WHERE visibility = 'public' AND deleted_at IS NULL;

-- Scheduler: close expired play_and_win quizzes
-- WHERE type='play_and_win' AND status='published' AND ends_at <= now()
CREATE INDEX IF NOT EXISTS idx_quizzes_ends_at
    ON quizzes (type, ends_at)
    WHERE status = 'published' AND ends_at IS NOT NULL;

-- Full-text search: ILIKE '%term%' on title/description.
-- GIN index on tsvector enables pg_trgm-style search without a full scan.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_quizzes_title_trgm
    ON quizzes USING gin (title gin_trgm_ops)
    WHERE deleted_at IS NULL;


-- ────────────────────────────────────────────────────────────────────────────
-- questions  (fetched in bulk for every quiz start + per-question grading)
-- ────────────────────────────────────────────────────────────────────────────

-- GetQuestionsForStudent / AddQuestion / UpdateQuestion all filter quiz_id,
-- often ordered by position.
CREATE INDEX IF NOT EXISTS idx_questions_quiz_position
    ON questions (quiz_id, position);


-- ────────────────────────────────────────────────────────────────────────────
-- points_ledger  (growing indefinitely — one row per completed attempt)
-- ────────────────────────────────────────────────────────────────────────────

-- Weekly digest subquery: WHERE user_id=X AND amount>0 AND created_at >= $1
CREATE INDEX IF NOT EXISTS idx_ledger_user_created
    ON points_ledger (user_id, created_at DESC);

-- Expiry job: WHERE expires_at < now() AND amount > 0
CREATE INDEX IF NOT EXISTS idx_ledger_expires_positive
    ON points_ledger (expires_at)
    WHERE amount > 0;


-- ────────────────────────────────────────────────────────────────────────────
-- users  (leaderboard and rank queries use total_points frequently)
-- ────────────────────────────────────────────────────────────────────────────

-- Leaderboard snapshot + rank query:
-- WHERE status='active' AND role IN ('student','teacher') ORDER BY total_points DESC
-- Partial index filters to the relevant subset.
CREATE INDEX IF NOT EXISTS idx_users_points_leaderboard
    ON users (total_points DESC)
    WHERE status = 'active' AND role IN ('student', 'teacher');

-- Institution-scoped leaderboard and rank comparison
CREATE INDEX IF NOT EXISTS idx_users_inst_points
    ON users (institution_id, total_points DESC)
    WHERE status = 'active';

-- Rank comparison subquery in streak service:
-- WHERE institution_id=X AND total_points > $N AND status='active'
-- Covered by idx_users_inst_points above (same predicate, same leading cols).

-- Email lookup during auth (already UNIQUE but an explicit index speeds up
-- ILIKE-free exact lookups via the B-tree):
-- Already covered by the UNIQUE constraint on email; no duplicate needed.


-- ────────────────────────────────────────────────────────────────────────────
-- user_notifications  (polled on every app open for unread count)
-- ────────────────────────────────────────────────────────────────────────────

-- idx_user_notifications_user already exists from migration 010.
-- Add compound index to cover ORDER BY created_at DESC on the list query,
-- avoiding a separate sort step.
CREATE INDEX IF NOT EXISTS idx_user_notifications_user_kind
    ON user_notifications (user_id, kind, created_at DESC);


-- ────────────────────────────────────────────────────────────────────────────
-- saved_quizzes  (quiz list query: EXISTS (SELECT 1 FROM saved_quizzes ...))
-- ────────────────────────────────────────────────────────────────────────────

-- The PRIMARY KEY already covers (user_id, quiz_id).
-- quiz_id-first index accelerates "who saved this quiz?" lookups.
CREATE INDEX IF NOT EXISTS idx_saved_quizzes_quiz
    ON saved_quizzes (quiz_id, user_id);


-- ────────────────────────────────────────────────────────────────────────────
-- badges  (achievements screen: all badges for a user)
-- ────────────────────────────────────────────────────────────────────────────

-- Composite covers both the user list and the per-type uniqueness check.
CREATE INDEX IF NOT EXISTS idx_badges_user_type
    ON badges (user_id, badge_type);


-- ────────────────────────────────────────────────────────────────────────────
-- leaderboard_snapshots  (weekly digest reads latest snapshot per scope)
-- ────────────────────────────────────────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_lb_snapshots_scope_week
    ON leaderboard_snapshots (scope, institution_id, week_start DESC);


-- ────────────────────────────────────────────────────────────────────────────
-- audit_log  (admin dashboard pagination — large and append-only)
-- ────────────────────────────────────────────────────────────────────────────

-- Compound index for the most common admin filter: by target type + time.
CREATE INDEX IF NOT EXISTS idx_audit_target_type_ts
    ON audit_log (target_type, timestamp DESC);


-- ────────────────────────────────────────────────────────────────────────────
-- contact_submissions  (admin list filtered by status + created_at)
-- ────────────────────────────────────────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_contact_status_created
    ON contact_submissions (status, created_at DESC);


-- ────────────────────────────────────────────────────────────────────────────
-- reports  (admin list filtered by status + quiz_id)
-- ────────────────────────────────────────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_reports_quiz_status
    ON reports (quiz_id, status);
