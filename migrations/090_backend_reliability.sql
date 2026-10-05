CREATE TABLE background_jobs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 kind text NOT NULL, dedupe_key text NOT NULL, owner_id text,
 payload jsonb NOT NULL, result jsonb,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','running','completed','failed')),
 attempts int NOT NULL DEFAULT 0, max_attempts int NOT NULL DEFAULT 8,
 available_at timestamptz NOT NULL DEFAULT now(), lease_until timestamptz, lease_token uuid,
 last_error text, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(kind,dedupe_key)
);
ALTER TABLE background_jobs ENABLE ROW LEVEL SECURITY;
CREATE INDEX background_jobs_pending ON background_jobs(available_at,id) WHERE state='pending';
CREATE INDEX background_jobs_leases ON background_jobs(lease_until) WHERE state='running';
CREATE INDEX background_jobs_owner ON background_jobs(owner_id,created_at DESC);
ALTER TABLE quiz_attempts ADD COLUMN completion_response jsonb;

CREATE TABLE distributed_rate_limits (
 namespace text NOT NULL, client_key text NOT NULL, tat timestamptz NOT NULL,
 PRIMARY KEY(namespace,client_key)
);
ALTER TABLE distributed_rate_limits ENABLE ROW LEVEL SECURITY;
CREATE INDEX distributed_rate_limits_expiry ON distributed_rate_limits(tat);

-- GCRA is atomic across API replicas; rejected calls do not extend the pause.
CREATE FUNCTION consume_rate_limit(ns text,k text,budget int,window_seconds double precision)
RETURNS TABLE(allowed boolean,retry_seconds double precision,remaining int)
LANGUAGE plpgsql SET search_path=public AS $$
DECLARE t timestamptz; n timestamptz:=clock_timestamp(); step interval; burst interval;
BEGIN
 IF budget<=0 OR window_seconds<=0 THEN RETURN QUERY SELECT false,window_seconds,0;RETURN;END IF;
 step:=make_interval(secs=>window_seconds/budget);burst:=step*(budget-1);
 INSERT INTO distributed_rate_limits VALUES(ns,k,n) ON CONFLICT DO NOTHING;
 SELECT tat INTO t FROM distributed_rate_limits WHERE namespace=ns AND client_key=k FOR UPDATE;
 t:=GREATEST(t,n);
 IF n<t-burst THEN RETURN QUERY SELECT false,EXTRACT(epoch FROM t-burst-n)::double precision,0;RETURN;END IF;
 UPDATE distributed_rate_limits SET tat=t+step WHERE namespace=ns AND client_key=k;
 RETURN QUERY SELECT true,0::double precision,GREATEST(0,LEAST(budget-1,FLOOR(EXTRACT(epoch FROM n+burst-t-step)/EXTRACT(epoch FROM step))::int+1));
END $$;

-- Only small durable IDs travel through NOTIFY; SSE payloads are read from storage.
CREATE FUNCTION broadcast_user_notification() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN PERFORM pg_notify('qwish_notifications',NEW.id::text);RETURN NEW;END $$;
CREATE TRIGGER user_notification_broadcast AFTER INSERT ON user_notifications FOR EACH ROW EXECUTE FUNCTION broadcast_user_notification();

-- Every mutable read cache is invalidated on all replicas after a write commits.
CREATE FUNCTION broadcast_cache_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN PERFORM pg_notify('qwish_cache',TG_TABLE_NAME);RETURN NULL;END $$;
DO $$ DECLARE t record;BEGIN
 FOR t IN SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename NOT IN
 ('schema_migrations','background_jobs','distributed_rate_limits','notification_log','attempt_behavior_events','recommendation_bandit_stats') LOOP
 EXECUTE format('CREATE TRIGGER qwish_cache_change AFTER INSERT OR UPDATE OR DELETE ON %I FOR EACH STATEMENT EXECUTE FUNCTION broadcast_cache_change()',t.tablename);
 END LOOP;
END $$;

-- Do not run content-version insertion triggers for difficulty calibration.
CREATE OR REPLACE FUNCTION snapshot_question_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND NEW.revision=OLD.revision THEN RETURN NEW;END IF;
 INSERT INTO question_versions(question_id,revision,type,prompt,media_url,options,correct_answer,time_limit_seconds,clues)
 VALUES(NEW.id,NEW.revision,NEW.type,NEW.prompt,NEW.media_url,NEW.options,NEW.correct_answer,NEW.time_limit_seconds,NEW.clues)
 ON CONFLICT(question_id,revision) DO NOTHING;RETURN NEW;
END $$;
