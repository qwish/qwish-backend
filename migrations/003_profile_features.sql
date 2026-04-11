-- Profile views
CREATE TABLE IF NOT EXISTS profile_views (
  id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  viewer_id  UUID        REFERENCES users(id) ON DELETE SET NULL,
  viewed_id  UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  viewed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_profile_views_viewed ON profile_views(viewed_id, viewed_at DESC);

-- User education
CREATE TABLE IF NOT EXISTS user_education (
  id               UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id          UUID    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  institution_name TEXT    NOT NULL,
  degree           TEXT,
  field            TEXT,
  start_year       INT,
  end_year         INT,
  is_current       BOOLEAN NOT NULL DEFAULT FALSE,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_education_user ON user_education(user_id);

-- User skills
CREATE TABLE IF NOT EXISTS user_skills (
  id         UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  skill_name TEXT    NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, skill_name)
);
CREATE INDEX IF NOT EXISTS idx_skills_user ON user_skills(user_id);

-- Domain/subject field on users
ALTER TABLE users ADD COLUMN IF NOT EXISTS domain TEXT;
