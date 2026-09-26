-- Freeze the interpretation delivered to an attempt, alongside its question version.
ALTER TABLE quiz_attempt_questions ADD COLUMN option_choices JSONB;
ALTER TABLE quiz_attempt_questions ADD COLUMN learning_map JSONB;

CREATE FUNCTION snapshot_learning_map(question UUID, version UUID) RETURNS JSONB
LANGUAGE sql STABLE AS $$
 SELECT COALESCE(jsonb_agg(jsonb_build_object(
   'concept_id',qc.concept_id,'weight',qc.weight,
   'misconceptions',COALESCE((
     SELECT jsonb_agg(jsonb_build_object('option_id',qo.id,'misconception_id',m.id) ORDER BY qo.id,m.id)
     FROM question_misconception_options mo
     JOIN question_options qo ON qo.id=mo.option_id AND qo.question_id=qc.question_id AND qo.active
     JOIN misconceptions m ON m.id=mo.misconception_id AND m.concept_id=qc.concept_id AND m.active
     WHERE mo.question_id=qc.question_id AND qv.type<>'arrange_order'
       AND to_jsonb(qo.label)<>qv.correct_answer
   ),'[]'::jsonb)
 ) ORDER BY qc.concept_id),'[]'::jsonb)
 FROM question_concepts qc JOIN questions q ON q.id=qc.question_id
 JOIN question_versions qv ON qv.id=version AND qv.question_id=q.id AND qv.revision=q.revision
 WHERE qc.question_id=question
$$;

CREATE FUNCTION snapshot_option_choices(question UUID, version UUID) RETURNS JSONB
LANGUAGE sql STABLE AS $$
 SELECT COALESCE(jsonb_agg(jsonb_build_object('id',qo.id,'label',v.label) ORDER BY v.position),'[]'::jsonb)
 FROM question_versions qv
 CROSS JOIN LATERAL jsonb_array_elements_text(qv.options) WITH ORDINALITY v(label,position)
 JOIN question_options qo ON qo.question_id=question AND qo.label=v.label
 WHERE qv.id=version AND qv.question_id=question
$$;

-- Old versions retain their option identities. Historical mapping state was not
-- recorded, so pre-migration attempts must not acquire guessed diagnostic tags.
UPDATE quiz_attempt_questions SET
 option_choices=snapshot_option_choices(question_id,question_version_id),
 learning_map='[]'::jsonb;
ALTER TABLE quiz_attempt_questions ALTER COLUMN option_choices SET NOT NULL;
ALTER TABLE quiz_attempt_questions ALTER COLUMN learning_map SET NOT NULL;

CREATE FUNCTION freeze_attempt_learning_context() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF (NEW.question_id,NEW.question_version_id,NEW.question_revision,NEW.option_choices,NEW.learning_map)
    IS DISTINCT FROM (OLD.question_id,OLD.question_version_id,OLD.question_revision,OLD.option_choices,OLD.learning_map) THEN
   RAISE EXCEPTION 'attempt learning context is immutable' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
 END IF;
 PERFORM 1 FROM questions WHERE id=NEW.question_id FOR SHARE;
 NEW.option_choices:=snapshot_option_choices(NEW.question_id,NEW.question_version_id);
 NEW.learning_map:=snapshot_learning_map(NEW.question_id,NEW.question_version_id);
 RETURN NEW;
END $$;
CREATE TRIGGER freeze_attempt_learning_context BEFORE INSERT OR UPDATE ON quiz_attempt_questions
 FOR EACH ROW EXECUTE FUNCTION freeze_attempt_learning_context();

-- Keep one evidence row per response/concept, with any number of diagnostic tags.
-- The original nullable column is retained for old readers; new writers leave it NULL.
CREATE TABLE learning_evidence_misconceptions (
 evidence_id UUID NOT NULL REFERENCES learning_evidence(id) ON DELETE CASCADE,
 misconception_id UUID NOT NULL REFERENCES misconceptions(id) ON DELETE RESTRICT,
 PRIMARY KEY(evidence_id,misconception_id)
);
CREATE INDEX learning_evidence_misconceptions_lookup ON learning_evidence_misconceptions(misconception_id,evidence_id);
ALTER TABLE learning_evidence_misconceptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE learning_evidence ADD COLUMN timed_out BOOLEAN NOT NULL DEFAULT false;

-- Repair recoverable timing/correctness data using the delivered answer key.
-- Game scores and the original question_responses remain unchanged.
UPDATE learning_evidence le SET
 is_correct=(qr.answer=qv.correct_answer),
 timed_out=(qv.time_limit_seconds>0 AND qr.time_taken_ms>qv.time_limit_seconds*1000+2000)
 FROM question_responses qr,question_versions qv
 WHERE qr.id=le.response_id AND qv.id=le.question_version_id;
INSERT INTO learning_evidence_misconceptions(evidence_id,misconception_id)
 SELECT id,misconception_id FROM learning_evidence
 WHERE misconception_id IS NOT NULL AND NOT is_correct AND NOT timed_out;

CREATE INDEX learning_evidence_insight_window ON learning_evidence(institution_id,occurred_at,user_id,concept_id) WHERE NOT timed_out;
