-- ── device_tokens ────────────────────────────────────────────────────────────
-- FCM (Firebase Cloud Messaging) registration tokens for end-user devices.
-- One row per (user, token). Tokens rotate over time and must be refreshed by
-- the client; stale tokens are auto-pruned on FCM 404/410 responses.
CREATE TABLE IF NOT EXISTS device_tokens (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token       TEXT        NOT NULL,
    platform    TEXT        NOT NULL DEFAULT 'unknown'
                                CHECK (platform IN ('ios', 'android', 'web', 'unknown')),
    app_version TEXT,
    locale      TEXT,
    last_seen   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, token)
);

CREATE INDEX IF NOT EXISTS idx_device_tokens_user  ON device_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_device_tokens_token ON device_tokens(token);
