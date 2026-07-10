-- ============================================================================
-- Migration 020: Domain / subdomain taxonomy + derived question difficulty
-- ============================================================================
-- Adds a two-level content taxonomy (domain -> subdomain) that teachers tag
-- quizzes with, and a per-question `difficulty` coefficient that a nightly job
-- derives from real response data (see scheduler.RecomputeQuestionDifficulty).
--
-- subdomains.difficulty is the COLD-START PRIOR for a question's difficulty;
-- questions.difficulty is the derived value the scoring engine actually uses.
-- All statements are idempotent so the file is safe to re-run.
-- ============================================================================

-- ── Reference: domains ──────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS domains (
  slug  TEXT PRIMARY KEY,
  label TEXT NOT NULL,
  sort  INT  NOT NULL DEFAULT 0
);

INSERT INTO domains (slug, label, sort) VALUES
  ('aptitude',         'Aptitude',       1),
  ('quantitative',     'Quantitative',   2),
  ('logical',          'Logical',        3),
  ('verbal',           'Verbal',         4),
  ('computer_science', 'Computer Sci.',  5),
  ('general',          'General',        99)
ON CONFLICT (slug) DO NOTHING;

-- ── Reference: subdomains (difficulty = cold-start prior, 0.4–1.0) ──────────
CREATE TABLE IF NOT EXISTS subdomains (
  slug        TEXT PRIMARY KEY,
  domain_slug TEXT NOT NULL REFERENCES domains(slug),
  label       TEXT NOT NULL,
  sort        INT  NOT NULL DEFAULT 0,
  difficulty  NUMERIC(3,2) NOT NULL DEFAULT 0.60
              CHECK (difficulty >= 0.40 AND difficulty <= 1.00)
);
CREATE INDEX IF NOT EXISTS idx_subdomains_domain ON subdomains(domain_slug);

INSERT INTO subdomains (slug, domain_slug, label, sort, difficulty) VALUES
  -- Quantitative
  ('quant_arithmetic',          'quantitative',     'Arithmetic',           1, 0.60),
  ('quant_algebra',             'quantitative',     'Algebra',              2, 0.70),
  ('quant_geometry',            'quantitative',     'Geometry',             3, 1.00),
  ('quant_number_system',       'quantitative',     'Number System',        4, 0.60),
  ('quant_percentages',         'quantitative',     'Percentages',          5, 0.60),
  ('quant_tsw',                 'quantitative',     'Time·Speed·Work',      6, 0.80),
  ('quant_probability',         'quantitative',     'Probability',          7, 0.90),
  ('quant_di',                  'quantitative',     'Data Interpretation',  8, 0.80),
  -- Logical
  ('logical_series',            'logical',          'Series',               1, 0.60),
  ('logical_blood_relations',   'logical',          'Blood Relations',      2, 0.70),
  ('logical_syllogisms',        'logical',          'Syllogisms',           3, 0.80),
  ('logical_coding_decoding',   'logical',          'Coding-Decoding',      4, 0.70),
  ('logical_seating',           'logical',          'Seating Arrangement',  5, 0.90),
  ('logical_puzzles',           'logical',          'Puzzles',              6, 1.00),
  -- Verbal
  ('verbal_rc',                 'verbal',           'Reading Comprehension',1, 0.70),
  ('verbal_grammar',            'verbal',           'Grammar',              2, 0.60),
  ('verbal_vocabulary',         'verbal',           'Vocabulary',           3, 0.50),
  ('verbal_sentence_correction','verbal',           'Sentence Correction',  4, 0.70),
  ('verbal_para_jumbles',       'verbal',           'Para Jumbles',         5, 0.80),
  -- Aptitude
  ('apt_mixed',                 'aptitude',         'Mixed',                1, 0.60),
  ('apt_di',                    'aptitude',         'Data Interpretation',  2, 0.80),
  ('apt_analytical',            'aptitude',         'Analytical',           3, 0.80),
  -- Computer Science
  ('cs_dsa',                    'computer_science', 'DSA',                  1, 0.90),
  ('cs_dbms',                   'computer_science', 'DBMS',                 2, 0.70),
  ('cs_os',                     'computer_science', 'OS',                   3, 0.80),
  ('cs_networks',               'computer_science', 'Networks',             4, 0.70),
  ('cs_oop',                    'computer_science', 'OOP',                  5, 0.60),
  ('cs_programming',            'computer_science', 'Programming',          6, 0.70),
  -- General (fallback)
  ('general_mixed',             'general',          'Mixed',                1, 0.60)
ON CONFLICT (slug) DO NOTHING;

-- ── Tag quizzes with domain + subdomain ─────────────────────────────────────
ALTER TABLE quizzes ADD COLUMN IF NOT EXISTS domain    TEXT REFERENCES domains(slug);
ALTER TABLE quizzes ADD COLUMN IF NOT EXISTS subdomain TEXT REFERENCES subdomains(slug);

-- Backfill existing quizzes into the General bucket so aggregation has a home.
UPDATE quizzes SET domain = 'general' WHERE domain IS NULL;
UPDATE quizzes SET subdomain = 'general_mixed' WHERE subdomain IS NULL;

CREATE INDEX IF NOT EXISTS idx_quizzes_domain ON quizzes(domain, subdomain);

-- ── Derived per-question difficulty (0.4–1.0, refined nightly) ──────────────
-- Default 0.60 = "medium"; the nightly job shrinks this toward the empirical
-- correct-rate as responses accumulate.
ALTER TABLE questions ADD COLUMN IF NOT EXISTS difficulty NUMERIC(3,2) NOT NULL DEFAULT 0.60
  CHECK (difficulty >= 0.40 AND difficulty <= 1.00);
