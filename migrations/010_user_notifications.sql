-- ── user_notifications ───────────────────────────────────────────────────────
-- In-app notifications surfaced to end users (badge unlocks, streak milestones,
-- rank movement, live quiz reminders, system announcements).
CREATE TABLE IF NOT EXISTS user_notifications (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        TEXT        NOT NULL,   -- 'badge' | 'rank' | 'streak' | 'live_quiz' | 'points' | 'profile_view' | 'system'
    title       TEXT        NOT NULL,
    body        TEXT        NOT NULL DEFAULT '',
    icon        TEXT,                   -- optional icon hint, e.g. 'emoji_events'
    color       TEXT,                   -- optional color hint, e.g. 'warning'
    reference   TEXT,                   -- free-form, e.g. 'badge:perfect_score'
    read_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_user_notifications_user        ON user_notifications(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_user_notifications_user_unread ON user_notifications(user_id) WHERE read_at IS NULL;
