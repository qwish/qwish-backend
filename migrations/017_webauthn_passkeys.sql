-- ── WebAuthn / Passkey authentication for admin accounts ─────────────────────
-- Passkeys are offered as an alternative login for the super-admin console,
-- alongside the existing email-OTP flow. Credentials are bound to an
-- admin_accounts row; a successful assertion mints a Supabase-compatible JWT
-- (signed with SUPABASE_JWT_SECRET) that the auth middleware already accepts.

-- One row per registered authenticator (security key / platform passkey).
-- The full go-webauthn Credential is stored as JSONB so sign-count, flags,
-- transports and AAGUID round-trip exactly; credential_id is duplicated into
-- its own column for fast lookup and a uniqueness guarantee.
CREATE TABLE IF NOT EXISTS webauthn_credentials (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id      UUID        NOT NULL REFERENCES admin_accounts(id) ON DELETE CASCADE,
    credential_id BYTEA       NOT NULL UNIQUE,
    name          TEXT        NOT NULL DEFAULT 'Passkey',
    credential    JSONB       NOT NULL,
    sign_count    BIGINT      NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_webauthn_credentials_admin ON webauthn_credentials(admin_id);

-- Short-lived challenge / SessionData store bridging the begin → finish round
-- trip of a ceremony. Keyed by (subject, purpose): subject is the admin id for
-- registration and the email for login; purpose is 'register' or 'login'.
-- Upserted on each begin so only the latest challenge per subject is valid.
CREATE TABLE IF NOT EXISTS webauthn_challenges (
    subject      TEXT        NOT NULL,
    purpose      TEXT        NOT NULL CHECK (purpose IN ('register', 'login')),
    session_data JSONB       NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (subject, purpose)
);

CREATE INDEX IF NOT EXISTS idx_webauthn_challenges_expiry ON webauthn_challenges(expires_at);
