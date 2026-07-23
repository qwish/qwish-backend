-- ── WebAuthn / Passkey authentication for end users (teachers, etc.) ─────────
-- Mirrors 017/021 (which bind passkeys to admin_accounts) but for the `users`
-- table, so teachers can register and sign in with a passkey from the teacher
-- panel. Kept as a separate table to leave the production admin passkey path
-- completely untouched. The webauthn_challenges table (017) is reused as-is —
-- user ceremonies namespace their subject with a "u:" prefix to avoid any
-- collision with admin subjects.

CREATE TABLE IF NOT EXISTS webauthn_user_credentials (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id BYTEA       NOT NULL UNIQUE,
    name          TEXT        NOT NULL DEFAULT 'Passkey',
    credential    JSONB       NOT NULL,
    sign_count    BIGINT      NOT NULL DEFAULT 0,
    is_primary    BOOLEAN     NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_webauthn_user_credentials_user
    ON webauthn_user_credentials(user_id);

-- At most one primary passkey per user.
CREATE UNIQUE INDEX IF NOT EXISTS idx_webauthn_user_primary
    ON webauthn_user_credentials(user_id) WHERE is_primary;
