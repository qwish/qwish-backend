-- College structure, first release (plans/institute-college-structure-gaps.md).
-- Programmes, admission cohorts, terms under existing academic years, and
-- course offerings taught through existing groups. Every link is optional so
-- school and general-learning classes keep working unchanged.

CREATE TABLE IF NOT EXISTS programmes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  department_id UUID NOT NULL,
  code TEXT NOT NULL CHECK (char_length(btrim(code)) BETWEEN 1 AND 20),
  name TEXT NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 160),
  -- Award or level as the college names it: "B.Tech", "M.Sc", "UG".
  award TEXT CHECK (award IS NULL OR char_length(award) <= 60),
  duration_terms SMALLINT CHECK (duration_terms IS NULL OR duration_terms BETWEEN 1 AND 20),
  archived_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (department_id, institution_id) REFERENCES departments(id, institution_id),
  UNIQUE (id, institution_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS programmes_institution_code
  ON programmes(institution_id, lower(btrim(code))) WHERE archived_at IS NULL;
CREATE INDEX IF NOT EXISTS programmes_department ON programmes(department_id);
ALTER TABLE programmes ENABLE ROW LEVEL SECURITY;

-- A cohort keeps its admission identity while it moves through terms.
CREATE TABLE IF NOT EXISTS cohorts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  programme_id UUID NOT NULL,
  admission_year SMALLINT NOT NULL CHECK (admission_year BETWEEN 1900 AND 2200),
  completion_year SMALLINT CHECK (completion_year IS NULL OR completion_year >= admission_year),
  archived_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (programme_id, institution_id) REFERENCES programmes(id, institution_id),
  UNIQUE (id, institution_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS cohorts_programme_year
  ON cohorts(programme_id, admission_year) WHERE archived_at IS NULL;
ALTER TABLE cohorts ENABLE ROW LEVEL SECURITY;

-- Terms sit under the existing academic years; no separate calendar layer yet.
CREATE TABLE IF NOT EXISTS academic_terms (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  academic_year_id UUID NOT NULL,
  name TEXT NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 80),
  sequence SMALLINT NOT NULL CHECK (sequence BETWEEN 1 AND 12),
  starts_on DATE NOT NULL,
  ends_on DATE NOT NULL CHECK (ends_on >= starts_on),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (academic_year_id, institution_id) REFERENCES academic_years(id, institution_id),
  UNIQUE (academic_year_id, sequence),
  UNIQUE (id, institution_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS academic_terms_year_name ON academic_terms(academic_year_id, lower(btrim(name)));
ALTER TABLE academic_terms ENABLE ROW LEVEL SECURITY;

-- A group may be a division of a cohort and/or run in a term. grade/section
-- remain the stage ("Semester III") and division ("A") labels.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS cohort_id UUID;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS term_id UUID;
ALTER TABLE groups DROP CONSTRAINT IF EXISTS groups_cohort_fk;
ALTER TABLE groups ADD CONSTRAINT groups_cohort_fk FOREIGN KEY (cohort_id, institution_id) REFERENCES cohorts(id, institution_id);
ALTER TABLE groups DROP CONSTRAINT IF EXISTS groups_term_fk;
ALTER TABLE groups ADD CONSTRAINT groups_term_fk FOREIGN KEY (term_id, institution_id) REFERENCES academic_terms(id, institution_id);
CREATE INDEX IF NOT EXISTS groups_cohort ON groups(cohort_id) WHERE cohort_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS groups_term ON groups(term_id) WHERE term_id IS NOT NULL;

-- A course taught in a real term. No course catalog yet: code/title live here.
CREATE TABLE IF NOT EXISTS course_offerings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  term_id UUID NOT NULL,
  department_id UUID,
  code TEXT NOT NULL CHECK (char_length(btrim(code)) BETWEEN 1 AND 30),
  title TEXT NOT NULL CHECK (char_length(btrim(title)) BETWEEN 1 AND 160),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (term_id, institution_id) REFERENCES academic_terms(id, institution_id),
  FOREIGN KEY (department_id, institution_id) REFERENCES departments(id, institution_id),
  UNIQUE (id, institution_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS course_offerings_term_code ON course_offerings(term_id, lower(btrim(code)));
ALTER TABLE course_offerings ENABLE ROW LEVEL SECURITY;

-- One offering teaches through theory, lab, tutorial... groups; one group may
-- serve several offerings.
CREATE TABLE IF NOT EXISTS offering_groups (
  offering_id UUID NOT NULL,
  group_id UUID NOT NULL,
  institution_id UUID NOT NULL,
  component TEXT NOT NULL DEFAULT 'theory' CHECK (component IN ('theory','lab','tutorial','other')),
  PRIMARY KEY (offering_id, group_id),
  FOREIGN KEY (offering_id, institution_id) REFERENCES course_offerings(id, institution_id) ON DELETE CASCADE,
  FOREIGN KEY (group_id, institution_id) REFERENCES groups(id, institution_id)
);
CREATE INDEX IF NOT EXISTS offering_groups_group ON offering_groups(group_id);
ALTER TABLE offering_groups ENABLE ROW LEVEL SECURITY;

-- All reads and writes go through the Go backend.
REVOKE ALL ON programmes, cohorts, academic_terms, course_offerings, offering_groups FROM anon, authenticated;
