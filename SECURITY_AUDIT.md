# Qwish backend security audit

Date: 2026-10-05. Scope: current working tree, including the recently implemented caching and workers.

This is a static source/configuration/migration review. No application code was changed, live endpoints attacked, or production database accessed. Findings 1–7 follow directly from code paths; 8–9 identify migration exposure whose live reachability requires checking Supabase Data API configuration and database grants. Severity reflects impact and prerequisites, not a claimed successful production exploit. Proposed code below is illustrative; helper functions and schema changes named in proposed designs still need implementation.

## Authorization remediation

Implemented after this review: findings 1–3 are addressed in the working tree. Authenticate now enforces live admin revocation/passkey policy using verified claims, rejects ordinary URL-token authentication, requires tracked admin identities, and blocks inactive teachers. Teacher passkey issuance/refresh also checks status. Admin refresh and login policy queries no longer treat lookup failures as permission to proceed. A failed admin refresh cannot fall back through a legacy users identity to replace its revoked session ID. Conditional invite activation cannot reactivate an account suspended concurrently. First-passkey bootstrap sessions are restricted to enrollment/sign-out routes; the store-review account is restricted to active learners. The notification-stream URL-token exception remains for EventSource compatibility and still runs all live checks; credential exposure through URLs remains a reason to prefer fetch streaming.

The original findings below document the reviewed state. The other findings remain outstanding. The authorization changes were compiled with `go build ./...`; security regression tests have not been run in this remediation turn.

Second remediation (same day): findings 4–9 and the JWT/forwarded-IP items are addressed in the working tree.
- 4: presigned PUTs require `size` (≤5 MiB), signed as Content-Length; prefix is allowlisted; multipart uploads are type-checked by content sniffing; upload routes are rate limited per user (30/min). Per-user storage quotas and quarantine are not implemented.
- 5: global `LimitBody` middleware (3 MiB JSON, 6 MiB multipart) under the existing per-route limits; UpdateMe capped at 16 KiB.
- 6: atomic first-claim (`parent_id IS NULL`), parent-role check, 128-bit `crypto/rand.Text` codes with 7-day expiry, 10/hour claim limit, NULL instead of the zero-UUID sentinel; acceptance requires a claimed link. Old short pending codes are revoked by migration 094.
- 7: offline pack downloads are recorded in `offline_answer_exposures`; online completion of an exposed quiz awards no points and applies no rating observations.
- 8/9: migration 094 revokes anon/authenticated access to badges, user_education, user_skills and EXECUTE on refresh_leaderboard_score.
- JWTs now require `exp` and `aud=authenticated`; X-Forwarded-For/X-Real-IP are trusted only from loopback/private peers.
Verified: migrations 001–094 apply on scratch Postgres; `go test ./...` passes; admin `TestAdminSessionRevocation` now signs a real token and runs it through Authenticate.

## High

### 1. Individual admin session revocation can be bypassed with a URL token

References: `internal/middleware/auth.go:49`, `internal/middleware/admin_session.go:24`, `internal/middleware/admin_session.go:65`.

Authenticate accepts either Authorization or `?token=...`. TrackAdminSessions reads the token only from Authorization and skips all session checks if no session ID is found. A holder of a revoked admin session's still-valid access token can supply it through the query string and reach admin handlers. Account-wide token-generation revocation is separate and still applies. Query tokens also risk leaking bearer credentials through URL logging.

Before:

```go
// Authenticate
 tokenStr = r.URL.Query().Get("token")
// TrackAdminSessions
 if adminID == "" || sid == "" { next.ServeHTTP(w, r); return }
```

After, required design:

```go
// Accept bearer credentials only through Authorization on normal API routes.
if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
 Unauthorized(w)
 return
}
// Authenticate stores verified session_id and amr in context.
// TrackAdminSessions reads those verified claims and rejects lookup failures.
```

For browser EventSource, use authenticated fetch streaming or a short-lived, stream-only ticket; never a general API JWT in the URL. Verify revoked sessions are denied with both header and attempted URL transport.

### 2. Pending teachers can access teacher API routes before verification

References: `internal/domain/auth/handler.go:173`, `internal/middleware/auth.go:154`, `cmd/api/main.go:740`.

The OTP handler blocks pending teachers, but Authenticate blocks only suspended users. Teacher routes require role=teacher without requiring active status. A newly created pending teacher already has a valid Supabase JWT from the signup flow; refusing tokens in a later login response cannot revoke that credential. This permits teacher operations such as authoring quizzes and obtaining upload URLs before institution approval. Class-specific handlers may impose further membership restrictions, so this is not a claim of unrestricted access to every student record.

Before:

```go
if u.Status == "suspended" { /* deny */ }
```

After:

```go
if u.Role == "teacher" && u.Status != "active" {
 Error(w, http.StatusForbidden, "PENDING_VERIFICATION", "teacher approval is required")
 return
}
```

Enforce the same rule on all teacher authentication paths, including passkey enrollment/login/refresh. If a pending teacher needs a status endpoint, expose that narrowly with its own policy.

### 3. Admin passkey requirement can be bypassed through direct Supabase login

References: `internal/domain/auth/handler.go:165`, `internal/domain/auth/handler.go:203`, `internal/domain/auth/passkey.go:864`, `internal/middleware/auth.go:44`.

The require_admin_passkeys policy is checked while returning a Qwish OTP login response. Neither Authenticate nor TrackAdminSessions enforces it on protected requests. An admin account holder, or an attacker who compromises its mailbox, can obtain an OTP session directly from Supabase and present that JWT to the Go API, bypassing the configured passkey requirement. Supabase exposes OTP sign-in and verification directly; see [signInWithOtp](https://supabase.com/docs/reference/javascript/auth-signinwithotp) and [verifyOtp](https://supabase.com/docs/reference/javascript/auth-verifyotp). Existing OTP sessions also remain accepted after the policy is enabled.

Before:

```go
// Only the OTP login handler applies this policy.
if h.svc.PasskeyRequiredForAdmin(ctx, uid) { /* refuse tokens */ }
```

After, required design:

```go
// In the authenticated admin boundary, after JWT verification:
required, err := loadAdminPasskeyPolicy(ctx, adminID)
if err != nil { /* return 503: fail closed */; return }
if required && !hasVerifiedAMR(claims, "webauthn") {
 Error(w, http.StatusForbidden, "PASSKEY_REQUIRED", "sign in with a passkey")
 return
}
```

The new policy loader must return errors instead of silently treating query failures as policy disabled. Preserve a narrowly scoped enrollment/bootstrap exception for administrators without enrolled credentials.

## Medium

### 4. Presigned uploads bypass the 5 MiB limit and lack quotas

References: `internal/storage/s3.go:107`, `internal/domain/upload/handler.go:28`, `cmd/api/main.go:844`.

Multipart upload has a 5 MiB request limit, but PresignPutObject sets no size bound. Upload routes have role checks but no per-user rate limit/storage quota. A permitted account can request many signed URLs and upload oversized objects directly to S3. The client also chooses the prefix, and image acceptance relies on declared MIME type rather than decoded file content. Primary impact is storage/bandwidth cost and unwanted content hosting, not demonstrated script execution.

Before:

```go
PutObjectInput{Bucket: ..., Key: ..., ContentType: ...}
```

After, required design:

```text
Issue signed POST policies with content-length-range [1, 5242880].
Generate keys server-side under <approved-kind>/<institution>/<user>/<uuid>.
Enforce per-user request and storage quotas before signing.
Upload into quarantine; decode/validate the image before publishing it.
```

AWS supports upload size conditions in [POST policies](https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sigv4-HTTPPOSTConstructPolicy.html). The actual bucket's independent restrictions were not inspected.

### 5. Unbounded request bodies allow memory exhaustion

References: `internal/domain/user/handler.go:49`, `internal/domain/auth/handler.go:410`, `internal/domain/offline/handler.go:40`.

UpdateMe calls io.ReadAll on an unrestricted body. Other routes decode unrestricted JSON, including public refresh and offline sync. Field and item-count checks occur after decoding. Server read timeouts restrict elapsed time, not allocated bytes; large concurrent requests can exhaust a small API container and disrupt all users.

Before:

```go
body, err := io.ReadAll(r.Body)
```

After:

```go
r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
body, err := io.ReadAll(r.Body)
```

Set suitable endpoint-specific bounds for JSON, imports, and multipart data; return 413 for oversized requests. Add public auth and resource-write rate limits, including refresh and uploads. Keep larger import limits explicit.

### 6. Parent-link claims can race student approval and change the approved parent

References: `internal/domain/parent/handler.go:58`, `internal/domain/parent/handler.go:68`, `internal/domain/parent/handler.go:82`.

Link first selects a pending link, then updates parent_id using only its ID. If the student approves between those operations, the later update can replace parent_id on the now-active link. Knowing a valid invite code is a prerequisite. Link also lacks a parent-role check, and there is no rate limit on the route. Generated invite codes use only eight UUID hex characters (32 bits) and have no expiration. This is a source-level concurrency/privacy issue; it was not exercised against live records. Separately, fresh invite generation uses an all-zero parent UUID despite a non-null parent foreign key, so successful generation depends on a sentinel row outside these migrations.

Before:

```sql
SELECT id FROM parent_student_links WHERE invite_code=$1 AND status='pending';
UPDATE parent_student_links SET parent_id=$1 WHERE id=$2;
```

After, required design:

```sql
UPDATE parent_student_links SET parent_id=$1
WHERE invite_code=$2 AND status='pending' AND parent_id IS NULL
  AND expires_at>now()
RETURNING id,student_id;
```

Use a nullable unclaimed parent field instead of a fake UUID; use a high-entropy expiring invite token. Require parent role. Student acceptance must bind an explicitly identified parent, and changing that identity must require new approval. Add attempts/issuance limits.

### 7. Offline answer keys can inflate online scores and rewards

References: `internal/domain/offline/service.go:108`, `internal/domain/attempt/service.go:808`, `internal/domain/attempt/service.go:817`.

Offline packs expose correct_answer for saved knowledge_check quizzes. Online completion of those same quizzes applies learner ratings and can award first-attempt points. A learner can save a quiz, download its answer key, then submit known-correct answers online. The offline comment that practice awards no points applies to offline sync, but does not protect the online scoring path. Repeated online knowledge checks suppress points but still call ApplyRatings.

Before:

```go
// Online completion applies ratings before considering repeat points.
scoreBefore, scoreAfter, err := scoring.ApplyRatings(ctx, tx, userID, ratingObs)
```

After, required design:

```text
Separate answer-visible practice banks from rated/rewarded assessment banks.
Track answer exposure by user and question version, including public review flows.
Apply rating/reward observations only when the assessment policy permits them.
Use fresh unseen versions/items for assessments following answer-visible practice.
```

Decide explicitly whether answer-visible practice should be rated. Merely disabling offline-sync rewards does not fix this online integrity gap.

## Deployment-dependent database exposure

### 8. High if reachable: permissive RLS bypasses profile privacy

References: `migrations/004_rls_policies.sql:258`, `migrations/004_rls_policies.sql:350`, `migrations/004_rls_policies.sql:355`, `internal/domain/user/service.go:162`.

Badges, user_education, and user_skills have authenticated SELECT policies USING(true). These policies do not check profile_private, recruiter visibility, owner, or viewer relationship. Revoking EXECUTE on RLS helper functions in migration 034 does not close these policies because they call no helpers. If those tables have authenticated SELECT grants and public is exposed through Supabase's Data API, any account can query them directly and scrape private users' education/skills/badges. Live grants and Data API status were not inspected, so production exposure is unconfirmed.

Before:

```sql
CREATE POLICY user_education_select ON user_education
FOR SELECT TO authenticated USING(true);
```

After, for the documented backend-only data path:

```sql
REVOKE SELECT ON user_education, user_skills, badges FROM anon, authenticated;
-- Preserve backend-role access. If direct client reads are required,
-- replace policies with owner/consent/viewer checks instead.
```

Audit all exposed tables and their actual grants; do not assume the Go privacy check protects direct database API reads. Supabase documents [Data API privileges and exposure](https://supabase.com/docs/guides/api/securing-your-api).

### 9. Medium if reachable: backend SECURITY DEFINER function retains public execution

References: `migrations/052_incremental_leaderboard.sql:11`, `migrations/072_learner_ratings.sql:18`.

refresh_leaderboard_score(uuid) runs with owner privileges, accepts any learner UUID, and has no caller authorization. Its migrations contain no EXECUTE revocation. Under default function privileges it can be exposed through the Supabase RPC API, letting callers repeatedly run history aggregates and write leaderboard rows outside Go authentication/rate limits. Scores are derived from stored data; this is not arbitrary score assignment. Repeated writes can also cause cross-replica cache invalidation. Actual ACLs/Data API exposure require live inspection. PostgreSQL grants newly created functions PUBLIC execution by default: [CREATE FUNCTION](https://www.postgresql.org/docs/current/sql-createfunction.html).

Before:

```sql
CREATE OR REPLACE FUNCTION refresh_leaderboard_score(p_user UUID)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER ...;
```

After:

```sql
REVOKE EXECUTE ON FUNCTION public.refresh_leaderboard_score(uuid)
FROM PUBLIC, anon, authenticated;
GRANT EXECUTE ON FUNCTION public.refresh_leaderboard_score(uuid) TO service_role;
```

Grant to the actual backend database role if different. Review every SECURITY DEFINER function, pin trusted schema resolution, and close default PUBLIC execution for future backend-only functions.

## Additional verification items

- Forwarded IP trust: clientIP trusts X-Forwarded-For/X-Real-IP without checking the peer. This is exploitable if the API is directly reachable or the ingress preserves spoofed headers. The checked compose file binds the API to loopback, and [Caddy ignores incoming X-Forwarded headers by default](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy), so this is not classified as a confirmed public bypass for that deployment. Verify the live ingress matches this configuration.
- Authenticate validates allowed signing algorithms and issuer, but does not require expiration or authenticated audience. Add `jwt.WithExpirationRequired()` and `jwt.WithAudience("authenticated")`. No unsigned-token or refresh-as-access bypass was established: local refresh JWTs lack the issuer that access validation requires.
- TrackAdminSessions ignores database errors during revocation lookup. Existing revocation must fail closed on lookup errors; handle pgx.ErrNoRows separately from outages.
- Verify Supabase OTP captcha/rate settings at the provider itself. Limits on the Go wrapper do not govern direct Supabase Auth requests.
- Review the actual S3 bucket/IAM policies, provider spending caps, and whether demo/reviewer accounts contain only disposable data. These runtime settings were outside the read-only code audit.

## Controls observed

JWT signature/algorithm/issuer validation, database-resolved roles, suspended-account checks, account-wide token generations, role-protected admin routes, parameterized SQL in reviewed paths, scoped caches after authentication, bounded cache capacity, distributed rate state with RLS, CORS production validation, secret-protected internal routes, and a Caddy rule blocking public internal endpoints are present. These controls do not compensate for the findings above.

A redacted pattern scan of tracked files found credential-shaped example URLs only; .env is ignored and not tracked. This is not a full Git-history secret scan. No local secret values were printed. govulncheck and gitleaks were unavailable; no dependency vulnerability or complete historical-secret clean bill is claimed. No security regression tests or exploit requests were run during this audit.

## Fix order

1. Fix URL-token/session enforcement, pending-teacher checks, and request-time admin passkey policy.
2. Bound JSON request bodies and upload sizes/quotas.
3. Make parent claiming/approval atomic and identity-bound.
4. Resolve practice/assessment answer exposure and rating policy.
5. Inspect live Supabase ACLs; restrict backend-only table/function exposure.
6. Add regressions for each authorization/concurrency/oversize case and run dependency/history scanners in a separate verification pass.
