-- Attempt starts and abandonments rolled up per learner, quiz and IST day, so
-- attempts_started, attempts_abandoned and the completion and abandonment rates
-- read a narrow table instead of scanning quiz_attempts on every dashboard
-- load. Keyed by learner and quiz so every metric scope (institution, class
-- members, authored quizzes) and distinct-learner counts still apply.
-- started_at holds the IST day bucket of quiz_attempts.started_at; the column
-- name matches the raw source so the same scope and window predicates apply.
CREATE TABLE attempt_daily_starts (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 quiz_id uuid NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
 started_at timestamptz NOT NULL,
 started bigint NOT NULL DEFAULT 0,
 abandoned bigint NOT NULL DEFAULT 0,
 PRIMARY KEY (user_id, quiz_id, started_at)
);
ALTER TABLE attempt_daily_starts ENABLE ROW LEVEL SECURITY;
CREATE INDEX attempt_daily_starts_window ON attempt_daily_starts(started_at, user_id, quiz_id);

CREATE FUNCTION maintain_attempt_daily_starts() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE d timestamptz;
BEGIN
 IF TG_OP <> 'INSERT' THEN
  d := date_trunc('day', OLD.started_at AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata';
  UPDATE attempt_daily_starts
     SET started = started - 1, abandoned = abandoned - (OLD.status = 'abandoned')::int
   WHERE user_id = OLD.user_id AND quiz_id = OLD.quiz_id AND started_at = d;
 END IF;
 IF TG_OP <> 'DELETE' THEN
  d := date_trunc('day', NEW.started_at AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata';
  INSERT INTO attempt_daily_starts VALUES (NEW.user_id, NEW.quiz_id, d, 1, (NEW.status = 'abandoned')::int)
  ON CONFLICT (user_id, quiz_id, started_at) DO UPDATE
     SET started = attempt_daily_starts.started + 1,
         abandoned = attempt_daily_starts.abandoned + EXCLUDED.abandoned;
 END IF;
 RETURN NULL;
END $$;

CREATE TRIGGER attempt_daily_starts_rows AFTER INSERT OR DELETE ON quiz_attempts
FOR EACH ROW EXECUTE FUNCTION maintain_attempt_daily_starts();
-- Answers update combo_level and last_answer_at on every question, and
-- completion changes status to 'completed'; neither moves a start or an
-- abandonment, so the WHEN clause keeps the trigger off that hot path.
CREATE TRIGGER attempt_daily_starts_moves
AFTER UPDATE OF user_id, quiz_id, started_at, status ON quiz_attempts
FOR EACH ROW WHEN (
 OLD.user_id IS DISTINCT FROM NEW.user_id OR OLD.quiz_id IS DISTINCT FROM NEW.quiz_id
 OR OLD.started_at IS DISTINCT FROM NEW.started_at
 OR (OLD.status = 'abandoned') IS DISTINCT FROM (NEW.status = 'abandoned'))
EXECUTE FUNCTION maintain_attempt_daily_starts();

-- CREATE TRIGGER holds the source-table lock until commit. Backfill after it
-- so concurrent starts cannot be omitted from the initial aggregate.
INSERT INTO attempt_daily_starts
SELECT user_id, quiz_id, date_trunc('day', started_at AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata',
       count(*), count(*) FILTER (WHERE status = 'abandoned')
FROM quiz_attempts GROUP BY 1, 2, 3;
