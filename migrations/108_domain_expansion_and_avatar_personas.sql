-- ============================================================================
-- Migration 108: Broad domain expansion + mastery-driven avatar personas
-- ============================================================================
-- 1. Grows the domain/subdomain taxonomy (020, 024, 042) to cover what general
--    trivia platforms (Open Trivia DB's 24 categories, Sporcle's 16) and Indian
--    competitive exams (UPSC, SSC, banking, JEE/NEET, CAT, GATE, CLAT) test.
-- 2. Gives every domain an avatar `persona` (a costume the avatar generator in
--    internal/domain/avatar knows how to draw). A subdomain may override it,
--    e.g. Space Science -> astronaut. NULL on a subdomain = inherit the domain's.
-- 3. users.avatar_persona: the persona earned from the learner's strongest
--    mastered topic. Refreshed weekly by scheduler.RefreshAvatarPersonas.
--
-- difficulty is the cold-start prior (0.40–1.00); the nightly job refines it.
-- Idempotent; safe to re-run.
-- ============================================================================

ALTER TABLE domains    ADD COLUMN IF NOT EXISTS persona TEXT NOT NULL DEFAULT 'scholar';
ALTER TABLE subdomains ADD COLUMN IF NOT EXISTS persona TEXT;
ALTER TABLE users      ADD COLUMN IF NOT EXISTS avatar_persona    TEXT;
ALTER TABLE users      ADD COLUMN IF NOT EXISTS avatar_persona_at TIMESTAMPTZ;

-- ── Personas for the existing domains ──────────────────────────────────────
UPDATE domains SET persona = v.persona FROM (VALUES
  ('aptitude',          'strategist'),
  ('quantitative',      'mathematician'),
  ('logical',           'detective'),
  ('verbal',            'author'),
  ('computer_science',  'coder'),
  ('corporate',         'executive'),
  ('entertainment',     'director'),
  ('science',           'scientist'),
  ('social_studies',    'historian'),
  ('current_affairs',   'journalist'),
  ('language_learning', 'linguist'),
  ('general',           'scholar')
) AS v(slug, persona) WHERE domains.slug = v.slug;

-- ── New domains ─────────────────────────────────────────────────────────────
INSERT INTO domains (slug, label, sort, persona) VALUES
  ('mathematics',         'Mathematics',          10, 'mathematician'),
  ('technology',          'Technology',           11, 'coder'),
  ('engineering',         'Engineering',          12, 'engineer'),
  ('business_finance',    'Business & Finance',   13, 'executive'),
  ('law',                 'Law',                  14, 'judge'),
  ('health_medicine',     'Health & Medicine',    15, 'doctor'),
  ('nature',              'Nature & Animals',     16, 'naturalist'),
  ('sports',              'Sports',               17, 'athlete'),
  ('arts',                'Arts & Culture',       18, 'artist'),
  ('food_drink',          'Food & Drink',         19, 'chef'),
  ('transport',           'Vehicles & Transport', 20, 'pilot'),
  ('philosophy_religion', 'Philosophy & Religion',21, 'philosopher'),
  ('games_puzzles',       'Games & Puzzles',      22, 'grandmaster'),
  ('lifestyle',           'Lifestyle',            23, 'trendsetter'),
  ('exam_prep',           'Exam Prep',            24, 'topper')
ON CONFLICT (slug) DO NOTHING;

-- ── New subdomains (persona NULL = inherit the domain's) ───────────────────
INSERT INTO subdomains (slug, domain_slug, label, sort, difficulty, persona) VALUES
  -- Quantitative (aptitude-exam staples)
  ('quant_ratio',               'quantitative', 'Ratio & Proportion',     9, 0.60, NULL),
  ('quant_average',             'quantitative', 'Averages',              10, 0.50, NULL),
  ('quant_profit_loss',         'quantitative', 'Profit & Loss',         11, 0.60, NULL),
  ('quant_interest',            'quantitative', 'Simple & Compound Interest', 12, 0.60, NULL),
  ('quant_mixtures',            'quantitative', 'Mixtures & Alligation', 13, 0.70, NULL),
  ('quant_permutations',        'quantitative', 'Permutations & Combinations', 14, 0.90, NULL),
  ('quant_mensuration',         'quantitative', 'Mensuration',           15, 0.80, NULL),
  ('quant_simplification',      'quantitative', 'Simplification',        16, 0.50, NULL),
  ('quant_pipes_cisterns',      'quantitative', 'Pipes & Cisterns',      17, 0.70, NULL),
  ('quant_boats_streams',       'quantitative', 'Boats & Streams',       18, 0.70, NULL),
  ('quant_ages',                'quantitative', 'Problems on Ages',      19, 0.50, NULL),
  ('quant_lcm_hcf',             'quantitative', 'LCM & HCF',             20, 0.50, NULL),
  -- Logical
  ('logical_direction',         'logical', 'Direction Sense',            7, 0.60, NULL),
  ('logical_analogy',           'logical', 'Analogy',                    8, 0.60, NULL),
  ('logical_classification',    'logical', 'Odd One Out',                9, 0.50, NULL),
  ('logical_venn',              'logical', 'Venn Diagrams',             10, 0.60, NULL),
  ('logical_calendar_clock',    'logical', 'Calendars & Clocks',        11, 0.70, NULL),
  ('logical_input_output',      'logical', 'Input-Output',              12, 0.80, NULL),
  ('logical_statement',         'logical', 'Statements & Assumptions',  13, 0.80, NULL),
  ('logical_nonverbal',         'logical', 'Non-Verbal Reasoning',      14, 0.70, NULL),
  ('logical_data_sufficiency',  'logical', 'Data Sufficiency',          15, 0.80, NULL),
  -- Verbal
  ('verbal_idioms',             'verbal', 'Idioms & Phrases',            6, 0.60, NULL),
  ('verbal_synonyms',           'verbal', 'Synonyms & Antonyms',         7, 0.50, NULL),
  ('verbal_one_word',           'verbal', 'One-Word Substitution',       8, 0.60, NULL),
  ('verbal_cloze',              'verbal', 'Cloze Test',                  9, 0.60, NULL),
  ('verbal_spelling',           'verbal', 'Spelling',                   10, 0.50, NULL),
  ('verbal_critical_reasoning', 'verbal', 'Critical Reasoning',         11, 0.80, NULL),
  -- Computer Science
  ('cs_web',                    'computer_science', 'Web Development',   7, 0.60, NULL),
  ('cs_ml',                     'computer_science', 'Machine Learning',  8, 0.80, NULL),
  ('cs_cloud',                  'computer_science', 'Cloud & DevOps',    9, 0.70, NULL),
  ('cs_security',               'computer_science', 'Cybersecurity',    10, 0.80, NULL),
  ('cs_coa',                    'computer_science', 'Computer Organization', 11, 0.80, NULL),
  ('cs_toc',                    'computer_science', 'Theory of Computation', 12, 0.90, NULL),
  ('cs_compilers',              'computer_science', 'Compilers',        13, 0.90, NULL),
  ('cs_software_eng',           'computer_science', 'Software Engineering', 14, 0.60, NULL),
  ('cs_python',                 'computer_science', 'Python',           15, 0.60, NULL),
  ('cs_java',                   'computer_science', 'Java',             16, 0.60, NULL),
  ('cs_c_cpp',                  'computer_science', 'C / C++',          17, 0.70, NULL),
  ('cs_sql',                    'computer_science', 'SQL',              18, 0.60, NULL),
  -- Corporate
  ('corp_data_privacy',         'corporate', 'Data Privacy',            11, 0.70, NULL),
  ('corp_anti_bribery',         'corporate', 'Anti-Bribery & Ethics',   12, 0.60, NULL),
  ('corp_dei',                  'corporate', 'Diversity & Inclusion',   13, 0.50, NULL),
  ('corp_project_mgmt',         'corporate', 'Project Management',      14, 0.60, NULL),
  ('corp_it_tools',             'corporate', 'Workplace Tools',         15, 0.50, NULL),
  ('corp_communication',        'corporate', 'Business Communication',  16, 0.50, NULL),
  -- Entertainment
  ('ent_bollywood',             'entertainment', 'Bollywood',           11, 0.50, NULL),
  ('ent_hollywood',             'entertainment', 'Hollywood',           12, 0.50, NULL),
  ('ent_regional_cinema',       'entertainment', 'Regional Cinema',     13, 0.60, NULL),
  ('ent_web_series',            'entertainment', 'Web Series',          14, 0.50, NULL),
  ('ent_cartoons',              'entertainment', 'Cartoons & Animation',15, 0.40, NULL),
  ('ent_superheroes',           'entertainment', 'Superheroes',         16, 0.50, NULL),
  ('ent_celebrities',           'entertainment', 'Celebrities',         17, 0.40, NULL),
  ('ent_musicals_theatre',      'entertainment', 'Musicals & Theatre',  18, 0.60, NULL),
  ('ent_kpop',                  'entertainment', 'K-Pop & K-Drama',     19, 0.50, 'musician'),
  ('ent_poetry',                'entertainment', 'Poetry',              20, 0.70, 'author'),
  -- Science
  ('science_earth',             'science', 'Earth Science & Geology',    6, 0.60, NULL),
  ('science_genetics',          'science', 'Genetics',                   7, 0.80, NULL),
  ('science_botany',            'science', 'Botany',                     8, 0.70, 'naturalist'),
  ('science_zoology',           'science', 'Zoology',                    9, 0.70, 'naturalist'),
  ('science_weather',           'science', 'Weather & Climate',         10, 0.60, NULL),
  ('science_inventions',        'science', 'Inventions & Discoveries',  11, 0.50, NULL),
  ('science_scientists',        'science', 'Famous Scientists',         12, 0.50, NULL),
  -- Social studies
  ('social_polity',             'social_studies', 'Indian Polity',       5, 0.70, 'judge'),
  ('social_ancient_history',    'social_studies', 'Ancient History',     6, 0.70, NULL),
  ('social_medieval_history',   'social_studies', 'Medieval History',    7, 0.70, NULL),
  ('social_modern_history',     'social_studies', 'Modern History',      8, 0.70, NULL),
  ('social_world_history',      'social_studies', 'World History',       9, 0.70, NULL),
  ('social_art_culture',        'social_studies', 'Indian Art & Culture',10, 0.70, NULL),
  ('social_intl_relations',     'social_studies', 'International Relations', 11, 0.80, 'journalist'),
  ('social_psychology',         'social_studies', 'Psychology',         12, 0.70, NULL),
  ('social_sociology',          'social_studies', 'Sociology',          13, 0.60, NULL),
  ('social_flags_capitals',     'social_studies', 'Flags & Capitals',   14, 0.50, 'explorer'),
  -- Current affairs
  ('current_science_env',       'current_affairs', 'Science & Environment', 5, 0.60, NULL),
  ('current_awards',            'current_affairs', 'Awards & Honours',   6, 0.50, NULL),
  ('current_economy',           'current_affairs', 'Economy & Budget',   7, 0.60, NULL),
  ('current_govt_schemes',      'current_affairs', 'Government Schemes', 8, 0.60, NULL),
  ('current_defence',           'current_affairs', 'Defence',            9, 0.60, NULL),
  ('current_appointments',      'current_affairs', 'Appointments',      10, 0.50, NULL),
  -- Language learning
  ('lang_spanish',              'language_learning', 'Spanish',          4, 0.50, NULL),
  ('lang_french',               'language_learning', 'French',           5, 0.50, NULL),
  ('lang_german',               'language_learning', 'German',           6, 0.50, NULL),
  ('lang_japanese',             'language_learning', 'Japanese',         7, 0.60, NULL),
  ('lang_korean',               'language_learning', 'Korean',           8, 0.60, NULL),
  ('lang_mandarin',             'language_learning', 'Mandarin',         9, 0.70, NULL),
  ('lang_arabic',               'language_learning', 'Arabic',          10, 0.70, NULL),
  ('lang_sanskrit',             'language_learning', 'Sanskrit',        11, 0.70, NULL),
  ('lang_tamil',                'language_learning', 'Tamil',           12, 0.50, NULL),
  ('lang_telugu',               'language_learning', 'Telugu',          13, 0.50, NULL),
  ('lang_bengali',              'language_learning', 'Bengali',         14, 0.50, NULL),
  ('lang_gujarati',             'language_learning', 'Gujarati',        15, 0.50, NULL),
  ('lang_kannada',              'language_learning', 'Kannada',         16, 0.50, NULL),
  ('lang_malayalam',            'language_learning', 'Malayalam',       17, 0.50, NULL),
  ('lang_punjabi',              'language_learning', 'Punjabi',         18, 0.50, NULL),
  ('lang_urdu',                 'language_learning', 'Urdu',            19, 0.50, NULL),
  -- Mathematics (beyond aptitude arithmetic)
  ('math_calculus',             'mathematics', 'Calculus',               1, 0.80, NULL),
  ('math_linear_algebra',       'mathematics', 'Linear Algebra',         2, 0.80, NULL),
  ('math_trigonometry',         'mathematics', 'Trigonometry',           3, 0.70, NULL),
  ('math_coordinate_geometry',  'mathematics', 'Coordinate Geometry',    4, 0.70, NULL),
  ('math_statistics',           'mathematics', 'Statistics',             5, 0.70, NULL),
  ('math_number_theory',        'mathematics', 'Number Theory',          6, 0.90, NULL),
  ('math_discrete',             'mathematics', 'Discrete Mathematics',   7, 0.80, NULL),
  ('math_combinatorics',        'mathematics', 'Combinatorics',          8, 0.90, NULL),
  ('math_mental',               'mathematics', 'Mental Math',            9, 0.50, NULL),
  ('math_olympiad',             'mathematics', 'Olympiad Problems',     10, 1.00, NULL),
  -- Technology
  ('tech_gadgets',              'technology', 'Gadgets',                 1, 0.50, NULL),
  ('tech_ai',                   'technology', 'Artificial Intelligence', 2, 0.70, NULL),
  ('tech_internet',             'technology', 'Internet & Social Media', 3, 0.40, NULL),
  ('tech_companies',            'technology', 'Tech Companies',          4, 0.50, NULL),
  ('tech_history',              'technology', 'History of Computing',    5, 0.60, NULL),
  ('tech_crypto',               'technology', 'Blockchain & Crypto',     6, 0.70, NULL),
  ('tech_space',                'technology', 'Space Technology',        7, 0.70, 'astronaut'),
  -- Engineering (GATE streams)
  ('eng_mechanical',            'engineering', 'Mechanical',             1, 0.80, NULL),
  ('eng_electrical',            'engineering', 'Electrical',             2, 0.80, NULL),
  ('eng_electronics',           'engineering', 'Electronics & Comm.',    3, 0.80, NULL),
  ('eng_civil',                 'engineering', 'Civil',                  4, 0.80, NULL),
  ('eng_chemical',              'engineering', 'Chemical',               5, 0.80, NULL),
  ('eng_aerospace',             'engineering', 'Aerospace',              6, 0.90, NULL),
  ('eng_instrumentation',       'engineering', 'Instrumentation',        7, 0.80, NULL),
  ('eng_robotics',              'engineering', 'Robotics',               8, 0.80, NULL),
  ('eng_math',                  'engineering', 'Engineering Mathematics',9, 0.90, 'mathematician'),
  -- Business & finance
  ('biz_accounting',            'business_finance', 'Accounting',        1, 0.70, NULL),
  ('biz_finance',               'business_finance', 'Corporate Finance', 2, 0.80, NULL),
  ('biz_marketing',             'business_finance', 'Marketing',         3, 0.60, NULL),
  ('biz_entrepreneurship',      'business_finance', 'Entrepreneurship',  4, 0.60, NULL),
  ('biz_stock_market',          'business_finance', 'Stock Market',      5, 0.70, NULL),
  ('biz_banking_insurance',     'business_finance', 'Banking & Insurance', 6, 0.60, NULL),
  ('biz_taxation',              'business_finance', 'Taxation',          7, 0.80, NULL),
  ('biz_brands',                'business_finance', 'Brands & Companies',8, 0.50, NULL),
  ('biz_personal_finance',      'business_finance', 'Personal Finance',  9, 0.50, NULL),
  -- Law
  ('law_constitution',          'law', 'Constitutional Law',             1, 0.70, NULL),
  ('law_criminal',              'law', 'Criminal Law',                   2, 0.80, NULL),
  ('law_contract',              'law', 'Contract Law',                   3, 0.80, NULL),
  ('law_ip',                    'law', 'Intellectual Property',          4, 0.80, NULL),
  ('law_international',         'law', 'International Law',              5, 0.80, NULL),
  ('law_cyber',                 'law', 'Cyber Law',                      6, 0.70, NULL),
  ('law_consumer',              'law', 'Consumer Rights',                7, 0.50, NULL),
  ('law_legal_reasoning',       'law', 'Legal Reasoning',                8, 0.70, NULL),
  -- Health & medicine
  ('health_anatomy',            'health_medicine', 'Anatomy',            1, 0.80, NULL),
  ('health_physiology',         'health_medicine', 'Physiology',         2, 0.80, NULL),
  ('health_pharmacology',       'health_medicine', 'Pharmacology',       3, 0.90, NULL),
  ('health_diseases',           'health_medicine', 'Diseases',           4, 0.70, NULL),
  ('health_first_aid',          'health_medicine', 'First Aid',          5, 0.50, NULL),
  ('health_nutrition',          'health_medicine', 'Nutrition & Fitness',6, 0.50, NULL),
  ('health_mental',             'health_medicine', 'Mental Health',      7, 0.60, NULL),
  ('health_public',             'health_medicine', 'Public Health',      8, 0.60, NULL),
  ('health_yoga',               'health_medicine', 'Yoga & Ayurveda',    9, 0.50, NULL),
  -- Nature & animals
  ('nature_animals',            'nature', 'Animals',                     1, 0.50, NULL),
  ('nature_birds',              'nature', 'Birds',                       2, 0.60, NULL),
  ('nature_marine',             'nature', 'Marine Life',                 3, 0.60, NULL),
  ('nature_insects',            'nature', 'Insects',                     4, 0.60, NULL),
  ('nature_plants',             'nature', 'Plants & Trees',              5, 0.60, NULL),
  ('nature_dinosaurs',          'nature', 'Dinosaurs',                   6, 0.60, NULL),
  ('nature_conservation',       'nature', 'Wildlife & Conservation',     7, 0.60, NULL),
  -- Sports
  ('sport_cricket',             'sports', 'Cricket',                     1, 0.50, NULL),
  ('sport_football',            'sports', 'Football',                    2, 0.50, NULL),
  ('sport_tennis',              'sports', 'Tennis',                      3, 0.50, NULL),
  ('sport_badminton',           'sports', 'Badminton',                   4, 0.50, NULL),
  ('sport_hockey',              'sports', 'Hockey',                      5, 0.50, NULL),
  ('sport_kabaddi',             'sports', 'Kabaddi',                     6, 0.50, NULL),
  ('sport_basketball',          'sports', 'Basketball',                  7, 0.50, NULL),
  ('sport_olympics',            'sports', 'Olympics',                    8, 0.60, NULL),
  ('sport_athletics',           'sports', 'Athletics',                   9, 0.60, NULL),
  ('sport_combat',              'sports', 'Combat Sports',              10, 0.60, NULL),
  ('sport_motorsport',          'sports', 'Formula 1 & Motorsport',     11, 0.60, NULL),
  ('sport_general',             'sports', 'General Sports',             12, 0.50, NULL),
  -- Arts & culture
  ('arts_painting',             'arts', 'Painting',                      1, 0.60, NULL),
  ('arts_sculpture',            'arts', 'Sculpture',                     2, 0.70, NULL),
  ('arts_architecture',         'arts', 'Architecture',                  3, 0.70, NULL),
  ('arts_photography',          'arts', 'Photography',                   4, 0.60, NULL),
  ('arts_design',               'arts', 'Graphic Design',                5, 0.60, NULL),
  ('arts_dance',                'arts', 'Dance',                         6, 0.50, NULL),
  ('arts_indian_classical',     'arts', 'Indian Classical Arts',         7, 0.70, NULL),
  ('arts_music_theory',         'arts', 'Music Theory',                  8, 0.80, 'musician'),
  ('arts_instruments',          'arts', 'Musical Instruments',           9, 0.60, 'musician'),
  -- Food & drink
  ('food_world_cuisine',        'food_drink', 'World Cuisine',           1, 0.50, NULL),
  ('food_indian_cuisine',       'food_drink', 'Indian Cuisine',          2, 0.50, NULL),
  ('food_baking',               'food_drink', 'Baking & Desserts',       3, 0.60, NULL),
  ('food_spices',               'food_drink', 'Spices & Ingredients',    4, 0.50, NULL),
  ('food_beverages',            'food_drink', 'Beverages',               5, 0.50, NULL),
  ('food_science',              'food_drink', 'Food Science',            6, 0.70, NULL),
  -- Vehicles & transport
  ('transport_cars',            'transport', 'Cars',                     1, 0.60, NULL),
  ('transport_bikes',           'transport', 'Motorcycles',              2, 0.50, NULL),
  ('transport_aviation',        'transport', 'Aviation',                 3, 0.70, NULL),
  ('transport_railways',        'transport', 'Railways',                 4, 0.60, NULL),
  ('transport_ships',           'transport', 'Ships & Navigation',       5, 0.70, NULL),
  -- Philosophy & religion
  ('phil_western',              'philosophy_religion', 'Western Philosophy', 1, 0.80, NULL),
  ('phil_eastern',              'philosophy_religion', 'Eastern Philosophy', 2, 0.80, NULL),
  ('phil_ethics',               'philosophy_religion', 'Ethics',         3, 0.70, NULL),
  ('phil_logic',                'philosophy_religion', 'Formal Logic',   4, 0.80, NULL),
  ('rel_world',                 'philosophy_religion', 'World Religions',5, 0.60, NULL),
  ('rel_hinduism',              'philosophy_religion', 'Hinduism',       6, 0.60, NULL),
  ('rel_buddhism_jainism',      'philosophy_religion', 'Buddhism & Jainism', 7, 0.60, NULL),
  ('rel_sikhism',               'philosophy_religion', 'Sikhism',        8, 0.60, NULL),
  ('rel_abrahamic',             'philosophy_religion', 'Abrahamic Religions', 9, 0.60, NULL),
  ('rel_festivals',             'philosophy_religion', 'Festivals',     10, 0.40, NULL),
  -- Games & puzzles
  ('games_chess',               'games_puzzles', 'Chess',                1, 0.80, NULL),
  ('games_board',               'games_puzzles', 'Board Games',          2, 0.50, NULL),
  ('games_card',                'games_puzzles', 'Card Games',           3, 0.50, NULL),
  ('games_crosswords',          'games_puzzles', 'Crosswords & Word Games', 4, 0.60, NULL),
  ('games_riddles',             'games_puzzles', 'Riddles',              5, 0.50, NULL),
  ('games_number_puzzles',      'games_puzzles', 'Sudoku & Number Puzzles', 6, 0.70, NULL),
  ('games_esports',             'games_puzzles', 'Esports',              7, 0.50, 'gamer'),
  -- Lifestyle
  ('life_fashion',              'lifestyle', 'Fashion',                  1, 0.50, NULL),
  ('life_travel',               'lifestyle', 'Travel',                   2, 0.50, 'explorer'),
  ('life_brands_logos',         'lifestyle', 'Brands & Logos',           3, 0.40, NULL),
  ('life_home_garden',          'lifestyle', 'Home & Garden',            4, 0.50, NULL),
  ('life_social_media',         'lifestyle', 'Influencers & Trends',     5, 0.40, NULL),
  -- Exam prep (mixed-syllabus mock tests)
  ('exam_upsc',                 'exam_prep', 'UPSC',                     1, 1.00, NULL),
  ('exam_state_psc',            'exam_prep', 'State PSC',                2, 0.80, NULL),
  ('exam_ssc',                  'exam_prep', 'SSC',                      3, 0.70, NULL),
  ('exam_banking',              'exam_prep', 'Banking (IBPS / SBI)',     4, 0.70, NULL),
  ('exam_railways',             'exam_prep', 'Railways (RRB)',           5, 0.60, NULL),
  ('exam_defence',              'exam_prep', 'NDA / CDS',                6, 0.80, NULL),
  ('exam_jee',                  'exam_prep', 'JEE',                      7, 1.00, NULL),
  ('exam_neet',                 'exam_prep', 'NEET',                     8, 0.90, NULL),
  ('exam_cuet',                 'exam_prep', 'CUET',                     9, 0.70, NULL),
  ('exam_cat',                  'exam_prep', 'CAT',                     10, 0.90, NULL),
  ('exam_gate',                 'exam_prep', 'GATE',                    11, 0.90, NULL),
  ('exam_clat',                 'exam_prep', 'CLAT',                    12, 0.80, NULL),
  ('exam_gre_gmat',             'exam_prep', 'GRE / GMAT',              13, 0.90, NULL),
  ('exam_tet',                  'exam_prep', 'Teaching (CTET / TET)',   14, 0.60, NULL)
ON CONFLICT (slug) DO NOTHING;

-- ── Persona overrides on pre-existing subdomains ───────────────────────────
UPDATE subdomains SET persona = v.persona FROM (VALUES
  ('science_space',    'astronaut'),
  ('social_geography', 'explorer'),
  ('social_civics',    'judge'),
  ('social_economics', 'executive'),
  ('current_sports',   'athlete'),
  ('ent_novels',       'author'),
  ('ent_comics',       'author'),
  ('ent_music',        'musician'),
  ('ent_games',        'gamer'),
  ('ent_mythology',    'philosopher'),
  ('logical_puzzles',  'grandmaster')
) AS v(slug, persona) WHERE subdomains.slug = v.slug AND subdomains.persona IS NULL;
