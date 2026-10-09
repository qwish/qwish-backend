-- Close remaining gaps on application tables in public, including the migration
-- ledger. Extension-owned tables are excluded. Existing policies stay intact;
-- newly protected tables have no client policies, matching 033 and the backend's
-- owner/service_role access model. Do not FORCE RLS: the backend must bypass it.
DO $$
DECLARE
  target RECORD;
BEGIN
  FOR target IN
    SELECT c.oid, n.nspname, c.relname
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'public'
      AND c.relkind IN ('r', 'p')
      AND NOT c.relrowsecurity
      AND NOT EXISTS (
        SELECT 1 FROM pg_depend d
        WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid
          AND d.deptype = 'e'
      )
    ORDER BY c.relname
  LOOP
    EXECUTE format('ALTER TABLE %I.%I ENABLE ROW LEVEL SECURITY', target.nspname, target.relname);
    EXECUTE format('REVOKE ALL ON TABLE %I.%I FROM anon, authenticated', target.nspname, target.relname);
  END LOOP;
END;
$$;

-- Cover every FK with the FK columns as the leading keys of a valid, full
-- B-tree index. Primary/unique indexes already count; INCLUDE columns, partial
-- indexes and expression keys do not provide general FK coverage. Inspect the
-- catalog inside the loop so duplicate constraints reuse the index just added.
-- Regular CREATE INDEX is intentional: the runner executes each migration in
-- a transaction. On large tables this can block writes until migration commit.
DO $$
DECLARE
  target RECORD;
  columns_sql TEXT;
BEGIN
  FOR target IN
    SELECT con.oid, con.conrelid, con.conkey, con.conname, n.nspname, c.relname
    FROM pg_constraint con
    JOIN pg_class c ON c.oid = con.conrelid
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE con.contype = 'f' AND n.nspname = 'public'
      AND c.relkind IN ('r', 'p')
      AND NOT EXISTS (
        SELECT 1 FROM pg_depend d
        WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid
          AND d.deptype = 'e'
      )
    ORDER BY c.relname, con.conname
  LOOP
    IF EXISTS (
      SELECT 1
      FROM pg_index i
      JOIN pg_class ic ON ic.oid = i.indexrelid
      JOIN pg_am am ON am.oid = ic.relam
      WHERE i.indrelid = target.conrelid
        AND i.indisvalid AND i.indisready
        AND i.indpred IS NULL AND am.amname = 'btree'
        AND i.indnkeyatts >= cardinality(target.conkey)
        AND ARRAY(
          SELECT key.attnum FROM unnest(i.indkey) WITH ORDINALITY AS key(attnum, position)
          WHERE key.position <= cardinality(target.conkey)
          ORDER BY key.position
        ) = target.conkey
    ) THEN
      CONTINUE;
    END IF;

    SELECT string_agg(format('%I', a.attname), ', ' ORDER BY key.position)
    INTO columns_sql
    FROM unnest(target.conkey) WITH ORDINALITY AS key(attnum, position)
    JOIN pg_attribute a ON a.attrelid = target.conrelid AND a.attnum = key.attnum;

    -- A hash keeps names below PostgreSQL's 63-byte limit without truncation
    -- collisions. Coverage checks make rerunning the migration a no-op.
    EXECUTE format('CREATE INDEX %I ON %I.%I (%s)',
      'idx_fk_098_' || md5(target.nspname || '.' || target.relname || '.' || target.conname),
      target.nspname, target.relname, columns_sql);
  END LOOP;
END;
$$;
