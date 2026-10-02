-- Teacher forms and identified polls (plans/teacher-forms-events-and-polls.md, Phase 1).
-- A poll is a one-question form, so both share one response table and one
-- uniqueness rule: one effective response per student per activity.

CREATE TABLE IF NOT EXISTS activities (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('form','poll')),
  created_by UUID NOT NULL REFERENCES users(id),
  title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 4000),
  status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','closed','archived')),
  -- Draft question schema; frozen into activity_versions on publish.
  draft_questions JSONB NOT NULL DEFAULT '[]'::jsonb,
  institution_wide BOOLEAN NOT NULL DEFAULT false,
  opens_at TIMESTAMPTZ,
  closes_at TIMESTAMPTZ,
  allow_edit BOOLEAN NOT NULL DEFAULT false,
  result_visibility TEXT NOT NULL DEFAULT 'after_close' CHECK (result_visibility IN ('after_vote','after_close','organisers')),
  current_version_id UUID,
  reach_estimate INT,
  reach_estimated_at TIMESTAMPTZ,
  reminded_at TIMESTAMPTZ,
  revision INT NOT NULL DEFAULT 1,
  published_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (closes_at IS NULL OR opens_at IS NULL OR closes_at > opens_at)
);
CREATE INDEX IF NOT EXISTS activities_institution_status ON activities(institution_id, status, closes_at);
CREATE INDEX IF NOT EXISTS activities_creator ON activities(created_by, created_at DESC);

CREATE TABLE IF NOT EXISTS activity_audience_groups (
  activity_id UUID NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
  group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  PRIMARY KEY (activity_id, group_id)
);
CREATE INDEX IF NOT EXISTS activity_audience_groups_group ON activity_audience_groups(group_id);

-- Immutable published schema. Answers bind to a version, never to the draft.
CREATE TABLE IF NOT EXISTS activity_versions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  activity_id UUID NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
  version INT NOT NULL,
  questions JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (activity_id, version)
);
ALTER TABLE activities DROP CONSTRAINT IF EXISTS activities_current_version_fk;
ALTER TABLE activities ADD CONSTRAINT activities_current_version_fk
  FOREIGN KEY (current_version_id) REFERENCES activity_versions(id);

CREATE TABLE IF NOT EXISTS activity_responses (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  activity_id UUID NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
  version_id UUID NOT NULL REFERENCES activity_versions(id),
  student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  answers JSONB NOT NULL DEFAULT '{}'::jsonb,
  status TEXT NOT NULL CHECK (status IN ('draft','submitted','withdrawn')),
  -- Identity/batch context captured at submission, shown to organisers.
  respondent JSONB NOT NULL DEFAULT '{}'::jsonb,
  submitted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (activity_id, student_id)
);
CREATE INDEX IF NOT EXISTS activity_responses_student ON activity_responses(student_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS activity_response_history (
  id BIGSERIAL PRIMARY KEY,
  response_id UUID NOT NULL REFERENCES activity_responses(id) ON DELETE CASCADE,
  answers JSONB NOT NULL,
  status TEXT NOT NULL,
  recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS activity_audit_events (
  id BIGSERIAL PRIMARY KEY,
  activity_id UUID NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
  actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
  action TEXT NOT NULL,
  detail JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS activity_audit_events_activity ON activity_audit_events(activity_id, created_at);

-- Publication writes the in-app rows in its own transaction; the reference is
-- the dedup key so a retried publish or reminder never doubles a row.
CREATE UNIQUE INDEX IF NOT EXISTS user_notifications_activity_reference
  ON user_notifications(user_id, reference)
  WHERE kind = 'activity' AND reference IS NOT NULL;

-- Backend-only tables: no direct client reads.
ALTER TABLE activities ENABLE ROW LEVEL SECURITY;
ALTER TABLE activity_audience_groups ENABLE ROW LEVEL SECURITY;
ALTER TABLE activity_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE activity_responses ENABLE ROW LEVEL SECURITY;
ALTER TABLE activity_response_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE activity_audit_events ENABLE ROW LEVEL SECURITY;
