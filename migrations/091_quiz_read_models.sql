CREATE TABLE quiz_read_stats (
 quiz_id uuid PRIMARY KEY REFERENCES quizzes(id) ON DELETE CASCADE,
 completions bigint NOT NULL DEFAULT 0, completed_attempts bigint NOT NULL DEFAULT 0,
 started_count bigint NOT NULL DEFAULT 0, avg_score_pct double precision, avg_seconds double precision,
 difficulty double precision, updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE quiz_read_stats ENABLE ROW LEVEL SECURITY;
CREATE INDEX quiz_read_stats_popular ON quiz_read_stats(completions DESC,quiz_id);

CREATE FUNCTION queue_quiz_stats() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE qid uuid;
BEGIN
 IF TG_TABLE_NAME='questions' AND TG_OP='UPDATE' THEN
  IF NEW.difficulty IS NOT DISTINCT FROM OLD.difficulty AND NEW.quiz_id=OLD.quiz_id THEN RETURN NULL;END IF;
 END IF;
 IF TG_TABLE_NAME='quiz_attempts' AND TG_OP='UPDATE' THEN
  IF ROW(NEW.status,NEW.score_pct,NEW.started_at,NEW.completed_at,NEW.quiz_id,NEW.user_id)
  IS NOT DISTINCT FROM ROW(OLD.status,OLD.score_pct,OLD.started_at,OLD.completed_at,OLD.quiz_id,OLD.user_id) THEN RETURN NULL;END IF;
 END IF;
 IF TG_TABLE_NAME='quizzes' THEN qid:=NEW.id;
 ELSIF TG_OP='DELETE' THEN qid:=OLD.quiz_id;ELSE qid:=NEW.quiz_id;END IF;
 INSERT INTO background_jobs(kind,dedupe_key,payload) VALUES('quiz_stats',gen_random_uuid()::text,jsonb_build_object('quiz_id',qid));
 IF TG_TABLE_NAME<>'quizzes' THEN
 IF TG_OP='UPDATE' AND NEW.quiz_id<>OLD.quiz_id THEN
 INSERT INTO background_jobs(kind,dedupe_key,payload) VALUES('quiz_stats',gen_random_uuid()::text,jsonb_build_object('quiz_id',OLD.quiz_id));
 END IF;
 END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER quiz_stats_attempt AFTER INSERT OR UPDATE OR DELETE ON quiz_attempts FOR EACH ROW EXECUTE FUNCTION queue_quiz_stats();
CREATE TRIGGER quiz_stats_question AFTER INSERT OR UPDATE OR DELETE ON questions FOR EACH ROW EXECUTE FUNCTION queue_quiz_stats();
CREATE TRIGGER quiz_stats_new AFTER INSERT ON quizzes FOR EACH ROW EXECUTE FUNCTION queue_quiz_stats();
CREATE TRIGGER qwish_cache_change AFTER INSERT OR UPDATE OR DELETE ON quiz_read_stats FOR EACH STATEMENT EXECUTE FUNCTION broadcast_cache_change();
-- Installing source triggers first holds write-conflicting table locks through
-- this migration transaction, so concurrent writes cannot fall between the
-- initial snapshot and trigger installation.
INSERT INTO quiz_read_stats(quiz_id,completions,completed_attempts,started_count,avg_score_pct,avg_seconds,difficulty)
SELECT q.id,COALESCE(a.users,0),COALESCE(a.completed,0),COALESCE(a.started,0),a.score,a.seconds,d.difficulty
FROM quizzes q
LEFT JOIN (SELECT quiz_id,count(*) started,count(*) FILTER(WHERE status='completed') completed,
 count(DISTINCT user_id) FILTER(WHERE status='completed') users,
 avg(score_pct) FILTER(WHERE status='completed') score,
 avg(EXTRACT(epoch FROM completed_at-started_at)) FILTER(WHERE status='completed') seconds
 FROM quiz_attempts GROUP BY quiz_id) a ON a.quiz_id=q.id
LEFT JOIN (SELECT quiz_id,avg(difficulty) difficulty FROM questions GROUP BY quiz_id)d ON d.quiz_id=q.id;

-- Matching the newest feed's cursor order avoids sorting all pages.
CREATE INDEX quizzes_newest_cursor ON quizzes(published_at DESC NULLS LAST,id) WHERE status='published' AND deleted_at IS NULL;
CREATE INDEX jobs_quiz_stats_coalesce ON background_jobs((payload->>'quiz_id'),created_at) WHERE kind='quiz_stats' AND state='pending';
