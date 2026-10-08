package db

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReopenClass un-archives a class within 90 days of ending. That matches the
// auto-end window: after it, members' enrollments may already have ended. scope is an extra
// predicate on groups with $2.. bound to scopeArgs (institute or teacher check).
// It returns an HTTP status and an error code ("" on success).
func ReopenClass(ctx context.Context, pool *pgxpool.Pool, groupID, scope string, scopeArgs ...any) (int, string) {
	if _, err := uuid.Parse(groupID); err != nil {
		return http.StatusNotFound, "NOT_FOUND"
	}
	args := append([]any{groupID}, scopeArgs...)
	tx, err := pool.Begin(ctx)
	if err != nil {
		return http.StatusInternalServerError, "INTERNAL"
	}
	defer tx.Rollback(ctx)
	// Shared lock order: institution, then class/department. All department
	// archive, placement and role-grant paths acquire this same institution lock.
	var inst string
	err = tx.QueryRow(ctx, `SELECT institution_id FROM groups WHERE id=$1 AND `+scope, args...).Scan(&inst)
	if errors.Is(err, pgx.ErrNoRows) {
		return http.StatusNotFound, "NOT_FOUND"
	}
	if err != nil {
		return http.StatusInternalServerError, "INTERNAL"
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM institutions WHERE id=$1 FOR UPDATE`, inst); err != nil {
		return http.StatusInternalServerError, "INTERNAL"
	}
	var blocked bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM departments d WHERE d.id=g.department_id AND d.archived_at IS NOT NULL) FROM groups g WHERE g.id=$1 FOR UPDATE OF g`, groupID).Scan(&blocked)
	if err != nil {
		return http.StatusInternalServerError, "INTERNAL"
	}
	if blocked {
		return http.StatusConflict, "DEPARTMENT_ARCHIVED"
	}
	tag, err := tx.Exec(ctx, `UPDATE groups SET archived_at=NULL WHERE id=$1 AND `+scope+`
		AND archived_at > now() - interval '90 days'`, args...)
	if err != nil {
		return http.StatusInternalServerError, "INTERNAL"
	}
	if tag.RowsAffected() == 1 {
		if err = tx.Commit(ctx); err != nil {
			return http.StatusInternalServerError, "INTERNAL"
		}
		return http.StatusOK, ""
	}
	// Nothing reopened: missing, already live, or past the window.
	var archived bool
	err = tx.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM groups WHERE id=$1 AND `+scope, args...).Scan(&archived)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return http.StatusNotFound, "NOT_FOUND"
	case err != nil:
		return http.StatusInternalServerError, "INTERNAL"
	case archived:
		return http.StatusConflict, "CLASS_REOPEN_EXPIRED"
	}
	return http.StatusOK, ""
}
