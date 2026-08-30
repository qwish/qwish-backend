-- Incrementally maintained leaderboard scores replace full-history aggregation
-- on every leaderboard request.
CREATE TABLE IF NOT EXISTS leaderboard_scores (
  user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  qwish_score DOUBLE PRECISION NOT NULL DEFAULT 100,
  completed_quizzes INT NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE leaderboard_scores ENABLE ROW LEVEL SECURITY;

CREATE OR REPLACE FUNCTION refresh_leaderboard_score(p_user UUID)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
DECLARE v_score DOUBLE PRECISION; v_completed INT;
BEGIN
  SELECT COUNT(DISTINCT quiz_id)::int,
    100 + 8*(
      CASE WHEN COALESCE(SUM(total_questions),0)>0 THEN
        ((COALESCE(SUM(total_correct),0)+5.0)/(SUM(total_questions)+10.0))*50 ELSE 0 END
      + COALESCE((SELECT SUM(q.difficulty) FILTER(WHERE qr.is_correct)/NULLIF(SUM(q.difficulty),0)*20
          FROM question_responses qr JOIN questions q ON q.id=qr.question_id
          JOIN quiz_attempts a ON a.id=qr.attempt_id WHERE a.user_id=p_user AND a.status='completed'),0)
	  + COALESCE((SELECT AVG(CASE
	      WHEN qr.time_taken_ms<1000 THEN 0.1
	      WHEN qr.time_taken_ms<=q.time_limit_seconds*1000/3.0 THEN 1.0
	      ELSE GREATEST((q.time_limit_seconds*1000.0-qr.time_taken_ms)/
	        NULLIF(q.time_limit_seconds*1000.0-q.time_limit_seconds*1000.0/3.0,0),0.1) END)*10
	      FROM question_responses qr JOIN questions q ON q.id=qr.question_id
	      JOIN quiz_attempts a ON a.id=qr.attempt_id
	      WHERE a.user_id=p_user AND a.status='completed' AND qr.is_correct AND qr.time_taken_ms IS NOT NULL),0)
      + (1-EXP(-COALESCE((SELECT current_streak FROM users WHERE id=p_user),0)::float8/14.0))*15
      + (1-EXP(-COUNT(*)::float8/20.0))*5)
  INTO v_completed,v_score FROM quiz_attempts WHERE user_id=p_user AND status='completed';
  INSERT INTO leaderboard_scores(user_id,qwish_score,completed_quizzes,updated_at)
  VALUES(p_user,COALESCE(v_score,100),COALESCE(v_completed,0),now())
  ON CONFLICT(user_id) DO UPDATE SET qwish_score=EXCLUDED.qwish_score,
    completed_quizzes=EXCLUDED.completed_quizzes,updated_at=now();
END $$;

CREATE OR REPLACE FUNCTION refresh_leaderboard_score_trigger()
RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
BEGIN
  IF TG_TABLE_NAME='users' THEN PERFORM refresh_leaderboard_score(NEW.id);
  ELSE PERFORM refresh_leaderboard_score(COALESCE(NEW.user_id,OLD.user_id)); END IF;
  RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS trg_attempt_leaderboard_score ON quiz_attempts;
CREATE TRIGGER trg_attempt_leaderboard_score AFTER INSERT OR DELETE OR UPDATE OF status,score_pct,total_correct,total_questions ON quiz_attempts
FOR EACH ROW EXECUTE FUNCTION refresh_leaderboard_score_trigger();
DROP TRIGGER IF EXISTS trg_user_leaderboard_score ON users;
CREATE TRIGGER trg_user_leaderboard_score AFTER UPDATE OF current_streak ON users
FOR EACH ROW EXECUTE FUNCTION refresh_leaderboard_score_trigger();

SELECT refresh_leaderboard_score(id) FROM users WHERE role='student';
CREATE INDEX IF NOT EXISTS leaderboard_scores_rank ON leaderboard_scores(qwish_score DESC,user_id);
