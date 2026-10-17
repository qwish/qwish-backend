-- ============================================================================
-- Migration 109: Avatar wardrobe — collectible, upgradable domain outfits
-- ============================================================================
-- A learner keeps every persona outfit they have earned (108 gave them only
-- the one they wear). Each outfit has a tier (1..3) that rises as they keep
-- mastering its domain, adding details to the costume; tiers never drop.
-- They may pick which earned outfit to wear (avatar_persona_chosen); until
-- they do, the weekly job dresses them in their strongest domain.
-- Refreshed by scheduler.RefreshAvatarPersonas. Idempotent; safe to re-run.
-- ============================================================================

CREATE TABLE IF NOT EXISTS user_avatar_outfits (
  user_id     UUID     NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  persona     TEXT     NOT NULL,
  tier        SMALLINT NOT NULL DEFAULT 1 CHECK (tier BETWEEN 1 AND 3),
  earned_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  upgraded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, persona)
);
ALTER TABLE user_avatar_outfits ENABLE ROW LEVEL SECURITY;

ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_tier SMALLINT NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_persona_chosen BOOLEAN NOT NULL DEFAULT false;

-- Whatever a learner already wears is the first item in their wardrobe.
INSERT INTO user_avatar_outfits (user_id, persona, tier, earned_at, upgraded_at)
SELECT id, avatar_persona, 1, COALESCE(avatar_persona_at, now()), COALESCE(avatar_persona_at, now())
  FROM users WHERE avatar_persona IS NOT NULL
ON CONFLICT DO NOTHING;
