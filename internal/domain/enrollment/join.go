package enrollment

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var (
	ErrJoinCodeInvalid = errors.New("join code invalid")
	ErrJoinClosed      = errors.New("class is invite-only")
	ErrJoinChanged     = errors.New("join destination changed")
	ErrJoinSuspended   = errors.New("enrollment suspended")
	ErrJoinRole        = errors.New("only students can join")
	ErrInstituteCap    = errors.New("student is at the institute limit")
)

// MaxLiveInstitutes is how many institutes a student may belong to at once.
const MaxLiveInstitutes = 2

// verifiedEmailsCTE lists every address the student has proven they control:
// the login email plus verified secondary emails. $1 is the user id.
const verifiedEmailsCTE = `emails AS (
	SELECT lower(btrim(email)) AS email FROM users WHERE id=$1
	UNION SELECT email FROM user_emails WHERE user_id=$1 AND verified_at IS NOT NULL)`

// JoinPreview describes the class behind a code and how the student would be
// admitted. Kind is always "class"; it stays for older clients.
type JoinPreview struct {
	Kind            string `json:"kind"`
	TargetID        string `json:"target_id"`
	InstitutionID   string `json:"institution_id"`
	InstitutionName string `json:"institution_name"`
	ClassName       string `json:"class_name"`
	Route           string `json:"route"`
	AlreadyJoined   bool   `json:"already_joined"`
}

type JoinResult struct {
	Destination JoinPreview `json:"destination"`
	Enrollment  *Enrollment `json:"enrollment,omitempty"`
	Status      string      `json:"status"`
}

type joinQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func resolveJoin(ctx context.Context, q joinQuerier, userID, code string) (JoinPreview, error) {
	p := JoinPreview{Kind: "class"}
	var joining bool
	err := q.QueryRow(ctx, `SELECT g.id::text, i.id::text, i.name, g.name, g.joining_enabled
		FROM groups g JOIN institutions i ON i.id=g.institution_id
		WHERE g.invite_code=$1 AND g.archived_at IS NULL AND i.status='verified'`, code).
		Scan(&p.TargetID, &p.InstitutionID, &p.InstitutionName, &p.ClassName, &joining)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrJoinCodeInvalid
	}
	if err != nil {
		return p, err
	}
	if err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM group_students WHERE group_id=$1 AND user_id=$2)`,
		p.TargetID, userID).Scan(&p.AlreadyJoined); err != nil {
		return p, err
	}
	var invite, domain bool
	if err = q.QueryRow(ctx, `WITH `+verifiedEmailsCTE+` SELECT
		EXISTS(SELECT 1 FROM student_invites si JOIN emails e ON e.email=si.email
		        WHERE si.status='pending' AND si.institution_id=$2 AND (si.group_id IS NULL OR si.group_id=$3)),
		EXISTS(SELECT 1 FROM emails e JOIN institution_domains d ON d.domain=split_part(e.email,'@',2)
		        WHERE d.verified_at IS NOT NULL AND d.institution_id=$2)`,
		userID, p.InstitutionID, p.TargetID).Scan(&invite, &domain); err != nil {
		return p, err
	}
	switch {
	case invite:
		p.Route = "invite"
	case domain:
		p.Route = "domain"
	case joining:
		p.Route = "code"
	default:
		return p, ErrJoinClosed
	}
	return p, nil
}

func (s *Service) PreviewJoin(ctx context.Context, userID, code string) (JoinPreview, error) {
	return resolveJoin(ctx, s.db, userID, code)
}

// ConfirmJoin admits the student to the class. The users row lock serializes a
// student's joins, which is what makes the institute cap safe under concurrency.
func (s *Service) ConfirmJoin(ctx context.Context, userID, code, targetID string) (JoinResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return JoinResult{}, err
	}
	defer tx.Rollback(ctx)

	var locked string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM groups WHERE id=$1 AND invite_code=$2 AND archived_at IS NULL FOR SHARE`,
		targetID, code).Scan(&locked); err != nil {
		return JoinResult{}, ErrJoinChanged
	}

	var role string
	if err = tx.QueryRow(ctx, `SELECT role FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&role); err != nil {
		return JoinResult{}, err
	}
	if role != "student" {
		return JoinResult{}, ErrJoinRole
	}
	p, err := resolveJoin(ctx, tx, userID, code)
	if err != nil {
		return JoinResult{}, err
	}
	if p.TargetID != targetID {
		return JoinResult{}, ErrJoinChanged
	}

	e, err := ensureEnrollment(ctx, tx, userID, p)
	if err != nil {
		return JoinResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO group_students (group_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, p.TargetID, userID); err != nil {
		return JoinResult{}, err
	}
	if p.Route == "invite" {
		if _, err = tx.Exec(ctx, `WITH `+verifiedEmailsCTE+`
			UPDATE student_invites si SET status='accepted', accepted_by=$1, accepted_at=now()
			  FROM emails e WHERE e.email=si.email AND si.status='pending' AND si.institution_id=$2
			   AND (si.group_id IS NULL OR si.group_id=$3)`, userID, p.InstitutionID, p.TargetID); err != nil {
			return JoinResult{}, err
		}
	}
	// The class the student just joined is what they want to see next.
	if _, err = tx.Exec(ctx, `UPDATE users SET institution_id=$1, updated_at=now() WHERE id=$2`, p.InstitutionID, userID); err != nil {
		return JoinResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return JoinResult{}, err
	}
	p.AlreadyJoined = true
	return JoinResult{Destination: p, Enrollment: &e, Status: "joined"}, nil
}

// ensureEnrollment returns the live enrollment at the class's institute,
// creating it when the student is under the institute cap.
func ensureEnrollment(ctx context.Context, tx pgx.Tx, userID string, p JoinPreview) (Enrollment, error) {
	e, err := scanEnrollment(tx.QueryRow(ctx, `SELECT `+selectCols+` FROM enrollments
		WHERE user_id=$1 AND institution_id=$2 AND status IN ('active','suspended')`, userID, p.InstitutionID))
	if err == nil {
		if e.Status == "suspended" {
			return e, ErrJoinSuspended
		}
		// Joining a class cancels a pending auto-end warning.
		_, err = tx.Exec(ctx, `UPDATE enrollments SET end_warned_at=NULL WHERE id=$1 AND end_warned_at IS NOT NULL`, e.ID)
		return e, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return e, err
	}
	var live int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM enrollments WHERE user_id=$1 AND status IN ('active','suspended')`, userID).Scan(&live); err != nil {
		return e, err
	}
	if live >= MaxLiveInstitutes {
		return e, ErrInstituteCap
	}
	return scanEnrollment(tx.QueryRow(ctx, `INSERT INTO enrollments (institution_id, user_id, full_name, email, status, joined_at, join_route)
		SELECT $1, id, COALESCE(NULLIF(full_name,''), display_name), email, 'active', now(), $3 FROM users WHERE id=$2
		RETURNING `+selectCols, p.InstitutionID, userID, p.Route))
}
