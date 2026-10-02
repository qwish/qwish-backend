-- Institution leadership roles (plans/institution-hierarchy-and-access-control.md),
-- limited to Director, Principal, Vice Principal, Dean and Head of Department.
-- users.role is untouched: a teacher who becomes HOD stays a teacher. Access
-- comes from active rows in staff_role_assignments, scoped to the institution
-- (department_id NULL) or to one department per row.

CREATE TABLE IF NOT EXISTS departments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 120),
  code TEXT CHECK (code IS NULL OR char_length(code) <= 20),
  archived_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (id, institution_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS departments_institution_name
  ON departments(institution_id, lower(btrim(name))) WHERE archived_at IS NULL;
ALTER TABLE departments ENABLE ROW LEVEL SECURITY;

-- A class belongs to at most one department, always in its own institution.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS department_id UUID;
ALTER TABLE groups DROP CONSTRAINT IF EXISTS groups_department_fk;
ALTER TABLE groups ADD CONSTRAINT groups_department_fk
  FOREIGN KEY (department_id, institution_id) REFERENCES departments(id, institution_id);
CREATE INDEX IF NOT EXISTS groups_department ON groups(department_id) WHERE department_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS staff_role_assignments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('director','principal','vice_principal','dean','hod')),
  department_id UUID,
  -- Optional displayed designation, e.g. "Head of Institution". Never used for access.
  title TEXT CHECK (title IS NULL OR char_length(title) <= 120),
  starts_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ends_at TIMESTAMPTZ,
  reason TEXT NOT NULL DEFAULT '' CHECK (char_length(reason) <= 500),
  granted_by UUID REFERENCES users(id) ON DELETE SET NULL,
  revoked_at TIMESTAMPTZ,
  revoked_by UUID REFERENCES users(id) ON DELETE SET NULL,
  revoke_reason TEXT CHECK (revoke_reason IS NULL OR char_length(revoke_reason) <= 500),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (department_id, institution_id) REFERENCES departments(id, institution_id),
  CHECK (ends_at IS NULL OR ends_at > starts_at),
  -- Director and Principal are institution-wide; Dean and HOD are per department;
  -- a Vice Principal may be either.
  CHECK (CASE role WHEN 'director' THEN department_id IS NULL
                   WHEN 'principal' THEN department_id IS NULL
                   WHEN 'dean' THEN department_id IS NOT NULL
                   WHEN 'hod' THEN department_id IS NOT NULL
                   ELSE true END)
);
CREATE UNIQUE INDEX IF NOT EXISTS staff_role_assignments_active
  ON staff_role_assignments(user_id, role, COALESCE(department_id, '00000000-0000-0000-0000-000000000000'::uuid))
  WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS staff_role_assignments_institution ON staff_role_assignments(institution_id, role) WHERE revoked_at IS NULL;
ALTER TABLE staff_role_assignments ENABLE ROW LEVEL SECURITY;
