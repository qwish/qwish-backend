package enrollment

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ListMine returns the student's live enrollments, active institute first.
func (s *Service) ListMine(ctx context.Context, userID string) ([]Enrollment, error) {
	rows, err := s.db.Query(ctx, `SELECT `+qualified("e", selectCols)+`, e.institution_id = u.institution_id
		FROM enrollments e JOIN users u ON u.id=e.user_id
		WHERE e.user_id=$1 AND e.status IN ('active','suspended')
		ORDER BY e.institution_id = u.institution_id DESC, e.joined_at`, userID)
	if err != nil {
		return nil, err
	}
	out := []Enrollment{}
	for rows.Next() {
		var e Enrollment
		if err := rows.Scan(&e.ID, &e.InstitutionID, &e.UserID, &e.FullName, &e.Email, &e.RollNumber, &e.Grade,
			&e.Section, &e.AdmissionDate, &e.ClaimCode, &e.Status, &e.JoinedAt, &e.EndedAt, &e.Active); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.addProfileContext(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) SetActive(ctx context.Context, userID, instID string) error {
	tag, err := s.db.Exec(ctx, `UPDATE users SET institution_id=$2, updated_at=now()
		WHERE id=$1 AND EXISTS(SELECT 1 FROM enrollments WHERE user_id=$1 AND institution_id=$2 AND status='active')`,
		userID, instID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ErrLeaveSuspended: a suspended student can't leave and rejoin fresh by code;
// the institute lifts the suspension or ends the enrollment.
var ErrLeaveSuspended = errors.New("cannot leave while suspended")

// Leave ends the student's own enrollment. Class rows at that institute are
// removed (history is kept by plan 3's trigger); the active pointer is moved
// by the sync_student_institute trigger.
func (s *Service) Leave(ctx context.Context, userID, enrollmentID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var instID string
	err = tx.QueryRow(ctx, `UPDATE enrollments SET status='left', ended_by='student', ended_at=now(), updated_at=now()
		WHERE id=$1 AND user_id=$2 AND status='active' RETURNING institution_id::text`, enrollmentID, userID).Scan(&instID)
	if errors.Is(err, pgx.ErrNoRows) {
		var suspended bool
		if s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM enrollments WHERE id=$1 AND user_id=$2 AND status='suspended')`, enrollmentID, userID).Scan(&suspended); suspended {
			return ErrLeaveSuspended
		}
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM group_students gs USING groups g
		WHERE gs.group_id=g.id AND gs.user_id=$1 AND g.institution_id=$2 AND g.archived_at IS NULL`, userID, instID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// qualified prefixes each column in a selectCols-style list with alias.
func qualified(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = alias + "." + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}
