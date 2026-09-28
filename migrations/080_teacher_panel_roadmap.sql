-- Teacher panel roadmap (R2 R3 R5 R7 R11 R14 R16). Additive only.

-- R2/R3: per-student extension + private note; last reminder per assignment.
ALTER TABLE learning_assignment_recipients
  ADD COLUMN IF NOT EXISTS due_at_override TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS teacher_note TEXT CHECK (teacher_note IS NULL OR char_length(teacher_note) <= 500);
ALTER TABLE learning_assignments
  ADD COLUMN IF NOT EXISTS last_reminded_at TIMESTAMPTZ;

-- R5: the assessment a teacher made in response to a topic request.
ALTER TABLE topic_requests
  ADD COLUMN IF NOT EXISTS resolved_quiz_id UUID REFERENCES quizzes(id) ON DELETE SET NULL;

-- R7: teacher notification channels. Missing keys fall back to defaults in code.
CREATE TABLE IF NOT EXISTS teacher_notification_preferences (
  user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  prefs JSONB NOT NULL DEFAULT '{}'::jsonb,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE teacher_notification_preferences ENABLE ROW LEVEL SECURITY;

-- R7: teacher-facing notifications are emitted by cron; the reference is the
-- idempotency key so repeated runs never duplicate a row.
CREATE UNIQUE INDEX IF NOT EXISTS user_notifications_teacher_reference
  ON user_notifications(user_id, reference)
  WHERE kind IN ('assignment_overdue','follow_up_evidence','support_review','topic_request','edit_request','quiz_review') AND reference IS NOT NULL;

-- R11: AI-suggested misconception per wrong option, excluded from signals
-- until a teacher reviews it.
CREATE TABLE IF NOT EXISTS question_misconception_suggestions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
  option_text TEXT NOT NULL CHECK (char_length(option_text) <= 500),
  title TEXT NOT NULL CHECK (char_length(title) <= 200),
  status TEXT NOT NULL DEFAULT 'suggested' CHECK (status IN ('suggested','accepted','rejected')),
  created_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS question_misconception_suggestions_question ON question_misconception_suggestions(question_id);
ALTER TABLE question_misconception_suggestions ENABLE ROW LEVEL SECURITY;

-- R14: read-only parent summary links.
CREATE TABLE IF NOT EXISTS parent_summaries (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  teacher_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  period TEXT NOT NULL,
  include JSONB NOT NULL,
  message TEXT NOT NULL DEFAULT '' CHECK (char_length(message) <= 1000),
  token TEXT NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS parent_summaries_student ON parent_summaries(student_id, created_at DESC);
ALTER TABLE parent_summaries ENABLE ROW LEVEL SECURITY;

-- R16: server-synced UI preferences (opaque string map, last write wins).
CREATE TABLE IF NOT EXISTS user_preferences (
  user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  prefs JSONB NOT NULL DEFAULT '{}'::jsonb,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE user_preferences ENABLE ROW LEVEL SECURITY;
