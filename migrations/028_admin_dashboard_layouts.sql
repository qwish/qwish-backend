-- ============================================================================
-- Migration 028: Per-admin dashboard layouts
-- ============================================================================
-- Each admin gets a private set of named dashboard layouts. Layouts are
-- personal working state with no audit value, so they cascade away with the
-- admin account.
--
-- `layout` is opaque to the server: widget shapes change with every frontend
-- release, and a server-side schema for them would need a migration each time.
-- The API validates only that it is a JSON object under a size cap.
-- ============================================================================

CREATE TABLE IF NOT EXISTS admin_dashboard_layouts (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  admin_id   UUID NOT NULL REFERENCES admin_accounts(id) ON DELETE CASCADE,
  name       TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 60),
  layout     JSONB NOT NULL,
  is_default BOOLEAN NOT NULL DEFAULT false,
  sort       INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (admin_id, name)
);

-- The only read pattern: this admin's layouts, in display order.
CREATE INDEX IF NOT EXISTS idx_admin_layouts_admin
    ON admin_dashboard_layouts (admin_id, sort);

-- At most one default per admin, enforced in the database rather than trusting
-- every write path to clear the previous one. A non-transactional
-- implementation now fails loudly instead of silently leaving two defaults.
CREATE UNIQUE INDEX IF NOT EXISTS idx_admin_layouts_one_default
    ON admin_dashboard_layouts (admin_id)
    WHERE is_default;
