package curriculum

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

var ErrYearReview = errors.New("review the date changes and overlapping years, then confirm with a reason")

type YearImpact struct {
	ID             string `json:"id"`
	ClassName      string `json:"class_name"`
	CurriculumName string `json:"curriculum_name"`
	Before         string `json:"before"`
	After          string `json:"after"`
}
type YearReview struct {
	Token       string       `json:"token"`
	Before      *YearInput   `json:"before"`
	After       YearInput    `json:"after"`
	Overlaps    []string     `json:"overlaps"`
	Assignments []YearImpact `json:"assignments"`
	Required    bool         `json:"required"`
}

// Institution lock precedes the year/class lock and serializes reviews with
// year writes and new curriculum assignments. The token binds the exact impact.
func reviewYear(ctx context.Context, tx pgx.Tx, inst, id string, in YearInput) (YearReview, error) {
	out := YearReview{After: YearInput{Name: in.Name, StartsOn: in.StartsOn, EndsOn: in.EndsOn}, Overlaps: []string{}, Assignments: []YearImpact{}}
	if _, err := tx.Exec(ctx, `SELECT id FROM institutions WHERE id=$1 FOR UPDATE`, inst); err != nil {
		return out, err
	}
	if id != "" {
		old := YearInput{}
		if err := tx.QueryRow(ctx, `SELECT name,starts_on::text,ends_on::text FROM academic_years WHERE id=$1 AND institution_id=$2 FOR UPDATE`, id, inst).Scan(&old.Name, &old.StartsOn, &old.EndsOn); err != nil {
			return out, dbError(err)
		}
		out.Before = &old
	}
	rows, err := tx.Query(ctx, `SELECT name FROM academic_years WHERE institution_id=$1 AND id::text<>$2 AND starts_on<=$4::text::date AND ends_on>=$3::text::date ORDER BY id`, inst, id, in.StartsOn, in.EndsOn)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return out, err
		}
		out.Overlaps = append(out.Overlaps, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if id != "" {
		rows, err = tx.Query(ctx, `SELECT cc.id,g.name,c.name,
 CASE WHEN d.today<y.starts_on THEN 'upcoming' WHEN d.today>y.ends_on THEN 'past' ELSE 'current' END,
 CASE WHEN d.today<$3::text::date THEN 'upcoming' WHEN d.today>$4::text::date THEN 'past' ELSE 'current' END
 FROM class_curricula cc JOIN groups g ON g.id=cc.group_id JOIN curriculum_versions v ON v.id=cc.version_id JOIN curricula c ON c.id=v.curriculum_id
 JOIN academic_years y ON y.id=cc.academic_year_id JOIN institutions i ON i.id=y.institution_id
 CROSS JOIN LATERAL (SELECT (now() AT TIME ZONE i.timezone)::date today) d
 WHERE cc.academic_year_id=$1 AND cc.institution_id=$2 AND cc.ended_at IS NULL ORDER BY cc.id`, id, inst, in.StartsOn, in.EndsOn)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var a YearImpact
			if err = rows.Scan(&a.ID, &a.ClassName, &a.CurriculumName, &a.Before, &a.After); err != nil {
				rows.Close()
				return out, err
			}
			out.Assignments = append(out.Assignments, a)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	datesChanged := out.Before != nil && (out.Before.StartsOn != in.StartsOn || out.Before.EndsOn != in.EndsOn)
	out.Required = len(out.Overlaps) > 0 || (datesChanged && len(out.Assignments) > 0)
	raw, _ := json.Marshal(out)
	sum := sha256.Sum256(append([]byte(inst+id), raw...))
	out.Token = hex.EncodeToString(sum[:])
	return out, nil
}
func (s *Service) PreviewYear(ctx context.Context, actor Actor, id string, in YearInput) (YearReview, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return YearReview{}, err
	}
	defer tx.Rollback(ctx)
	return reviewYear(ctx, tx, actor.InstitutionID, id, in)
}
func approveYear(review YearReview, in YearInput) error {
	if review.Required && (in.ReviewToken != review.Token || strings.TrimSpace(in.ReviewReason) == "") {
		return ErrYearReview
	}
	return nil
}
func auditYear(ctx context.Context, tx pgx.Tx, actor Actor, id, action string, review YearReview, in YearInput) error {
	payload, _ := json.Marshal(map[string]any{"reason": in.ReviewReason, "before": review.Before, "after": review.After, "overlaps": review.Overlaps, "assignments": review.Assignments})
	_, err := tx.Exec(ctx, `INSERT INTO audit_log(admin_id,admin_name,admin_role,action_type,target_type,target_id,institution_id,reason)
 VALUES($1,COALESCE((SELECT display_name FROM users WHERE id=$1),''),'institution_admin',$2,'academic_year',$3,$4,$5)`, actor.ID, action, id, actor.InstitutionID, string(payload))
	return err
}
