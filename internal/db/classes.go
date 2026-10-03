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
	tag, err := pool.Exec(ctx, `UPDATE groups SET archived_at=NULL WHERE id=$1 AND `+scope+`
		AND archived_at > now() - interval '90 days'`, args...)
	if err != nil {
		return http.StatusInternalServerError, "INTERNAL"
	}
	if tag.RowsAffected() == 1 {
		return http.StatusOK, ""
	}
	// Nothing reopened: missing, already live, or past the window.
	var archived bool
	err = pool.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM groups WHERE id=$1 AND `+scope, args...).Scan(&archived)
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
