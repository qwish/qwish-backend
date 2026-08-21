-- Migration 042: Expand the shared quiz taxonomy.
--
-- These rows are consumed by both the teacher quiz editor and the public
-- onboarding picker. Keeping them in the reference tables means new content
-- categories appear everywhere without another client release.

INSERT INTO domains (slug, label, sort) VALUES
  ('science',           'Science',           6),
  ('social_studies',    'Social Studies',    7),
  ('current_affairs',   'Current Affairs',   8),
  ('language_learning', 'Language Learning', 9)
ON CONFLICT (slug) DO NOTHING;


INSERT INTO subdomains (slug, domain_slug, label, sort, difficulty) VALUES
  -- Science
  ('science_physics',       'science', 'Physics',             1, 0.80),
  ('science_chemistry',     'science', 'Chemistry',           2, 0.80),
  ('science_biology',       'science', 'Biology',             3, 0.70),
  ('science_environment',   'science', 'Environmental Science', 4, 0.60),
  ('science_space',         'science', 'Space Science',       5, 0.70),
  -- Social studies
  ('social_history',        'social_studies', 'History',      1, 0.60),
  ('social_geography',      'social_studies', 'Geography',    2, 0.60),
  ('social_civics',         'social_studies', 'Civics',       3, 0.60),
  ('social_economics',      'social_studies', 'Economics',    4, 0.70),
  -- Current affairs
  ('current_india',         'current_affairs', 'India',       1, 0.50),
  ('current_world',         'current_affairs', 'World',       2, 0.50),
  ('current_business_tech', 'current_affairs', 'Business & Technology', 3, 0.60),
  ('current_sports',        'current_affairs', 'Sports',      4, 0.50),
  -- Language learning
  ('lang_english',          'language_learning', 'English',   1, 0.50),
  ('lang_hindi',            'language_learning', 'Hindi',     2, 0.50),
  ('lang_marathi',          'language_learning', 'Marathi',   3, 0.50)
ON CONFLICT (slug) DO NOTHING;
