-- Stable question choices and auditable concept evidence for misconception insights.
-- Legacy string option/answer fields remain intact for older clients.

ALTER TABLE questions ADD COLUMN IF NOT EXISTS revision INT NOT NULL DEFAULT 1;
ALTER TABLE quiz_attempt_questions ADD COLUMN IF NOT EXISTS question_revision INT NOT NULL DEFAULT 1;
ALTER TABLE question_responses ADD COLUMN IF NOT EXISTS option_id UUID;

CREATE TABLE IF NOT EXISTS question_versions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  question_id UUID NOT NULL REFERENCES questions(id) ON DELETE RESTRICT,
  revision INT NOT NULL,
  type TEXT NOT NULL,
  prompt TEXT NOT NULL,
  media_url TEXT,
  options JSONB NOT NULL,
  correct_answer JSONB NOT NULL,
  time_limit_seconds INT NOT NULL,
  clues JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (question_id, revision)
);
ALTER TABLE quiz_attempt_questions ADD COLUMN IF NOT EXISTS question_version_id UUID REFERENCES question_versions(id) ON DELETE RESTRICT;

CREATE TABLE IF NOT EXISTS question_options (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
  label TEXT NOT NULL CHECK (char_length(label) BETWEEN 1 AND 1000),
  position INT NOT NULL CHECK (position > 0),
  active BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (question_id, label)
);
CREATE UNIQUE INDEX IF NOT EXISTS question_options_active_position
  ON question_options (question_id, position) WHERE active;

ALTER TABLE question_responses
  ADD CONSTRAINT question_responses_option_id_fkey
  FOREIGN KEY (option_id) REFERENCES question_options(id) ON DELETE RESTRICT NOT VALID;

CREATE TABLE IF NOT EXISTS question_concepts (
  question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
  concept_id UUID NOT NULL REFERENCES curriculum_concepts(id) ON DELETE RESTRICT,
  weight NUMERIC(5,4) NOT NULL DEFAULT 1 CHECK (weight > 0 AND weight <= 1),
  mapped_by UUID REFERENCES users(id) ON DELETE SET NULL,
  mapped_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (question_id, concept_id)
);

CREATE TABLE IF NOT EXISTS misconceptions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  concept_id UUID NOT NULL REFERENCES curriculum_concepts(id) ON DELETE CASCADE,
  code TEXT NOT NULL,
  title TEXT NOT NULL,
  description TEXT,
  active BOOLEAN NOT NULL DEFAULT true,
  created_by UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (institution_id, code),
  UNIQUE (id, concept_id)
);

CREATE TABLE IF NOT EXISTS question_misconception_options (
  question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
  option_id UUID NOT NULL REFERENCES question_options(id) ON DELETE CASCADE,
  misconception_id UUID NOT NULL REFERENCES misconceptions(id) ON DELETE CASCADE,
  reviewed_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  reviewed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (question_id, option_id, misconception_id)
);

CREATE TABLE IF NOT EXISTS learning_evidence (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  response_id UUID NOT NULL REFERENCES question_responses(id) ON DELETE CASCADE,
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  attempt_id UUID NOT NULL REFERENCES quiz_attempts(id) ON DELETE CASCADE,
  question_id UUID NOT NULL REFERENCES questions(id) ON DELETE RESTRICT,
  question_revision INT NOT NULL,
  question_version_id UUID NOT NULL REFERENCES question_versions(id) ON DELETE RESTRICT,
  concept_id UUID NOT NULL REFERENCES curriculum_concepts(id) ON DELETE RESTRICT,
  option_id UUID REFERENCES question_options(id) ON DELETE RESTRICT,
  misconception_id UUID REFERENCES misconceptions(id) ON DELETE RESTRICT,
  is_correct BOOLEAN NOT NULL,
  confidence_level TEXT CHECK (confidence_level IN ('not_sure','pretty_sure','very_confident')),
  clues_used INT NOT NULL DEFAULT 0,
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (response_id, concept_id)
);

CREATE TABLE IF NOT EXISTS misconception_reviews (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  misconception_id UUID NOT NULL REFERENCES misconceptions(id) ON DELETE CASCADE,
  status TEXT NOT NULL CHECK (status IN ('confirmed','dismissed')),
  reason TEXT NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 1000),
  reviewed_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  reviewed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (institution_id, student_id, misconception_id)
);

CREATE INDEX IF NOT EXISTS learning_evidence_student_concept_time
  ON learning_evidence (institution_id, user_id, concept_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS learning_evidence_misconception_time
  ON learning_evidence (institution_id, misconception_id, occurred_at DESC)
  WHERE misconception_id IS NOT NULL;

CREATE OR REPLACE FUNCTION sync_question_options() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  UPDATE question_options SET active=false WHERE question_id=NEW.id;
  INSERT INTO question_options (question_id, label, position, active)
  SELECT NEW.id, value, ordinality::int, true
  FROM jsonb_array_elements_text(COALESCE(NEW.options, '[]'::jsonb)) WITH ORDINALITY
  ON CONFLICT (question_id, label) DO UPDATE
    SET position=EXCLUDED.position, active=true;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION increment_question_revision() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF ROW(NEW.type,NEW.prompt,NEW.media_url,NEW.options,NEW.correct_answer,NEW.time_limit_seconds,NEW.clues)
     IS DISTINCT FROM ROW(OLD.type,OLD.prompt,OLD.media_url,OLD.options,OLD.correct_answer,OLD.time_limit_seconds,OLD.clues) THEN
    NEW.revision := OLD.revision + 1;
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION snapshot_question_version() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO question_versions(question_id,revision,type,prompt,media_url,options,correct_answer,time_limit_seconds,clues)
  VALUES(NEW.id,NEW.revision,NEW.type,NEW.prompt,NEW.media_url,NEW.options,NEW.correct_answer,NEW.time_limit_seconds,NEW.clues)
  ON CONFLICT(question_id,revision) DO NOTHING;
  RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS questions_increment_revision ON questions;
CREATE TRIGGER questions_increment_revision BEFORE UPDATE ON questions
FOR EACH ROW EXECUTE FUNCTION increment_question_revision();
DROP TRIGGER IF EXISTS questions_snapshot_version ON questions;
CREATE TRIGGER questions_snapshot_version AFTER INSERT OR UPDATE ON questions
FOR EACH ROW EXECUTE FUNCTION snapshot_question_version();

INSERT INTO question_versions(question_id,revision,type,prompt,media_url,options,correct_answer,time_limit_seconds,clues)
SELECT id,revision,type,prompt,media_url,options,correct_answer,time_limit_seconds,clues FROM questions
ON CONFLICT(question_id,revision) DO NOTHING;

UPDATE quiz_attempt_questions aq SET question_version_id=qv.id
FROM question_versions qv WHERE qv.question_id=aq.question_id AND qv.revision=aq.question_revision AND aq.question_version_id IS NULL;
ALTER TABLE quiz_attempt_questions ALTER COLUMN question_version_id SET NOT NULL;

DROP TRIGGER IF EXISTS questions_sync_options ON questions;
CREATE TRIGGER questions_sync_options AFTER INSERT OR UPDATE OF options ON questions
FOR EACH ROW EXECUTE FUNCTION sync_question_options();

INSERT INTO question_options (question_id, label, position)
SELECT q.id, value, ordinality::int
FROM questions q, jsonb_array_elements_text(COALESCE(q.options, '[]'::jsonb)) WITH ORDINALITY
ON CONFLICT (question_id, label) DO UPDATE SET position=EXCLUDED.position, active=true;

ALTER TABLE question_options ENABLE ROW LEVEL SECURITY;
ALTER TABLE question_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE question_concepts ENABLE ROW LEVEL SECURITY;
ALTER TABLE misconceptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE question_misconception_options ENABLE ROW LEVEL SECURITY;
ALTER TABLE learning_evidence ENABLE ROW LEVEL SECURITY;
ALTER TABLE misconception_reviews ENABLE ROW LEVEL SECURITY;
