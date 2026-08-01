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
