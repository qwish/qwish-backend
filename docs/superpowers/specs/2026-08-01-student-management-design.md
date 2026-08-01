# Student Management — Design Spec

Date: 2026-08-01
Status: Approved for planning
Scope: `qwish-backend`, `numpie`, `qwish-teacher-panel`, `qwish-institute-dashboard`, `qwish-super-admin`

## Problem

Student records today carry almost no information and almost no lifecycle.

A student is a `users` row with `full_name`, `display_name`, `email`, `role`,
`institution_id`, `status`, plus gamification counters. There is no roll number,
grade, section, guardian contact, or academic history. There is no way for an
institution to create a student, import a roster, promote a class, graduate a
cohort, or transfer a student. `groups.invite_code` exists but nothing consumes
it, so a student cannot join a class by code. Teachers have no write access to
students of any kind: `GET /teacher/students` and `GET /teacher/students/{userId}`
are the entire surface. Institution admins can only toggle status and move
students in and out of groups.

This spec defines the student record, who owns each part of it, how a record
comes into existence, and how it changes over a student's life on the platform.

## Principles

1. **The person and the enrollment are different things.** A `users` row is a
   human with an account. An enrollment is that human's relationship with one
   institution. Separating them makes transfer, graduation, and
   institution-less students fall out of the model instead of needing special
   cases.
2. **Permissions are structural, not configured.** "Institution-locked" means
   the field lives on a table students have no write endpoint for. There is no
   per-field permission engine to build, configure, or debug.
3. **Institution membership is optional.** A student with no institution is a
   first-class user, not a degraded one.

## Data Model

One new migration: `qwish-backend/migrations/031_student_enrollment.sql`.
Migrations are append-only and ordered by filename; nothing already shipped is
edited.

### `enrollments` (new)

One row per (student, institution) relationship, past or present.

```sql
CREATE TABLE IF NOT EXISTS enrollments (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id  UUID NOT NULL REFERENCES institutions(id),
  user_id         UUID REFERENCES users(id),   -- NULL until claimed
  full_name       TEXT NOT NULL,               -- from import; source for claim
  email           TEXT,                        -- from import; optional
  -- Staging for import-supplied personal values. Copied to the users row at
  -- claim time (only into fields the student left blank), then never read again.
  import_phone           TEXT,
  import_guardian_name   TEXT,
  import_guardian_phone  TEXT,
  import_guardian_email  TEXT,
  roll_number     TEXT,
  grade           TEXT,
  section         TEXT,
  admission_date  DATE,
  claim_code      TEXT UNIQUE,
  status          TEXT NOT NULL DEFAULT 'pending_claim'
                    CHECK (status IN ('pending_claim','active','suspended',
                                      'graduated','transferred')),
  joined_at       TIMESTAMPTZ,                 -- set when claimed
  ended_at        TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Roll numbers are unique within an institution among live enrollments only,
-- so a number can be reused after a student graduates or transfers out.
CREATE UNIQUE INDEX enrollments_roll_unique
  ON enrollments(institution_id, roll_number)
  WHERE roll_number IS NOT NULL AND ended_at IS NULL;

-- A student holds at most one live enrollment. pending_claim rows have
-- user_id NULL and are therefore exempt: an institution may pre-provision a
-- student who is currently enrolled elsewhere; the code simply will not
-- redeem until they leave.
CREATE UNIQUE INDEX enrollments_one_active_per_user
  ON enrollments(user_id)
  WHERE user_id IS NOT NULL AND status IN ('active','suspended');

CREATE INDEX enrollments_institution_status ON enrollments(institution_id, status);
CREATE INDEX enrollments_user ON enrollments(user_id) WHERE user_id IS NOT NULL;
```

`users.institution_id` is retained as a denormalized pointer to the institution
of the student's current live enrollment, so existing institution-scoped
queries keep working unchanged. It is written only by the code paths that
create or end an enrollment, and is `NULL` for students with no live
enrollment.

### `users` additions

Student-owned personal fields:

```sql
ALTER TABLE users
  ADD COLUMN IF NOT EXISTS date_of_birth         DATE,
  ADD COLUMN IF NOT EXISTS gender                TEXT,
  ADD COLUMN IF NOT EXISTS phone                 TEXT,
  ADD COLUMN IF NOT EXISTS address               TEXT,
  ADD COLUMN IF NOT EXISTS guardian_name         TEXT,
  ADD COLUMN IF NOT EXISTS guardian_phone        TEXT,
  ADD COLUMN IF NOT EXISTS guardian_email        TEXT,
  ADD COLUMN IF NOT EXISTS highest_qualification TEXT;
```

Guardian contact sits on `users` and is student-editable. An institution may
supply guardian values in an import; those land on the student's `users` row at
claim time as an initial value and become the student's to maintain from then
on.

### CV / profile entries

`user_education` and `user_skills` already exist (migration
`003_profile_features.sql`) and are unchanged. One new table covers the
remaining CV kinds rather than four near-identical tables:

```sql
CREATE TABLE IF NOT EXISTS user_profile_entries (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL
                CHECK (kind IN ('experience','certification','achievement','course')),
  title       TEXT NOT NULL,
  org         TEXT,
  start_date  DATE,
  end_date    DATE,
  description TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX user_profile_entries_user_kind ON user_profile_entries(user_id, kind);
```

### `student_edit_requests` (new)

Teacher-proposed corrections to institution-locked fields.

```sql
CREATE TABLE IF NOT EXISTS student_edit_requests (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  enrollment_id UUID NOT NULL REFERENCES enrollments(id) ON DELETE CASCADE,
  requested_by  UUID NOT NULL REFERENCES users(id),
  field         TEXT NOT NULL
                  CHECK (field IN ('roll_number','grade','section','admission_date')),
  current_value TEXT,
  proposed_value TEXT NOT NULL,
  note          TEXT,
  status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','approved','rejected')),
  reviewed_by   UUID REFERENCES users(id),
  reviewed_at   TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX student_edit_requests_pending
  ON student_edit_requests(enrollment_id) WHERE status = 'pending';
```

RLS policies for the new tables follow the pattern in `004_rls_policies.sql`.

## Ownership and Permissions

The lock is the table boundary: institution-owned fields live on `enrollments`,
and no student-facing endpoint writes to `enrollments`.

| Field group | Student | Teacher | Institution admin | Parent | Super-admin |
|---|---|---|---|---|---|
| roll_number, grade, section, admission_date | read | propose | write | — | write |
| enrollment status | read | — | write | — | write |
| class membership | read | write (own classes) | write | — | write |
| full_name, display_name, avatar | write | read | read | — | write |
| DOB, gender, phone, address, guardian contact | write | read | read | — | write |
| CV: education, skills, experience, certs, achievements, courses | write | read | read | — | write |
| progress: points, streak, attempts, quiz results | read | read | read | read | read |

Parents are progress-only: no identity fields, no CV, no edit path, no access
to the proposal queue.

Teacher scope is bounded by class: a teacher may act on a student only when
that student shares a `groups` row with the teacher via `group_teachers` /
`group_students`. `internal/domain/teacher/metrics_scope.go` already
establishes this scoping pattern and is the reference for the new endpoints.

## Record Origination (Hybrid)

Three paths produce a student.

**Institution pre-provisioning.** An admin imports a CSV or adds a student by
form. This creates an `enrollments` row with `status='pending_claim'`,
`user_id NULL`, and a generated `claim_code`. No `users` row exists yet; the
`users` table requires a `supabase_uid`, and a roster entry has no
authenticated identity behind it.

**Claim.** The student signs up in numpie through the normal Supabase OTP flow
and enters the claim code. The server sets `user_id`, `joined_at`,
`status='active'`, and `users.institution_id`. Import-supplied values for
name, guardian contact, and phone are copied to the `users` row only where the
student left the corresponding field blank — the student's own entry always
wins. A code that is already redeemed returns `409 CLAIM_CODE_USED`. A student
who already holds a live enrollment returns `409 ENROLLMENT_EXISTS`, naming the
current institution.

**Self-signup.** A student signs up with no code at all and is an independent
user (see below). They may later join a class with the class's
`groups.invite_code` — currently unused by any endpoint — which creates an
`active` enrollment with `roll_number`, `grade`, and `section` left NULL for an
admin to fill in. The institution referral-code path in
`auth.CreateProfile` continues to work and now also creates an enrollment.

## Students Without an Institution

An independent student is a `users` row with zero `enrollments` rows.
`users.institution_id` is already nullable, so this needs no schema change.

- Nothing is locked for them. With no enrollment, there are no
  institution-owned fields; they own their entire record.
- Roll number, grade, and section do not exist for them, rather than existing
  and being empty.
- They never appear in teacher or institution rosters, because every roster
  query joins `enrollments`.
- Parent links still work: `parent_student_links` is user-to-user and does not
  involve an institution.
- Joining an institution later is purely additive. The account was always
  theirs; an enrollment attaches to it.
- Leaving an institution returns them to this state with points, streak,
  attempts, and CV intact.

In numpie, the profile endpoint returns a single flag derived from the live
enrollment count. Zero: institution navigation is hidden, the leaderboard shows
global and friends scopes only, and a dismissible "Join your institution"
entry point accepts a claim or class code. One: institution tabs appear. This
is one conditional in the existing shell, not a separate app mode.

## Lifecycle

- **Suspend / reactivate** — `enrollments.status` moves between `active` and
  `suspended`, and the same call writes `users.status`, which is what actually
  blocks login. Data and history are retained.
  This supersedes today's `PATCH /institution/students/{userId}/status`, which
  writes `users.status`; that endpoint is repointed at the enrollment and
  keeps its path for compatibility.
- **Graduate** — `status='graduated'`, `ended_at=now()`,
  `users.institution_id=NULL`. The student keeps their account, points, streak,
  attempts, and CV in numpie. The institution loses roster access but retains
  its own `quiz_attempts` and report data.
- **Transfer within an institution** — a change to `group_students` only. The
  enrollment is untouched.
- **Transfer between institutions** — the old enrollment ends with
  `status='transferred'`; the new institution issues a claim code, which the
  student redeems to create a fresh enrollment. The old institution's attempt
  and report data stay with the old institution. The new institution's
  student-scoped queries filter to `quiz_attempts.created_at >= enrollments.joined_at`,
  so one school never sees another's assessment history.
- **Bulk promotion** — one action advances a filtered set (by grade, or grade +
  section) to a new grade and optionally remaps class membership. Runs in a
  transaction and reports rows affected.

## Bulk Import

`POST /api/v1/institution/students/import`, multipart CSV.

With `dry_run=true` the server validates and writes nothing, returning a
per-row verdict: `create`, `update` (matched to a live enrollment by
`roll_number`, or by `email` when no roll number is supplied), or `error` with
a reason. Duplicate roll numbers within the file, roll numbers colliding with
live enrollments, and malformed dates are all row-level errors.

Committing without `dry_run` applies the previewed changes in a transaction and
returns the generated claim codes as a downloadable CSV for distribution. The
preview step is what keeps a bad file from half-importing.

Columns: `full_name` (required), `email`, `roll_number`, `grade`, `section`,
`admission_date`, `guardian_name`, `guardian_phone`, `guardian_email`, `phone`.

## Super-Admin

- **Cross-institution search** — find any student by email, roll number, or
  name across all institutions, for support and duplicate resolution.
- **Force-merge duplicates** — one human with two records, typically a
  self-signup plus an unclaimed roster row. Points are summed, attempts and
  enrollments are repointed at the surviving user, the loser is soft-deleted,
  and an `audit_log` entry records the merge.
- **Impersonate** — already supported via `impersonation_sessions`; extended to
  cover the new student views.
- **Hard delete / erasure** — permanent purge beyond the soft `deleted_at`,
  with an `audit_log` entry.

## API Surface

New and changed endpoints, grouped by caller.

**Student** (`/api/v1`)
- `POST /students/claim` `{claim_code}` — redeem a roster row.
- `POST /students/join-class` `{invite_code}` — join by class code.
- `PATCH /users/me` — extended with the new personal fields.
- `GET|POST|PATCH|DELETE /users/me/profile-entries` — CV entries.
- `GET /users/me/enrollment` — current enrollment or null.

**Teacher** (`/api/v1/teacher`)
- `POST /classes/{classId}/students` / `DELETE /classes/{classId}/students/{userId}`
- `POST /enrollments/{enrollmentId}/edit-requests` — propose a correction.
- `GET /edit-requests` — own proposals and their status.

**Institution** (`/api/v1/institution`)
- `POST /students` — create one roster entry.
- `POST /students/import` — CSV, with `dry_run`.
- `PATCH /enrollments/{enrollmentId}` — write institution-owned fields.
- `PATCH /enrollments/{enrollmentId}/status` — suspend, reactivate, graduate,
  transfer out. One endpoint, since all four are the same state change.
- `POST /enrollments/promote` — bulk promotion.
- `GET /edit-requests`, `PATCH /edit-requests/{id}` — review queue.
- Existing student/group endpoints repointed at `enrollments`.

Enrollment-addressed routes sit under `/enrollments/...` rather than
`/students/...` because the existing `PATCH /students/{userId}/status` stays in
place for the institute dashboard, and chi rejects two routes of the same shape
and depth with different parameter names.

**Super-admin** (`/api/v1/admin`)
- `GET /students/search`
- `POST /students/merge` `{keep_user_id, merge_user_id}`
- `DELETE /students/{userId}/purge`

## Error Handling

Follows the existing `middleware.Error(w, status, code, message)` convention.

| Code | Status | Condition |
|---|---|---|
| `CLAIM_CODE_INVALID` | 400 | No matching `pending_claim` enrollment |
| `CLAIM_CODE_USED` | 409 | Enrollment already claimed |
| `ENROLLMENT_EXISTS` | 409 | Student already holds a live enrollment |
| `ROLL_NUMBER_TAKEN` | 409 | Collides with a live enrollment |
| `IMPORT_VALIDATION_FAILED` | 422 | Dry-run found row errors; body carries per-row detail |
| `NOT_IN_YOUR_CLASS` | 403 | Teacher acting outside their class scope |
| `EDIT_REQUEST_RESOLVED` | 409 | Reviewing an already-decided request |

Bulk operations (import commit, promotion, merge) run in a single transaction
and either fully apply or fully roll back.

## Testing

Follows the existing `testdb_test.go` / `fixtures_test.go` pattern in
`internal/domain/teacher`.

- Claim: valid, already-used, student already enrolled, blank-field merge
  precedence.
- Single-active-enrollment index: a second `active` enrollment fails; a second
  `pending_claim` succeeds.
- Roll-number uniqueness: collides while live, reusable after `ended_at` is set.
- Teacher scope: proposal and class-membership calls rejected for a student
  outside the teacher's classes.
- Transfer: new institution's queries exclude attempts predating `joined_at`.
- Import dry-run writes nothing; commit is all-or-nothing on a bad row.
- Independent student: profile returns zero live enrollments, absent from
  every roster query.

## Out of Scope

- Attendance tracking.
- Fee or billing records.
- Multiple simultaneous enrollments. The unique index enforces one; lifting it
  later means auditing every institution-scoped query in the `teacher`,
  `institution`, `metrics`, and `leaderboard` domains.
- Parent-initiated corrections. Parents stay progress-only.

## Open Question

`date_of_birth` is student-owned in this design. If age gating (under-13
account restrictions, age-band leaderboards) is ever introduced, DOB must
become immutable once set, or move to `enrollments` as an institution-owned
field. Deferred until a gating requirement is real.
