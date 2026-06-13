-- Migration 013: Admin invite lifecycle
--
-- Previously admin_accounts had only ('active','suspended','deleted') and rows
-- were created 'active' immediately, so an invited admin who never accepted
-- still showed as Active. This adds:
--   - 'pending'        : invite sent, awaiting first sign-in (acceptance)
--   - 'invite_failed'  : the invite email could not be delivered
--   - accepted_at      : set when the invite is accepted (first successful auth)
--
-- A pending/invite_failed admin is promoted to 'active' on first successful
-- authentication (see middleware.Authenticate).

ALTER TABLE admin_accounts DROP CONSTRAINT IF EXISTS admin_accounts_status_check;
ALTER TABLE admin_accounts
  ADD CONSTRAINT admin_accounts_status_check
  CHECK (status IN ('pending','invite_failed','active','suspended','deleted'));

ALTER TABLE admin_accounts ADD COLUMN IF NOT EXISTS accepted_at TIMESTAMPTZ;

-- Existing rows predate the invite lifecycle; treat them as already accepted so
-- they keep working and don't get demoted to pending.
UPDATE admin_accounts SET accepted_at = created_at WHERE accepted_at IS NULL AND status = 'active';
