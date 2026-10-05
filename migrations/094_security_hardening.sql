-- Security audit 2026-10-05 follow-ups.

-- Parent invites: unclaimed links carry NULL instead of an all-zero sentinel
-- UUID, so the claim can atomically require "parent_id IS NULL".
ALTER TABLE parent_student_links ALTER COLUMN parent_id DROP NOT NULL;
UPDATE parent_student_links SET parent_id = NULL
WHERE parent_id = '00000000-0000-0000-0000-000000000000';
-- Old 8-hex-char codes are guessable; retire any still unclaimed.
UPDATE parent_student_links SET status = 'revoked'
WHERE status = 'pending' AND parent_id IS NULL AND length(invite_code) < 26;

-- These tables had USING(true) SELECT policies, letting any signed-in client
-- read private profiles over the Data API. All reads go through the Go backend.
REVOKE ALL ON badges, user_education, user_skills FROM anon, authenticated;

-- Backend-only SECURITY DEFINER function: no PostgREST RPC access.
REVOKE ALL ON FUNCTION public.refresh_leaderboard_score(uuid) FROM PUBLIC, anon, authenticated;
GRANT EXECUTE ON FUNCTION public.refresh_leaderboard_score(uuid) TO service_role;

-- Offline packs ship correct answers. Quizzes whose key a learner has
-- downloaded no longer award points or move their rating when completed online.
CREATE TABLE IF NOT EXISTS offline_answer_exposures (
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  quiz_id UUID NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
  exposed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, quiz_id)
);
ALTER TABLE offline_answer_exposures ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON offline_answer_exposures FROM anon, authenticated;
