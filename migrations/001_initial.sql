-- QuizApp Initial Schema Migration
-- Run this against your Supabase PostgreSQL database

-- =============================================
-- ADMIN ACCOUNTS (defined first, referenced by institutions)
-- =============================================
CREATE TABLE IF NOT EXISTS admin_accounts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  supabase_uid UUID UNIQUE NOT NULL,
  name TEXT NOT NULL,
  email TEXT UNIQUE NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('super_admin','moderator','support_agent')),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','deleted')),
  created_by UUID REFERENCES admin_accounts(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);

-- =============================================
-- INSTITUTIONS
-- =============================================
CREATE TABLE IF NOT EXISTS institutions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('school','college','tuition')),
  contact_email TEXT NOT NULL,
  timezone TEXT NOT NULL DEFAULT 'Asia/Kolkata',
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','verified','suspended')),
  student_referral_code TEXT UNIQUE NOT NULL,
  teacher_referral_code TEXT UNIQUE NOT NULL,
  verified_at TIMESTAMPTZ,
  verified_by UUID REFERENCES admin_accounts(id),
  point_multiplier NUMERIC(4,2) NOT NULL DEFAULT 1.0,
  streak_grace_enabled BOOLEAN NOT NULL DEFAULT TRUE,
  play_win_score_hidden BOOLEAN NOT NULL DEFAULT FALSE,
  point_expiry_months INT NOT NULL DEFAULT 6,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);

-- =============================================
-- USERS
-- =============================================
CREATE TABLE IF NOT EXISTS users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  supabase_uid UUID UNIQUE NOT NULL,
  full_name TEXT NOT NULL,
  display_name TEXT NOT NULL,
  email TEXT UNIQUE NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('student','teacher','institution_admin','parent','moderator','support_agent','super_admin')),
  institution_id UUID REFERENCES institutions(id),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','deleted')),
  total_points BIGINT NOT NULL DEFAULT 0,
  current_streak INT NOT NULL DEFAULT 0,
  longest_streak INT NOT NULL DEFAULT 0,
  last_completed_date DATE,
  challenge_code CHAR(8) UNIQUE,
  member_since TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_active_at TIMESTAMPTZ,
  suspension_reason TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);

-- =============================================
-- GROUPS (CLASSES)
-- =============================================
CREATE TABLE IF NOT EXISTS groups (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID NOT NULL REFERENCES institutions(id),
  name TEXT NOT NULL,
  description TEXT,
  invite_code TEXT UNIQUE NOT NULL,
  archived_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS group_students (
  group_id UUID REFERENCES groups(id) ON DELETE CASCADE,
  user_id UUID REFERENCES users(id) ON DELETE CASCADE,
  joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (group_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_teachers (
  group_id UUID REFERENCES groups(id) ON DELETE CASCADE,
  user_id UUID REFERENCES users(id) ON DELETE CASCADE,
  assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (group_id, user_id)
);

-- =============================================
-- QUIZZES
-- =============================================
CREATE TABLE IF NOT EXISTS quizzes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID REFERENCES institutions(id),
  created_by UUID NOT NULL REFERENCES users(id),
  title TEXT NOT NULL,
  description TEXT,
  type TEXT NOT NULL CHECK (type IN ('knowledge_check','play_and_win')),
  visibility TEXT NOT NULL DEFAULT 'institution' CHECK (visibility IN ('institution','public')),
  status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','pending_approval','published','closed','rejected')),
  question_count INT NOT NULL DEFAULT 0,
  ends_at TIMESTAMPTZ,
  published_at TIMESTAMPTZ,
  approved_by UUID REFERENCES admin_accounts(id),
  approved_at TIMESTAMPTZ,
  rejection_reason TEXT,
  group_id UUID REFERENCES groups(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);

-- =============================================
-- QUESTIONS
-- =============================================
CREATE TABLE IF NOT EXISTS questions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  quiz_id UUID NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
  position INT NOT NULL,
  type TEXT NOT NULL CHECK (type IN (
    'multiple_choice','confidence_based','eliminate_wrong',
    'puzzle','speed_chain','arrange_order','clue_reveal'
  )),
  prompt TEXT NOT NULL,
  media_url TEXT,
  options JSONB NOT NULL DEFAULT '[]',
  correct_answer JSONB NOT NULL,
  time_limit_seconds INT NOT NULL DEFAULT 15,
  clues JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =============================================
-- QUIZ ATTEMPTS
-- =============================================
CREATE TABLE IF NOT EXISTS quiz_attempts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  quiz_id UUID NOT NULL REFERENCES quizzes(id),
  user_id UUID NOT NULL REFERENCES users(id),
  status TEXT NOT NULL DEFAULT 'in_progress' CHECK (status IN ('in_progress','completed','abandoned')),
  score_pct NUMERIC(5,2),
  points_delta BIGINT,
  total_correct INT,
  total_questions INT,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  point_config_snapshot JSONB
);

-- =============================================
-- QUESTION RESPONSES
-- =============================================
CREATE TABLE IF NOT EXISTS question_responses (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  attempt_id UUID NOT NULL REFERENCES quiz_attempts(id) ON DELETE CASCADE,
  question_id UUID NOT NULL REFERENCES questions(id),
  answer JSONB,
  is_correct BOOLEAN,
  time_taken_ms INT,
  clues_used INT DEFAULT 0,
  confidence_level TEXT CHECK (confidence_level IN ('not_sure','pretty_sure','very_confident')),
  combo_level INT DEFAULT 0,
  points_earned BIGINT DEFAULT 0,
  submitted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =============================================
-- POINTS LEDGER
-- =============================================
CREATE TABLE IF NOT EXISTS points_ledger (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id),
  amount BIGINT NOT NULL,
  reason TEXT NOT NULL CHECK (reason IN ('quiz_attempt','streak_bonus','expiry','manual_adjustment','badge_bonus')),
  reference_id UUID,
  balance_after BIGINT NOT NULL,
  expires_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =============================================
-- STREAKS
-- =============================================
CREATE TABLE IF NOT EXISTS streaks (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID UNIQUE NOT NULL REFERENCES users(id),
  current_streak INT NOT NULL DEFAULT 0,
  longest_streak INT NOT NULL DEFAULT 0,
  last_completed_date DATE,
  grace_window_active BOOLEAN NOT NULL DEFAULT FALSE,
  milestone_7_claimed BOOLEAN NOT NULL DEFAULT FALSE,
  milestone_15_claimed BOOLEAN NOT NULL DEFAULT FALSE,
  milestone_30_claimed BOOLEAN NOT NULL DEFAULT FALSE,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =============================================
-- BADGES
-- =============================================
CREATE TABLE IF NOT EXISTS badges (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id),
  badge_type TEXT NOT NULL CHECK (badge_type IN (
    'first_quiz','on_a_roll','unstoppable','top_10',
    'perfect_score','speed_demon','sharp_mind','explorer'
  )),
  earned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, badge_type)
);

-- =============================================
-- SAVED QUIZZES
-- =============================================
CREATE TABLE IF NOT EXISTS saved_quizzes (
  user_id UUID REFERENCES users(id) ON DELETE CASCADE,
  quiz_id UUID REFERENCES quizzes(id) ON DELETE CASCADE,
  saved_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, quiz_id)
);

-- =============================================
-- PARENT-STUDENT LINKS
-- =============================================
CREATE TABLE IF NOT EXISTS parent_student_links (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  parent_id UUID NOT NULL REFERENCES users(id),
  student_id UUID NOT NULL REFERENCES users(id),
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','active','revoked')),
  invite_code TEXT UNIQUE NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  linked_at TIMESTAMPTZ
);

-- =============================================
-- TOPIC REQUESTS
-- =============================================
CREATE TABLE IF NOT EXISTS topic_requests (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  student_id UUID NOT NULL REFERENCES users(id),
  institution_id UUID NOT NULL REFERENCES institutions(id),
  topic TEXT NOT NULL,
  subject TEXT,
  description TEXT,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','in_progress','done')),
  assigned_to UUID REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =============================================
-- REPORTS
-- =============================================
CREATE TABLE IF NOT EXISTS reports (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  reporter_id UUID NOT NULL REFERENCES users(id),
  quiz_id UUID REFERENCES quizzes(id),
  question_id UUID REFERENCES questions(id),
  reason TEXT NOT NULL CHECK (reason IN (
    'incorrect_answer','offensive_content','unclear_question','technical_issue','other'
  )),
  description TEXT,
  status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','reviewing','resolved')),
  resolution TEXT CHECK (resolution IN ('no_action','edit_required','remove_quiz','escalated')),
  priority TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN ('normal','high')),
  reviewed_by UUID REFERENCES admin_accounts(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at TIMESTAMPTZ
);

-- =============================================
-- AUDIT LOG
-- =============================================
CREATE TABLE IF NOT EXISTS audit_log (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  timestamp TIMESTAMPTZ NOT NULL DEFAULT now(),
  admin_id UUID NOT NULL,
  admin_name TEXT NOT NULL,
  admin_role TEXT NOT NULL,
  action_type TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_id UUID,
  reason TEXT,
  old_value JSONB,
  new_value JSONB
);

-- =============================================
-- POINT ECONOMY CONFIG
-- =============================================
CREATE TABLE IF NOT EXISTS point_economy_config (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  key TEXT UNIQUE NOT NULL,
  value JSONB NOT NULL,
  description TEXT,
  updated_by UUID REFERENCES admin_accounts(id),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed default point economy config
INSERT INTO point_economy_config (key, value, description) VALUES
  ('base_points_per_question', '10', 'Points awarded per correct answer on a standard question'),
  ('performance_bonus_pct_75', '20', 'Bonus percentage when score >= 75%'),
  ('deduction_pct_below_50', '50', 'Percentage of base points deducted when score < 50%'),
  ('streak_bonus_7_day', '50', 'Flat bonus points at 7-day streak milestone'),
  ('streak_bonus_15_day', '100', 'Flat bonus points at 15-day streak milestone'),
  ('streak_bonus_30_day', '250', 'Flat bonus points at 30-day streak milestone'),
  ('combo_multiplier_step', '0.5', 'Points multiplier increment per consecutive correct in Speed Chain'),
  ('clue_reveal_deduction_per_clue', '0.25', 'Point multiplier reduction per clue revealed'),
  ('points_expiry_months', '6', 'Default months until earned points expire'),
  ('points_floor', '0', 'Minimum points balance'),
  ('confidence_multiplier_table', '{"very_confident_correct":1.5,"pretty_sure_correct":1.0,"not_sure_correct":0.5,"very_confident_wrong":-0.5,"pretty_sure_wrong":0,"not_sure_wrong":0}', 'Point multipliers per confidence level')
ON CONFLICT (key) DO NOTHING;

-- =============================================
-- ANNOUNCEMENTS
-- =============================================
CREATE TABLE IF NOT EXISTS announcements (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  cta_label TEXT,
  cta_url TEXT,
  delivery_types TEXT[] NOT NULL,
  audience TEXT NOT NULL CHECK (audience IN ('all','students','teachers','institution','country')),
  institution_id UUID REFERENCES institutions(id),
  status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','scheduled','sent','retracted')),
  scheduled_at TIMESTAMPTZ,
  sent_at TIMESTAMPTZ,
  created_by UUID REFERENCES admin_accounts(id),
  approved_by UUID REFERENCES admin_accounts(id),
  estimated_reach INT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =============================================
-- PROMOTIONAL CONTENT
-- =============================================
CREATE TABLE IF NOT EXISTS promotional_content (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  type TEXT NOT NULL CHECK (type IN ('home_banner','quiz_browser_banner','splash_interstitial','achievement_prompt')),
  title TEXT NOT NULL,
  body TEXT,
  cta_label TEXT,
  cta_url TEXT,
  image_url TEXT,
  audience TEXT NOT NULL DEFAULT 'all',
  institution_id UUID REFERENCES institutions(id),
  status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','active','inactive')),
  starts_at TIMESTAMPTZ,
  ends_at TIMESTAMPTZ,
  created_by UUID REFERENCES admin_accounts(id),
  approved_by UUID REFERENCES admin_accounts(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =============================================
-- IMPERSONATION SESSIONS
-- =============================================
CREATE TABLE IF NOT EXISTS impersonation_sessions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  admin_id UUID NOT NULL REFERENCES admin_accounts(id),
  user_id UUID NOT NULL REFERENCES users(id),
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at TIMESTAMPTZ
);

-- =============================================
-- LEADERBOARD SNAPSHOTS
-- =============================================
CREATE TABLE IF NOT EXISTS leaderboard_snapshots (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  scope TEXT NOT NULL CHECK (scope IN ('institution','global')),
  institution_id UUID REFERENCES institutions(id),
  week_start DATE NOT NULL,
  rankings JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =============================================
-- INDEXES
-- =============================================
CREATE INDEX IF NOT EXISTS idx_users_institution ON users(institution_id);
CREATE INDEX IF NOT EXISTS idx_users_supabase_uid ON users(supabase_uid);
CREATE INDEX IF NOT EXISTS idx_users_status ON users(status);
CREATE INDEX IF NOT EXISTS idx_quizzes_institution ON quizzes(institution_id);
CREATE INDEX IF NOT EXISTS idx_quizzes_status ON quizzes(status);
CREATE INDEX IF NOT EXISTS idx_quizzes_created_by ON quizzes(created_by);
CREATE INDEX IF NOT EXISTS idx_attempts_user ON quiz_attempts(user_id);
CREATE INDEX IF NOT EXISTS idx_attempts_quiz ON quiz_attempts(quiz_id);
CREATE INDEX IF NOT EXISTS idx_attempts_status ON quiz_attempts(status);
CREATE INDEX IF NOT EXISTS idx_points_user ON points_ledger(user_id);
CREATE INDEX IF NOT EXISTS idx_points_expires ON points_ledger(expires_at);
CREATE INDEX IF NOT EXISTS idx_reports_status ON reports(status);
CREATE INDEX IF NOT EXISTS idx_reports_priority ON reports(priority);
CREATE INDEX IF NOT EXISTS idx_audit_admin ON audit_log(admin_id);
CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_log(action_type);
CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_log(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_streaks_user ON streaks(user_id);
CREATE INDEX IF NOT EXISTS idx_badges_user ON badges(user_id);
CREATE INDEX IF NOT EXISTS idx_groups_institution ON groups(institution_id);
CREATE INDEX IF NOT EXISTS idx_topic_requests_institution ON topic_requests(institution_id);
CREATE INDEX IF NOT EXISTS idx_parent_links_student ON parent_student_links(student_id);
CREATE INDEX IF NOT EXISTS idx_parent_links_parent ON parent_student_links(parent_id);
