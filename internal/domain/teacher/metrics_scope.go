package teacher

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/metrics"
	"github.com/qwish/backend/internal/middleware"
)

// NoClassesReason is shown verbatim in the panel. An unassigned teacher stays
// class-scoped over an empty class set, so every student metric reads zero;
// this sentence tells them why instead of letting zero read as "all clear".
const NoClassesReason = "no classes assigned — assign a class to see student data"

// MetricsScopeResolver picks the teacher's scope from the `scope` parameter and
// their group assignments. The id always comes from the token: `scope` selects
// the kind and nothing else. It never widens to the institution: a teacher with
// no classes gets the no-assigned-classes state, not institute-wide data.
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
		if kind == metrics.ScopeQuizzes {
			return metrics.Scope{Kind: kind, ID: teacherID}, note, nil
		}

		assigned, err := hasGroups(r.Context(), db, teacherID)
		if err != nil {
			return metrics.Scope{}, metrics.ScopeNote{}, err
		}
		if !assigned {
			note.Reason = NoClassesReason
		}
		return metrics.Scope{Kind: metrics.ScopeClasses, ID: teacherID}, note, nil
	}
}

// hasGroups is the analytics counterpart of hasGroupAssignments, which takes a
// *http.Request and swallows its error. This one reports the error, because a
// failed lookup here must not be mistaken for "no classes".
func hasGroups(ctx context.Context, db *pgxpool.Pool, teacherID string) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM group_teachers WHERE user_id = $1)`, teacherID).Scan(&exists)
	return exists, err
}
