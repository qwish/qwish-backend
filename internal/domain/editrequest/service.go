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
