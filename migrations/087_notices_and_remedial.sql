-- Teacher notices and remedial-group sources (spec 2026-10-03, plan 4).

CREATE TABLE IF NOT EXISTS notices (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id   UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  created_by       UUID NOT NULL REFERENCES users(id),
  title            TEXT NOT NULL CHECK (char_length(btrim(title)) BETWEEN 1 AND 120),
  body             TEXT NOT NULL CHECK (char_length(btrim(body)) BETWEEN 1 AND 2000),
  category         TEXT NOT NULL CHECK (category IN ('event','test','general')),
  institution_wide BOOLEAN NOT NULL DEFAULT false,
  recipient_count  INT NOT NULL DEFAULT 0,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS notices_institution_created ON notices(institution_id, created_at DESC);
CREATE INDEX IF NOT EXISTS notices_creator ON notices(created_by, created_at DESC);
ALTER TABLE notices ENABLE ROW LEVEL SECURITY;

CREATE TABLE IF NOT EXISTS notice_audience (
  notice_id     UUID NOT NULL REFERENCES notices(id) ON DELETE CASCADE,
  group_id      UUID REFERENCES groups(id) ON DELETE CASCADE,
  department_id UUID REFERENCES departments(id) ON DELETE CASCADE,
  CHECK ((group_id IS NULL) <> (department_id IS NULL))
);
CREATE INDEX IF NOT EXISTS notice_audience_notice ON notice_audience(notice_id);
ALTER TABLE notice_audience ENABLE ROW LEVEL SECURITY;

-- Where a remedial group came from: the class and concept the insight showed.
ALTER TABLE groups
  ADD COLUMN IF NOT EXISTS source_group_id   UUID REFERENCES groups(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS source_concept_id UUID REFERENCES curriculum_concepts(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS created_by        UUID REFERENCES users(id) ON DELETE SET NULL;
