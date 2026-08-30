CREATE TABLE anonymous_surveys (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  slug TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 160),
  description TEXT,
  status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','closed')),
  created_by UUID REFERENCES admin_accounts(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE anonymous_survey_questions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  survey_id UUID NOT NULL REFERENCES anonymous_surveys(id) ON DELETE CASCADE,
  position INT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('single_choice','multiple_choice','rating','free_text')),
  prompt TEXT NOT NULL CHECK (char_length(prompt) BETWEEN 1 AND 2000),
  options JSONB NOT NULL DEFAULT '[]',
  required BOOLEAN NOT NULL DEFAULT true,
  UNIQUE (survey_id, position)
);

CREATE TABLE anonymous_survey_responses (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  survey_id UUID NOT NULL REFERENCES anonymous_surveys(id) ON DELETE CASCADE,
  receipt_hash BYTEA NOT NULL,
  answers JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (survey_id, receipt_hash)
);

CREATE INDEX idx_anonymous_survey_questions_survey ON anonymous_survey_questions(survey_id, position);
CREATE INDEX idx_anonymous_survey_responses_survey ON anonymous_survey_responses(survey_id, created_at DESC);
