package enrollment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Promotion moves chosen students out of one class and into another, and keeps
// enough of their prior position to undo the whole thing.
//
// It replaces a bulk UPDATE keyed on grade and section. An admin thinks in
// classes, not in grade/section pairs, and wants to decide per student who goes
// up — which a WHERE clause cannot express.

var (
	ErrNoStudentsChosen     = errors.New("no students selected to promote")
	ErrBatchNotFound        = errors.New("promotion not found")
	ErrBatchNotRevertible   = errors.New("this promotion is past its 30-day undo window")
	ErrBatchAlreadyReverted = errors.New("this promotion has already been reverted")
)

// PromotionRequest is one run of the flow: a source class, the students chosen
// out of it, the destination, and the students deliberately left behind.
type PromotionRequest struct {
	SourceGroupID string
	TargetGroupID string
	ToGrade       string
	ToSection     string
	// Enrollment ids the admin ticked.
	Promote []string
	// Students left behind, with the filter that excluded them, in words.
	Retained []RetainedStudent
}

type RetainedStudent struct {
	EnrollmentID string
	Reason       string
}

type PromotionResult struct {
	BatchID         string    `json:"batch_id"`
	Promoted        int       `json:"promoted"`
	Retained        int       `json:"retained"`
	RevertibleUntil time.Time `json:"revertible_until"`
}

// Promote runs one promotion as a single transaction: either every student
// moves and the batch is recorded, or nothing happens. A half-applied promotion
// with no record is the state the undo could never recover from.
func (s *Service) PromoteBatch(ctx context.Context, instID, adminID string, req PromotionRequest) (*PromotionResult, error) {
	if req.ToGrade == "" {
		return nil, fmt.Errorf("to_grade is required")
	}
	if len(req.Promote) == 0 {
		return nil, ErrNoStudentsChosen
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var batchID string
	var revertibleUntil time.Time
	if err := tx.QueryRow(ctx,
		`INSERT INTO promotion_batches
		   (institution_id, performed_by, source_group_id, target_group_id, to_grade, to_section,
		    promoted_count, retained_count)
		 VALUES ($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5,NULLIF($6,''),$7,$8)
		 RETURNING id, revertible_until`,
		instID, adminID, req.SourceGroupID, req.TargetGroupID, req.ToGrade, req.ToSection,
		len(req.Promote), len(req.Retained),
	).Scan(&batchID, &revertibleUntil); err != nil {
		return nil, err
	}

	moved := 0
	for _, enrollmentID := range req.Promote {
		// Read the prior position and confirm the enrollment is this
		// institution's and still live. Scoping the SELECT is what stops a
		// crafted enrollment id from another school being moved.
		var userID *string
		var priorGrade, priorSection *string
		err := tx.QueryRow(ctx,
			`SELECT user_id, grade, section FROM enrollments
			  WHERE id=$1 AND institution_id=$2
			    AND status IN ('pending_claim','active','suspended')`,
			enrollmentID, instID,
		).Scan(&userID, &priorGrade, &priorSection)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // not ours, or no longer live — silently not promoted
		}
		if err != nil {
			return nil, err
		}

		// An unclaimed roster row has no user, so it holds no class membership.
		// Its grade still advances; only the group move is skipped.
		var priorGroupID *string
		if userID != nil && req.SourceGroupID != "" {
			var g string
			if err := tx.QueryRow(ctx,
				`SELECT group_id FROM group_students WHERE user_id=$1 AND group_id=$2`,
				*userID, req.SourceGroupID,
			).Scan(&g); err == nil {
				priorGroupID = &g
			}
		}

		if _, err := tx.Exec(ctx,
			`UPDATE enrollments
			    SET grade=$1, section=CASE WHEN $2 <> '' THEN $2 ELSE section END, updated_at=now()
			  WHERE id=$3`,
			req.ToGrade, req.ToSection, enrollmentID); err != nil {
			return nil, err
		}

		if userID != nil && req.TargetGroupID != "" {
			if req.SourceGroupID != "" {
				if _, err := tx.Exec(ctx,
					`DELETE FROM group_students WHERE user_id=$1 AND group_id=$2`,
					*userID, req.SourceGroupID); err != nil {
					return nil, err
				}
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO group_students (group_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
				req.TargetGroupID, *userID); err != nil {
				return nil, err
			}
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO promotion_batch_students
			   (batch_id, enrollment_id, outcome, prior_group_id, prior_grade, prior_section)
			 VALUES ($1,$2,'promoted',$3,$4,$5)`,
			batchID, enrollmentID, priorGroupID, priorGrade, priorSection); err != nil {
			return nil, err
		}
		moved++
	}

	for _, r := range req.Retained {
		var priorGrade, priorSection *string
		err := tx.QueryRow(ctx,
			`SELECT grade, section FROM enrollments WHERE id=$1 AND institution_id=$2`,
			r.EnrollmentID, instID,
		).Scan(&priorGrade, &priorSection)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// Nothing changes for a retained student. The row exists so the reason
		// survives, and so the class they stayed in is answerable later.
		if _, err := tx.Exec(ctx,
			`INSERT INTO promotion_batch_students
			   (batch_id, enrollment_id, outcome, prior_group_id, prior_grade, prior_section, retained_reason)
			 VALUES ($1,$2,'retained',NULLIF($3,'')::uuid,$4,$5,NULLIF($6,''))`,
			batchID, r.EnrollmentID, req.SourceGroupID, priorGrade, priorSection, r.Reason); err != nil {
			return nil, err
		}
	}

	// The header counts what actually happened, not what was asked for.
	if _, err := tx.Exec(ctx,
		`UPDATE promotion_batches SET promoted_count=$1 WHERE id=$2`, moved, batchID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &PromotionResult{
		BatchID: batchID, Promoted: moved, Retained: len(req.Retained),
		RevertibleUntil: revertibleUntil,
	}, nil
}

type RevertResult struct {
	Reverted int `json:"reverted"`
	// Students left alone because someone moved them after the promotion.
	Skipped int `json:"skipped"`
}

// RevertBatch undoes a promotion, skipping any student whose position changed
// since it ran.
//
// The skip is the whole point: a blanket revert would silently undo a
// correction someone made on purpose. A student only goes back if they are
// still exactly where the promotion put them.
func (s *Service) RevertBatch(ctx context.Context, instID, adminID, batchID string) (*RevertResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var targetGroupID *string
	var toGrade string
	var toSection *string
	var revertibleUntil time.Time
	var revertedAt *time.Time
	if err := tx.QueryRow(ctx,
		`SELECT target_group_id, to_grade, to_section, revertible_until, reverted_at
		   FROM promotion_batches WHERE id=$1 AND institution_id=$2`,
		batchID, instID,
	).Scan(&targetGroupID, &toGrade, &toSection, &revertibleUntil, &revertedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBatchNotFound
		}
		return nil, err
	}
	if revertedAt != nil {
		return nil, ErrBatchAlreadyReverted
	}
	if time.Now().After(revertibleUntil) {
		return nil, ErrBatchNotRevertible
	}

	rows, err := tx.Query(ctx,
		`SELECT pbs.enrollment_id, pbs.prior_group_id, pbs.prior_grade, pbs.prior_section, e.user_id
		   FROM promotion_batch_students pbs
		   JOIN enrollments e ON e.id = pbs.enrollment_id
		  WHERE pbs.batch_id=$1 AND pbs.outcome='promoted'`,
		batchID)
	if err != nil {
		return nil, err
	}
	type row struct {
		enrollmentID string
		priorGroup   *string
		priorGrade   *string
		priorSection *string
		userID       *string
	}
	var students []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.enrollmentID, &r.priorGroup, &r.priorGrade, &r.priorSection, &r.userID); err != nil {
			rows.Close()
			return nil, err
		}
		students = append(students, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	reverted, skipped := 0, 0
	for _, st := range students {
		// Still where the promotion left them? Grade must match, and section
		// only when the promotion set one.
		var stillThere bool
		if err := tx.QueryRow(ctx,
			`SELECT (grade IS NOT DISTINCT FROM $2)
			    AND ($3::text IS NULL OR section IS NOT DISTINCT FROM $3)
			   FROM enrollments WHERE id=$1`,
			st.enrollmentID, toGrade, toSection,
		).Scan(&stillThere); err != nil {
			return nil, err
		}
		if stillThere && st.userID != nil && targetGroupID != nil {
			var inTarget int
			if err := tx.QueryRow(ctx,
				`SELECT COUNT(*) FROM group_students WHERE user_id=$1 AND group_id=$2`,
				*st.userID, *targetGroupID).Scan(&inTarget); err != nil {
				return nil, err
			}
			stillThere = inTarget > 0
		}
		if !stillThere {
			skipped++
			continue
		}

		if _, err := tx.Exec(ctx,
			`UPDATE enrollments SET grade=$1, section=$2, updated_at=now() WHERE id=$3`,
			st.priorGrade, st.priorSection, st.enrollmentID); err != nil {
			return nil, err
		}
		if st.userID != nil && targetGroupID != nil {
			if _, err := tx.Exec(ctx,
				`DELETE FROM group_students WHERE user_id=$1 AND group_id=$2`,
				*st.userID, *targetGroupID); err != nil {
				return nil, err
			}
			if st.priorGroup != nil {
				if _, err := tx.Exec(ctx,
					`INSERT INTO group_students (group_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
					*st.priorGroup, *st.userID); err != nil {
					return nil, err
				}
			}
		}
		reverted++
	}

	if _, err := tx.Exec(ctx,
		`UPDATE promotion_batches SET reverted_at=now(), reverted_by=$1, reverted_skipped=$2 WHERE id=$3`,
		adminID, skipped, batchID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &RevertResult{Reverted: reverted, Skipped: skipped}, nil
}
