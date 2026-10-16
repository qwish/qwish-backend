-- The API ignores qwish_cache notifications for activity tables
-- (middleware.activityTables): those reads expire through their TTL. The
-- triggers still fired a NOTIFY on every write statement, and a committing
-- transaction that has sent NOTIFY takes a database-wide lock, so every quiz
-- answer and completion serialized its commit behind every other one for a
-- message nobody acts on. Keep this list in step with activityTables.
--
-- DROP TRIGGER takes ACCESS EXCLUSIVE, held until this migration commits. If a
-- long query holds one of these hot tables, give up instead of stalling live
-- traffic queued behind the lock; the boot fails and the restart retries.
SET LOCAL lock_timeout = '3s';
DO $$ DECLARE t text;BEGIN
 FOREACH t IN ARRAY ARRAY[
  'quiz_attempts','quiz_attempt_questions','question_responses','clue_reveals',
  'learning_evidence','learning_evidence_misconceptions','leaderboard_scores',
  'learner_ratings','learner_topic_mastery','quiz_read_stats','attempt_daily_metrics',
  'points_ledger','streaks','badges','saved_quizzes','quiz_ratings',
  'learning_assignment_recipients','practice_sessions','questions','users',
  'admin_sessions','user_notifications','notification_preferences','device_tokens',
  'content_delivery_events','profile_views'] LOOP
  IF to_regclass(t) IS NOT NULL THEN
   EXECUTE format('DROP TRIGGER IF EXISTS qwish_cache_change ON %I',t);
  END IF;
 END LOOP;
END $$;
