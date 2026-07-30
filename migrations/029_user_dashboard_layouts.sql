-- ============================================================================
-- Migration 029: Per-user dashboard layouts + role-scoped analytics indexes
-- ============================================================================
-- Institution admins and teachers live in `users`, not `admin_accounts`, so
-- they cannot share 028's table. Same shape, different owner.
--
-- `layout` is opaque to the server: widget shapes change with every frontend
-- release, and a server-side schema for them would need a migration each time.
-- The API validates only that it is a JSON object under a size cap.
-- ============================================================================

CREATE TABLE IF NOT EXISTS user_dashboard_layouts (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 60),
  layout     JSONB NOT NULL,
  is_default BOOLEAN NOT NULL DEFAULT false,
  sort       INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, name)
);

-- The only read pattern: this user's layouts, in display order.
CREATE INDEX IF NOT EXISTS idx_user_layouts_user
    ON user_dashboard_layouts (user_id, sort);

-- At most one default per user, enforced in the database rather than trusting
-- every write path to clear the previous one.
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_layouts_one_default
    ON user_dashboard_layouts (user_id)
    WHERE is_default;

-- ── Scope-join indexes ──────────────────────────────────────────────────────
-- The teacher scopes filter on columns nothing has filtered on before.
-- group_students and group_teachers are keyed (group_id, user_id), so their
-- primary keys do not serve a user_id-leading lookup, and the class-membership
-- subquery runs once per analytics source.

CREATE INDEX IF NOT EXISTS idx_group_students_user
    ON group_students (user_id);

CREATE INDEX IF NOT EXISTS idx_group_teachers_user
    ON group_teachers (user_id, group_id);

CREATE INDEX IF NOT EXISTS idx_quizzes_created_by
    ON quizzes (created_by)
    WHERE deleted_at IS NULL;
