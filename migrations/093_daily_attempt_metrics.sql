CREATE TABLE attempt_daily_metrics (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 quiz_id uuid NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
 completed_at timestamptz NOT NULL,
 completed bigint NOT NULL DEFAULT 0, score_sum double precision NOT NULL DEFAULT 0, score_count bigint NOT NULL DEFAULT 0,
 PRIMARY KEY(user_id,quiz_id,completed_at)
);
ALTER TABLE attempt_daily_metrics ENABLE ROW LEVEL SECURITY;
CREATE INDEX attempt_daily_metrics_window ON attempt_daily_metrics(completed_at,user_id,quiz_id);
CREATE FUNCTION maintain_daily_attempt_metrics() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE d timestamptz;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.user_id,NEW.quiz_id,NEW.status,NEW.completed_at,NEW.score_pct) IS NOT DISTINCT FROM ROW(OLD.user_id,OLD.quiz_id,OLD.status,OLD.completed_at,OLD.score_pct) THEN RETURN NULL;END IF;
 END IF;
 IF TG_OP<>'INSERT' THEN
  IF OLD.status='completed' AND OLD.completed_at IS NOT NULL THEN
   d:=date_trunc('day',OLD.completed_at AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata';
   UPDATE attempt_daily_metrics SET completed=completed-1,score_sum=score_sum-COALESCE(OLD.score_pct,0),score_count=score_count-CASE WHEN OLD.score_pct IS NULL THEN 0 ELSE 1 END
   WHERE user_id=OLD.user_id AND quiz_id=OLD.quiz_id AND completed_at=d;
  END IF;
 END IF;
 IF TG_OP<>'DELETE' THEN
  IF NEW.status='completed' AND NEW.completed_at IS NOT NULL THEN
   d:=date_trunc('day',NEW.completed_at AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata';
   INSERT INTO attempt_daily_metrics VALUES(NEW.user_id,NEW.quiz_id,d,1,COALESCE(NEW.score_pct,0),CASE WHEN NEW.score_pct IS NULL THEN 0 ELSE 1 END)
   ON CONFLICT(user_id,quiz_id,completed_at) DO UPDATE SET completed=attempt_daily_metrics.completed+1,
   score_sum=attempt_daily_metrics.score_sum+EXCLUDED.score_sum,score_count=attempt_daily_metrics.score_count+EXCLUDED.score_count;
  END IF;
 END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER daily_attempt_metrics AFTER INSERT OR UPDATE OR DELETE ON quiz_attempts FOR EACH ROW EXECUTE FUNCTION maintain_daily_attempt_metrics();
-- CREATE TRIGGER holds the source-table lock until commit. Backfill after it
-- so concurrent completions cannot be omitted from the initial aggregate.
INSERT INTO attempt_daily_metrics
SELECT user_id,quiz_id,date_trunc('day',completed_at AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata',count(*),COALESCE(sum(score_pct),0),count(score_pct)
FROM quiz_attempts WHERE status='completed' AND completed_at IS NOT NULL GROUP BY 1,2,3;
CREATE INDEX notification_cursor ON user_notifications(user_id,created_at DESC,id DESC);
CREATE INDEX ledger_cursor ON points_ledger(user_id,created_at DESC,id DESC);
