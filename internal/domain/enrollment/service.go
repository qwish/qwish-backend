// Package enrollment owns the enrollments table: the relationship between a
// student and an institution. Institution-owned academic fields live here and
// have no student-facing write path, which is what makes them institution-owned.
package enrollment

import (
	"context"
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
	ErrNotFound         = errors.New("enrollment not found")
)

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
	Grade         *string    `json:"grade,omitempty"`
	Section       *string    `json:"section,omitempty"`
	Status        string     `json:"status"`
	JoinRoute     *string    `json:"join_route,omitempty"`
	JoinedAt      *time.Time `json:"joined_at,omitempty"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	// Presentation context is read-only. These values let student surfaces use
	// the institution's actual name and assigned class instead of local guesses.
	InstitutionName *string `json:"institution_name,omitempty"`
	ClassName       *string `json:"class_name,omitempty"`
	// Active marks the institute the student's app is currently showing.
	Active bool `json:"active"`
}

const selectCols = `id, institution_id, user_id, full_name, email, grade, section, status, joined_at, ended_at, join_route`

func scanEnrollment(row pgx.Row) (Enrollment, error) {
	var e Enrollment
	err := row.Scan(&e.ID, &e.InstitutionID, &e.UserID, &e.FullName, &e.Email,
		&e.Grade, &e.Section, &e.Status, &e.JoinedAt, &e.EndedAt, &e.JoinRoute)
	return e, err
}

type Service struct {
	db       *pgxpool.Pool
	sendMail func(ctx context.Context, to, subject, html string) error
}

// SetMailer wires outbound email; nil leaves invites unsent (tests).
func (s *Service) SetMailer(fn func(ctx context.Context, to, subject, html string) error) {
	s.sendMail = fn
}

func NewService(db *pgxpool.Pool) *Service { return &Service{db: db} }

// ActiveByUser returns the live enrollment at the student's active institute,
// or nil when they have none. A student with no institution is a normal user, not an error case.
func (s *Service) ActiveByUser(ctx context.Context, userID string) (*Enrollment, error) {
	e, err := scanEnrollment(s.db.QueryRow(ctx,
		`SELECT `+selectCols+` FROM enrollments
		 WHERE user_id=$1 AND status IN ('active','suspended')
		   AND institution_id=(SELECT institution_id FROM users WHERE id=$1)`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.addProfileContext(ctx, &e); err != nil {
		return nil, err
	}
	e.Active = true
	return &e, nil
}

// addProfileContext decorates a live enrollment for student-facing reads. A
// learner may belong to several groups, so the most recently joined active
// class is the one shown in compact profile surfaces.
func (s *Service) addProfileContext(ctx context.Context, e *Enrollment) error {
	var institutionName string
	if err := s.db.QueryRow(ctx,
		`SELECT name FROM institutions WHERE id=$1`, e.InstitutionID,
	).Scan(&institutionName); err != nil {
		return err
	}
	e.InstitutionName = &institutionName

	if e.UserID == nil {
		return nil
	}
	var className string
	err := s.db.QueryRow(ctx, `
		SELECT g.name
		FROM groups g
		JOIN group_students gs ON gs.group_id=g.id
		WHERE gs.user_id=$1 AND g.institution_id=$2 AND g.archived_at IS NULL
		ORDER BY gs.joined_at DESC, g.name ASC
		LIMIT 1`, *e.UserID, e.InstitutionID,
	).Scan(&className)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	e.ClassName = &className
	return nil
}

// JoinByClassCode is the legacy one-shot join used by /students/join-class.
func (s *Service) JoinByClassCode(ctx context.Context, userID, code string) (Enrollment, error) {
	p, err := s.PreviewJoin(ctx, userID, code)
	if err != nil {
		return Enrollment{}, err
	}
	r, err := s.ConfirmJoin(ctx, userID, code, p.TargetID)
	if err != nil {
		return Enrollment{}, err
	}
	return *r.Enrollment, nil
}

// terminalStatuses end the relationship: the enrollment is closed and the
// student returns to institution-less, keeping account, points and history.
var terminalStatuses = map[string]bool{"graduated": true, "transferred": true}

// SetStatus moves an enrollment through its lifecycle. Suspension is per
// institute: it pauses that institute (it can't be the active one) and never
// locks the account. Terminal statuses remove the student from live classes.
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
		        ended_by = CASE WHEN $1 IN ('graduated','transferred') THEN 'institution' ELSE NULL END,
		        updated_at = now()
		  WHERE id=$2 AND institution_id=$3
		  RETURNING user_id`, status, enrollmentID, instID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if userID != nil && terminalStatuses[status] {
		if _, err = tx.Exec(ctx, `DELETE FROM group_students gs USING groups g
			WHERE gs.group_id=g.id AND gs.user_id=$1 AND g.institution_id=$2 AND g.archived_at IS NULL`, *userID, instID); err != nil {
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

// SetJoining turns class-code joining on or off for one class. With joining
// off, only invited students and verified institute emails get in.
func (s *Service) SetJoining(ctx context.Context, instID, groupID string, enabled bool) error {
	tag, err := s.db.Exec(ctx, `UPDATE groups SET joining_enabled=$3 WHERE id=$1 AND institution_id=$2`, groupID, instID, enabled)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
