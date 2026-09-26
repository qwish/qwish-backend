package enrollment

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var (
	ErrJoinCodeInvalid = errors.New("join code invalid or ambiguous")
	ErrJoinChanged     = errors.New("join destination changed")
	ErrJoinSuspended   = errors.New("enrollment suspended")
	ErrJoinRole        = errors.New("only students can join")
)

// JoinPreview contains destination information only, never the roster's personal
// information. Codes are case-sensitive. A collision across code namespaces is
// rejected instead of guessing which destination the student intended.
type JoinPreview struct {
	AlreadyRequested bool   `json:"already_requested"`
	Kind             string `json:"kind"`
	TargetID         string `json:"target_id"`
	InstitutionID    string `json:"institution_id"`
	InstitutionName  string `json:"institution_name"`
	ClassName        string `json:"class_name,omitempty"`
	AlreadyJoined    bool   `json:"already_joined"`
	RequiresApproval bool   `json:"requires_approval"`
	TransferRequired bool   `json:"transfer_required"`
	RequestID        string `json:"request_id,omitempty"`
	RequestStatus    string `json:"request_status,omitempty"`
}

type JoinResult struct {
	Destination JoinPreview `json:"destination"`
	Enrollment  *Enrollment `json:"enrollment,omitempty"`
	Status      string      `json:"status"`
}

type joinQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func resolveJoin(ctx context.Context, q joinQuerier, userID, code string) (JoinPreview, error) {
	rows, err := q.Query(ctx, `
 SELECT 'claim', e.id::text, i.id::text, i.name, '', e.status, COALESCE(e.user_id::text,'')
 FROM enrollments e JOIN institutions i ON i.id=e.institution_id
 WHERE e.claim_code=$1 AND i.status='verified'
 UNION ALL
 SELECT 'institution', i.id::text, i.id::text, i.name, '', '', ''
 FROM institutions i WHERE i.student_referral_code=$1 AND i.status='verified'
 UNION ALL
 SELECT 'class', g.id::text, i.id::text, i.name, g.name, '', ''
 FROM groups g JOIN institutions i ON i.id=g.institution_id
 WHERE g.invite_code=$1 AND g.archived_at IS NULL AND i.status='verified'`, code)
	if err != nil {
		return JoinPreview{}, err
	}
	var p JoinPreview
	var status, owner string
	count := 0
	for rows.Next() {
		count++
		if err := rows.Scan(&p.Kind, &p.TargetID, &p.InstitutionID, &p.InstitutionName, &p.ClassName, &status, &owner); err != nil {
			rows.Close()
			return p, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	if count != 1 {
		return p, ErrJoinCodeInvalid
	}
	if p.Kind == "claim" && status != "pending_claim" && !(status == "active" && owner == userID) {
		return p, ErrClaimCodeUsed
	}
	var enrollmentID, instID, enrollmentStatus string
	err = q.QueryRow(ctx, `SELECT id::text, institution_id::text, status FROM enrollments WHERE user_id=$1 AND status IN ('active','suspended')`, userID).Scan(&enrollmentID, &instID, &enrollmentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if instID != p.InstitutionID {
		if enrollmentStatus == "suspended" {
			return p, ErrJoinSuspended
		}
		p.TransferRequired = true
		return p, nil
	}
	if enrollmentStatus == "suspended" {
		return p, ErrJoinSuspended
	}
	if p.Kind == "claim" && enrollmentID != p.TargetID {
		return p, ErrEnrollmentExists
	}
	p.AlreadyJoined = true
	if p.Kind == "class" {
		err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM group_students WHERE group_id=$1 AND user_id=$2)`, p.TargetID, userID).Scan(&p.AlreadyJoined)
	}
	return p, err
}

func (s *Service) PreviewJoin(ctx context.Context, userID, code string) (JoinPreview, error) {
	p, err := resolveJoin(ctx, s.db, userID, code)
	if err == nil {
		err = decorateJoin(ctx, s.db, userID, code, &p)
	}
	return p, err
}

// ConfirmJoin revalidates the reviewed destination and serializes this student's
// joins. Repeating a completed request returns its result; class membership and
// enrollment commit together. Existing memberships are never transferred.
func (s *Service) ConfirmJoin(ctx context.Context, userID, code, kind, targetID string) (JoinResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return JoinResult{}, err
	}
	defer tx.Rollback(ctx)
	var name, role string
	var userInstitution *string
	err = tx.QueryRow(ctx, `SELECT full_name, role, institution_id::text FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&name, &role, &userInstitution)
	if err != nil {
		return JoinResult{}, err
	}
	if role != "student" {
		return JoinResult{}, ErrJoinRole
	}
	p, err := resolveJoin(ctx, tx, userID, code)
	if err != nil {
		return JoinResult{}, err
	}
	if p.Kind != kind || p.TargetID != targetID {
		return JoinResult{}, ErrJoinChanged
	}
	if userInstitution != nil && *userInstitution != p.InstitutionID {
		p.TransferRequired = true
	}
	// Keep the destination valid until commit, including concurrent code resets,
	// roster claims, class archival and institution suspension.
	var lockedID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM institutions WHERE id=$1 AND status='verified' FOR SHARE`, p.InstitutionID).Scan(&lockedID)
	if err != nil {
		return JoinResult{}, ErrJoinChanged
	}
	switch p.Kind {
	case "claim":
		err = tx.QueryRow(ctx, `SELECT id::text FROM enrollments WHERE id=$1 AND claim_code=$2 FOR UPDATE`, p.TargetID, code).Scan(&lockedID)
	case "class":
		err = tx.QueryRow(ctx, `SELECT id::text FROM groups WHERE id=$1 AND invite_code=$2 AND archived_at IS NULL FOR SHARE`, p.TargetID, code).Scan(&lockedID)
	case "institution":
		err = tx.QueryRow(ctx, `SELECT id::text FROM institutions WHERE id=$1 AND student_referral_code=$2`, p.TargetID, code).Scan(&lockedID)
	}
	if err != nil {
		return JoinResult{}, ErrJoinChanged
	}
	p, err = resolveJoin(ctx, tx, userID, code)
	if err != nil {
		return JoinResult{}, err
	}
	if p.Kind != kind || p.TargetID != targetID {
		return JoinResult{}, ErrJoinChanged
	}
	if err = decorateJoin(ctx, tx, userID, code, &p); err != nil {
		return JoinResult{}, err
	}
	if p.RequiresApproval || p.TransferRequired || p.RequestID != "" {
		var source *string
		if p.TransferRequired {
			source = userInstitution
			if source == nil {
				var existing string
				if err = tx.QueryRow(ctx, `SELECT institution_id FROM enrollments WHERE user_id=$1 AND status='active'`, userID).Scan(&existing); err != nil {
					return JoinResult{}, err
				}
				source = &existing
			}
		}
		id, err := queueAdmission(ctx, tx, userID, code, p, source)
		if err != nil {
			return JoinResult{}, err
		}
		p.RequestID = id
		if err = tx.QueryRow(ctx, `SELECT status FROM admission_requests WHERE id=$1`, id).Scan(&p.RequestStatus); err != nil {
			return JoinResult{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return JoinResult{}, err
		}
		return JoinResult{Destination: p, Status: p.RequestStatus}, nil
	}
	e, err := activateEnrollment(ctx, tx, userID, name, p)
	if err != nil {
		return JoinResult{}, err
	}
	if p.Kind == "class" {
		if _, err = tx.Exec(ctx, `INSERT INTO group_students (group_id,user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, p.TargetID, userID); err != nil {
			return JoinResult{}, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET institution_id=$1, updated_at=now() WHERE id=$2`, p.InstitutionID, userID); err != nil {
		return JoinResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return JoinResult{}, err
	}
	return JoinResult{Destination: p, Enrollment: &e, Status: "joined"}, nil
}

func decorateJoin(ctx context.Context, q joinQuerier, user, code string, p *JoinPreview) error {
	var role, status string
	var inst *string
	if err := q.QueryRow(ctx, `SELECT role,status,institution_id::text FROM users WHERE id=$1`, user).Scan(&role, &status, &inst); err != nil {
		return err
	}
	if role != "student" {
		return ErrJoinRole
	}
	if status != "active" {
		return ErrJoinSuspended
	}
	if inst != nil && *inst != p.InstitutionID {
		p.TransferRequired = true
	}
	var member bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM enrollments WHERE user_id=$1 AND institution_id=$2 AND status='active')`, user, p.InstitutionID).Scan(&member); err != nil {
		return err
	}
	if member {
		return nil
	}
	var requestInst string
	err := q.QueryRow(ctx, `SELECT id,institution_id,status FROM admission_requests WHERE user_id=$1 AND status IN ('pending','approved')`, user).Scan(&p.RequestID, &requestInst, &p.RequestStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil && requestInst != p.InstitutionID {
		return ErrPendingElsewhere
	}
	if p.RequestID != "" {
		if err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admission_targets WHERE request_id=$1 AND kind=$2 AND target_id=$3 AND code=$4)`, p.RequestID, p.Kind, p.TargetID, code).Scan(&p.AlreadyRequested); err != nil {
			return err
		}
	}
	p.RequiresApproval, err = needsReview(ctx, q, user, p.InstitutionID)
	p.RequiresApproval = p.RequiresApproval || p.RequestID != ""
	return err
}
