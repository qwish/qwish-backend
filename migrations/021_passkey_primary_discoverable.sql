-- ── Passkey enhancements: primary credential + discoverable (usernameless) login ──

-- Let an admin mark one passkey as their primary/default. Enforced to at most
-- one per admin via a partial unique index.
ALTER TABLE webauthn_credentials
    ADD COLUMN IF NOT EXISTS is_primary BOOLEAN NOT NULL DEFAULT false;

CREATE UNIQUE INDEX IF NOT EXISTS idx_webauthn_primary_per_admin
    ON webauthn_credentials(admin_id) WHERE is_primary;

-- Discoverable-credential login (conditional-UI autofill) has no email up front,
-- so its ceremony is keyed by the challenge itself. Allow the new purpose.
ALTER TABLE webauthn_challenges
    DROP CONSTRAINT IF EXISTS webauthn_challenges_purpose_check;
ALTER TABLE webauthn_challenges
    ADD CONSTRAINT webauthn_challenges_purpose_check
    CHECK (purpose IN ('register', 'login', 'login_discoverable'));
