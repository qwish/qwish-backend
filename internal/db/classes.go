package db

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ReopenWindow is how long an ended class can be reopened. It matches the
// auto-end window: after it, members' enrollments may already have ended.
const ReopenWindow = 90 * 24 * time.Hour

// ReopenClass un-archives a class within ReopenWindow. scope is an extra
// predicate on groups with $2 bound to scopeArg (institute or teacher check).
// It returns an HTTP status and an error code ("" on success).
func ReopenClass(ctx context.Context, pool *pgxpool.Pool, groupID, scope, scopeArg string) (int, string) {
	var archivedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT archived_at FROM groups WHERE id=$1 AND `+scope, groupID, scopeArg).Scan(&archivedAt); err != nil {
		return http.StatusNotFound, "NOT_FOUND"
	}
	if archivedAt == nil {
		return http.StatusOK, ""
	}
	if time.Since(*archivedAt) > ReopenWindow {
		return http.StatusConflict, "CLASS_REOPEN_EXPIRED"
	}
	if _, err := pool.Exec(ctx, `UPDATE groups SET archived_at=NULL WHERE id=$1`, groupID); err != nil {
		return http.StatusInternalServerError, "INTERNAL"
	}
	return http.StatusOK, ""
}
