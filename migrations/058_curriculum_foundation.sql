-- Institution-owned academic structure. Published versions are immutable;
-- future question mappings can reference concepts without changing history.
CREATE TABLE academic_years (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID NOT NULL REFERENCES institutions(id),
  name TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 120),
  starts_on DATE NOT NULL,
  ends_on DATE NOT NULL CHECK (ends_on >= starts_on),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (institution_id, name),
  UNIQUE (id, institution_id)
);

CREATE TABLE curricula (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID NOT NULL REFERENCES institutions(id),
  name TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 160),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (institution_id, name),
  UNIQUE (id, institution_id)
);

CREATE TABLE curriculum_versions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  curriculum_id UUID NOT NULL,
  institution_id UUID NOT NULL,
  label TEXT NOT NULL CHECK (length(btrim(label)) BETWEEN 1 AND 80),
  subject TEXT NOT NULL CHECK (length(btrim(subject)) BETWEEN 1 AND 120),
  grade TEXT NOT NULL CHECK (length(btrim(grade)) BETWEEN 1 AND 80),
  status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published')),
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  published_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (curriculum_id, institution_id) REFERENCES curricula(id, institution_id),
  CHECK ((status = 'published') = (published_at IS NOT NULL)),
  UNIQUE (curriculum_id, label),
  UNIQUE (id, curriculum_id, institution_id)
);
CREATE INDEX curriculum_versions_institution ON curriculum_versions(institution_id, created_at DESC, id);

CREATE TABLE curriculum_chapters (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  version_id UUID NOT NULL REFERENCES curriculum_versions(id),
  title TEXT NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 160),
  position INTEGER NOT NULL CHECK (position > 0),
  UNIQUE (version_id, position)
);

CREATE TABLE curriculum_concepts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  chapter_id UUID NOT NULL REFERENCES curriculum_chapters(id),
  code TEXT NOT NULL CHECK (length(btrim(code)) BETWEEN 1 AND 80),
  title TEXT NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 160),
  learning_outcome TEXT NOT NULL DEFAULT '' CHECK (length(learning_outcome) <= 1000),
  position INTEGER NOT NULL CHECK (position > 0),
  UNIQUE (chapter_id, position),
  UNIQUE (chapter_id, code)
);

-- Pair the group with its owner at the database boundary as well as in the API.
ALTER TABLE groups ADD CONSTRAINT groups_id_institution_unique UNIQUE (id, institution_id);
CREATE TABLE class_curricula (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID NOT NULL,
  group_id UUID NOT NULL,
  academic_year_id UUID NOT NULL,
  curriculum_id UUID NOT NULL,
  version_id UUID NOT NULL,
  assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at TIMESTAMPTZ,
  FOREIGN KEY (group_id, institution_id) REFERENCES groups(id, institution_id),
  FOREIGN KEY (academic_year_id, institution_id) REFERENCES academic_years(id, institution_id),
  FOREIGN KEY (version_id, curriculum_id, institution_id)
    REFERENCES curriculum_versions(id, curriculum_id, institution_id)
);
CREATE UNIQUE INDEX class_curricula_live ON class_curricula(group_id, academic_year_id, curriculum_id)
  WHERE ended_at IS NULL;
CREATE INDEX class_curricula_version ON class_curricula(version_id);
CREATE INDEX class_curricula_year ON class_curricula(academic_year_id);
CREATE INDEX class_curricula_institution ON class_curricula(institution_id);

-- Serialize child changes with publication so even concurrent writes cannot
-- mutate a version after publication. No SECURITY DEFINER / bypass privileges.
CREATE FUNCTION protect_curriculum_content() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  target_version UUID;
  version_status TEXT;
BEGIN
  IF TG_TABLE_NAME = 'curriculum_versions' THEN
    IF OLD.status = 'published' THEN
      RAISE EXCEPTION 'published curriculum versions are immutable' USING ERRCODE = '23514';
    END IF;
    IF TG_OP = 'UPDATE' AND (NEW.id, NEW.curriculum_id, NEW.institution_id)
      IS DISTINCT FROM (OLD.id, OLD.curriculum_id, OLD.institution_id) THEN
      RAISE EXCEPTION 'curriculum ownership cannot change' USING ERRCODE = '23514';
    END IF;
  ELSE
    IF TG_TABLE_NAME = 'curriculum_chapters' THEN
      IF TG_OP = 'UPDATE' AND (NEW.id, NEW.version_id) IS DISTINCT FROM (OLD.id, OLD.version_id) THEN
        RAISE EXCEPTION 'chapter ownership cannot change' USING ERRCODE = '23514';
      END IF;
      IF TG_OP = 'DELETE' THEN target_version := OLD.version_id; ELSE target_version := NEW.version_id; END IF;
    ELSE
      IF TG_OP = 'UPDATE' AND (NEW.id, NEW.chapter_id) IS DISTINCT FROM (OLD.id, OLD.chapter_id) THEN
        RAISE EXCEPTION 'concept ownership cannot change' USING ERRCODE = '23514';
      END IF;
      SELECT version_id INTO target_version FROM curriculum_chapters
        WHERE id = CASE WHEN TG_OP = 'DELETE' THEN OLD.chapter_id ELSE NEW.chapter_id END;
    END IF;
    SELECT status INTO version_status FROM curriculum_versions WHERE id = target_version FOR UPDATE;
    IF version_status = 'published' THEN
      RAISE EXCEPTION 'published curriculum content is immutable' USING ERRCODE = '23514';
    END IF;
  END IF;
  IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;
CREATE TRIGGER curriculum_version_guard BEFORE UPDATE OR DELETE ON curriculum_versions
  FOR EACH ROW EXECUTE FUNCTION protect_curriculum_content();
CREATE TRIGGER curriculum_chapter_guard BEFORE INSERT OR UPDATE OR DELETE ON curriculum_chapters
  FOR EACH ROW EXECUTE FUNCTION protect_curriculum_content();
CREATE TRIGGER curriculum_concept_guard BEFORE INSERT OR UPDATE OR DELETE ON curriculum_concepts
  FOR EACH ROW EXECUTE FUNCTION protect_curriculum_content();

ALTER TABLE academic_years ENABLE ROW LEVEL SECURITY;
ALTER TABLE curricula ENABLE ROW LEVEL SECURITY;
ALTER TABLE curriculum_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE curriculum_chapters ENABLE ROW LEVEL SECURITY;
ALTER TABLE curriculum_concepts ENABLE ROW LEVEL SECURITY;
ALTER TABLE class_curricula ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON academic_years, curricula, curriculum_versions, curriculum_chapters,
  curriculum_concepts, class_curricula FROM anon, authenticated;
