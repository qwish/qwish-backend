-- Qwish Score becomes a skill rating (see internal/domain/scoring/rating.go).
-- Streak, speed and activity no longer feed it; they stay in XP (total_points).
CREATE TABLE IF NOT EXISTS learner_ratings (
  user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  theta DOUBLE PRECISION NOT NULL,
  sigma DOUBLE PRECISION NOT NULL,
  n INT NOT NULL DEFAULT 0,
  score DOUBLE PRECISION NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE learner_ratings ENABLE ROW LEVEL SECURITY;

-- NULL until first answered; seeded from questions.difficulty in Go.
ALTER TABLE questions ADD COLUMN IF NOT EXISTS rating_b DOUBLE PRECISION;
ALTER TABLE questions ADD COLUMN IF NOT EXISTS rating_n INT NOT NULL DEFAULT 0;

-- The leaderboard now mirrors the rating instead of recomputing a composite.
CREATE OR REPLACE FUNCTION refresh_leaderboard_score(p_user UUID)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
BEGIN
  INSERT INTO leaderboard_scores(user_id,qwish_score,completed_quizzes,updated_at)
  SELECT p_user,
         COALESCE((SELECT score FROM learner_ratings WHERE user_id=p_user),100),
         (SELECT COUNT(DISTINCT quiz_id)::int FROM quiz_attempts WHERE user_id=p_user AND status='completed'),
         now()
  ON CONFLICT(user_id) DO UPDATE SET qwish_score=EXCLUDED.qwish_score,
    completed_quizzes=EXCLUDED.completed_quizzes,updated_at=now();
END $$;

-- Streak changes no longer move the score.
DROP TRIGGER IF EXISTS trg_user_leaderboard_score ON users;
SELECT refresh_leaderboard_score(user_id) FROM leaderboard_scores;
