CREATE TABLE IF NOT EXISTS teacher_feature_onboarding (
  teacher_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  feature_key TEXT NOT NULL CHECK (char_length(feature_key) BETWEEN 1 AND 80),
  status TEXT NOT NULL CHECK (status IN ('started', 'dismissed', 'completed')),
  last_step INT NOT NULL DEFAULT 0 CHECK (last_step BETWEEN 0 AND 50),
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  dismissed_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (teacher_id, institution_id, feature_key)
);

CREATE INDEX IF NOT EXISTS teacher_feature_onboarding_institution
  ON teacher_feature_onboarding (institution_id, updated_at DESC);

ALTER TABLE teacher_feature_onboarding ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON teacher_feature_onboarding FROM anon, authenticated;
