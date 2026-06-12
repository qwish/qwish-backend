# Institute Dashboard API Implementation Plan

Plan to implement missing APIs powering the institute-dashboard pitch features (skill heatmaps, cohort trends, placement readiness, recruiter reports, SSO + LMS, success management).

Current backend covers institution CRUD + 5 canned reports (student-performance, teacher-activity, quiz-analytics, streak-health, points-summary). Pitch-tier analytics all missing.

---

## Pre-work (shared)

### M012 migration `012_institution_analytics.sql`

- `departments(id, institution_id, name)` — dept proxy stronger than groups.
- `domains(id, slug, name)` — canonical skill domain (DSA, DBMS, Aptitude, etc).
- Add `department_id` FK to `user_profiles` (or `institution_members`).
- Add `domain_id` FK to `quizzes` + `quiz_questions` (link content → domain).
- `cohort_snapshots(id, institution_id, cohort_label, period_start, period_end, metrics_json, created_at)` — store YoY rollups.
- `placement_seasons(id, institution_id, name, target_score, cutoff_json, start_date, end_date)`.
- `intervention_plans(id, student_id, created_by, plan_text, status, due_date, created_at)`.
- `recruiter_reports(id, institution_id, student_ids[], pdf_url, signature, signed_at, branding_json, created_at)`.
- `institution_alert_rules(id, institution_id, metric, threshold, comparator, channel, enabled)`.
- `institution_branding(institution_id PK, logo_url, primary_color, accent_color, footer_text)`.
- `sso_connections(id, institution_id, provider, client_id, client_secret_enc, metadata_json, enabled)`.
- `lms_connections(id, institution_id, provider, base_url, oauth_token_enc, refresh_token_enc, last_sync_at)`.
- `lms_roster_sync_log(id, lms_connection_id, started_at, finished_at, status, error, added, updated, removed)`.

RLS: scope every new table by `institution_id` like existing pattern.

---

## Domain: `internal/domain/analytics/`

New package. Files: `service.go`, `handler.go`, `queries.go`, `aggregations.go`.

Endpoints:
- `GET /institution/reports/skill-heatmap?period=&dept_id=` → matrix `[dept][domain] = avg_score`.
- `GET /institution/reports/skill-heatmap/student/{userId}` → drill student per-domain.
- `GET /institution/reports/cohort-trends?cohort=&metric=&from=&to=` → series.
- `GET /institution/reports/cohort-trends/export?format=csv|xlsx` → stream file.
- `GET /institution/reports/yoy?metric=&years=` → YoY benchmark vs own history.
- `GET /institution/reports/inflection-points?metric=` → detect dips/spikes (z-score over rolling window).

SQL: aggregate `attempts JOIN quiz_questions JOIN domains` group by `(department_id, domain_id)`.

Cache layer: stale-while-revalidate via `cohort_snapshots`, recompute nightly job.

---

## Domain: `internal/domain/placement/`

Endpoints:
- `POST /institution/placement-seasons` — create season, set cutoffs.
- `GET /institution/placement-seasons` — list.
- `GET /institution/placement-seasons/{id}/readiness` → `{per_domain_pct, eligible_count, risk_count}`.
- `GET /institution/placement-seasons/{id}/students?risk=high|medium|low` — flag list.
- `POST /institution/students/{userId}/intervention-plans` — create plan.
- `GET /institution/students/{userId}/intervention-plans` — list.
- `PATCH /institution/intervention-plans/{id}` — update status.

Risk scoring: compare student domain scores to season `cutoff_json`. Service func `ComputeReadiness(seasonID)`.

---

## Domain: `internal/domain/recruiterreport/`

Endpoints:
- `POST /institution/recruiter-reports` body `{student_ids, template, branding_override}` → enqueue PDF gen, return job id.
- `GET /institution/recruiter-reports` — list.
- `GET /institution/recruiter-reports/{id}` — meta.
- `GET /institution/recruiter-reports/{id}/pdf` — signed R2 URL.
- `GET /recruiter-reports/{id}/verify?sig=` — **public** verify endpoint (no auth), HMAC over `(report_id, student_ids, scores)` using server key.

PDF: use `github.com/jung-kurt/gofpdf` or `chromedp` headless. Store PDF in R2 via existing `storage` pkg. Sign with `HMAC-SHA256(REPORT_SIGNING_KEY)`.

New env: `REPORT_SIGNING_KEY`.

---

## Domain: `internal/domain/branding/` (or extend `institution`)

- `GET /institution/branding` — fetch.
- `PUT /institution/branding` — update logo, colors, footer.
- `POST /institution/branding/logo` — upload via existing `upload` handler.

---

## Domain: `internal/domain/sso/`

Endpoints (public):
- `GET /auth/sso/{provider}/start?institution_slug=` → 302 to provider.
- `GET /auth/sso/{provider}/callback` → exchange code, mint Supabase session via admin API, redirect.
- Providers: `google`, `microsoft` (OIDC), `saml` later.

Admin:
- `POST /institution/sso-connections` — configure client id/secret.
- `GET /institution/sso-connections`.
- `PATCH /institution/sso-connections/{id}`.
- `DELETE /institution/sso-connections/{id}`.

Lib: `golang.org/x/oauth2`. Encrypt secrets with AES-GCM using `SSO_ENC_KEY` env.

---

## Domain: `internal/domain/lms/`

Endpoints:
- `POST /institution/lms-connections` — start OAuth (Moodle/Canvas).
- `GET /institution/lms-connections/{provider}/callback` — store tokens.
- `GET /institution/lms-connections` — list.
- `DELETE /institution/lms-connections/{id}`.
- `POST /institution/lms-connections/{id}/sync` — trigger roster sync (async).
- `GET /institution/lms-connections/{id}/sync-log` — history.
- `POST /institution/lms-connections/{id}/assignments` — create LMS assignment from quiz.
- `POST /webhooks/lms/{provider}` — receive grade-passback / roster events. HMAC verify per provider.

Sync worker: in `internal/scheduler/`, goroutine pulls roster, upserts `user_profiles`, links `department_id`.

---

## Domain: `internal/domain/alerts/`

- `POST /institution/alert-rules` — `{metric, threshold, comparator, channel}`.
- `GET /institution/alert-rules`.
- `PATCH /institution/alert-rules/{id}`.
- `DELETE /institution/alert-rules/{id}`.

Evaluator: cron `/cron/evaluate-alerts` runs nightly. Match against `cohort_snapshots`. Dispatch via existing `notification` + Resend email.

---

## Cron additions (`cmd/api/main.go`)

- `POST /cron/snapshot-cohorts` — nightly cohort rollup.
- `POST /cron/compute-readiness` — refresh placement readiness.
- `POST /cron/evaluate-alerts` — fire inflection alerts.
- `POST /cron/lms-roster-sync` — periodic sync all connections.

Reuse `CRON_SECRET` guard.

---

## Wiring (`cmd/api/main.go`)

Mount under existing `/institution` group with `RequireInstitutionAdmin` middleware. Public SSO + verify routes outside auth group.

---

## Config additions (`internal/config/config.go`)

- `REPORT_SIGNING_KEY` (required)
- `SSO_ENC_KEY` (32-byte, required)
- `GOOGLE_OAUTH_CLIENT_ID/SECRET`, `MS_OAUTH_CLIENT_ID/SECRET` (optional defaults; per-tenant override DB-side)
- `MOODLE_REDIRECT_URI`, `CANVAS_REDIRECT_URI`.

---

## Sequencing

1. **M012 migration + domain/dept tagging backfill** (blocker for everything).
2. **analytics** (heatmap, cohort, YoY) — reuse existing attempts data.
3. **placement** (depends on analytics).
4. **branding + recruiterreport** (PDF gen + HMAC verify).
5. **alerts** (depends on cohort snapshots).
6. **sso** (Google → MS → SAML later).
7. **lms** (Canvas → Moodle; webhook + sync).
8. Cron + scheduler wiring.

---

## Testing

- Per domain `service_test.go` with `pgxmock` or testcontainers Postgres.
- HMAC verify endpoint: golden-test signature.
- SSO/LMS: stub OAuth server in tests.

---

## Effort estimate

| Item | Days |
|---|---|
| Migration + tagging | 1 |
| Analytics | 3 |
| Placement | 2 |
| Recruiter PDF + verify | 3 |
| Branding | 0.5 |
| Alerts | 1.5 |
| SSO (2 providers) | 4 |
| LMS (Canvas+Moodle + webhook + sync) | 5 |
| Cron + tests | 2 |
| **Total** | **~22 dev-days** |

---

## Feature → API coverage tally

| Feature | Required APIs | Currently Implemented |
|---|---|---|
| Skill heatmaps | 4 | 3 (no heatmap aggregate) |
| Cohort trends | 3 | 0 |
| Placement readiness | 3 | 0 |
| Recruiter reports | 3 | 0 (settings only) |
| SSO + LMS | 5 | 0 |
| Success management | 2 | 1 |
| **Total** | **~20** | **~4** |
