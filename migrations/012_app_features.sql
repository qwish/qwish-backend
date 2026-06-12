-- ── App feature pack ─────────────────────────────────────────────────────────
-- Adds: dark-mode preference, privacy (private-by-default + recruiter visibility),
-- notification preferences, batchmate follows, study groups (private leagues),
-- and offline practice sync.

-- ── Dark mode + privacy (on users) ───────────────────────────────────────────
ALTER TABLE users ADD COLUMN IF NOT EXISTS theme TEXT NOT NULL DEFAULT 'auto'
    CHECK (theme IN ('auto', 'light', 'dark'));
-- Private by default: profiles are hidden until the user opts in to recruiter
-- visibility.
ALTER TABLE users ADD COLUMN IF NOT EXISTS profile_private    BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE users ADD COLUMN IF NOT EXISTS recruiter_visible  BOOLEAN NOT NULL DEFAULT false;
-- Tracks the last global rank we pushed an alert for, so we only notify on change.
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_notified_rank INT;

-- ── Notification preferences ─────────────────────────────────────────────────
-- One row per user. Missing row ⇒ all categories enabled (defaults below).
CREATE TABLE IF NOT EXISTS notification_preferences (
    user_id               UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    push_rank_changes     BOOLEAN     NOT NULL DEFAULT true,
    push_weekly_digest    BOOLEAN     NOT NULL DEFAULT true,
    push_streak_nudge     BOOLEAN     NOT NULL DEFAULT true,
    push_study_group      BOOLEAN     NOT NULL DEFAULT true,
    email_weekly_insights BOOLEAN     NOT NULL DEFAULT true,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── Batchmate follows ────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS user_follows (
    follower_id UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    followee_id UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (follower_id, followee_id),
    CHECK (follower_id <> followee_id)
);
CREATE INDEX IF NOT EXISTS idx_user_follows_followee ON user_follows(followee_id);

-- ── Study groups (private leagues) ───────────────────────────────────────────
CREATE TABLE IF NOT EXISTS study_groups (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL,
    description TEXT,
    owner_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    invite_code TEXT        UNIQUE NOT NULL,
    archived_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_study_groups_owner ON study_groups(owner_id);

CREATE TABLE IF NOT EXISTS study_group_members (
    group_id  UUID        NOT NULL REFERENCES study_groups(id) ON DELETE CASCADE,
    user_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role      TEXT        NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'member')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_study_group_members_user ON study_group_members(user_id);

-- ── Offline practice sync ────────────────────────────────────────────────────
-- Practice sessions completed offline and synced when connectivity returns.
-- id is client-generated (UUID) so re-sync is idempotent. Practice is
-- non-competitive: it awards no points and never touches the leaderboard.
CREATE TABLE IF NOT EXISTS practice_sessions (
    id              UUID         PRIMARY KEY,
    user_id         UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    quiz_id         UUID         REFERENCES quizzes(id) ON DELETE SET NULL,
    total_questions INT          NOT NULL DEFAULT 0,
    correct_count   INT          NOT NULL DEFAULT 0,
    score_pct       NUMERIC(5,2) NOT NULL DEFAULT 0,
    answers         JSONB,
    completed_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    synced_at       TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_practice_sessions_user ON practice_sessions(user_id, completed_at DESC);
