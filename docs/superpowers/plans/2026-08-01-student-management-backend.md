# Student Management (Backend) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `qwish-backend` a real student record — enrollment lifecycle, roster import, claim flow, teacher edit proposals, and super-admin tooling — as specified in `docs/superpowers/specs/2026-08-01-student-management-design.md`.

**Architecture:** A new `enrollments` table separates the person (`users`) from their relationship with an institution. Institution-owned fields live on `enrollments`, which no student-facing endpoint writes to — that table boundary *is* the permission model. A new `internal/domain/enrollment` package owns the table and every operation on it; `internal/domain/editrequest` owns the teacher proposal queue. Existing `teacher` and `institution` handlers are repointed at `enrollments` rather than rewritten.

**Tech Stack:** Go 1.26, chi v5 router, pgx v5 (`pgxpool`), Postgres (Supabase), no ORM, no migration tool beyond `internal/db/migrate.go`.

## Scope

This plan covers **`qwish-backend` only**. The spec also touches four frontends (numpie, teacher panel, institute dashboard, super-admin). Those are separate plans, one per user type, written after this one lands — the API has to exist before a UI can consume it.

## Global Constraints

- Migrations are **append-only and ordered by filename**. Add `031_student_enrollment.sql`; never edit a shipped migration.
- All responses go through `internal/middleware`: `JSON(w, status, data)`, `JSONWithMeta(w, status, data, *Meta)`, `Error(w, status, code, message)`, `BadRequest`, `Forbidden`, `NotFound(w, resource)`, `InternalError(w)`.
- Request identity comes from `middleware.GetUserID(r)`, `GetInstitutionID(r)`, `GetRole(r)`, `GetAdminID(r)`, `GetSupabaseUID(r)`, `GetEmail(r)`.
- Tests are integration tests against a scratch database. They read `TEST_DATABASE_URL` and **skip** when it is unset (pattern: `internal/domain/teacher/testdb_test.go`).
- `internal/db.RunMigrations` globs `migrations/*.sql` relative to the process working directory, so it cannot be called from a package test. Apply new migrations to the scratch DB with `psql` before running tests.
- Every institution-scoped write records an entry via the existing `logAudit(ctx, db, actorID, action, entityType, entityID, detail)` helper in `internal/domain/institution/handler.go`.
- Bulk operations (import commit, promotion, merge) run in one `pgx` transaction and either fully apply or fully roll back.
- Error codes are exactly those in the spec's Error Handling table. Do not invent new ones.
- Run `go build ./... && go vet ./...` before every commit.

## File Structure

**Created:**
- `migrations/031_student_enrollment.sql` — `enrollments`, `student_edit_requests`, `user_profile_entries`, `users` columns, indexes, RLS.
- `internal/domain/enrollment/service.go` — the `enrollments` table's only owner: claim, join, roster CRUD, lifecycle.
- `internal/domain/enrollment/import.go` — CSV parse, dry-run preview, commit.
- `internal/domain/enrollment/handler_student.go` — student-facing endpoints (claim, join-class, my enrollment).
- `internal/domain/enrollment/handler_institution.go` — institution-facing endpoints (roster CRUD, import, lifecycle).
- `internal/domain/enrollment/handler_teacher.go` — teacher class-membership writes.
- `internal/domain/enrollment/testdb_test.go`, `fixtures_test.go` — test harness, copied from the `teacher` package pattern.
- `internal/domain/editrequest/service.go`, `handler.go` — proposal queue.
- `internal/domain/user/profile_entries.go` — CV entry CRUD.

**Modified:**
- `cmd/api/main.go` — wire the new handlers and routes.
- `internal/domain/institution/handler.go` — `ListStudents` / `GetStudent` / `UpdateStudentStatus` repointed at `enrollments`.
- `internal/domain/teacher/handler.go` — `ListStudents` / `GetStudent` join `enrollments` and filter attempts by `joined_at`.
- `internal/domain/auth/handler.go` — `CreateProfile` creates an enrollment when a referral code is used.
- `internal/domain/user/handler.go` — `PATCH /users/me` accepts the new personal fields.
- `internal/domain/admin/handler.go` — cross-institution search, merge, purge.

Splitting `enrollment` into four files (service, import, three handlers) keeps each under a few hundred lines. `institution/handler.go` is already ~700 lines; this plan does not restructure it, only repoints three functions inside it.

---

### Task 1: Migration and enrollment test harness

**Files:**
- Create: `qwish-backend/migrations/031_student_enrollment.sql`
- Create: `qwish-backend/internal/domain/enrollment/testdb_test.go`
- Create: `qwish-backend/internal/domain/enrollment/fixtures_test.go`
- Test: `qwish-backend/internal/domain/enrollment/schema_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: the `enrollments`, `student_edit_requests`, `user_profile_entries` tables; the test helpers `openTestDB(t) *pgxpool.Pool` and `seedFixture(t, pool) fixture` where `fixture` has fields `InstitutionID, OtherInstitutionID, TeacherID, LonerTeacherID, StudentID, UnclaimedEnrollmentID, ClaimCode, GroupID string`.

- [ ] **Step 1: Write the migration**

Create `migrations/031_student_enrollment.sql`:

```sql
-- Student management: separate the person (users) from their relationship with
-- an institution (enrollments). Institution-owned academic fields live here and
-- have no student-facing write path — the table boundary is the permission model.

CREATE TABLE IF NOT EXISTS enrollments (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id  UUID NOT NULL REFERENCES institutions(id),
  user_id         UUID REFERENCES users(id),
  full_name       TEXT NOT NULL,
  email           TEXT,
  -- Staging for import-supplied personal values. Copied onto the users row at
  -- claim time (only into fields the student left blank), then never read again.
  import_phone          TEXT,
  import_guardian_name  TEXT,
  import_guardian_phone TEXT,
  import_guardian_email TEXT,
  roll_number     TEXT,
  grade           TEXT,
  section         TEXT,
  admission_date  DATE,
  claim_code      TEXT UNIQUE,
  status          TEXT NOT NULL DEFAULT 'pending_claim'
                    CHECK (status IN ('pending_claim','active','suspended','graduated','transferred')),
  joined_at       TIMESTAMPTZ,
  ended_at        TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Roll numbers are unique per institution among live enrollments only, so a
-- number is reusable once a student graduates or transfers out.
CREATE UNIQUE INDEX IF NOT EXISTS enrollments_roll_unique
  ON enrollments(institution_id, roll_number)
  WHERE roll_number IS NOT NULL AND ended_at IS NULL;

-- A student holds at most one live enrollment. pending_claim rows have
-- user_id NULL and are exempt, so an institution may pre-provision a student
-- who is currently enrolled elsewhere; the code will not redeem until they leave.
CREATE UNIQUE INDEX IF NOT EXISTS enrollments_one_active_per_user
  ON enrollments(user_id)
  WHERE user_id IS NOT NULL AND status IN ('active','suspended');

CREATE INDEX IF NOT EXISTS enrollments_institution_status ON enrollments(institution_id, status);
CREATE INDEX IF NOT EXISTS enrollments_user ON enrollments(user_id) WHERE user_id IS NOT NULL;

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS date_of_birth         DATE,
  ADD COLUMN IF NOT EXISTS gender                TEXT,
  ADD COLUMN IF NOT EXISTS phone                 TEXT,
  ADD COLUMN IF NOT EXISTS address               TEXT,
  ADD COLUMN IF NOT EXISTS guardian_name         TEXT,
  ADD COLUMN IF NOT EXISTS guardian_phone        TEXT,
  ADD COLUMN IF NOT EXISTS guardian_email        TEXT,
  ADD COLUMN IF NOT EXISTS highest_qualification TEXT;

CREATE TABLE IF NOT EXISTS user_profile_entries (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL CHECK (kind IN ('experience','certification','achievement','course')),
  title       TEXT NOT NULL,
  org         TEXT,
  start_date  DATE,
  end_date    DATE,
  description TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS user_profile_entries_user_kind ON user_profile_entries(user_id, kind);

CREATE TABLE IF NOT EXISTS student_edit_requests (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  enrollment_id  UUID NOT NULL REFERENCES enrollments(id) ON DELETE CASCADE,
  requested_by   UUID NOT NULL REFERENCES users(id),
  field          TEXT NOT NULL CHECK (field IN ('roll_number','grade','section','admission_date')),
  current_value  TEXT,
  proposed_value TEXT NOT NULL,
  note           TEXT,
  status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
  reviewed_by    UUID REFERENCES users(id),
  reviewed_at    TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS student_edit_requests_pending
  ON student_edit_requests(enrollment_id) WHERE status = 'pending';

-- RLS: all writes go through the Go backend (service_role bypasses RLS).
-- These policies restrict SELECT for the `authenticated` Supabase role.
ALTER TABLE enrollments ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_profile_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE student_edit_requests ENABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS enrollments_select ON enrollments;
CREATE POLICY enrollments_select ON enrollments FOR SELECT USING (
  user_id = auth_user_id()
  OR (auth_institution_id() IS NOT NULL AND institution_id = auth_institution_id())
);

DROP POLICY IF EXISTS user_profile_entries_select ON user_profile_entries;
CREATE POLICY user_profile_entries_select ON user_profile_entries FOR SELECT USING (
  user_id = auth_user_id()
);

DROP POLICY IF EXISTS student_edit_requests_select ON student_edit_requests;
CREATE POLICY student_edit_requests_select ON student_edit_requests FOR SELECT USING (
  requested_by = auth_user_id()
  OR EXISTS (SELECT 1 FROM enrollments e WHERE e.id = enrollment_id
             AND e.institution_id = auth_institution_id())
);
```

- [ ] **Step 2: Apply it to the scratch database**

Run:
```bash
cd qwish-backend && psql "$TEST_DATABASE_URL" -f migrations/031_student_enrollment.sql
```
Expected: `CREATE TABLE` / `CREATE INDEX` / `ALTER TABLE` / `CREATE POLICY` output, no errors.

- [ ] **Step 3: Create the test harness**

Create `internal/domain/enrollment/testdb_test.go` — identical in shape to `internal/domain/teacher/testdb_test.go`:

```go
package enrollment

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// openTestDB connects to TEST_DATABASE_URL, or skips the test.
//
// Point TEST_DATABASE_URL at a scratch database — these tests write rows.
func openTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping database integration test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping TEST_DATABASE_URL: %v", err)
	}
	return pool
}
```

Create `internal/domain/enrollment/fixtures_test.go`:

```go
package enrollment

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fixture is two institutions plus the people needed to exercise scope rules:
//
//	InstitutionID       — holds GroupID, TeacherID, StudentID and one unclaimed roster row
//	OtherInstitutionID  — used to test cross-institution isolation
//	TeacherID           — assigned to GroupID
//	LonerTeacherID      — assigned to no group
//	StudentID           — a claimed, active student in GroupID
//	SoloStudentID       — a student with no enrollment at all
//	UnclaimedEnrollmentID / ClaimCode — a pending_claim roster row
type fixture struct {
	InstitutionID         string
	OtherInstitutionID    string
	TeacherID             string
	LonerTeacherID        string
	StudentID             string
	SoloStudentID         string
	StudentEnrollmentID   string
	UnclaimedEnrollmentID string
	ClaimCode             string
	GroupID               string
}

func seedFixture(t *testing.T, pool *pgxpool.Pool) fixture {
	t.Helper()
	ctx := context.Background()
	// A per-run suffix keeps unique constraints on email, referral and claim
	// codes satisfied when the suite runs twice against the same scratch DB.
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var f fixture

	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}

	newInst := func(label string, dest *string) {
		t.Helper()
		must(label, pool.QueryRow(ctx, `
			INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
			VALUES ($1||' '||$2, 'school', $1||'-'||$2||'@example.test', 'S'||$1||$2, 'T'||$1||$2, 'verified')
			RETURNING id`, label, tag).Scan(dest))
	}
	newInst("main", &f.InstitutionID)
	newInst("other", &f.OtherInstitutionID)

	newUser := func(role, label string, instID *string, dest *string) {
		t.Helper()
		must(label, pool.QueryRow(ctx, `
			INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
			VALUES (gen_random_uuid(), $1, $1, $2, $3, $4)
			RETURNING id`,
			label+" "+tag, label+"-"+tag+"@example.test", role, instID).Scan(dest))
	}
	newUser("teacher", "assigned-teacher", &f.InstitutionID, &f.TeacherID)
	newUser("teacher", "loner-teacher", &f.InstitutionID, &f.LonerTeacherID)
	newUser("student", "group-student", &f.InstitutionID, &f.StudentID)
	newUser("student", "solo-student", nil, &f.SoloStudentID)

	must("group", pool.QueryRow(ctx, `
		INSERT INTO groups (institution_id, name, invite_code)
		VALUES ($1, 'Fixture Class', 'INV'||$2)
		RETURNING id`, f.InstitutionID, tag).Scan(&f.GroupID))

	_, err := pool.Exec(ctx,
		`INSERT INTO group_teachers (group_id, user_id) VALUES ($1, $2)`, f.GroupID, f.TeacherID)
	must("group_teachers", err)
	_, err = pool.Exec(ctx,
		`INSERT INTO group_students (group_id, user_id) VALUES ($1, $2)`, f.GroupID, f.StudentID)
	must("group_students", err)

	must("claimed enrollment", pool.QueryRow(ctx, `
		INSERT INTO enrollments (institution_id, user_id, full_name, roll_number, grade, section, status, joined_at)
		VALUES ($1, $2, 'group-student', 'R-'||$3, '9', 'A', 'active', now())
		RETURNING id`, f.InstitutionID, f.StudentID, tag).Scan(&f.StudentEnrollmentID))

	f.ClaimCode = "CLAIM" + tag
	must("unclaimed enrollment", pool.QueryRow(ctx, `
		INSERT INTO enrollments (institution_id, full_name, email, roll_number, grade, section, claim_code, status)
		VALUES ($1, 'Unclaimed Student', $2, 'U-'||$3, '9', 'B', $4, 'pending_claim')
		RETURNING id`,
		f.InstitutionID, "unclaimed-"+tag+"@example.test", tag, f.ClaimCode).Scan(&f.UnclaimedEnrollmentID))

	t.Cleanup(func() {
		ctx := context.Background()
		users := []string{f.TeacherID, f.LonerTeacherID, f.StudentID, f.SoloStudentID}
		insts := []string{f.InstitutionID, f.OtherInstitutionID}
		pool.Exec(ctx, `DELETE FROM student_edit_requests WHERE enrollment_id IN
			(SELECT id FROM enrollments WHERE institution_id = ANY($1))`, insts)
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id = ANY($1)`, insts)
		pool.Exec(ctx, `DELETE FROM user_profile_entries WHERE user_id = ANY($1)`, users)
		pool.Exec(ctx, `DELETE FROM groups WHERE institution_id = ANY($1)`, insts)
		pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, users)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id = ANY($1)`, insts)
	})

	return f
}
```

- [ ] **Step 4: Write the failing constraint tests**

Create `internal/domain/enrollment/schema_test.go`:

```go
package enrollment

import (
	"context"
	"testing"
)

// A student may hold only one live enrollment, but any number of unredeemed
// roster rows may point at them across institutions.
func TestOneActiveEnrollmentPerUser(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at)
		VALUES ($1, $2, 'dupe', 'active', now())`, f.OtherInstitutionID, f.StudentID)
	if err == nil {
		t.Fatal("expected a second active enrollment to violate enrollments_one_active_per_user")
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO enrollments (institution_id, full_name, claim_code, status)
		VALUES ($1, 'pre-provisioned', 'OTHERCODE-'||$2, 'pending_claim')`,
		f.OtherInstitutionID, f.StudentID)
	if err != nil {
		t.Fatalf("pending_claim rows must be exempt from the one-active index: %v", err)
	}
}

// Roll numbers collide only among live enrollments; ending one frees its number.
func TestRollNumberUniquePerLiveEnrollment(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()

	var roll string
	if err := pool.QueryRow(ctx,
		`SELECT roll_number FROM enrollments WHERE id=$1`, f.StudentEnrollmentID).Scan(&roll); err != nil {
		t.Fatalf("read roll_number: %v", err)
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO enrollments (institution_id, full_name, roll_number, claim_code, status)
		VALUES ($1, 'collides', $2, 'C-'||$2, 'pending_claim')`, f.InstitutionID, roll)
	if err == nil {
		t.Fatal("expected duplicate roll_number in a live enrollment to be rejected")
	}

	if _, err := pool.Exec(ctx,
		`UPDATE enrollments SET status='graduated', ended_at=now() WHERE id=$1`,
		f.StudentEnrollmentID); err != nil {
		t.Fatalf("graduate: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO enrollments (institution_id, full_name, roll_number, claim_code, status)
		VALUES ($1, 'reuses', $2, 'C2-'||$2, 'pending_claim')`, f.InstitutionID, roll)
	if err != nil {
		t.Fatalf("roll_number must be reusable after the enrollment ends: %v", err)
	}
}
```

- [ ] **Step 5: Run the tests**

Run: `cd qwish-backend && go test ./internal/domain/enrollment/ -run 'TestOneActive|TestRollNumber' -v`
Expected: PASS (the migration from Step 2 is what makes them pass). If `TEST_DATABASE_URL` is unset they SKIP — set it before claiming this task is done.

- [ ] **Step 6: Commit**

```bash
git add migrations/031_student_enrollment.sql internal/domain/enrollment/
git commit -m "feat(enrollment): add enrollments, profile entries and edit request tables"
```

---

### Task 2: Enrollment service — claim and lookup

**Files:**
- Create: `qwish-backend/internal/domain/enrollment/service.go`
- Test: `qwish-backend/internal/domain/enrollment/service_test.go`

**Interfaces:**
- Consumes: `fixture` and `openTestDB` from Task 1.
- Produces:
  - `type Service struct` with `NewService(db *pgxpool.Pool) *Service`
  - `type Enrollment struct` (fields listed in Step 1)
  - `func GenerateClaimCode() (string, error)`
  - `func (s *Service) Claim(ctx context.Context, userID, code string) (Enrollment, error)`
  - `func (s *Service) ActiveByUser(ctx context.Context, userID string) (*Enrollment, error)`
  - errors `ErrClaimCodeInvalid`, `ErrClaimCodeUsed`, `ErrEnrollmentExists`

- [ ] **Step 1: Write the failing tests**

Create `internal/domain/enrollment/service_test.go`:

```go
package enrollment

import (
	"context"
	"errors"
	"testing"
)

func TestClaimAssignsEnrollmentAndInstitution(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	e, err := svc.Claim(ctx, f.SoloStudentID, f.ClaimCode)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if e.Status != "active" {
		t.Fatalf("status = %q, want active", e.Status)
	}
	if e.UserID == nil || *e.UserID != f.SoloStudentID {
		t.Fatalf("user_id = %v, want %s", e.UserID, f.SoloStudentID)
	}

	var instID *string
	pool.QueryRow(ctx, `SELECT institution_id FROM users WHERE id=$1`, f.SoloStudentID).Scan(&instID)
	if instID == nil || *instID != f.InstitutionID {
		t.Fatalf("users.institution_id = %v, want %s", instID, f.InstitutionID)
	}
}

func TestClaimRejectsUsedCode(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	if _, err := svc.Claim(ctx, f.SoloStudentID, f.ClaimCode); err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	_, err := svc.Claim(ctx, f.TeacherID, f.ClaimCode)
	if !errors.Is(err, ErrClaimCodeUsed) {
		t.Fatalf("err = %v, want ErrClaimCodeUsed", err)
	}
}

func TestClaimRejectsAlreadyEnrolledStudent(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	// f.StudentID already holds an active enrollment from the fixture.
	_, err := svc.Claim(context.Background(), f.StudentID, f.ClaimCode)
	if !errors.Is(err, ErrEnrollmentExists) {
		t.Fatalf("err = %v, want ErrEnrollmentExists", err)
	}
}

func TestClaimRejectsUnknownCode(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	_, err := svc.Claim(context.Background(), f.SoloStudentID, "NO-SUCH-CODE")
	if !errors.Is(err, ErrClaimCodeInvalid) {
		t.Fatalf("err = %v, want ErrClaimCodeInvalid", err)
	}
}

// Import-supplied personal values fill only the blanks the student left.
func TestClaimMergesImportValuesWithoutOverwriting(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		UPDATE enrollments SET import_phone='555-import', import_guardian_name='Import Guardian'
		WHERE id=$1`, f.UnclaimedEnrollmentID)
	if err != nil {
		t.Fatalf("stage import values: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE users SET phone='555-mine' WHERE id=$1`, f.SoloStudentID); err != nil {
		t.Fatalf("preset phone: %v", err)
	}

	if _, err := svc.Claim(ctx, f.SoloStudentID, f.ClaimCode); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	var phone, guardian *string
	pool.QueryRow(ctx,
		`SELECT phone, guardian_name FROM users WHERE id=$1`, f.SoloStudentID).Scan(&phone, &guardian)
	if phone == nil || *phone != "555-mine" {
		t.Fatalf("phone = %v, want the student's own value to survive", phone)
	}
	if guardian == nil || *guardian != "Import Guardian" {
		t.Fatalf("guardian_name = %v, want the import value to fill the blank", guardian)
	}
}

func TestActiveByUserReturnsNilForSoloStudent(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	got, err := svc.ActiveByUser(context.Background(), f.SoloStudentID)
	if err != nil {
		t.Fatalf("ActiveByUser: %v", err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil for a student with no enrollment", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd qwish-backend && go test ./internal/domain/enrollment/ -v`
Expected: FAIL to build — `undefined: NewService`, `undefined: ErrClaimCodeUsed`.

- [ ] **Step 3: Write the service**

Create `internal/domain/enrollment/service.go`:

```go
// Package enrollment owns the enrollments table: the relationship between a
// student and an institution. Institution-owned academic fields live here and
// have no student-facing write path, which is what makes them institution-owned.
package enrollment

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrClaimCodeInvalid = errors.New("claim code invalid")
	ErrClaimCodeUsed    = errors.New("claim code already used")
	ErrEnrollmentExists = errors.New("student already holds a live enrollment")
)

type Enrollment struct {
	ID             string     `json:"id"`
	InstitutionID  string     `json:"institution_id"`
	UserID         *string    `json:"user_id,omitempty"`
	FullName       string     `json:"full_name"`
	Email          *string    `json:"email,omitempty"`
	RollNumber     *string    `json:"roll_number,omitempty"`
	Grade          *string    `json:"grade,omitempty"`
	Section        *string    `json:"section,omitempty"`
	AdmissionDate  *time.Time `json:"admission_date,omitempty"`
	ClaimCode      *string    `json:"claim_code,omitempty"`
	Status         string     `json:"status"`
	JoinedAt       *time.Time `json:"joined_at,omitempty"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
}

const selectCols = `id, institution_id, user_id, full_name, email, roll_number,
	grade, section, admission_date, claim_code, status, joined_at, ended_at`

func scanEnrollment(row pgx.Row) (Enrollment, error) {
	var e Enrollment
	err := row.Scan(&e.ID, &e.InstitutionID, &e.UserID, &e.FullName, &e.Email,
		&e.RollNumber, &e.Grade, &e.Section, &e.AdmissionDate, &e.ClaimCode,
		&e.Status, &e.JoinedAt, &e.EndedAt)
	return e, err
}

type Service struct{ db *pgxpool.Pool }

func NewService(db *pgxpool.Pool) *Service { return &Service{db: db} }

// GenerateClaimCode returns a 10-character code from an unambiguous alphabet.
// Codes are read off paper and typed by hand, so base32 (no 0/1/8/I/O) beats hex.
func GenerateClaimCode() (string, error) {
	b := make([]byte, 7)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	return enc[:10], nil
}

// ActiveByUser returns the student's live enrollment, or nil when they have
// none. A student with no institution is a normal user, not an error case.
func (s *Service) ActiveByUser(ctx context.Context, userID string) (*Enrollment, error) {
	e, err := scanEnrollment(s.db.QueryRow(ctx,
		`SELECT `+selectCols+` FROM enrollments
		 WHERE user_id=$1 AND status IN ('active','suspended')`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// Claim binds a pending_claim roster row to an authenticated student.
//
// Import-supplied personal values are copied onto the users row only where the
// student left the field blank — the student's own entry always wins.
func (s *Service) Claim(ctx context.Context, userID, code string) (Enrollment, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Enrollment{}, err
	}
	defer tx.Rollback(ctx)

	var id, status string
	err = tx.QueryRow(ctx,
		`SELECT id, status FROM enrollments WHERE claim_code=$1 FOR UPDATE`, code).Scan(&id, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Enrollment{}, ErrClaimCodeInvalid
	}
	if err != nil {
		return Enrollment{}, err
	}
	if status != "pending_claim" {
		return Enrollment{}, ErrClaimCodeUsed
	}

	var live int
	tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM enrollments
		 WHERE user_id=$1 AND status IN ('active','suspended')`, userID).Scan(&live)
	if live > 0 {
		return Enrollment{}, ErrEnrollmentExists
	}

	e, err := scanEnrollment(tx.QueryRow(ctx,
		`UPDATE enrollments
		    SET user_id=$1, status='active', joined_at=now(), updated_at=now()
		  WHERE id=$2
		  RETURNING `+selectCols, userID, id))
	if err != nil {
		return Enrollment{}, err
	}

	// NULLIF('' ,'') collapses empty strings to NULL so blank-but-present
	// values are treated as blanks, not as the student's answer.
	if _, err := tx.Exec(ctx, `
		UPDATE users u SET
			institution_id = e.institution_id,
			phone          = COALESCE(NULLIF(u.phone,''),          e.import_phone),
			guardian_name  = COALESCE(NULLIF(u.guardian_name,''),  e.import_guardian_name),
			guardian_phone = COALESCE(NULLIF(u.guardian_phone,''), e.import_guardian_phone),
			guardian_email = COALESCE(NULLIF(u.guardian_email,''), e.import_guardian_email),
			updated_at     = now()
		FROM enrollments e
		WHERE u.id=$1 AND e.id=$2`, userID, id); err != nil {
		return Enrollment{}, err
	}

	return e, tx.Commit(ctx)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd qwish-backend && go test ./internal/domain/enrollment/ -v`
Expected: PASS, all six tests.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/enrollment/service.go internal/domain/enrollment/service_test.go
git commit -m "feat(enrollment): claim a roster row by code"
```

---

### Task 3: Student endpoints — claim, join by class code, my enrollment

**Files:**
- Create: `qwish-backend/internal/domain/enrollment/handler_student.go`
- Modify: `qwish-backend/internal/domain/enrollment/service.go` (add `JoinByClassCode`)
- Modify: `qwish-backend/cmd/api/main.go`
- Test: `qwish-backend/internal/domain/enrollment/join_test.go`

**Interfaces:**
- Consumes: `Service`, `Enrollment`, the three errors from Task 2.
- Produces:
  - `func (s *Service) JoinByClassCode(ctx context.Context, userID, inviteCode string) (Enrollment, error)`
  - `type StudentHandler struct` with `NewStudentHandler(svc *Service) *StudentHandler`
  - methods `Claim`, `JoinClass`, `Mine` — all `func(http.ResponseWriter, *http.Request)`

- [ ] **Step 1: Write the failing test**

Create `internal/domain/enrollment/join_test.go`:

```go
package enrollment

import (
	"context"
	"errors"
	"testing"
)

// Joining by class code enrolls the student with academic fields left blank
// for an admin to fill in, and adds them to the class in one step.
func TestJoinByClassCodeCreatesEnrollmentAndMembership(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	var invite string
	if err := pool.QueryRow(ctx,
		`SELECT invite_code FROM groups WHERE id=$1`, f.GroupID).Scan(&invite); err != nil {
		t.Fatalf("read invite_code: %v", err)
	}

	e, err := svc.JoinByClassCode(ctx, f.SoloStudentID, invite)
	if err != nil {
		t.Fatalf("JoinByClassCode: %v", err)
	}
	if e.Status != "active" || e.RollNumber != nil {
		t.Fatalf("got status=%q roll=%v, want active with a blank roll number", e.Status, e.RollNumber)
	}

	var member int
	pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM group_students WHERE group_id=$1 AND user_id=$2`,
		f.GroupID, f.SoloStudentID).Scan(&member)
	if member != 1 {
		t.Fatalf("group_students rows = %d, want 1", member)
	}
}

func TestJoinByClassCodeRejectsAlreadyEnrolled(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	var invite string
	pool.QueryRow(ctx, `SELECT invite_code FROM groups WHERE id=$1`, f.GroupID).Scan(&invite)

	_, err := svc.JoinByClassCode(ctx, f.StudentID, invite)
	if !errors.Is(err, ErrEnrollmentExists) {
		t.Fatalf("err = %v, want ErrEnrollmentExists", err)
	}
}

func TestJoinByClassCodeRejectsUnknownCode(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	_, err := svc.JoinByClassCode(context.Background(), f.SoloStudentID, "NOPE")
	if !errors.Is(err, ErrClassCodeInvalid) {
		t.Fatalf("err = %v, want ErrClassCodeInvalid", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd qwish-backend && go test ./internal/domain/enrollment/ -run TestJoinByClassCode -v`
Expected: FAIL to build — `undefined: ErrClassCodeInvalid`, `svc.JoinByClassCode undefined`.

- [ ] **Step 3: Add JoinByClassCode to the service**

Append to `internal/domain/enrollment/service.go`, and add `ErrClassCodeInvalid` to the existing `var (...)` error block:

```go
var ErrClassCodeInvalid = errors.New("class invite code invalid")

// JoinByClassCode is the self-signup path: a student with no institution joins
// a class directly. Academic fields stay blank for an admin to fill in later.
func (s *Service) JoinByClassCode(ctx context.Context, userID, inviteCode string) (Enrollment, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Enrollment{}, err
	}
	defer tx.Rollback(ctx)

	var groupID, instID string
	err = tx.QueryRow(ctx,
		`SELECT id, institution_id FROM groups WHERE invite_code=$1 AND archived_at IS NULL`,
		inviteCode).Scan(&groupID, &instID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Enrollment{}, ErrClassCodeInvalid
	}
	if err != nil {
		return Enrollment{}, err
	}

	var live int
	tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM enrollments
		 WHERE user_id=$1 AND status IN ('active','suspended')`, userID).Scan(&live)
	if live > 0 {
		return Enrollment{}, ErrEnrollmentExists
	}

	var fullName string
	if err := tx.QueryRow(ctx, `SELECT full_name FROM users WHERE id=$1`, userID).Scan(&fullName); err != nil {
		return Enrollment{}, err
	}

	e, err := scanEnrollment(tx.QueryRow(ctx,
		`INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at)
		 VALUES ($1, $2, $3, 'active', now())
		 RETURNING `+selectCols, instID, userID, fullName))
	if err != nil {
		return Enrollment{}, err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO group_students (group_id, user_id) VALUES ($1,$2)
		 ON CONFLICT DO NOTHING`, groupID, userID); err != nil {
		return Enrollment{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET institution_id=$1, updated_at=now() WHERE id=$2`, instID, userID); err != nil {
		return Enrollment{}, err
	}

	return e, tx.Commit(ctx)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd qwish-backend && go test ./internal/domain/enrollment/ -run TestJoinByClassCode -v`
Expected: PASS, all three tests.

- [ ] **Step 5: Write the student handler**

Create `internal/domain/enrollment/handler_student.go`:

```go
package enrollment

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/qwish/backend/internal/middleware"
)

type StudentHandler struct{ svc *Service }

func NewStudentHandler(svc *Service) *StudentHandler { return &StudentHandler{svc: svc} }

// POST /api/v1/students/claim  {claim_code}
func (h *StudentHandler) Claim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClaimCode string `json:"claim_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ClaimCode == "" {
		middleware.BadRequest(w, "claim_code is required")
		return
	}

	e, err := h.svc.Claim(r.Context(), middleware.GetUserID(r), req.ClaimCode)
	switch {
	case errors.Is(err, ErrClaimCodeInvalid):
		middleware.Error(w, http.StatusBadRequest, "CLAIM_CODE_INVALID", "this code is not valid")
		return
	case errors.Is(err, ErrClaimCodeUsed):
		middleware.Error(w, http.StatusConflict, "CLAIM_CODE_USED", "this code has already been used")
		return
	case errors.Is(err, ErrEnrollmentExists):
		middleware.Error(w, http.StatusConflict, "ENROLLMENT_EXISTS",
			"you are already enrolled at an institution; leave it before joining another")
		return
	case err != nil:
		log.Printf("Claim: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, e)
}

// POST /api/v1/students/join-class  {invite_code}
func (h *StudentHandler) JoinClass(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InviteCode string `json:"invite_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.InviteCode == "" {
		middleware.BadRequest(w, "invite_code is required")
		return
	}

	e, err := h.svc.JoinByClassCode(r.Context(), middleware.GetUserID(r), req.InviteCode)
	switch {
	case errors.Is(err, ErrClassCodeInvalid):
		middleware.Error(w, http.StatusBadRequest, "CLAIM_CODE_INVALID", "this class code is not valid")
		return
	case errors.Is(err, ErrEnrollmentExists):
		middleware.Error(w, http.StatusConflict, "ENROLLMENT_EXISTS",
			"you are already enrolled at an institution; leave it before joining another")
		return
	case err != nil:
		log.Printf("JoinClass: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, e)
}

// GET /api/v1/users/me/enrollment
//
// Returns null for a student with no institution. numpie keys its shell off
// this: null hides institution navigation and shows the join prompt.
func (h *StudentHandler) Mine(w http.ResponseWriter, r *http.Request) {
	e, err := h.svc.ActiveByUser(r.Context(), middleware.GetUserID(r))
	if err != nil {
		log.Printf("Mine: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, e)
}
```

- [ ] **Step 6: Wire the routes**

In `cmd/api/main.go`, add the import `"github.com/qwish/backend/internal/domain/enrollment"`, construct the handler next to the other handler constructions:

```go
enrollmentSvc := enrollment.NewService(pool)
enrollmentStudentH := enrollment.NewStudentHandler(enrollmentSvc)
```

Then register the routes inside the authenticated block that already carries `r.Get("/users/me/following", ...)` (around `cmd/api/main.go:288`):

```go
r.Post("/students/claim", enrollmentStudentH.Claim)
r.Post("/students/join-class", enrollmentStudentH.JoinClass)
r.Get("/users/me/enrollment", enrollmentStudentH.Mine)
```

- [ ] **Step 7: Give the referral-code signup path an enrollment**

`auth.CreateProfile` (`internal/domain/auth/handler.go:136`) sets
`users.institution_id` from a student referral code but creates no enrollment,
so a student who signs up that way would be invisible to every roster query.

In the `case req.ReferralCode != "":` branch, after `CreateUser` succeeds, add:

```go
	// A referral-code signup is a real enrollment; without one the student
	// carries an institution_id that no roster query would ever surface.
	if instID != nil && role == "student" {
		if _, err := h.svc.CreateStudentEnrollment(r.Context(), *instID, newUser.ID, req.FullName); err != nil {
			log.Printf("CreateProfile: enrollment for %s: %v", newUser.ID, err)
		}
	}
```

Add to the auth service (`internal/domain/auth/service.go`):

```go
// CreateStudentEnrollment records a referral-code signup as an active
// enrollment with the academic fields left blank for an admin to fill in.
func (s *Service) CreateStudentEnrollment(ctx context.Context, instID, userID, fullName string) (string, error) {
	var id string
	err := s.db.QueryRow(ctx,
		`INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at)
		 VALUES ($1,$2,$3,'active',now()) RETURNING id`, instID, userID, fullName).Scan(&id)
	return id, err
}
```

Match the receiver name and pool field already used in that file — check with
`grep -n "type Service struct" -A 5 internal/domain/auth/service.go` first.

- [ ] **Step 8: Verify the build**

Run: `cd qwish-backend && go build ./... && go vet ./...`
Expected: no output.

- [ ] **Step 9: Commit**

```bash
git add internal/domain/enrollment/ internal/domain/auth/ cmd/api/main.go
git commit -m "feat(enrollment): student claim and join-by-class-code endpoints"
```

---

### Task 4: Student-owned personal fields on PATCH /users/me

**Files:**
- Modify: `qwish-backend/internal/domain/user/handler.go`
- Test: `qwish-backend/internal/domain/user/personal_fields_test.go`

**Interfaces:**
- Consumes: the `users` columns added in Task 1.
- Produces: `PATCH /api/v1/users/me` accepting `date_of_birth`, `gender`, `phone`, `address`, `guardian_name`, `guardian_phone`, `guardian_email`, `highest_qualification`; and `buildUserPatch(req personalFields) (string, []interface{})` for the test to exercise directly.

- [ ] **Step 1: Read the existing handler**

Run: `cd qwish-backend && grep -n "func (h \*Handler) UpdateMe" -A 60 internal/domain/user/handler.go`
This shows the current field list and update-building style. Match it; do not restructure it.

- [ ] **Step 2: Write the failing test**

Create `internal/domain/user/personal_fields_test.go`:

```go
package user

import "testing"

// Only the fields the client actually sent are written. A nil pointer means
// "not supplied" and must not blank an existing value.
func TestBuildUserPatchOnlyIncludesSuppliedFields(t *testing.T) {
	phone := "555-0100"
	req := personalFields{Phone: &phone}

	set, args := buildUserPatch(req)
	if set != "phone=$1" {
		t.Fatalf("set = %q, want phone=$1", set)
	}
	if len(args) != 1 || args[0] != phone {
		t.Fatalf("args = %v, want [%s]", args, phone)
	}
}

func TestBuildUserPatchEmptyRequest(t *testing.T) {
	set, args := buildUserPatch(personalFields{})
	if set != "" || len(args) != 0 {
		t.Fatalf("set=%q args=%v, want empty for a no-op patch", set, args)
	}
}

func TestBuildUserPatchNumbersFieldsInOrder(t *testing.T) {
	gender, guardian := "female", "A Guardian"
	set, args := buildUserPatch(personalFields{Gender: &gender, GuardianName: &guardian})
	if set != "gender=$1, guardian_name=$2" {
		t.Fatalf("set = %q", set)
	}
	if len(args) != 2 {
		t.Fatalf("args = %v, want two", args)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd qwish-backend && go test ./internal/domain/user/ -run TestBuildUserPatch -v`
Expected: FAIL to build — `undefined: personalFields`, `undefined: buildUserPatch`.

- [ ] **Step 4: Implement**

Add to `internal/domain/user/handler.go`:

```go
// personalFields are the student-owned columns on users. Pointers distinguish
// "not supplied" from "supplied as empty", so a partial patch never blanks a
// field the client did not mention.
type personalFields struct {
	DateOfBirth          *string `json:"date_of_birth"`
	Gender               *string `json:"gender"`
	Phone                *string `json:"phone"`
	Address              *string `json:"address"`
	GuardianName         *string `json:"guardian_name"`
	GuardianPhone        *string `json:"guardian_phone"`
	GuardianEmail        *string `json:"guardian_email"`
	HighestQualification *string `json:"highest_qualification"`
}

// buildUserPatch returns the SET clause and its arguments for the supplied
// fields, numbered from $1.
func buildUserPatch(req personalFields) (string, []interface{}) {
	cols := []struct {
		name string
		val  *string
	}{
		{"date_of_birth", req.DateOfBirth},
		{"gender", req.Gender},
		{"phone", req.Phone},
		{"address", req.Address},
		{"guardian_name", req.GuardianName},
		{"guardian_phone", req.GuardianPhone},
		{"guardian_email", req.GuardianEmail},
		{"highest_qualification", req.HighestQualification},
	}
	var set []string
	var args []interface{}
	for _, c := range cols {
		if c.val == nil {
			continue
		}
		args = append(args, *c.val)
		set = append(set, fmt.Sprintf("%s=$%d", c.name, len(args)))
	}
	return strings.Join(set, ", "), args
}
```

Ensure `fmt` and `strings` are imported. Then, inside the existing `UpdateMe` handler, after its current field handling, decode the same body into `personalFields` and apply the patch:

```go
	// Re-read the body into the personal-field shape. UpdateMe has already
	// consumed r.Body, so decode from the bytes it buffered.
	var pf personalFields
	if err := json.Unmarshal(bodyBytes, &pf); err == nil {
		if set, args := buildUserPatch(pf); set != "" {
			args = append(args, userID)
			h.db.Exec(r.Context(),
				fmt.Sprintf(`UPDATE users SET %s, updated_at=now() WHERE id=$%d`, set, len(args)),
				args...)
		}
	}
```

If `UpdateMe` currently decodes straight from `r.Body`, change it to read the body once into `bodyBytes` with `io.ReadAll(r.Body)` and `json.Unmarshal` both shapes from it. Import `io`.

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd qwish-backend && go test ./internal/domain/user/ -run TestBuildUserPatch -v`
Expected: PASS, all three tests.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/user/
git commit -m "feat(user): accept student-owned personal fields on PATCH /users/me"
```

---

### Task 5: CV profile entries CRUD

**Files:**
- Create: `qwish-backend/internal/domain/user/profile_entries.go`
- Modify: `qwish-backend/cmd/api/main.go`
- Test: `qwish-backend/internal/domain/user/profile_entries_test.go`

**Interfaces:**
- Consumes: `user_profile_entries` from Task 1.
- Produces: `type ProfileEntryHandler struct` with `NewProfileEntryHandler(db *pgxpool.Pool) *ProfileEntryHandler` and methods `List`, `Create`, `Update`, `Delete`; plus `func validKind(kind string) bool`.

- [ ] **Step 1: Write the failing test**

Create `internal/domain/user/profile_entries_test.go`:

```go
package user

import "testing"

func TestValidKind(t *testing.T) {
	for _, k := range []string{"experience", "certification", "achievement", "course"} {
		if !validKind(k) {
			t.Fatalf("validKind(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"", "education", "skill", "EXPERIENCE"} {
		if validKind(k) {
			t.Fatalf("validKind(%q) = true, want false", k)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd qwish-backend && go test ./internal/domain/user/ -run TestValidKind -v`
Expected: FAIL to build — `undefined: validKind`.

- [ ] **Step 3: Implement**

Create `internal/domain/user/profile_entries.go`:

```go
package user

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

// Education and skills already have their own tables from 003_profile_features.
// Everything else on a student's CV shares one shape, so it shares one table.
var profileEntryKinds = map[string]bool{
	"experience": true, "certification": true, "achievement": true, "course": true,
}

func validKind(kind string) bool { return profileEntryKinds[kind] }

type ProfileEntryHandler struct{ db *pgxpool.Pool }

func NewProfileEntryHandler(db *pgxpool.Pool) *ProfileEntryHandler {
	return &ProfileEntryHandler{db: db}
}

type profileEntry struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Title       string     `json:"title"`
	Org         *string    `json:"org,omitempty"`
	StartDate   *time.Time `json:"start_date,omitempty"`
	EndDate     *time.Time `json:"end_date,omitempty"`
	Description *string    `json:"description,omitempty"`
}

// GET /api/v1/users/me/profile-entries?kind=experience
func (h *ProfileEntryHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	kind := r.URL.Query().Get("kind")
	if kind != "" && !validKind(kind) {
		middleware.BadRequest(w, "unknown kind")
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, kind, title, org, start_date, end_date, description
		   FROM user_profile_entries
		  WHERE user_id=$1 AND ($2='' OR kind=$2)
		  ORDER BY COALESCE(start_date, '1900-01-01') DESC, created_at DESC`, userID, kind)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	entries := []profileEntry{}
	for rows.Next() {
		var e profileEntry
		rows.Scan(&e.ID, &e.Kind, &e.Title, &e.Org, &e.StartDate, &e.EndDate, &e.Description)
		entries = append(entries, e)
	}
	middleware.JSON(w, http.StatusOK, entries)
}

type profileEntryInput struct {
	Kind        string  `json:"kind"`
	Title       string  `json:"title"`
	Org         *string `json:"org"`
	StartDate   *string `json:"start_date"`
	EndDate     *string `json:"end_date"`
	Description *string `json:"description"`
}

// POST /api/v1/users/me/profile-entries
func (h *ProfileEntryHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in profileEntryInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if !validKind(in.Kind) {
		middleware.BadRequest(w, "kind must be one of experience, certification, achievement, course")
		return
	}
	if in.Title == "" {
		middleware.BadRequest(w, "title is required")
		return
	}

	var id string
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO user_profile_entries (user_id, kind, title, org, start_date, end_date, description)
		 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		middleware.GetUserID(r), in.Kind, in.Title, in.Org, in.StartDate, in.EndDate, in.Description).Scan(&id)
	if err != nil {
		log.Printf("profile entry create: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": id})
}

// PATCH /api/v1/users/me/profile-entries/{entryId}
func (h *ProfileEntryHandler) Update(w http.ResponseWriter, r *http.Request) {
	var in profileEntryInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if in.Title == "" {
		middleware.BadRequest(w, "title is required")
		return
	}

	// The user_id predicate is the authorization check: a student can only
	// touch their own entries.
	tag, err := h.db.Exec(r.Context(),
		`UPDATE user_profile_entries
		    SET title=$1, org=$2, start_date=$3, end_date=$4, description=$5
		  WHERE id=$6 AND user_id=$7`,
		in.Title, in.Org, in.StartDate, in.EndDate, in.Description,
		chi.URLParam(r, "entryId"), middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "profile entry")
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// DELETE /api/v1/users/me/profile-entries/{entryId}
func (h *ProfileEntryHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tag, err := h.db.Exec(r.Context(),
		`DELETE FROM user_profile_entries WHERE id=$1 AND user_id=$2`,
		chi.URLParam(r, "entryId"), middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "profile entry")
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd qwish-backend && go test ./internal/domain/user/ -run TestValidKind -v`
Expected: PASS.

- [ ] **Step 5: Wire the routes**

In `cmd/api/main.go`, next to the other user routes:

```go
profileEntryH := user.NewProfileEntryHandler(pool)
...
r.Get("/users/me/profile-entries", profileEntryH.List)
r.Post("/users/me/profile-entries", profileEntryH.Create)
r.Patch("/users/me/profile-entries/{entryId}", profileEntryH.Update)
r.Delete("/users/me/profile-entries/{entryId}", profileEntryH.Delete)
```

- [ ] **Step 6: Verify the build and commit**

Run: `cd qwish-backend && go build ./... && go vet ./...`
Expected: no output.

```bash
git add internal/domain/user/profile_entries.go internal/domain/user/profile_entries_test.go cmd/api/main.go
git commit -m "feat(user): CV profile entries CRUD"
```

---

### Task 6: Institution roster CRUD and repointed student queries

**Files:**
- Create: `qwish-backend/internal/domain/enrollment/handler_institution.go`
- Modify: `qwish-backend/internal/domain/enrollment/service.go`
- Modify: `qwish-backend/internal/domain/institution/handler.go` (`ListStudents`, `GetStudent`)
- Modify: `qwish-backend/cmd/api/main.go`
- Test: `qwish-backend/internal/domain/enrollment/roster_test.go`

**Interfaces:**
- Consumes: `Service`, `Enrollment` from Task 2.
- Produces:
  - `type RosterInput struct { FullName, Email, RollNumber, Grade, Section, AdmissionDate, Phone, GuardianName, GuardianPhone, GuardianEmail string }`
  - `func (s *Service) CreateRosterEntry(ctx context.Context, instID string, in RosterInput) (Enrollment, error)`
  - `func (s *Service) UpdateRosterEntry(ctx context.Context, instID, enrollmentID string, in RosterInput) error`
  - `ErrRollNumberTaken`
  - `type InstitutionHandler struct` with `NewInstitutionHandler(svc *Service, db *pgxpool.Pool) *InstitutionHandler` and methods `CreateStudent`, `UpdateStudent`

- [ ] **Step 1: Write the failing test**

Create `internal/domain/enrollment/roster_test.go`:

```go
package enrollment

import (
	"context"
	"errors"
	"testing"
)

func TestCreateRosterEntryGeneratesClaimCode(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	e, err := svc.CreateRosterEntry(context.Background(), f.InstitutionID, RosterInput{
		FullName: "New Student", RollNumber: "NEW-1", Grade: "10", Section: "C",
	})
	if err != nil {
		t.Fatalf("CreateRosterEntry: %v", err)
	}
	if e.Status != "pending_claim" {
		t.Fatalf("status = %q, want pending_claim", e.Status)
	}
	if e.ClaimCode == nil || len(*e.ClaimCode) != 10 {
		t.Fatalf("claim_code = %v, want a 10-character code", e.ClaimCode)
	}
	if e.UserID != nil {
		t.Fatalf("user_id = %v, want nil before the row is claimed", e.UserID)
	}
}

func TestCreateRosterEntryRejectsDuplicateRollNumber(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	var roll string
	pool.QueryRow(ctx, `SELECT roll_number FROM enrollments WHERE id=$1`, f.StudentEnrollmentID).Scan(&roll)

	_, err := svc.CreateRosterEntry(ctx, f.InstitutionID, RosterInput{FullName: "Dupe", RollNumber: roll})
	if !errors.Is(err, ErrRollNumberTaken) {
		t.Fatalf("err = %v, want ErrRollNumberTaken", err)
	}
}

// A transferred-in student's previous school's attempts must not count toward
// the new institution's numbers. This is the query shape Step 6 installs.
func TestInstitutionStatsExcludeAttemptsBeforeJoining(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()

	var quizID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO quizzes (institution_id, created_by, title, type, status)
		VALUES ($1, $2, 'Scope Quiz', 'knowledge_check', 'published')
		RETURNING id`, f.InstitutionID, f.TeacherID).Scan(&quizID); err != nil {
		t.Fatalf("seed quiz: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id=$1`, quizID)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, quizID)
	})

	// One attempt before the student joined, one after.
	for _, offset := range []string{"-10 days", "-1 day"} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO quiz_attempts (quiz_id, user_id, status, score_pct, completed_at)
			VALUES ($1, $2, 'completed', 80, now() + $3::interval)`,
			quizID, f.StudentID, offset); err != nil {
			t.Fatalf("seed attempt %s: %v", offset, err)
		}
	}
	if _, err := pool.Exec(ctx,
		`UPDATE enrollments SET joined_at = now() - interval '5 days' WHERE id=$1`,
		f.StudentEnrollmentID); err != nil {
		t.Fatalf("backdate joined_at: %v", err)
	}

	var counted int
	pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM quiz_attempts qa
		  JOIN enrollments e ON e.user_id = qa.user_id
		 WHERE e.id=$1 AND qa.status='completed'
		   AND qa.completed_at >= COALESCE(e.joined_at, '-infinity'::timestamptz)`,
		f.StudentEnrollmentID).Scan(&counted)
	if counted != 1 {
		t.Fatalf("counted %d attempts, want 1 — attempts predating joined_at leaked in", counted)
	}
}

// An institution may only edit its own roster rows.
func TestUpdateRosterEntryIsInstitutionScoped(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	err := svc.UpdateRosterEntry(context.Background(), f.OtherInstitutionID, f.StudentEnrollmentID,
		RosterInput{FullName: "Hijacked", Grade: "12"})
	if err == nil {
		t.Fatal("expected an update from another institution to fail")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd qwish-backend && go test ./internal/domain/enrollment/ -run Roster -v`
Expected: FAIL to build — `undefined: RosterInput`, `undefined: ErrRollNumberTaken`.

- [ ] **Step 3: Implement the service methods**

Append to `internal/domain/enrollment/service.go` (add `ErrRollNumberTaken` and `ErrNotFound` to the error block, and import `"github.com/jackc/pgx/v5/pgconn"`):

```go
var (
	ErrRollNumberTaken = errors.New("roll number already in use")
	ErrNotFound        = errors.New("enrollment not found")
)

// RosterInput is the institution-owned half of a student record. Empty strings
// mean "not supplied" and are stored as NULL.
type RosterInput struct {
	FullName      string
	Email         string
	RollNumber    string
	Grade         string
	Section       string
	AdmissionDate string // YYYY-MM-DD
	Phone         string
	GuardianName  string
	GuardianPhone string
	GuardianEmail string
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// isUniqueViolation reports whether err is a Postgres 23505 on the given index.
func isUniqueViolation(err error, index string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == index
}

func (s *Service) CreateRosterEntry(ctx context.Context, instID string, in RosterInput) (Enrollment, error) {
	code, err := GenerateClaimCode()
	if err != nil {
		return Enrollment{}, err
	}

	e, err := scanEnrollment(s.db.QueryRow(ctx,
		`INSERT INTO enrollments
			(institution_id, full_name, email, roll_number, grade, section, admission_date,
			 import_phone, import_guardian_name, import_guardian_phone, import_guardian_email,
			 claim_code, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'pending_claim')
		 RETURNING `+selectCols,
		instID, in.FullName, nilIfEmpty(in.Email), nilIfEmpty(in.RollNumber),
		nilIfEmpty(in.Grade), nilIfEmpty(in.Section), nilIfEmpty(in.AdmissionDate),
		nilIfEmpty(in.Phone), nilIfEmpty(in.GuardianName), nilIfEmpty(in.GuardianPhone),
		nilIfEmpty(in.GuardianEmail), code))
	if isUniqueViolation(err, "enrollments_roll_unique") {
		return Enrollment{}, ErrRollNumberTaken
	}
	return e, err
}

// UpdateRosterEntry writes the institution-owned fields. The institution_id
// predicate is the authorization check.
func (s *Service) UpdateRosterEntry(ctx context.Context, instID, enrollmentID string, in RosterInput) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE enrollments
		    SET full_name=$1, email=$2, roll_number=$3, grade=$4, section=$5,
		        admission_date=$6, updated_at=now()
		  WHERE id=$7 AND institution_id=$8`,
		in.FullName, nilIfEmpty(in.Email), nilIfEmpty(in.RollNumber), nilIfEmpty(in.Grade),
		nilIfEmpty(in.Section), nilIfEmpty(in.AdmissionDate), enrollmentID, instID)
	if isUniqueViolation(err, "enrollments_roll_unique") {
		return ErrRollNumberTaken
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd qwish-backend && go test ./internal/domain/enrollment/ -run Roster -v`
Expected: PASS, all three tests.

- [ ] **Step 5: Write the institution handler**

Create `internal/domain/enrollment/handler_institution.go`:

```go
package enrollment

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

type InstitutionHandler struct {
	svc *Service
	db  *pgxpool.Pool
}

func NewInstitutionHandler(svc *Service, db *pgxpool.Pool) *InstitutionHandler {
	return &InstitutionHandler{svc: svc, db: db}
}

type rosterRequest struct {
	FullName      string `json:"full_name"`
	Email         string `json:"email"`
	RollNumber    string `json:"roll_number"`
	Grade         string `json:"grade"`
	Section       string `json:"section"`
	AdmissionDate string `json:"admission_date"`
	Phone         string `json:"phone"`
	GuardianName  string `json:"guardian_name"`
	GuardianPhone string `json:"guardian_phone"`
	GuardianEmail string `json:"guardian_email"`
}

func (r rosterRequest) toInput() RosterInput {
	return RosterInput{
		FullName: r.FullName, Email: r.Email, RollNumber: r.RollNumber,
		Grade: r.Grade, Section: r.Section, AdmissionDate: r.AdmissionDate,
		Phone: r.Phone, GuardianName: r.GuardianName,
		GuardianPhone: r.GuardianPhone, GuardianEmail: r.GuardianEmail,
	}
}

// POST /api/v1/institution/students
func (h *InstitutionHandler) CreateStudent(w http.ResponseWriter, r *http.Request) {
	var req rosterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if req.FullName == "" {
		middleware.BadRequest(w, "full_name is required")
		return
	}

	e, err := h.svc.CreateRosterEntry(r.Context(), middleware.GetInstitutionID(r), req.toInput())
	if errors.Is(err, ErrRollNumberTaken) {
		middleware.Error(w, http.StatusConflict, "ROLL_NUMBER_TAKEN",
			"another live enrollment already uses this roll number")
		return
	}
	if err != nil {
		log.Printf("CreateStudent: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, e)
}

// PATCH /api/v1/institution/enrollments/{enrollmentId}
func (h *InstitutionHandler) UpdateStudent(w http.ResponseWriter, r *http.Request) {
	var req rosterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if req.FullName == "" {
		middleware.BadRequest(w, "full_name is required")
		return
	}

	err := h.svc.UpdateRosterEntry(r.Context(), middleware.GetInstitutionID(r),
		chi.URLParam(r, "enrollmentId"), req.toInput())
	switch {
	case errors.Is(err, ErrRollNumberTaken):
		middleware.Error(w, http.StatusConflict, "ROLL_NUMBER_TAKEN",
			"another live enrollment already uses this roll number")
		return
	case errors.Is(err, ErrNotFound):
		middleware.NotFound(w, "student")
		return
	case err != nil:
		log.Printf("UpdateStudent: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
```

- [ ] **Step 6: Repoint the existing institution student queries**

In `internal/domain/institution/handler.go`, `ListStudents` currently selects from `users u` with `where := "u.institution_id=$1 AND u.role='student' AND u.deleted_at IS NULL"`. Change the source to join enrollments, so unclaimed roster rows appear and graduated students disappear:

```go
	// Students are listed through their live enrollment: unclaimed roster rows
	// appear (user_id IS NULL), graduated and transferred students do not.
	where := `e.institution_id=$1 AND e.status IN ('pending_claim','active','suspended')`
```

and change the two queries' `FROM` clauses to:

```go
	`SELECT COUNT(*) FROM enrollments e LEFT JOIN users u ON u.id = e.user_id WHERE ` + where
```

```go
	`SELECT e.id, e.user_id, COALESCE(u.display_name, e.full_name), COALESCE(u.email, e.email, ''),
	        e.roll_number, e.grade, e.section, e.status,
	        COALESCE(u.total_points,0), COALESCE(u.current_streak,0), u.last_active_at,
	        COALESCE((SELECT AVG(score_pct) FROM quiz_attempts
	                   WHERE user_id=e.user_id AND status='completed'
	                     AND completed_at >= COALESCE(e.joined_at, '-infinity'::timestamptz)),0) AS avg_score
	   FROM enrollments e LEFT JOIN users u ON u.id = e.user_id
	  WHERE ` + where + ` ORDER BY ` + sortCol + fmt.Sprintf(` LIMIT $%d OFFSET $%d`, n, n+1)
```

Extend `studentRow` to match:

```go
	type studentRow struct {
		EnrollmentID  string     `json:"enrollment_id"`
		ID            *string    `json:"id"` // null until the row is claimed
		DisplayName   string     `json:"display_name"`
		Email         string     `json:"email"`
		RollNumber    *string    `json:"roll_number,omitempty"`
		Grade         *string    `json:"grade,omitempty"`
		Section       *string    `json:"section,omitempty"`
		Status        string     `json:"status"`
		TotalPoints   int64      `json:"total_points"`
		CurrentStreak int        `json:"current_streak"`
		LastActiveAt  *time.Time `json:"last_active_at,omitempty"`
		AverageScore  float64    `json:"average_score"`
	}
```

Update the `rows.Scan` call and the `sortCol` cases to use the `u.`-prefixed columns unchanged (`u.display_name`, `u.total_points DESC`, `avg_score DESC`, `u.last_active_at DESC NULLS LAST`), and change the `search` predicate to `(COALESCE(u.display_name, e.full_name) ILIKE $n OR COALESCE(u.email, e.email) ILIKE $n)`, the `status` predicate to `e.status=$n`, and the `group_id` predicate to `EXISTS (SELECT 1 FROM group_students gs WHERE gs.user_id=e.user_id AND gs.group_id=$n)`.

The `completed_at >= COALESCE(e.joined_at, '-infinity')` clause is what keeps a transferred-in student's previous school's attempts out of this institution's numbers.

- [ ] **Step 7: Wire the routes**

In `cmd/api/main.go`, inside the institution block (near `cmd/api/main.go:393`):

```go
enrollmentInstH := enrollment.NewInstitutionHandler(enrollmentSvc, pool)
...
r.Post("/students", enrollmentInstH.CreateStudent)
r.Patch("/enrollments/{enrollmentId}", enrollmentInstH.UpdateStudent)
```

Enrollment-addressed routes live under `/enrollments/...`, never `/students/...`.
The institute dashboard still calls `PATCH /students/{userId}/status`, and chi
panics when two routes share a shape at the same depth with different parameter
names. A separate prefix avoids the collision outright.

- [ ] **Step 8: Verify and commit**

Run: `cd qwish-backend && go build ./... && go vet ./... && go test ./internal/domain/enrollment/ ./internal/domain/institution/ -v`
Expected: build clean, tests PASS.

```bash
git add internal/domain/enrollment/ internal/domain/institution/handler.go cmd/api/main.go
git commit -m "feat(institution): roster CRUD and enrollment-backed student list"
```

---

### Task 7: CSV import with dry-run preview

**Files:**
- Create: `qwish-backend/internal/domain/enrollment/import.go`
- Modify: `qwish-backend/internal/domain/enrollment/handler_institution.go`
- Modify: `qwish-backend/cmd/api/main.go`
- Test: `qwish-backend/internal/domain/enrollment/import_test.go`

**Interfaces:**
- Consumes: `Service`, `RosterInput`, `CreateRosterEntry` from Task 6.
- Produces:
  - `func ParseCSV(r io.Reader) ([]RosterInput, []RowVerdict, error)`
  - `type RowVerdict struct { Row int; Action string; RollNumber string; FullName string; Reason string }` where `Action` is `create`, `update`, or `error`
  - `func (s *Service) PreviewImport(ctx context.Context, instID string, rows []RosterInput) ([]RowVerdict, error)`
  - `func (s *Service) CommitImport(ctx context.Context, instID string, rows []RosterInput) ([]Enrollment, error)`
  - handler method `ImportStudents`

- [ ] **Step 1: Write the failing tests**

Create `internal/domain/enrollment/import_test.go`:

```go
package enrollment

import (
	"context"
	"strings"
	"testing"
)

const csvHeader = "full_name,email,roll_number,grade,section,admission_date,guardian_name,guardian_phone,guardian_email,phone\n"

func TestParseCSVReadsRowsAndFlagsBadOnes(t *testing.T) {
	in := csvHeader +
		"Alice,alice@example.test,R1,9,A,2024-06-01,Ann,555,ann@example.test,556\n" +
		",bob@example.test,R2,9,A,,,,,\n" + // missing full_name
		"Carol,carol@example.test,R3,9,A,not-a-date,,,,\n" // bad date

	rows, bad, err := ParseCSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(rows) != 1 || rows[0].FullName != "Alice" {
		t.Fatalf("rows = %+v, want only Alice", rows)
	}
	if len(bad) != 2 {
		t.Fatalf("bad = %+v, want two error rows", bad)
	}
	for _, v := range bad {
		if v.Action != "error" || v.Reason == "" {
			t.Fatalf("verdict %+v, want action=error with a reason", v)
		}
	}
}

func TestParseCSVRejectsDuplicateRollNumbersWithinFile(t *testing.T) {
	in := csvHeader +
		"Alice,,DUP,9,A,,,,,\n" +
		"Bob,,DUP,9,A,,,,,\n"

	rows, bad, err := ParseCSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(rows) != 1 || len(bad) != 1 {
		t.Fatalf("rows=%d bad=%d, want the second DUP rejected", len(rows), len(bad))
	}
}

func TestPreviewImportWritesNothing(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	var before int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM enrollments WHERE institution_id=$1`, f.InstitutionID).Scan(&before)

	verdicts, err := svc.PreviewImport(ctx, f.InstitutionID, []RosterInput{
		{FullName: "Preview One", RollNumber: "P-1"},
	})
	if err != nil {
		t.Fatalf("PreviewImport: %v", err)
	}
	if len(verdicts) != 1 || verdicts[0].Action != "create" {
		t.Fatalf("verdicts = %+v, want one create", verdicts)
	}

	var after int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM enrollments WHERE institution_id=$1`, f.InstitutionID).Scan(&after)
	if after != before {
		t.Fatalf("dry run wrote rows: before=%d after=%d", before, after)
	}
}

func TestPreviewImportMarksExistingRollAsUpdate(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	var roll string
	pool.QueryRow(ctx, `SELECT roll_number FROM enrollments WHERE id=$1`, f.StudentEnrollmentID).Scan(&roll)

	verdicts, err := svc.PreviewImport(ctx, f.InstitutionID, []RosterInput{
		{FullName: "Renamed", RollNumber: roll},
	})
	if err != nil {
		t.Fatalf("PreviewImport: %v", err)
	}
	if verdicts[0].Action != "update" {
		t.Fatalf("action = %q, want update", verdicts[0].Action)
	}
}

// A bad row must roll the whole commit back, not half-import the file.
func TestCommitImportIsAllOrNothing(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	var before int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM enrollments WHERE institution_id=$1`, f.InstitutionID).Scan(&before)

	// The second row targets another institution's live roll number only after
	// the first row has already been inserted inside the transaction.
	_, err := svc.CommitImport(ctx, f.InstitutionID, []RosterInput{
		{FullName: "Good", RollNumber: "OK-1"},
		{FullName: "Bad", RollNumber: "OK-1"},
	})
	if err == nil {
		t.Fatal("expected the duplicate roll number to fail the commit")
	}

	var after int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM enrollments WHERE institution_id=$1`, f.InstitutionID).Scan(&after)
	if after != before {
		t.Fatalf("partial import: before=%d after=%d", before, after)
	}
}

func TestCommitImportReturnsClaimCodes(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	created, err := svc.CommitImport(context.Background(), f.InstitutionID, []RosterInput{
		{FullName: "Imported One", RollNumber: "I-1"},
		{FullName: "Imported Two", RollNumber: "I-2"},
	})
	if err != nil {
		t.Fatalf("CommitImport: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("created %d rows, want 2", len(created))
	}
	for _, e := range created {
		if e.ClaimCode == nil || *e.ClaimCode == "" {
			t.Fatalf("enrollment %s has no claim code", e.ID)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd qwish-backend && go test ./internal/domain/enrollment/ -run Import -v`
Expected: FAIL to build — `undefined: ParseCSV`, `undefined: RowVerdict`.

- [ ] **Step 3: Implement**

Create `internal/domain/enrollment/import.go`:

```go
package enrollment

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"
)

// RowVerdict is one row's outcome. In a dry run this is the whole response;
// on commit it is the summary.
type RowVerdict struct {
	Row        int    `json:"row"` // 1-based, counting the header as row 1
	Action     string `json:"action"` // create | update | error
	FullName   string `json:"full_name"`
	RollNumber string `json:"roll_number,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

var importColumns = []string{
	"full_name", "email", "roll_number", "grade", "section", "admission_date",
	"guardian_name", "guardian_phone", "guardian_email", "phone",
}

// ParseCSV reads the roster file, returning the usable rows and a verdict for
// each unusable one. Header order is taken from the file, not assumed, so a
// column the school reordered still lands in the right field.
func ParseCSV(r io.Reader) ([]RosterInput, []RowVerdict, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("read header: %w", err)
	}
	index := map[string]int{}
	for i, h := range header {
		index[strings.ToLower(strings.TrimSpace(h))] = i
	}
	if _, ok := index["full_name"]; !ok {
		return nil, nil, fmt.Errorf("csv must have a full_name column")
	}

	get := func(rec []string, col string) string {
		i, ok := index[col]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	var rows []RosterInput
	var bad []RowVerdict
	seenRolls := map[string]int{}

	for line := 2; ; line++ {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			bad = append(bad, RowVerdict{Row: line, Action: "error", Reason: err.Error()})
			continue
		}

		in := RosterInput{
			FullName:      get(rec, "full_name"),
			Email:         get(rec, "email"),
			RollNumber:    get(rec, "roll_number"),
			Grade:         get(rec, "grade"),
			Section:       get(rec, "section"),
			AdmissionDate: get(rec, "admission_date"),
			GuardianName:  get(rec, "guardian_name"),
			GuardianPhone: get(rec, "guardian_phone"),
			GuardianEmail: get(rec, "guardian_email"),
			Phone:         get(rec, "phone"),
		}

		if in.FullName == "" {
			bad = append(bad, RowVerdict{Row: line, Action: "error", Reason: "full_name is required"})
			continue
		}
		if in.AdmissionDate != "" {
			if _, err := time.Parse("2006-01-02", in.AdmissionDate); err != nil {
				bad = append(bad, RowVerdict{Row: line, Action: "error", FullName: in.FullName,
					Reason: "admission_date must be YYYY-MM-DD"})
				continue
			}
		}
		if in.RollNumber != "" {
			if first, dup := seenRolls[in.RollNumber]; dup {
				bad = append(bad, RowVerdict{Row: line, Action: "error", FullName: in.FullName,
					RollNumber: in.RollNumber,
					Reason:     fmt.Sprintf("roll_number duplicates row %d in this file", first)})
				continue
			}
			seenRolls[in.RollNumber] = line
		}

		rows = append(rows, in)
	}

	return rows, bad, nil
}

// PreviewImport validates rows against the live roster and writes nothing.
func (s *Service) PreviewImport(ctx context.Context, instID string, rows []RosterInput) ([]RowVerdict, error) {
	verdicts := make([]RowVerdict, 0, len(rows))
	for i, in := range rows {
		v := RowVerdict{Row: i + 2, FullName: in.FullName, RollNumber: in.RollNumber, Action: "create"}

		var existing int
		switch {
		case in.RollNumber != "":
			s.db.QueryRow(ctx,
				`SELECT COUNT(*) FROM enrollments
				  WHERE institution_id=$1 AND roll_number=$2 AND ended_at IS NULL`,
				instID, in.RollNumber).Scan(&existing)
		case in.Email != "":
			s.db.QueryRow(ctx,
				`SELECT COUNT(*) FROM enrollments
				  WHERE institution_id=$1 AND email=$2 AND ended_at IS NULL`,
				instID, in.Email).Scan(&existing)
		}
		if existing > 0 {
			v.Action = "update"
		}
		verdicts = append(verdicts, v)
	}
	return verdicts, nil
}

// CommitImport applies every row in one transaction. Rows matching a live
// enrollment are updated in place; the rest are created with a claim code.
func (s *Service) CommitImport(ctx context.Context, instID string, rows []RosterInput) ([]Enrollment, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	created := make([]Enrollment, 0, len(rows))
	for i, in := range rows {
		var existingID string
		if in.RollNumber != "" {
			tx.QueryRow(ctx,
				`SELECT id FROM enrollments
				  WHERE institution_id=$1 AND roll_number=$2 AND ended_at IS NULL`,
				instID, in.RollNumber).Scan(&existingID)
		} else if in.Email != "" {
			tx.QueryRow(ctx,
				`SELECT id FROM enrollments
				  WHERE institution_id=$1 AND email=$2 AND ended_at IS NULL`,
				instID, in.Email).Scan(&existingID)
		}

		if existingID != "" {
			if _, err := tx.Exec(ctx,
				`UPDATE enrollments
				    SET full_name=$1, email=$2, grade=$3, section=$4,
				        admission_date=$5, updated_at=now()
				  WHERE id=$6`,
				in.FullName, nilIfEmpty(in.Email), nilIfEmpty(in.Grade), nilIfEmpty(in.Section),
				nilIfEmpty(in.AdmissionDate), existingID); err != nil {
				return nil, fmt.Errorf("row %d: %w", i+2, err)
			}
			continue
		}

		code, err := GenerateClaimCode()
		if err != nil {
			return nil, err
		}
		e, err := scanEnrollment(tx.QueryRow(ctx,
			`INSERT INTO enrollments
				(institution_id, full_name, email, roll_number, grade, section, admission_date,
				 import_phone, import_guardian_name, import_guardian_phone, import_guardian_email,
				 claim_code, status)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'pending_claim')
			 RETURNING `+selectCols,
			instID, in.FullName, nilIfEmpty(in.Email), nilIfEmpty(in.RollNumber),
			nilIfEmpty(in.Grade), nilIfEmpty(in.Section), nilIfEmpty(in.AdmissionDate),
			nilIfEmpty(in.Phone), nilIfEmpty(in.GuardianName), nilIfEmpty(in.GuardianPhone),
			nilIfEmpty(in.GuardianEmail), code))
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i+2, err)
		}
		created = append(created, e)
	}

	return created, tx.Commit(ctx)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd qwish-backend && go test ./internal/domain/enrollment/ -run Import -v`
Expected: PASS, all six tests.

- [ ] **Step 5: Add the handler**

Append to `internal/domain/enrollment/handler_institution.go` (add imports `"encoding/csv"`, `"strconv"`):

```go
// POST /api/v1/institution/students/import?dry_run=true   multipart: file=<csv>
//
// The dry run is the point: it returns a verdict per row so a bad file is
// caught before anything is written.
func (h *InstitutionHandler) ImportStudents(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)

	file, _, err := r.FormFile("file")
	if err != nil {
		middleware.BadRequest(w, "a CSV file field named 'file' is required")
		return
	}
	defer file.Close()

	rows, bad, err := ParseCSV(file)
	if err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}

	dryRun, _ := strconv.ParseBool(r.URL.Query().Get("dry_run"))
	if dryRun {
		verdicts, err := h.svc.PreviewImport(r.Context(), instID, rows)
		if err != nil {
			log.Printf("PreviewImport: %v", err)
			middleware.InternalError(w)
			return
		}
		middleware.JSON(w, http.StatusOK, map[string]interface{}{
			"verdicts": append(verdicts, bad...),
			"ok":       len(bad) == 0,
		})
		return
	}

	if len(bad) > 0 {
		middleware.JSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"error": map[string]interface{}{
				"code":     "IMPORT_VALIDATION_FAILED",
				"message":  "some rows could not be imported",
				"verdicts": bad,
			},
		})
		return
	}

	created, err := h.svc.CommitImport(r.Context(), instID, rows)
	if err != nil {
		log.Printf("CommitImport: %v", err)
		middleware.InternalError(w)
		return
	}

	// Claim codes come back as a CSV so the school can print and distribute them.
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="claim-codes.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"full_name", "roll_number", "claim_code"})
	for _, e := range created {
		roll, code := "", ""
		if e.RollNumber != nil {
			roll = *e.RollNumber
		}
		if e.ClaimCode != nil {
			code = *e.ClaimCode
		}
		cw.Write([]string{e.FullName, roll, code})
	}
	cw.Flush()
}
```

- [ ] **Step 6: Wire the route**

In `cmd/api/main.go`, in the institution block:

```go
r.Post("/students/import", enrollmentInstH.ImportStudents)
```

- [ ] **Step 7: Verify and commit**

Run: `cd qwish-backend && go build ./... && go vet ./... && go test ./internal/domain/enrollment/ -v`
Expected: build clean, tests PASS.

```bash
git add internal/domain/enrollment/ cmd/api/main.go
git commit -m "feat(institution): CSV roster import with dry-run preview"
```

---

### Task 8: Lifecycle — suspend, graduate, transfer out, bulk promote

**Files:**
- Modify: `qwish-backend/internal/domain/enrollment/service.go`
- Modify: `qwish-backend/internal/domain/enrollment/handler_institution.go`
- Modify: `qwish-backend/internal/domain/institution/handler.go` (`UpdateStudentStatus`)
- Modify: `qwish-backend/cmd/api/main.go`
- Test: `qwish-backend/internal/domain/enrollment/lifecycle_test.go`

**Interfaces:**
- Consumes: `Service`, `ErrNotFound` from Task 6.
- Produces:
  - `func (s *Service) SetStatus(ctx context.Context, instID, enrollmentID, status string) error` — accepts `active`, `suspended`, `graduated`, `transferred`; sets `ended_at` and clears `users.institution_id` for the two terminal states, and mirrors `users.status` for the two live ones
  - `type PromoteFilter struct { FromGrade, FromSection, ToGrade, ToSection string }`
  - `func (s *Service) Promote(ctx context.Context, instID string, f PromoteFilter) (int64, error)`
  - handler methods `SetStudentStatus`, `PromoteStudents`

- [ ] **Step 1: Write the failing tests**

Create `internal/domain/enrollment/lifecycle_test.go`:

```go
package enrollment

import (
	"context"
	"testing"
)

// Graduating ends the enrollment and returns the student to institution-less
// status, keeping their account and history.
func TestGraduateEndsEnrollmentAndClearsInstitution(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	if err := svc.SetStatus(ctx, f.InstitutionID, f.StudentEnrollmentID, "graduated"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	var status string
	var endedAt *string
	pool.QueryRow(ctx, `SELECT status, ended_at::text FROM enrollments WHERE id=$1`,
		f.StudentEnrollmentID).Scan(&status, &endedAt)
	if status != "graduated" || endedAt == nil {
		t.Fatalf("status=%q ended_at=%v, want graduated with an end date", status, endedAt)
	}

	var instID *string
	pool.QueryRow(ctx, `SELECT institution_id FROM users WHERE id=$1`, f.StudentID).Scan(&instID)
	if instID != nil {
		t.Fatalf("users.institution_id = %v, want NULL after graduation", instID)
	}
}

// After graduating, the student can claim a new institution's code — the
// one-active-enrollment index no longer blocks them.
func TestGraduatedStudentCanEnrollElsewhere(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	if err := svc.SetStatus(ctx, f.InstitutionID, f.StudentEnrollmentID, "transferred"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	var code string
	if err := pool.QueryRow(ctx, `
		INSERT INTO enrollments (institution_id, full_name, claim_code, status)
		VALUES ($1, 'Transferred In', 'XFER-'||$2, 'pending_claim')
		RETURNING claim_code`, f.OtherInstitutionID, f.StudentEnrollmentID).Scan(&code); err != nil {
		t.Fatalf("seed target enrollment: %v", err)
	}

	if _, err := svc.Claim(ctx, f.StudentID, code); err != nil {
		t.Fatalf("Claim after transfer out: %v", err)
	}
}

// Suspension has to reach users.status, which is what actually blocks login.
func TestSuspendMirrorsUserStatus(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	if err := svc.SetStatus(ctx, f.InstitutionID, f.StudentEnrollmentID, "suspended"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	var userStatus string
	pool.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, f.StudentID).Scan(&userStatus)
	if userStatus != "suspended" {
		t.Fatalf("users.status = %q, want suspended", userStatus)
	}

	if err := svc.SetStatus(ctx, f.InstitutionID, f.StudentEnrollmentID, "active"); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	pool.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, f.StudentID).Scan(&userStatus)
	if userStatus != "active" {
		t.Fatalf("users.status = %q, want active after reactivation", userStatus)
	}
}

func TestSetStatusIsInstitutionScoped(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	err := svc.SetStatus(context.Background(), f.OtherInstitutionID, f.StudentEnrollmentID, "suspended")
	if err == nil {
		t.Fatal("expected another institution's status change to fail")
	}
}

func TestPromoteAdvancesMatchingEnrollmentsOnly(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	// Fixture: StudentEnrollmentID is grade 9 section A; the unclaimed row is 9/B.
	n, err := svc.Promote(ctx, f.InstitutionID, PromoteFilter{
		FromGrade: "9", FromSection: "A", ToGrade: "10", ToSection: "A",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if n != 1 {
		t.Fatalf("promoted %d rows, want 1", n)
	}

	var grade string
	pool.QueryRow(ctx, `SELECT grade FROM enrollments WHERE id=$1`, f.StudentEnrollmentID).Scan(&grade)
	if grade != "10" {
		t.Fatalf("grade = %q, want 10", grade)
	}
	pool.QueryRow(ctx, `SELECT grade FROM enrollments WHERE id=$1`, f.UnclaimedEnrollmentID).Scan(&grade)
	if grade != "9" {
		t.Fatalf("section B was promoted too: grade = %q, want 9", grade)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd qwish-backend && go test ./internal/domain/enrollment/ -run 'TestGraduate|TestSuspend|TestSetStatus|TestPromote' -v`
Expected: FAIL to build — `svc.SetStatus undefined`, `undefined: PromoteFilter`.

- [ ] **Step 3: Implement**

Append to `internal/domain/enrollment/service.go`:

```go
// terminalStatuses end the relationship: the enrollment is closed and the
// student returns to institution-less, keeping account, points and history.
var terminalStatuses = map[string]bool{"graduated": true, "transferred": true}

// SetStatus moves an enrollment through its lifecycle.
//
// users.status is what actually blocks login, so live transitions mirror onto
// it; terminal transitions instead clear users.institution_id.
func (s *Service) SetStatus(ctx context.Context, instID, enrollmentID, status string) error {
	switch status {
	case "active", "suspended", "graduated", "transferred":
	default:
		return fmt.Errorf("unknown status %q", status)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var userID *string
	err = tx.QueryRow(ctx,
		`UPDATE enrollments
		    SET status=$1,
		        ended_at = CASE WHEN $1 IN ('graduated','transferred') THEN now() ELSE NULL END,
		        updated_at = now()
		  WHERE id=$2 AND institution_id=$3
		  RETURNING user_id`, status, enrollmentID, instID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	// Unclaimed roster rows have no user to mirror onto.
	if userID != nil {
		if terminalStatuses[status] {
			_, err = tx.Exec(ctx,
				`UPDATE users SET institution_id=NULL, status='active', updated_at=now() WHERE id=$1`, *userID)
		} else {
			_, err = tx.Exec(ctx,
				`UPDATE users SET status=$1, institution_id=$2, updated_at=now() WHERE id=$3`,
				status, instID, *userID)
		}
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// PromoteFilter selects the cohort to advance. FromSection empty means the
// whole grade; ToSection empty leaves each student's section unchanged.
type PromoteFilter struct {
	FromGrade   string
	FromSection string
	ToGrade     string
	ToSection   string
}

// Promote advances a cohort in one statement. Only live enrollments move.
func (s *Service) Promote(ctx context.Context, instID string, f PromoteFilter) (int64, error) {
	if f.FromGrade == "" || f.ToGrade == "" {
		return 0, fmt.Errorf("from_grade and to_grade are required")
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE enrollments
		    SET grade=$1,
		        section = CASE WHEN $2 <> '' THEN $2 ELSE section END,
		        updated_at = now()
		  WHERE institution_id=$3
		    AND grade=$4
		    AND ($5='' OR section=$5)
		    AND status IN ('pending_claim','active','suspended')`,
		f.ToGrade, f.ToSection, instID, f.FromGrade, f.FromSection)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
```

Add `"fmt"` to the imports if it is not already there.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd qwish-backend && go test ./internal/domain/enrollment/ -run 'TestGraduate|TestSuspend|TestSetStatus|TestPromote' -v`
Expected: PASS, all five tests.

- [ ] **Step 5: Add the handlers**

Append to `internal/domain/enrollment/handler_institution.go`:

```go
// PATCH /api/v1/institution/enrollments/{enrollmentId}/status  {status, reason}
func (h *InstitutionHandler) SetStudentStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status string `json:"status"` // active | suspended | graduated | transferred
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}

	enrollmentID := chi.URLParam(r, "enrollmentId")
	err := h.svc.SetStatus(r.Context(), middleware.GetInstitutionID(r), enrollmentID, req.Status)
	switch {
	case errors.Is(err, ErrNotFound):
		middleware.NotFound(w, "student")
		return
	case err != nil:
		log.Printf("SetStudentStatus: %v", err)
		middleware.BadRequest(w, err.Error())
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": req.Status})
}

// POST /api/v1/institution/students/promote  {from_grade, from_section, to_grade, to_section}
func (h *InstitutionHandler) PromoteStudents(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FromGrade   string `json:"from_grade"`
		FromSection string `json:"from_section"`
		ToGrade     string `json:"to_grade"`
		ToSection   string `json:"to_section"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}

	n, err := h.svc.Promote(r.Context(), middleware.GetInstitutionID(r), PromoteFilter{
		FromGrade: req.FromGrade, FromSection: req.FromSection,
		ToGrade: req.ToGrade, ToSection: req.ToSection,
	})
	if err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]int64{"promoted": n})
}
```

- [ ] **Step 6: Repoint the legacy status endpoint**

`institution.UpdateStudentStatus` currently writes `users.status` directly. Replace its body's update with a call through the enrollment service so the enrollment stays in sync. In `internal/domain/institution/handler.go`, replace the `h.db.Exec(... UPDATE users SET status=...)` line with:

```go
	// The enrollment is the source of truth; the service mirrors users.status.
	var enrollmentID string
	h.db.QueryRow(r.Context(),
		`SELECT id FROM enrollments
		  WHERE user_id=$1 AND institution_id=$2 AND status IN ('active','suspended')`,
		studentID, instID).Scan(&enrollmentID)
	if enrollmentID == "" {
		middleware.NotFound(w, "student")
		return
	}
	if err := h.enrollment.SetStatus(r.Context(), instID, enrollmentID, newStatus); err != nil {
		middleware.InternalError(w)
		return
	}
	if req.Reason != "" {
		h.db.Exec(r.Context(), `UPDATE users SET suspension_reason=$1 WHERE id=$2`, req.Reason, studentID)
	}
```

Add an `enrollment *enrollment.Service` field to `institution.Handler` and a parameter to `institution.NewHandler`, updating the call in `cmd/api/main.go`.

- [ ] **Step 7: Wire the routes**

In `cmd/api/main.go`, institution block:

```go
r.Patch("/enrollments/{enrollmentId}/status", enrollmentInstH.SetStudentStatus)
r.Post("/enrollments/promote", enrollmentInstH.PromoteStudents)
```

Leave the existing `r.Patch("/students/{userId}/status", institutionH.UpdateStudentStatus)` in place — the institute dashboard still calls it, and Step 6 has just repointed its body at the enrollment service, so both paths now write the same source of truth.

- [ ] **Step 8: Verify and commit**

Run: `cd qwish-backend && go build ./... && go vet ./... && go test ./internal/domain/enrollment/ ./internal/domain/institution/ -v`
Expected: build clean, tests PASS.

```bash
git add internal/domain/enrollment/ internal/domain/institution/handler.go cmd/api/main.go
git commit -m "feat(institution): enrollment lifecycle and bulk promotion"
```

---

### Task 9: Teacher class-membership writes

**Files:**
- Create: `qwish-backend/internal/domain/enrollment/handler_teacher.go`
- Modify: `qwish-backend/internal/domain/enrollment/service.go`
- Modify: `qwish-backend/cmd/api/main.go`
- Test: `qwish-backend/internal/domain/enrollment/teacher_scope_test.go`

**Interfaces:**
- Consumes: `Service` from Task 2.
- Produces:
  - `func (s *Service) TeacherOwnsClass(ctx context.Context, teacherID, groupID string) (bool, error)`
  - `func (s *Service) AddStudentToClass(ctx context.Context, teacherID, groupID, studentID string) error`
  - `func (s *Service) RemoveStudentFromClass(ctx context.Context, teacherID, groupID, studentID string) error`
  - `ErrNotYourClass`
  - `type TeacherHandler struct` with `NewTeacherHandler(svc *Service) *TeacherHandler` and methods `AddStudent`, `RemoveStudent`

- [ ] **Step 1: Write the failing tests**

Create `internal/domain/enrollment/teacher_scope_test.go`:

```go
package enrollment

import (
	"context"
	"errors"
	"testing"
)

func TestAddStudentToClassRequiresOwnership(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	// LonerTeacherID is assigned to no group.
	err := svc.AddStudentToClass(context.Background(), f.LonerTeacherID, f.GroupID, f.StudentID)
	if !errors.Is(err, ErrNotYourClass) {
		t.Fatalf("err = %v, want ErrNotYourClass", err)
	}
}

func TestAddAndRemoveStudentInOwnClass(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	// Give the solo student an enrollment at this institution first.
	if _, err := svc.Claim(ctx, f.SoloStudentID, f.ClaimCode); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if err := svc.AddStudentToClass(ctx, f.TeacherID, f.GroupID, f.SoloStudentID); err != nil {
		t.Fatalf("AddStudentToClass: %v", err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM group_students WHERE group_id=$1 AND user_id=$2`,
		f.GroupID, f.SoloStudentID).Scan(&n)
	if n != 1 {
		t.Fatalf("membership rows = %d, want 1", n)
	}

	if err := svc.RemoveStudentFromClass(ctx, f.TeacherID, f.GroupID, f.SoloStudentID); err != nil {
		t.Fatalf("RemoveStudentFromClass: %v", err)
	}
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM group_students WHERE group_id=$1 AND user_id=$2`,
		f.GroupID, f.SoloStudentID).Scan(&n)
	if n != 0 {
		t.Fatalf("membership rows = %d, want 0 after removal", n)
	}
}

// A teacher cannot pull in a student who is not enrolled at their institution.
func TestAddStudentRejectsOutsider(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	err := svc.AddStudentToClass(context.Background(), f.TeacherID, f.GroupID, f.SoloStudentID)
	if err == nil {
		t.Fatal("expected adding an unenrolled student to fail")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd qwish-backend && go test ./internal/domain/enrollment/ -run 'TestAdd|TestRemove' -v`
Expected: FAIL to build — `undefined: ErrNotYourClass`.

- [ ] **Step 3: Implement**

Append to `internal/domain/enrollment/service.go`:

```go
var ErrNotYourClass = errors.New("teacher is not assigned to this class")

// TeacherOwnsClass is the scope check for every teacher write: a teacher may
// act only on classes they are assigned to via group_teachers.
func (s *Service) TeacherOwnsClass(ctx context.Context, teacherID, groupID string) (bool, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM group_teachers WHERE group_id=$1 AND user_id=$2`,
		groupID, teacherID).Scan(&n)
	return n > 0, err
}

func (s *Service) AddStudentToClass(ctx context.Context, teacherID, groupID, studentID string) error {
	ok, err := s.TeacherOwnsClass(ctx, teacherID, groupID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotYourClass
	}

	// The student must hold a live enrollment at the same institution as the class.
	var n int
	if err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM enrollments e
		   JOIN groups g ON g.institution_id = e.institution_id
		  WHERE e.user_id=$1 AND g.id=$2 AND e.status IN ('active','suspended')`,
		studentID, groupID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}

	_, err = s.db.Exec(ctx,
		`INSERT INTO group_students (group_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		groupID, studentID)
	return err
}

func (s *Service) RemoveStudentFromClass(ctx context.Context, teacherID, groupID, studentID string) error {
	ok, err := s.TeacherOwnsClass(ctx, teacherID, groupID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotYourClass
	}
	_, err = s.db.Exec(ctx,
		`DELETE FROM group_students WHERE group_id=$1 AND user_id=$2`, groupID, studentID)
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd qwish-backend && go test ./internal/domain/enrollment/ -run 'TestAdd|TestRemove' -v`
Expected: PASS, all three tests.

- [ ] **Step 5: Write the handler**

Create `internal/domain/enrollment/handler_teacher.go`:

```go
package enrollment

import (
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/middleware"
)

type TeacherHandler struct{ svc *Service }

func NewTeacherHandler(svc *Service) *TeacherHandler { return &TeacherHandler{svc: svc} }

func writeScopeError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ErrNotYourClass):
		middleware.Error(w, http.StatusForbidden, "NOT_IN_YOUR_CLASS",
			"you are not assigned to this class")
		return true
	case errors.Is(err, ErrNotFound):
		middleware.NotFound(w, "student")
		return true
	case err != nil:
		log.Printf("teacher class membership: %v", err)
		middleware.InternalError(w)
		return true
	}
	return false
}

// POST /api/v1/teacher/classes/{classId}/students  {user_id}
func (h *TeacherHandler) AddStudent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		middleware.BadRequest(w, "user_id is required")
		return
	}
	err := h.svc.AddStudentToClass(r.Context(), middleware.GetUserID(r),
		chi.URLParam(r, "classId"), req.UserID)
	if writeScopeError(w, err) {
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "added"})
}

// DELETE /api/v1/teacher/classes/{classId}/students/{userId}
func (h *TeacherHandler) RemoveStudent(w http.ResponseWriter, r *http.Request) {
	err := h.svc.RemoveStudentFromClass(r.Context(), middleware.GetUserID(r),
		chi.URLParam(r, "classId"), chi.URLParam(r, "userId"))
	if writeScopeError(w, err) {
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "removed"})
}
```

Add `"encoding/json"` to the imports.

- [ ] **Step 6: Wire the routes**

In `cmd/api/main.go`, in the teacher block (near `cmd/api/main.go:356`):

```go
enrollmentTeacherH := enrollment.NewTeacherHandler(enrollmentSvc)
...
r.Post("/classes/{classId}/students", enrollmentTeacherH.AddStudent)
r.Delete("/classes/{classId}/students/{userId}", enrollmentTeacherH.RemoveStudent)
```

- [ ] **Step 7: Verify and commit**

Run: `cd qwish-backend && go build ./... && go vet ./... && go test ./internal/domain/enrollment/ -v`
Expected: build clean, tests PASS.

```bash
git add internal/domain/enrollment/ cmd/api/main.go
git commit -m "feat(teacher): class membership writes scoped to assigned classes"
```

---

### Task 10: Teacher edit proposals and institution review queue

**Files:**
- Create: `qwish-backend/internal/domain/editrequest/service.go`
- Create: `qwish-backend/internal/domain/editrequest/handler.go`
- Create: `qwish-backend/internal/domain/editrequest/testdb_test.go`
- Modify: `qwish-backend/cmd/api/main.go`
- Test: `qwish-backend/internal/domain/editrequest/service_test.go`

**Interfaces:**
- Consumes: `student_edit_requests` and `enrollments` from Task 1.
- Produces:
  - `type Service struct` with `NewService(db *pgxpool.Pool) *Service`
  - `func (s *Service) Propose(ctx context.Context, teacherID, enrollmentID, field, proposedValue, note string) (string, error)`
  - `func (s *Service) Review(ctx context.Context, instID, adminID, requestID, decision string) error` — `decision` is `approved` or `rejected`; approving writes the field onto the enrollment
  - `func (s *Service) ListForInstitution(ctx context.Context, instID, status string) ([]Request, error)`
  - `ErrNotYourClass`, `ErrAlreadyResolved`, `ErrInvalidField`
  - `type Handler struct` with `NewHandler(svc *Service) *Handler` and methods `Propose`, `ListMine`, `ListForReview`, `Review`

- [ ] **Step 1: Write the failing tests**

Create `internal/domain/editrequest/testdb_test.go` — the same `openTestDB` helper as Task 1, in `package editrequest`.

Create `internal/domain/editrequest/service_test.go`:

```go
package editrequest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seed builds one institution with a teacher assigned to a class, a student
// enrolled and in that class, and a second teacher assigned to nothing.
type seeded struct {
	InstitutionID string
	TeacherID     string
	LonerID       string
	StudentID     string
	EnrollmentID  string
	GroupID       string
}

func seed(t *testing.T, pool *pgxpool.Pool) seeded {
	t.Helper()
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var s seeded

	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}

	must("institution", pool.QueryRow(ctx, `
		INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
		VALUES ('ER '||$1, 'school', 'er-'||$1||'@example.test', 'S'||$1, 'T'||$1, 'verified')
		RETURNING id`, tag).Scan(&s.InstitutionID))

	newUser := func(role, label string, dest *string) {
		t.Helper()
		must(label, pool.QueryRow(ctx, `
			INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
			VALUES (gen_random_uuid(), $1, $1, $2, $3, $4)
			RETURNING id`, label+tag, label+"-"+tag+"@example.test", role, s.InstitutionID).Scan(dest))
	}
	newUser("teacher", "er-teacher", &s.TeacherID)
	newUser("teacher", "er-loner", &s.LonerID)
	newUser("student", "er-student", &s.StudentID)

	must("group", pool.QueryRow(ctx, `
		INSERT INTO groups (institution_id, name, invite_code) VALUES ($1,'ER Class','ERINV'||$2)
		RETURNING id`, s.InstitutionID, tag).Scan(&s.GroupID))
	_, err := pool.Exec(ctx, `INSERT INTO group_teachers (group_id, user_id) VALUES ($1,$2)`, s.GroupID, s.TeacherID)
	must("group_teachers", err)
	_, err = pool.Exec(ctx, `INSERT INTO group_students (group_id, user_id) VALUES ($1,$2)`, s.GroupID, s.StudentID)
	must("group_students", err)

	must("enrollment", pool.QueryRow(ctx, `
		INSERT INTO enrollments (institution_id, user_id, full_name, roll_number, grade, section, status, joined_at)
		VALUES ($1,$2,'er-student','ER-'||$3,'9','A','active',now())
		RETURNING id`, s.InstitutionID, s.StudentID, tag).Scan(&s.EnrollmentID))

	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM student_edit_requests WHERE enrollment_id=$1`, s.EnrollmentID)
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id=$1`, s.InstitutionID)
		pool.Exec(ctx, `DELETE FROM groups WHERE institution_id=$1`, s.InstitutionID)
		pool.Exec(ctx, `DELETE FROM users WHERE institution_id=$1`, s.InstitutionID)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, s.InstitutionID)
	})
	return s
}

func TestProposeRequiresSharedClass(t *testing.T) {
	pool := openTestDB(t)
	s := seed(t, pool)
	svc := NewService(pool)

	_, err := svc.Propose(context.Background(), s.LonerID, s.EnrollmentID, "section", "B", "")
	if !errors.Is(err, ErrNotYourClass) {
		t.Fatalf("err = %v, want ErrNotYourClass", err)
	}
}

func TestProposeRejectsUnknownField(t *testing.T) {
	pool := openTestDB(t)
	s := seed(t, pool)
	svc := NewService(pool)

	_, err := svc.Propose(context.Background(), s.TeacherID, s.EnrollmentID, "total_points", "999", "")
	if !errors.Is(err, ErrInvalidField) {
		t.Fatalf("err = %v, want ErrInvalidField", err)
	}
}

// Approving is what actually writes the enrollment; proposing must not.
func TestProposeDoesNotWriteUntilApproved(t *testing.T) {
	pool := openTestDB(t)
	s := seed(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	id, err := svc.Propose(ctx, s.TeacherID, s.EnrollmentID, "section", "B", "wrong section")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	var section string
	pool.QueryRow(ctx, `SELECT section FROM enrollments WHERE id=$1`, s.EnrollmentID).Scan(&section)
	if section != "A" {
		t.Fatalf("section = %q, want A while the request is pending", section)
	}

	if err := svc.Review(ctx, s.InstitutionID, s.TeacherID, id, "approved"); err != nil {
		t.Fatalf("Review: %v", err)
	}
	pool.QueryRow(ctx, `SELECT section FROM enrollments WHERE id=$1`, s.EnrollmentID).Scan(&section)
	if section != "B" {
		t.Fatalf("section = %q, want B after approval", section)
	}
}

func TestRejectLeavesEnrollmentUnchanged(t *testing.T) {
	pool := openTestDB(t)
	s := seed(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	id, _ := svc.Propose(ctx, s.TeacherID, s.EnrollmentID, "grade", "10", "")
	if err := svc.Review(ctx, s.InstitutionID, s.TeacherID, id, "rejected"); err != nil {
		t.Fatalf("Review: %v", err)
	}

	var grade string
	pool.QueryRow(ctx, `SELECT grade FROM enrollments WHERE id=$1`, s.EnrollmentID).Scan(&grade)
	if grade != "9" {
		t.Fatalf("grade = %q, want 9 after rejection", grade)
	}
}

func TestReviewTwiceFails(t *testing.T) {
	pool := openTestDB(t)
	s := seed(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	id, _ := svc.Propose(ctx, s.TeacherID, s.EnrollmentID, "grade", "10", "")
	if err := svc.Review(ctx, s.InstitutionID, s.TeacherID, id, "approved"); err != nil {
		t.Fatalf("first Review: %v", err)
	}
	err := svc.Review(ctx, s.InstitutionID, s.TeacherID, id, "rejected")
	if !errors.Is(err, ErrAlreadyResolved) {
		t.Fatalf("err = %v, want ErrAlreadyResolved", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd qwish-backend && go test ./internal/domain/editrequest/ -v`
Expected: FAIL to build — `undefined: NewService`.

- [ ] **Step 3: Implement the service**

Create `internal/domain/editrequest/service.go`:

```go
// Package editrequest is the teacher proposal queue. Teachers cannot write
// institution-owned fields directly; they propose a change and an institution
// admin approves it, which is also where the audit trail comes from.
package editrequest

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotYourClass    = errors.New("student is not in one of your classes")
	ErrAlreadyResolved = errors.New("edit request already resolved")
	ErrInvalidField    = errors.New("field is not proposable")
	ErrNotFound        = errors.New("edit request not found")
)

// Only institution-owned academic fields are proposable. Student-owned fields
// are the student's to change, so they never enter this queue.
var proposableFields = map[string]bool{
	"roll_number": true, "grade": true, "section": true, "admission_date": true,
}

type Request struct {
	ID            string     `json:"id"`
	EnrollmentID  string     `json:"enrollment_id"`
	StudentName   string     `json:"student_name"`
	RequestedBy   string     `json:"requested_by"`
	TeacherName   string     `json:"teacher_name"`
	Field         string     `json:"field"`
	CurrentValue  *string    `json:"current_value,omitempty"`
	ProposedValue string     `json:"proposed_value"`
	Note          *string    `json:"note,omitempty"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	ReviewedAt    *time.Time `json:"reviewed_at,omitempty"`
}

type Service struct{ db *pgxpool.Pool }

func NewService(db *pgxpool.Pool) *Service { return &Service{db: db} }

// Propose records a correction for review. It never writes the enrollment.
func (s *Service) Propose(ctx context.Context, teacherID, enrollmentID, field, proposedValue, note string) (string, error) {
	if !proposableFields[field] {
		return "", ErrInvalidField
	}
	if proposedValue == "" {
		return "", errors.New("proposed_value is required")
	}

	// The teacher must share a class with the student behind this enrollment.
	var shared int
	if err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM enrollments e
		  JOIN group_students gs ON gs.user_id = e.user_id
		  JOIN group_teachers gt ON gt.group_id = gs.group_id
		 WHERE e.id=$1 AND gt.user_id=$2`, enrollmentID, teacherID).Scan(&shared); err != nil {
		return "", err
	}
	if shared == 0 {
		return "", ErrNotYourClass
	}

	var id string
	err := s.db.QueryRow(ctx, `
		INSERT INTO student_edit_requests
			(enrollment_id, requested_by, field, current_value, proposed_value, note)
		SELECT $1, $2, $3,
		       CASE $3 WHEN 'roll_number' THEN e.roll_number
		               WHEN 'grade'       THEN e.grade
		               WHEN 'section'     THEN e.section
		               ELSE e.admission_date::text END,
		       $4, NULLIF($5,'')
		  FROM enrollments e WHERE e.id=$1
		RETURNING id`, enrollmentID, teacherID, field, proposedValue, note).Scan(&id)
	return id, err
}

// Review decides a request. Approving is the only path that writes the
// enrollment, and it does so in the same transaction that closes the request.
func (s *Service) Review(ctx context.Context, instID, adminID, requestID, decision string) error {
	if decision != "approved" && decision != "rejected" {
		return errors.New("decision must be approved or rejected")
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var enrollmentID, field, proposed, status string
	err = tx.QueryRow(ctx, `
		SELECT r.enrollment_id, r.field, r.proposed_value, r.status
		  FROM student_edit_requests r
		  JOIN enrollments e ON e.id = r.enrollment_id
		 WHERE r.id=$1 AND e.institution_id=$2
		 FOR UPDATE OF r`, requestID, instID).Scan(&enrollmentID, &field, &proposed, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "pending" {
		return ErrAlreadyResolved
	}

	if decision == "approved" {
		// field comes from proposableFields, so this switch is exhaustive and
		// the column name is never interpolated from user input.
		var sql string
		switch field {
		case "roll_number":
			sql = `UPDATE enrollments SET roll_number=$1, updated_at=now() WHERE id=$2`
		case "grade":
			sql = `UPDATE enrollments SET grade=$1, updated_at=now() WHERE id=$2`
		case "section":
			sql = `UPDATE enrollments SET section=$1, updated_at=now() WHERE id=$2`
		case "admission_date":
			sql = `UPDATE enrollments SET admission_date=$1::date, updated_at=now() WHERE id=$2`
		default:
			return ErrInvalidField
		}
		if _, err := tx.Exec(ctx, sql, proposed, enrollmentID); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE student_edit_requests
		    SET status=$1, reviewed_by=$2, reviewed_at=now()
		  WHERE id=$3`, decision, adminID, requestID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ListForTeacher returns a teacher's own proposals, newest first.
func (s *Service) ListForTeacher(ctx context.Context, teacherID string) ([]Request, error) {
	return s.list(ctx, `r.requested_by=$1`, teacherID, "")
}

// ListForInstitution returns the review queue. status "" means all.
func (s *Service) ListForInstitution(ctx context.Context, instID, status string) ([]Request, error) {
	return s.list(ctx, `e.institution_id=$1`, instID, status)
}

// list is the shared query behind both listings. The caller supplies the
// scoping predicate; $1 is its argument and $2 the optional status filter.
func (s *Service) list(ctx context.Context, scope, scopeArg, status string) ([]Request, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.enrollment_id, COALESCE(su.display_name, e.full_name),
		       r.requested_by, t.display_name, r.field, r.current_value,
		       r.proposed_value, r.note, r.status, r.created_at, r.reviewed_at
		  FROM student_edit_requests r
		  JOIN enrollments e ON e.id = r.enrollment_id
		  JOIN users t ON t.id = r.requested_by
		  LEFT JOIN users su ON su.id = e.user_id
		 WHERE `+scope+` AND ($2='' OR r.status=$2)
		 ORDER BY r.created_at DESC`, scopeArg, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Request{}
	for rows.Next() {
		var q Request
		if err := rows.Scan(&q.ID, &q.EnrollmentID, &q.StudentName, &q.RequestedBy,
			&q.TeacherName, &q.Field, &q.CurrentValue, &q.ProposedValue, &q.Note,
			&q.Status, &q.CreatedAt, &q.ReviewedAt); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd qwish-backend && go test ./internal/domain/editrequest/ -v`
Expected: PASS, all five tests.

- [ ] **Step 5: Write the handler**

Create `internal/domain/editrequest/handler.go`:

```go
package editrequest

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// POST /api/v1/teacher/enrollments/{enrollmentId}/edit-requests
//   {field, proposed_value, note}
func (h *Handler) Propose(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Field         string `json:"field"`
		ProposedValue string `json:"proposed_value"`
		Note          string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}

	id, err := h.svc.Propose(r.Context(), middleware.GetUserID(r),
		chi.URLParam(r, "enrollmentId"), req.Field, req.ProposedValue, req.Note)
	switch {
	case errors.Is(err, ErrNotYourClass):
		middleware.Error(w, http.StatusForbidden, "NOT_IN_YOUR_CLASS",
			"this student is not in one of your classes")
		return
	case errors.Is(err, ErrInvalidField):
		middleware.BadRequest(w, "field must be one of roll_number, grade, section, admission_date")
		return
	case err != nil:
		log.Printf("Propose: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": id})
}

// GET /api/v1/teacher/edit-requests
//
// A teacher's own proposals and where they landed.
func (h *Handler) ListMine(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListForTeacher(r.Context(), middleware.GetUserID(r))
	if err != nil {
		log.Printf("ListMine: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, list)
}

// GET /api/v1/institution/edit-requests?status=pending
func (h *Handler) ListForReview(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListForInstitution(r.Context(),
		middleware.GetInstitutionID(r), r.URL.Query().Get("status"))
	if err != nil {
		log.Printf("ListForReview: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, list)
}

// PATCH /api/v1/institution/edit-requests/{requestId}  {decision}
func (h *Handler) Review(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Decision string `json:"decision"` // approved | rejected
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}

	err := h.svc.Review(r.Context(), middleware.GetInstitutionID(r), middleware.GetUserID(r),
		chi.URLParam(r, "requestId"), req.Decision)
	switch {
	case errors.Is(err, ErrAlreadyResolved):
		middleware.Error(w, http.StatusConflict, "EDIT_REQUEST_RESOLVED",
			"this request has already been decided")
		return
	case errors.Is(err, ErrNotFound):
		middleware.NotFound(w, "edit request")
		return
	case err != nil:
		log.Printf("Review: %v", err)
		middleware.BadRequest(w, err.Error())
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": req.Decision})
}
```

- [ ] **Step 6: Wire the routes**

In `cmd/api/main.go`:

```go
editRequestH := editrequest.NewHandler(editrequest.NewService(pool))
```

Teacher block:
```go
r.Post("/enrollments/{enrollmentId}/edit-requests", editRequestH.Propose)
r.Get("/edit-requests", editRequestH.ListMine)
```

Institution block:
```go
r.Get("/edit-requests", editRequestH.ListForReview)
r.Patch("/edit-requests/{requestId}", editRequestH.Review)
```

- [ ] **Step 7: Verify and commit**

Run: `cd qwish-backend && go build ./... && go vet ./... && go test ./internal/domain/editrequest/ -v`
Expected: build clean, tests PASS.

```bash
git add internal/domain/editrequest/ cmd/api/main.go
git commit -m "feat(editrequest): teacher proposals with institution review queue"
```

---

### Task 11: Super-admin search, merge and purge

**Files:**
- Modify: `qwish-backend/internal/domain/admin/handler.go`
- Create: `qwish-backend/internal/domain/admin/student_admin.go`
- Create: `qwish-backend/internal/domain/admin/student_admin_test.go`
- Create: `qwish-backend/internal/domain/admin/testdb_test.go`
- Modify: `qwish-backend/cmd/api/main.go`

**Interfaces:**
- Consumes: `enrollments` from Task 1.
- Produces:
  - `func MergeStudents(ctx context.Context, db *pgxpool.Pool, keepID, mergeID, actorID string) error`
  - `type StudentAdminHandler struct` with `NewStudentAdminHandler(db *pgxpool.Pool) *StudentAdminHandler` and methods `Search`, `Merge`, `Purge`

- [ ] **Step 1: Write the failing test**

Create `internal/domain/admin/testdb_test.go` — the same `openTestDB` helper as Task 1, in `package admin`.

Create `internal/domain/admin/student_admin_test.go`:

```go
package admin

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Merging folds one duplicate into the other: points sum, attempts and
// enrollments repoint, and the loser is soft-deleted.
func TestMergeStudentsFoldsPointsAndAttempts(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())

	var keepID, mergeID, actorID string
	mk := func(label string, points int, dest *string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (supabase_uid, full_name, display_name, email, role, total_points)
			VALUES (gen_random_uuid(), $1, $1, $2, 'student', $3) RETURNING id`,
			label+tag, label+"-"+tag+"@example.test", points).Scan(dest); err != nil {
			t.Fatalf("seed %s: %v", label, err)
		}
	}
	mk("keep", 100, &keepID)
	mk("merge", 40, &mergeID)
	mk("actor", 0, &actorID)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`,
			[]string{keepID, mergeID, actorID})
	})

	if err := MergeStudents(ctx, pool, keepID, mergeID, actorID); err != nil {
		t.Fatalf("MergeStudents: %v", err)
	}

	var points int64
	pool.QueryRow(ctx, `SELECT total_points FROM users WHERE id=$1`, keepID).Scan(&points)
	if points != 140 {
		t.Fatalf("total_points = %d, want 140", points)
	}

	var deletedAt *time.Time
	pool.QueryRow(ctx, `SELECT deleted_at FROM users WHERE id=$1`, mergeID).Scan(&deletedAt)
	if deletedAt == nil {
		t.Fatal("merged user should be soft-deleted")
	}
}

func TestMergeStudentsRejectsSelfMerge(t *testing.T) {
	pool := openTestDB(t)
	err := MergeStudents(context.Background(), pool, "same-id", "same-id", "actor")
	if err == nil {
		t.Fatal("expected merging a user into itself to fail")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd qwish-backend && go test ./internal/domain/admin/ -run TestMerge -v`
Expected: FAIL to build — `undefined: MergeStudents`.

- [ ] **Step 3: Implement**

Create `internal/domain/admin/student_admin.go`:

```go
package admin

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

// MergeStudents folds mergeID into keepID: one human, two records, usually a
// self-signup plus a roster row that was later claimed under a second account.
func MergeStudents(ctx context.Context, db *pgxpool.Pool, keepID, mergeID, actorID string) error {
	if keepID == mergeID {
		return errors.New("cannot merge a user into itself")
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, q := range []string{
		`UPDATE quiz_attempts SET user_id=$1 WHERE user_id=$2`,
		`UPDATE points_ledger SET user_id=$1 WHERE user_id=$2`,
		`UPDATE user_profile_entries SET user_id=$1 WHERE user_id=$2`,
	} {
		if _, err := tx.Exec(ctx, q, keepID, mergeID); err != nil {
			return err
		}
	}

	// Enrollments move only if they would not violate the one-live-enrollment
	// index; ended ones are always safe to move.
	if _, err := tx.Exec(ctx,
		`UPDATE enrollments SET user_id=$1
		  WHERE user_id=$2
		    AND (status NOT IN ('active','suspended')
		         OR NOT EXISTS (SELECT 1 FROM enrollments k
		                         WHERE k.user_id=$1 AND k.status IN ('active','suspended')))`,
		keepID, mergeID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE users SET total_points = total_points +
			COALESCE((SELECT total_points FROM users WHERE id=$2), 0),
			updated_at = now()
		WHERE id=$1`, keepID, mergeID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE users SET deleted_at=now(), status='deleted', updated_at=now() WHERE id=$1`,
		mergeID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (actor_id, action, entity_type, entity_id, detail)
		 VALUES ($1, 'merge_students', 'user', $2, 'merged '||$3)`,
		actorID, keepID, mergeID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

type StudentAdminHandler struct{ db *pgxpool.Pool }

func NewStudentAdminHandler(db *pgxpool.Pool) *StudentAdminHandler {
	return &StudentAdminHandler{db: db}
}

// GET /api/v1/admin/students/search?q=<email|roll|name>
func (h *StudentAdminHandler) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) < 3 {
		middleware.BadRequest(w, "q must be at least 3 characters")
		return
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT u.id, u.display_name, u.email, u.status, u.deleted_at IS NOT NULL,
		       e.id, e.roll_number, i.name
		  FROM users u
		  LEFT JOIN enrollments e ON e.user_id = u.id AND e.status IN ('active','suspended')
		  LEFT JOIN institutions i ON i.id = e.institution_id
		 WHERE u.role='student'
		   AND (u.email ILIKE $1 OR u.display_name ILIKE $1 OR e.roll_number ILIKE $1)
		 ORDER BY u.display_name LIMIT 50`, "%"+q+"%")
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type hit struct {
		ID              string  `json:"id"`
		DisplayName     string  `json:"display_name"`
		Email           string  `json:"email"`
		Status          string  `json:"status"`
		Deleted         bool    `json:"deleted"`
		EnrollmentID    *string `json:"enrollment_id,omitempty"`
		RollNumber      *string `json:"roll_number,omitempty"`
		InstitutionName *string `json:"institution_name,omitempty"`
	}
	out := []hit{}
	for rows.Next() {
		var x hit
		rows.Scan(&x.ID, &x.DisplayName, &x.Email, &x.Status, &x.Deleted,
			&x.EnrollmentID, &x.RollNumber, &x.InstitutionName)
		out = append(out, x)
	}
	middleware.JSON(w, http.StatusOK, out)
}

// POST /api/v1/admin/students/merge  {keep_user_id, merge_user_id}
func (h *StudentAdminHandler) Merge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeepUserID  string `json:"keep_user_id"`
		MergeUserID string `json:"merge_user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		req.KeepUserID == "" || req.MergeUserID == "" {
		middleware.BadRequest(w, "keep_user_id and merge_user_id are required")
		return
	}

	if err := MergeStudents(r.Context(), h.db, req.KeepUserID, req.MergeUserID,
		middleware.GetAdminID(r)); err != nil {
		log.Printf("Merge: %v", err)
		middleware.BadRequest(w, err.Error())
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "merged"})
}

// DELETE /api/v1/admin/students/{userId}/purge
//
// Permanent erasure beyond the soft deleted_at. Cascades handle the child
// tables; enrollments are detached rather than deleted so the institution
// keeps its historical roster count.
func (h *StudentAdminHandler) Purge(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(r.Context())

	for _, q := range []string{
		`UPDATE enrollments SET user_id=NULL, status='transferred', ended_at=COALESCE(ended_at, now()) WHERE user_id=$1`,
		`DELETE FROM quiz_attempts WHERE user_id=$1`,
		`DELETE FROM points_ledger WHERE user_id=$1`,
		`DELETE FROM user_profile_entries WHERE user_id=$1`,
		`DELETE FROM group_students WHERE user_id=$1`,
		`DELETE FROM parent_student_links WHERE student_id=$1 OR parent_id=$1`,
	} {
		if _, err := tx.Exec(r.Context(), q, userID); err != nil {
			log.Printf("Purge %q: %v", q, err)
			middleware.InternalError(w)
			return
		}
	}

	if _, err := tx.Exec(r.Context(),
		`INSERT INTO audit_log (actor_id, action, entity_type, entity_id, detail)
		 VALUES ($1, 'purge_student', 'user', $2, 'permanent erasure')`,
		middleware.GetAdminID(r), userID); err != nil {
		middleware.InternalError(w)
		return
	}
	if _, err := tx.Exec(r.Context(), `DELETE FROM users WHERE id=$1`, userID); err != nil {
		log.Printf("Purge delete user: %v", err)
		middleware.InternalError(w)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "purged"})
}
```

Add `"encoding/json"` to the imports.

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd qwish-backend && go test ./internal/domain/admin/ -run TestMerge -v`
Expected: PASS, both tests.

- [ ] **Step 5: Wire the routes**

In `cmd/api/main.go`, in the admin block:

```go
studentAdminH := admin.NewStudentAdminHandler(pool)
...
r.Get("/students/search", studentAdminH.Search)
r.Post("/students/merge", studentAdminH.Merge)
r.Delete("/students/{userId}/purge", studentAdminH.Purge)
```

- [ ] **Step 6: Run the whole suite**

Run: `cd qwish-backend && go build ./... && go vet ./... && go test ./...`
Expected: build clean, every package PASS or SKIP.

- [ ] **Step 7: Update API_DOC.md**

Add the endpoints from Tasks 3, 5, 6, 7, 8, 9, 10 and 11 to `qwish-backend/API_DOC.md`, following the format already used there. Each entry needs the method, path, required role, request body, and response shape. The four frontend plans will be written against this document.

- [ ] **Step 8: Commit**

```bash
git add internal/domain/admin/ cmd/api/main.go API_DOC.md
git commit -m "feat(admin): cross-institution student search, merge and purge"
```

---

## Verification

After Task 11, confirm the whole feature end to end against a scratch database:

```bash
cd qwish-backend
export TEST_DATABASE_URL=postgres://...   # scratch DB, not production
psql "$TEST_DATABASE_URL" -f migrations/031_student_enrollment.sql
go build ./... && go vet ./... && go test ./...
```

Every test must PASS. A SKIP means `TEST_DATABASE_URL` was unset and nothing was actually verified — do not report success on skips.

## Follow-On Plans

Once this lands, one plan per user type, each against the endpoints documented in Step 7 of Task 11:

1. **numpie (student)** — claim/join screens, enrollment-aware shell, personal fields and CV editors.
2. **qwish-institute-dashboard** — roster table, import wizard with dry-run preview, lifecycle actions, review queue.
3. **qwish-teacher-panel** — class membership management, propose-a-correction flow.
4. **qwish-super-admin** — cross-institution search, merge, purge.
