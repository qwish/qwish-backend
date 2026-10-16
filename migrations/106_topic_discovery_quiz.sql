-- Migration 106: Topic & Sub-Topic Discovery quiz, the first-run sampler.
--
-- Ten questions with no right answer. Q1-Q5 score the 12 topics, Q6-Q10 pick
-- sub-topics; the scoring table lives in internal/domain/onboardingsession
-- (profileQuiz), keyed by question position and option order, so edit the two
-- together. The quiz is a draft and not a demo, which keeps it out of browse,
-- the /demo list and recommendations; the sampler serves it by this fixed id.
-- correct_answer is NOT NULL, so it holds an empty string.

INSERT INTO quizzes (id, created_by, title, description, type, visibility, status, question_count)
VALUES (
  'b0a7d1e5-0000-4000-8000-000000000106',
  '00000000-0000-0000-0000-000000000001',
  'Topic & Sub-Topic Discovery',
  'Ten quick questions that pick the topics and sub-topics to start you on.',
  'knowledge_check', 'public', 'draft', 10
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO questions (quiz_id, position, type, prompt, options, correct_answer, time_limit_seconds)
SELECT 'b0a7d1e5-0000-4000-8000-000000000106', v.position, 'multiple_choice', v.prompt, v.options::jsonb, '""', 60
FROM (VALUES
  (1, $$What is your main goal right now?$$, $$[
    "Cracking placement tests, competitive exams, or sharpening my problem-solving speed.",
    "Mastering tech skills and understanding how software and hardware systems work.",
    "Excelling at work, understanding business dynamics, and improving professional communication.",
    "Exploring how the world works—from history and global news to science and nature.",
    "Unwinding with pop culture, stories, trivia, or learning a new language for fun."]$$),
  (2, $$Which type of challenge feels the most satisfying to solve?$$, $$[
    "Crunching numbers, spotting trends in charts, or calculating probabilities.",
    "Untangling a complex brainteaser, seating arrangement, or secret code.",
    "Debugging broken code, structuring a database, or optimizing an algorithm.",
    "Finding the exact right words, fixing flawed sentences, or mastering a new language.",
    "Navigating a tricky workplace scenario, pitching a product, or leading a team."]$$),
  (3, $$When you open your feed or watch videos, what topic makes you stop scrolling?$$, $$[
    "Breaking news, geopolitics, market trends, or live sports updates.",
    "Space exploration, physics phenomena, wildlife, or how the human body works.",
    "Deep dives into movies, anime, video games, books, or ancient mythology.",
    "Stories of ancient empires, world geography, or how laws and societies evolved.",
    "A random mix of everything—I love bite-sized trivia across all subjects."]$$),
  (4, $$Imagine you are joining a high-stakes competition. What role do you take on the team?$$, $$[
    "The Strategist: Handling the math, logic puzzles, and analytical data.",
    "The Architect: Building the technical system, network, and code infrastructure.",
    "The Spokesperson: Crafting the pitch, editing the copy, and speaking in multiple languages.",
    "The Operations Lead: Ensuring security, compliance, team culture, and customer success.",
    "The Lore Master: Answering all the wild-card questions on history, science, pop culture, and news."]$$),
  (5, $$How do you prefer your mental workouts structured?$$, $$[
    "Timed, analytical, and objective—give me equations, charts, and logic rules.",
    "Practical and career-focused—give me real-world tech problems or workplace scenarios.",
    "Language-driven—help me read faster, write sharper, and converse fluently.",
    "Fact-driven and informative—test my awareness of the real world, past and present.",
    "Fun and story-driven—test me on fandoms, music, movies, and general trivia."]$$),
  (6, $$When dealing with numbers, logic, and data, which specific area do you want to tackle first?$$, $$[
    "Speed Math & Word Problems: Percentages, Time-Speed-Work, Arithmetic, and Number Systems.",
    "Higher Math: Algebra, Geometry, and Probability.",
    "Charts & Analytics: Extracting insights from tables, graphs, and mixed aptitude sets.",
    "Arrangements & Deduction: Seating Arrangements, Puzzles, Syllogisms, and Blood Relations.",
    "Patterns & Ciphers: Number/Letter Series and Coding-Decoding."]$$),
  (7, $$In technology and science, which domain fascinates you the most?$$, $$[
    "Core Coding & Problem Solving: Programming languages, OOP, and Data Structures & Algorithms (DSA).",
    "Systems & Infrastructure: Databases (DBMS), Operating Systems (OS), and Computer Networks.",
    "The Universe & Matter: Physics, Chemistry, and Space Science.",
    "Life & Planet Earth: Biology and Environmental Science.",
    "Skip / Not my primary focus."]$$),
  (8, $$For language and communication, what would you like to sharpen?$$, $$[
    "English Mechanics: Grammar, Vocabulary, and Sentence Correction.",
    "Reading & Logic Flow: Reading Comprehension and Para Jumbles.",
    "English Fluency: Everyday and professional English language mastery.",
    "Hindi: Vocabulary, grammar, and comprehension in Hindi.",
    "Marathi: Vocabulary, grammar, and comprehension in Marathi."]$$),
  (9, $$In the professional world and global affairs, what matters most to you right now?$$, $$[
    "Workplace Foundations: Onboarding, Company Policies, HR & Culture, and Health & Safety.",
    "Risk & Governance: Security Awareness and Compliance.",
    "Business Growth: Leadership, Sales Enablement, Customer Service, and Product Knowledge.",
    "Today’s Headlines: India & World News, Business & Tech updates, and Sports.",
    "Foundations of Society: History, Geography, Civics, and Economics."]$$),
  (10, $$When you just want to have fun with a quiz, what’s your go-to vibe?$$, $$[
    "Screen & Sound: Movies, TV Shows, and Music.",
    "Geek Culture & Gaming: Anime & Manga, Video Games, Comics, and Sci-Fi & Fantasy.",
    "Stories & Legends: Novels & Literature, and Mythology & Folklore.",
    "The Ultimate Mix: Surprise me with General Trivia and Mixed topics from everywhere!"]$$)
) AS v(position, prompt, options)
WHERE NOT EXISTS (
  SELECT 1 FROM questions WHERE quiz_id = 'b0a7d1e5-0000-4000-8000-000000000106'
);
