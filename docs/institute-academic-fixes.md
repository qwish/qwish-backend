# Academic-flow fixes: contracts and rollout

## Metrics (B04)

List `average_score_30d`, detail `average_score`, and roster
`class_average_score` cover the same rolling 30 × 24 hours ending now. Each
completed attempt (including retakes) has equal weight. The accompanying counts
are `attempt_count_30d` and `class_attempts`; absence is null, not zero percent.
Incomplete attempts, public standalone play and assignments for other classes
are excluded. Scores follow immutable attempt→assignment→recipient context,
never present membership. Migration 095 backfills uniquely linked recipients.
For legacy attempts it snapshots only institution-owned, explicitly class-linked
quizzes with no assignment records. Ambiguous legacy evidence is not attributed.
This avoids rewriting old metrics when a quiz is subsequently reassigned.

Attempt creation and recipient claiming commit atomically. The existing
ResponseCache.ClearOnWrite middleware invalidates date/performance reads after
mutations; class and curriculum detail reads themselves are uncached.

## Labels and rosters (B05–B06)

Only active normal classes with a grade or section supply compatibility labels.
One distinct pair derives the labels; multiple pairs clear both labels and set
`class_label_state=ambiguous`; no pair produces `unset`. Remedial memberships
never supply labels. Recalculation locks the live enrollment before reading the
memberships, including concurrent inserts, class edits and end/reopen changes.
Historical enrollment labels remain historical. Migration 096 saves conflicts
in `academic_label_review` before reconciling live labels.

Consumers inventoried before changing the trigger:

- institution ListStudents filters/rows and StudentDetail enrollment summaries;
- teacher ListStudents, StudentDetail and roster export;
- user education/report history reads;
- enrollment fixtures and membership lifecycle services;
- teacher remedial-group creation.

Readers already support nullable labels. The database state additionally exposes
why labels are absent; no cohort/programme source of truth has been introduced.
Active rosters select one live enrollment. Ended rosters select the most recent
enrollment overlapping the retained membership, bounded by class ending; absent
context is explicitly `unavailable`. Membership rows determine both count and
row identity, so missing enrollments remain visible for supported correction.

## Lifecycle and lock order (B01–B02, B07–B08)

Institution membership mutations lock the scoped class, validate active staff or
students and use institution-constrained SQL. Repeat requests produce no change
audit. Audit failure rolls back the mutation. Ended membership removal is an
explicit historical correction and retains the existing history trigger.

Department archive, placement, role grants and reopen use the same lock order:
institution first, then class/department/role. Eligibility is read after the
institution lock. Reopen refuses archived departments and preserves provenance.
Reopen retains memberships and unended curricula; ended curriculum assignments
stay ended. The 90-day limit remains in force. Institute curriculum history
includes ended assignments/classes; ordinary teacher scope stays restricted to
classes they teach and unended assignments. Upcoming and past years remain
readable for preparation/history within that scope.

## Calendars and clients (B03, B09–B13)

Date boundaries are inclusive dates in institutions.timezone. Backend year
responses include `today`, `temporal_state`, and overlapping year names;
assignment responses include temporal state separately from `ended_at`.
POST /institution/academic-years/preview returns the exact before/after dates,
affected assignments, overlaps and a review token. Overlaps or assigned-year
date edits require that token plus a nonempty reason. A changed impact rejects a
stale token. Applying dates and recording before/after audit data is atomic.

GET /institution/staff supports page/limit/search and eligible teachers/admins;
GET /institution/teachers retains teacher-only semantics. Published curriculum
search/filtering runs before pagination. Setup completion derives from saved
department, teachers, curriculum assignments, invitations or enrolled students.

`current_curricula` and `current_curriculum_count` replace the dashboard's
single-edition display. `current_curriculum` remains a deprecated compatibility
object with its original type. Remove it only after all deployed clients stop
reading it. Multiple current years contribute all their assignments.

## Rollout

1. Run scripts/academic/preflight.sql against the deployment database and review
   staff anomalies, missing enrollments, conflicting labels and overlapping years.
   Do not delete retained historical teacher assignments.
2. Take and verify a recoverable database snapshot before migration 095/096.
3. Apply migrations in order before deploying the backend; then deploy dashboard.
4. Repair reviewed missing memberships/enrollment anomalies through lifecycle
   services. Overlapping calendars remain unchanged until explicitly edited.
5. Monitor existing request/audit logs for authorization failures, mutation errors
   and latency, and compare class list/detail metrics and roster counts.

Only an isolated local test database was migrated during implementation. Running
production reports, taking the production snapshot and deployment remain rollout
steps; no production records were inspected or altered.

## Reproducing validation

Use a disposable PostgreSQL database with all migrations applied in order (one
transaction per migration) and the Supabase `anon`, `authenticated` and
`service_role` roles. Set `TEST_DATABASE_URL` and run the institution, curriculum,
leadership, attempt, db, enrollment and teacher Go packages. `go test ./...`
also checks the full repository's ordinary suites.

In the dashboard run `tsc --noEmit`, ESLint on the changed files, and `next build`.
The browser regression script requires Playwright available to Node, a Chromium
installation (`CHROMIUM_EXECUTABLE_PATH` may select an installed browser), and the
built dashboard on `DASHBOARD_TEST_URL` (default `http://localhost:3198`). It mocks
all API calls and does not write to the connected backend. Example with an
external Playwright installation:

```sh
NODE_PATH=/tmp/qwish-browser-check/node_modules \
CHROMIUM_EXECUTABLE_PATH=/path/to/chromium \
node scripts/academic-fixes.browser.cjs
```
