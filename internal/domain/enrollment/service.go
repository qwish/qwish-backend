// Package enrollment owns the enrollments table: the relationship between a
// student and an institution. Institution-owned academic fields live here and
// have no student-facing write path, which is what makes them institution-owned.
package enrollment

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrClaimCodeInvalid = errors.New("claim code invalid")
	ErrClaimCodeUsed    = errors.New("claim code already used")
	ErrEnrollmentExists = errors.New("student already holds a live enrollment")
	ErrClassCodeInvalid = errors.New("class invite code invalid")
	ErrRollNumberTaken  = errors.New("roll number already in use")
	ErrNotFound         = errors.New("enrollment not found")
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

type Enrollment struct {
	ID            string     `json:"id"`
	InstitutionID string     `json:"institution_id"`
	UserID        *string    `json:"user_id,omitempty"`
	FullName      string     `json:"full_name"`
	Email         *string    `json:"email,omitempty"`
	RollNumber    *string    `json:"roll_number,omitempty"`
	Grade         *string    `json:"grade,omitempty"`
	Section       *string    `json:"section,omitempty"`
	AdmissionDate *time.Time `json:"admission_date,omitempty"`
	ClaimCode     *string    `json:"claim_code,omitempty"`
	Status        string     `json:"status"`
	JoinedAt      *time.Time `json:"joined_at,omitempty"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
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

// CreateRosterEntry pre-provisions a student the institution knows about but
// who has not signed up yet. The claim code is what the student later redeems.
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
