-- =============================================
-- Supabase linter WARN cleanup:
--   0011 function_search_path_mutable  — update_updated_at
--   0028/0029 *_security_definer_function_executable — the 004/016 auth helpers
-- Idempotent.
-- =============================================

-- Trigger function: pin search_path so a role-level search_path can't shadow it.
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER
LANGUAGE plpgsql
SET search_path = public
AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$;

-- The SECURITY DEFINER auth helpers exist only to be called from RLS policy
-- expressions, never over PostgREST. No frontend calls /rest/v1/rpc/* (all data
-- access goes through the Go backend), so drop EXECUTE for the API roles and
-- leave it with service_role + the owner.
--
-- Consequence: if a Supabase-client session ever hits a policy that calls one of
-- these, it errors with "permission denied for function" instead of returning
-- rows. That is the intended posture — grant EXECUTE back to `authenticated` for
-- the specific helper if a frontend genuinely needs PostgREST reads.
DO $$
DECLARE fn TEXT;
BEGIN
  FOREACH fn IN ARRAY ARRAY[
    'auth_user_id()',
    'auth_user_role()',
    'auth_admin_role()',
    'auth_institution_id()',
    'is_admin()',
    'is_super_admin()',
    'is_study_group_member(uuid)'
  ] LOOP
    EXECUTE format('REVOKE ALL ON FUNCTION public.%s FROM PUBLIC, anon, authenticated', fn);
    EXECUTE format('GRANT EXECUTE ON FUNCTION public.%s TO service_role', fn);
  END LOOP;
END $$;
