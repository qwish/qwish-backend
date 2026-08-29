-- Recruiter workspace: tenant membership, roles, pipeline state and audit trail.
-- Candidate discovery remains opt-in through users.recruiter_visible.

CREATE TABLE IF NOT EXISTS recruiter_organisations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 2 AND 120),
  slug TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS recruiter_memberships (
  organisation_id UUID NOT NULL REFERENCES recruiter_organisations(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('owner','admin','recruiter','hiring_manager','viewer')),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','invited','suspended')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (organisation_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_recruiter_memberships_user
  ON recruiter_memberships (user_id) WHERE status='active';

CREATE TABLE IF NOT EXISTS recruiter_roles (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organisation_id UUID NOT NULL REFERENCES recruiter_organisations(id) ON DELETE CASCADE,
  title TEXT NOT NULL CHECK (length(trim(title)) BETWEEN 2 AND 120),
  status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('draft','open','paused','closed')),
  headcount INT NOT NULL DEFAULT 1 CHECK (headcount BETWEEN 1 AND 10000),
  created_by UUID NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_recruiter_roles_org_status
  ON recruiter_roles (organisation_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS recruiter_candidate_states (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organisation_id UUID NOT NULL REFERENCES recruiter_organisations(id) ON DELETE CASCADE,
  candidate_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role_id UUID REFERENCES recruiter_roles(id) ON DELETE CASCADE,
  stage TEXT NOT NULL DEFAULT 'shortlisted' CHECK (stage IN ('sourced','shortlisted','assessment','interview','offer','hired','rejected')),
  contacted_at TIMESTAMPTZ,
  updated_by UUID NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_recruiter_candidate_general_state
  ON recruiter_candidate_states (organisation_id, candidate_id) WHERE role_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_recruiter_candidate_role_state
  ON recruiter_candidate_states (organisation_id, candidate_id, role_id) WHERE role_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_recruiter_candidate_states_org_stage
  ON recruiter_candidate_states (organisation_id, stage, updated_at DESC);

CREATE TABLE IF NOT EXISTS recruiter_audit_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organisation_id UUID NOT NULL REFERENCES recruiter_organisations(id) ON DELETE CASCADE,
  actor_id UUID NOT NULL REFERENCES users(id),
  action TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_id UUID,
  metadata JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_recruiter_audit_org_created
  ON recruiter_audit_events (organisation_id, created_at DESC);

-- The API owns these tables. Direct PostgREST access is deliberately denied;
-- handlers apply membership and consent checks before every query.
ALTER TABLE recruiter_organisations ENABLE ROW LEVEL SECURITY;
ALTER TABLE recruiter_memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE recruiter_roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE recruiter_candidate_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE recruiter_audit_events ENABLE ROW LEVEL SECURITY;
