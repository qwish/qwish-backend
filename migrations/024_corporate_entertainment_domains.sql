-- ============================================================================
-- Migration 024: Corporate + Entertainment domains
-- ============================================================================
-- Extends the domain/subdomain taxonomy (see 020_quiz_domains.sql) with:
--   • corporate     — knowledge-check activities run by companies (onboarding,
--                     compliance, product/security training, etc.)
--   • entertainment — quizzes on novels, movies, fictional shows, games, etc.
--
-- difficulty is the cold-start prior (0.40–1.00); the nightly job refines the
-- per-question value from real responses, so 0.60 ("medium") is a safe default
-- for content whose difficulty we can't rank up front. Idempotent; safe to re-run.
-- ============================================================================

INSERT INTO domains (slug, label, sort) VALUES
  ('corporate',     'Corporate',     6),
  ('entertainment', 'Entertainment', 7)
ON CONFLICT (slug) DO NOTHING;

INSERT INTO subdomains (slug, domain_slug, label, sort, difficulty) VALUES
  -- Corporate (company knowledge-check activities)
  ('corp_onboarding',       'corporate', 'Onboarding',            1, 0.50),
  ('corp_compliance',       'corporate', 'Compliance',            2, 0.70),
  ('corp_policies',         'corporate', 'Company Policies',      3, 0.60),
  ('corp_product',          'corporate', 'Product Knowledge',     4, 0.60),
  ('corp_security',         'corporate', 'Security Awareness',    5, 0.70),
  ('corp_safety',           'corporate', 'Health & Safety',       6, 0.60),
  ('corp_hr',               'corporate', 'HR & Culture',          7, 0.50),
  ('corp_sales',            'corporate', 'Sales Enablement',      8, 0.60),
  ('corp_customer_service', 'corporate', 'Customer Service',      9, 0.60),
  ('corp_leadership',       'corporate', 'Leadership',           10, 0.70),
  -- Entertainment (novels, fictional shows, movies, etc.)
  ('ent_novels',            'entertainment', 'Novels & Literature',        1, 0.60),
  ('ent_movies',            'entertainment', 'Movies',                     2, 0.50),
  ('ent_tv',                'entertainment', 'TV Shows',                   3, 0.50),
  ('ent_scifi_fantasy',     'entertainment', 'Sci-Fi & Fantasy',          4, 0.60),
  ('ent_anime',             'entertainment', 'Anime & Manga',             5, 0.60),
  ('ent_comics',            'entertainment', 'Comics & Graphic Novels',   6, 0.60),
  ('ent_games',             'entertainment', 'Video Games',               7, 0.60),
  ('ent_music',             'entertainment', 'Music',                     8, 0.50),
  ('ent_mythology',         'entertainment', 'Mythology & Folklore',      9, 0.70),
  ('ent_general',           'entertainment', 'General Trivia',           10, 0.50)
ON CONFLICT (slug) DO NOTHING;
