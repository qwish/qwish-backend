-- Student portfolio (plans/student-portfolio-and-achievements.md, Phase 1 slice).
-- Additive only: existing rows keep their id, kind and content. They become
-- drafts — never submitted, never reviewed — which is exactly what they are.

ALTER TABLE user_profile_entries
  ADD COLUMN IF NOT EXISTS subtype TEXT
    CHECK (subtype IN ('project','internship','hackathon','certification','achievement')),
  ADD COLUMN IF NOT EXISTS details JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN IF NOT EXISTS skills TEXT[] NOT NULL DEFAULT '{}',
  ADD COLUMN IF NOT EXISTS links TEXT[] NOT NULL DEFAULT '{}',
  ADD COLUMN IF NOT EXISTS ongoing BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS academic_year TEXT,
  ADD COLUMN IF NOT EXISTS semester SMALLINT CHECK (semester BETWEEN 1 AND 12),
  ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'draft'
    CHECK (status IN ('draft','submitted','reviewed','changes_requested')),
  ADD COLUMN IF NOT EXISTS pinned BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS current_revision INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Immutable snapshot taken at each submission. Teachers review a revision,
-- never the live row, so later draft edits stay private and a review badge
-- cannot drift onto changed content.
CREATE TABLE IF NOT EXISTS user_profile_entry_revisions (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  entry_id       UUID NOT NULL REFERENCES user_profile_entries(id) ON DELETE CASCADE,
  revision       INT NOT NULL,
  content        JSONB NOT NULL,
  schema_version SMALLINT NOT NULL DEFAULT 1,
  institution_id UUID REFERENCES institutions(id) ON DELETE SET NULL,
  submitted_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (entry_id, revision)
);

CREATE TABLE IF NOT EXISTS user_profile_entry_reviews (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  revision_id    UUID NOT NULL REFERENCES user_profile_entry_revisions(id) ON DELETE CASCADE,
  reviewer_id    UUID REFERENCES users(id) ON DELETE SET NULL,
  institution_id UUID REFERENCES institutions(id) ON DELETE SET NULL,
  decision       TEXT NOT NULL CHECK (decision IN ('reviewed','changes_requested')),
  comment        TEXT NOT NULL DEFAULT '' CHECK (char_length(comment) <= 2000),
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS user_profile_entry_reviews_revision
  ON user_profile_entry_reviews(revision_id, created_at DESC);

-- All writes go through the Go backend; nothing here is readable directly.
ALTER TABLE user_profile_entry_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_profile_entry_reviews ENABLE ROW LEVEL SECURITY;
