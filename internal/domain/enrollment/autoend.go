package enrollment

import (
	"context"
	"time"
)

const (
	autoEndAfter = 90 * 24 * time.Hour
	warnBefore   = 7 * 24 * time.Hour
)

type EndNotice struct {
	UserID          string    `json:"user_id"`
	InstitutionName string    `json:"institution_name"`
	EndsOn          time.Time `json:"ends_on"`
}

// classlessSQL lists live enrollments with no live class at their institute and
// the moment they became classless. $1 = now.
const classlessSQL = `
SELECT e.id, e.user_id::text, i.name, e.end_warned_at,
       GREATEST(COALESCE(e.joined_at, e.created_at),
                COALESCE((SELECT max(g.archived_at) FROM group_students gs JOIN groups g ON g.id=gs.group_id
                          WHERE gs.user_id=e.user_id AND g.institution_id=e.institution_id), '-infinity')) AS since
  FROM enrollments e JOIN institutions i ON i.id=e.institution_id
 WHERE e.status IN ('active','suspended') AND e.user_id IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM group_students gs JOIN groups g ON g.id=gs.group_id
                    WHERE gs.user_id=e.user_id AND g.institution_id=e.institution_id AND g.archived_at IS NULL)`

// EndInactive warns students 7 days before, and ends enrollments that have had
// no live class for 90 days. Callers send the notifications.
func (s *Service) EndInactive(ctx context.Context, now time.Time) (warn, ended []EndNotice, err error) {
	rows, err := s.db.Query(ctx, classlessSQL)
	if err != nil {
		return nil, nil, err
	}
	type row struct {
		id, user, inst string
		warned         *time.Time
		since          time.Time
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.user, &r.inst, &r.warned, &r.since); err != nil {
			rows.Close()
			return nil, nil, err
		}
		all = append(all, r)
	}
	rows.Close()
	for _, r := range all {
		endsOn := r.since.Add(autoEndAfter)
		switch {
		case !now.Before(endsOn):
			tag, err := s.db.Exec(ctx, `UPDATE enrollments SET status='left', ended_by='system', ended_at=$2, updated_at=now()
				WHERE id=$1 AND status IN ('active','suspended')`, r.id, now)
			if err != nil {
				return warn, ended, err
			}
			if tag.RowsAffected() == 1 {
				ended = append(ended, EndNotice{r.user, r.inst, endsOn})
			}
		case !now.Before(endsOn.Add(-warnBefore)) && r.warned == nil:
			if _, err := s.db.Exec(ctx, `UPDATE enrollments SET end_warned_at=$2 WHERE id=$1`, r.id, now); err != nil {
				return warn, ended, err
			}
			warn = append(warn, EndNotice{r.user, r.inst, endsOn})
		}
	}
	return warn, ended, nil
}
