-- Curriculum structure rules and richer concept metadata (Institute dashboard
-- "advanced setup"). The shapes are validated in Go; JSONB keeps the schema
-- small while the product settles which fields matter.
-- ponytail: JSONB, not ~25 typed columns. Promote a field to a column when
-- something needs to index or join on it.

ALTER TABLE curriculum_versions
  ADD COLUMN IF NOT EXISTS settings JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN IF NOT EXISTS copied_from_version_id UUID REFERENCES curriculum_versions(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

ALTER TABLE curriculum_chapters
  ADD COLUMN IF NOT EXISTS details JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE curriculum_concepts
  ADD COLUMN IF NOT EXISTS details JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Every saved revision of a draft, with the note that explains it and the
-- content as it stood, so an editor can show history and compare revisions.
CREATE TABLE IF NOT EXISTS curriculum_version_revisions (
  version_id UUID NOT NULL REFERENCES curriculum_versions(id) ON DELETE CASCADE,
  revision INTEGER NOT NULL CHECK (revision > 0),
  action TEXT NOT NULL CHECK (action IN ('created', 'saved', 'published')),
  note TEXT NOT NULL DEFAULT '' CHECK (length(note) <= 500),
  actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
  snapshot JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (version_id, revision)
);

ALTER TABLE curriculum_version_revisions ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON curriculum_version_revisions FROM anon, authenticated;
