-- Server-authoritative achievement catalogue support.
-- Progress is derived from quiz/user records; clients never write counters.

ALTER TABLE badges DROP CONSTRAINT IF EXISTS badges_badge_type_check;
-- Retire legacy predicates whose names now have different product meanings.
-- Explorer previously meant question-type coverage, which is Try Everything.
UPDATE badges SET badge_type = 'try_everything' WHERE badge_type = 'explorer';
DELETE FROM badges WHERE badge_type IN ('speed_demon', 'sharp_mind');
ALTER TABLE badges ADD CONSTRAINT badges_badge_type_check CHECK (badge_type IN (
  'welcome_aboard','profile_ready','first_quiz','score_unlocked','first_steps','getting_serious',
  'quiz_machine','half_century','century','quiz_storm','marathon_mind',
  'warming_up','on_a_roll','locked_in','unstoppable','iron_will',
  'sharp_mind','perfect_score','triple_threat','hot_streak',
  'explorer','jack_of_all_trades','try_everything','crowd_pleaser',
  'on_the_board','top_10','number_one','close_call'
));

-- A share is only credited once per UTC day.  The server supplies the user and
-- date, making retries idempotent and preventing a forged numeric increment.
CREATE TABLE IF NOT EXISTS scorecard_share_days (
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  shared_on DATE NOT NULL DEFAULT CURRENT_DATE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, shared_on)
);

ALTER TABLE scorecard_share_days ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON scorecard_share_days FROM anon, authenticated;
