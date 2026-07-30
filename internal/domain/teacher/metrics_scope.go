package teacher

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/metrics"
	"github.com/qwish/backend/internal/middleware"
)

// fallbackReason is shown verbatim in the panel. An unassigned teacher is
// answered institution-wide, and without this sentence they would read those
// numbers as their own class's.
const fallbackReason = "no classes assigned — showing the whole institution"

// MetricsScopeResolver picks the teacher's scope from the `scope` parameter and
// their group assignments. The id always comes from the token: `scope` selects
// the kind and nothing else.
//
// PRD §5.4: a teacher assigned to no group sees all institution students. That
// rule already governs the student list (hasGroupAssignments), and analytics
// honours it here — reporting the substitution rather than performing it
// silently.
func MetricsScopeResolver(db *pgxpool.Pool) metrics.ScopeResolver {
	return func(r *http.Request) (metrics.Scope, metrics.ScopeNote, error) {
		kind, err := metrics.ParseScopeKind(r.URL.Query().Get("scope"))
		if err != nil {
			return metrics.Scope{}, metrics.ScopeNote{},
				fmt.Errorf("%w: %s", metrics.ErrBadScopeRequest, err)
		}

		teacherID := middleware.GetUserID(r)
		if teacherID == "" {
			return metrics.Scope{}, metrics.ScopeNote{},
				fmt.Errorf("%w: no teacher on this token", metrics.ErrBadScopeRequest)
		}

		note := metrics.ScopeNote{Requested: kind, Effective: kind}

		// The quizzes scope never falls back: a teacher with no classes may
		// still have authored quizzes, and "no classes" is not a reason to
		// widen that view.
		if kind == metrics.ScopeQuizzes {
			return metrics.Scope{Kind: kind, ID: teacherID}, note, nil
		}

		assigned, err := hasGroups(r.Context(), db, teacherID)
		if err != nil {
			return metrics.Scope{}, metrics.ScopeNote{}, err
		}
		if assigned {
			return metrics.Scope{Kind: metrics.ScopeClasses, ID: teacherID}, note, nil
		}

		instID := middleware.GetInstitutionID(r)
		if instID == "" {
			return metrics.Scope{}, metrics.ScopeNote{},
				fmt.Errorf("%w: you have no classes and no institution to fall back to",
					metrics.ErrBadScopeRequest)
		}
		note.Effective = metrics.ScopeInstitution
		note.Reason = fallbackReason
		return metrics.Scope{Kind: metrics.ScopeInstitution, ID: instID}, note, nil
	}
}

// hasGroups is the analytics counterpart of hasGroupAssignments, which takes a
// *http.Request and swallows its error. This one reports the error, because a
// failed lookup here would silently widen a teacher's scope.
func hasGroups(ctx context.Context, db *pgxpool.Pool, teacherID string) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM group_teachers WHERE user_id = $1)`, teacherID).Scan(&exists)
	return exists, err
}
