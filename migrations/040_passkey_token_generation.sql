-- Passkey session revocation.
--
-- A passkey session is authenticated by a self-minted HS256 JWT (see
-- internal/domain/auth/passkey.go:mintRefreshToken), not by a Supabase session.
-- Nothing about it is stored server-side, so before this migration there was no
-- way to end one: POST /auth/logout calls SupabaseLogout with a token Supabase
-- never issued, which is a no-op, and the refresh token stayed valid for its
-- full 30 days. Deleting every passkey was the only kill switch.
--
-- token_generation is a monotonic counter stamped into each minted token as the
-- `gen` claim. Bumping the column invalidates every token issued before the bump
-- — one UPDATE ends every session on every device, immediately.
--
-- Default 0 so existing rows and tokens minted before this migration (which
-- carry no `gen` claim) continue to validate; see the absent-claim handling in
-- verifyTokenGeneration.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS token_generation INTEGER NOT NULL DEFAULT 0;

ALTER TABLE admin_accounts
    ADD COLUMN IF NOT EXISTS token_generation INTEGER NOT NULL DEFAULT 0;

COMMENT ON COLUMN users.token_generation IS
    'Monotonic counter for passkey session revocation. Bump to invalidate every issued token for this user.';

COMMENT ON COLUMN admin_accounts.token_generation IS
    'Monotonic counter for passkey session revocation. Bump to invalidate every issued token for this admin.';
