package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

// maxLayoutBytes caps a stored layout. The server does not interpret the JSON
// beyond this and a valid-object check: widget shapes change every frontend
// release, and a server-side schema for them would need a migration each time.
const maxLayoutBytes = 256 * 1024

var (
	ErrDuplicateName = errors.New("a layout with that name already exists")
	ErrNotFound      = errors.New("layout not found")
	ErrLayoutTooBig  = errors.New("layout exceeds the size limit")
)

type Layout struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Layout    json.RawMessage `json:"layout"`
	IsDefault bool            `json:"is_default"`
	Sort      int             `json:"sort"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// LayoutsService serves one layouts table. The table and owner column are
// supplied at construction from compile-time constants at the wiring site —
// never from request data — so interpolating them into the SQL is safe.
//
// Two tables exist because layouts cascade with their owner, and the two owner
// kinds live in different tables: super admins in admin_accounts, institution
// admins and teachers in users.
type LayoutsService struct {
	db       *pgxpool.Pool
	table    string
	ownerCol string
}

func NewLayoutsService(db *pgxpool.Pool, table, ownerCol string) *LayoutsService {
	return &LayoutsService{db: db, table: table, ownerCol: ownerCol}
}

const layoutCols = `id, name, layout, is_default, sort, created_at, updated_at`

func scanLayout(row pgx.Row) (Layout, error) {
	var l Layout
	err := row.Scan(&l.ID, &l.Name, &l.Layout, &l.IsDefault, &l.Sort, &l.CreatedAt, &l.UpdatedAt)
	return l, err
}

// isUniqueViolation reads the Postgres error code rather than matching on the
// message text, so a duplicate name becomes a 409 instead of a 500 and stays
// that way across locales and driver versions.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (s *LayoutsService) List(ctx context.Context, ownerID string) ([]Layout, error) {
	rows, err := s.db.Query(ctx, fmt.Sprintf(
		`SELECT `+layoutCols+` FROM %s
		  WHERE %s = $1 ORDER BY sort, created_at`, s.table, s.ownerCol), ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Layout{} // never nil — serialises as [] for a fresh owner
	for rows.Next() {
		l, err := scanLayout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *LayoutsService) Create(
	ctx context.Context, ownerID, name string, layout json.RawMessage, isDefault bool,
) (Layout, error) {
	if len(layout) > maxLayoutBytes {
		return Layout{}, ErrLayoutTooBig
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Layout{}, err
	}
	defer tx.Rollback(ctx)

	// Clearing the old default and setting the new one must be atomic, or the
	// partial unique index rejects the second write.
	if isDefault {
		if _, err := tx.Exec(ctx, fmt.Sprintf(
			`UPDATE %s SET is_default = false, updated_at = now()
			  WHERE %s = $1 AND is_default`, s.table, s.ownerCol), ownerID); err != nil {
			return Layout{}, err
		}
	}

	l, err := scanLayout(tx.QueryRow(ctx, fmt.Sprintf(
		`INSERT INTO %s (%s, name, layout, is_default, sort)
		 VALUES ($1, $2, $3, $4,
		   COALESCE((SELECT MAX(sort) + 1 FROM %s WHERE %s = $1), 0))
		 RETURNING `+layoutCols, s.table, s.ownerCol, s.table, s.ownerCol),
		ownerID, strings.TrimSpace(name), []byte(layout), isDefault))
	if err != nil {
		if isUniqueViolation(err) {
			return Layout{}, ErrDuplicateName
		}
		return Layout{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Layout{}, err
	}
	return l, nil
}

// Update applies a partial change. A nil pointer means "leave alone".
func (s *LayoutsService) Update(
	ctx context.Context, ownerID, id string,
	name *string, layout *json.RawMessage, isDefault *bool, sort *int,
) (Layout, error) {
	if layout != nil && len(*layout) > maxLayoutBytes {
		return Layout{}, ErrLayoutTooBig
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Layout{}, err
	}
	defer tx.Rollback(ctx)

	// The ownership check and the write share one transaction, and every WHERE
	// carries the owner column, so another owner's row is never reachable.
	var exists bool
	if err := tx.QueryRow(ctx, fmt.Sprintf(
		`SELECT EXISTS (SELECT 1 FROM %s WHERE id = $1 AND %s = $2)`, s.table, s.ownerCol),
		id, ownerID).Scan(&exists); err != nil {
		return Layout{}, err
	}
	if !exists {
		return Layout{}, ErrNotFound
	}

	if isDefault != nil && *isDefault {
		if _, err := tx.Exec(ctx, fmt.Sprintf(
			`UPDATE %s SET is_default = false, updated_at = now()
			  WHERE %s = $1 AND is_default AND id <> $2`, s.table, s.ownerCol),
			ownerID, id); err != nil {
			return Layout{}, err
		}
	}

	// Parameters are cast explicitly so Postgres can type a NULL placeholder
	// inside COALESCE without inferring it from the column.
	var nameArg, layoutArg, defaultArg, sortArg any
	if name != nil {
		nameArg = strings.TrimSpace(*name)
	}
	if layout != nil {
		layoutArg = []byte(*layout)
	}
	if isDefault != nil {
		defaultArg = *isDefault
	}
	if sort != nil {
		sortArg = *sort
	}

	l, err := scanLayout(tx.QueryRow(ctx, fmt.Sprintf(
		`UPDATE %s SET
		   name       = COALESCE($3::text,    name),
		   layout     = COALESCE($4::jsonb,   layout),
		   is_default = COALESCE($5::boolean, is_default),
		   sort       = COALESCE($6::int,     sort),
		   updated_at = now()
		 WHERE id = $1 AND %s = $2
		 RETURNING `+layoutCols, s.table, s.ownerCol),
		id, ownerID, nameArg, layoutArg, defaultArg, sortArg))
	if err != nil {
		if isUniqueViolation(err) {
			return Layout{}, ErrDuplicateName
		}
		return Layout{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Layout{}, err
	}
	return l, nil
}

// Delete removes a layout and promotes the lowest-sort survivor if the deleted
// one was the default.
func (s *LayoutsService) Delete(ctx context.Context, ownerID, id string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var wasDefault bool
	err = tx.QueryRow(ctx, fmt.Sprintf(
		`DELETE FROM %s
		  WHERE id = $1 AND %s = $2
		  RETURNING is_default`, s.table, s.ownerCol), id, ownerID).Scan(&wasDefault)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	if wasDefault {
		if _, err := tx.Exec(ctx, fmt.Sprintf(
			`UPDATE %s SET is_default = true, updated_at = now()
			  WHERE id = (
			    SELECT id FROM %s
			     WHERE %s = $1 ORDER BY sort, created_at LIMIT 1
			  )`, s.table, s.table, s.ownerCol), ownerID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Reorder rewrites sort for the given ids in one statement, so dragging a
// layout in the switcher is a single write rather than N. The owner-column
// predicate means ids belonging to another owner are silently ignored.
func (s *LayoutsService) Reorder(ctx context.Context, ownerID string, ids []string) error {
	_, err := s.db.Exec(ctx, fmt.Sprintf(
		`UPDATE %s AS l
		    SET sort = v.ord, updated_at = now()
		   FROM (SELECT id, ord FROM unnest($2::uuid[]) WITH ORDINALITY AS t(id, ord)) AS v
		  WHERE l.id = v.id AND l.%s = $1`, s.table, s.ownerCol), ownerID, ids)
	return err
}

type LayoutsHandler struct {
	svc   *LayoutsService
	owner func(*http.Request) string
}

// NewLayoutsHandler binds a layouts table to the function that names its owner.
// The admin console reads GetAdminID; institution and teacher panels read
// GetUserID, because their accounts live in `users`.
func NewLayoutsHandler(
	db *pgxpool.Pool, table, ownerCol string, owner func(*http.Request) string,
) *LayoutsHandler {
	return &LayoutsHandler{svc: NewLayoutsService(db, table, ownerCol), owner: owner}
}

// requireOwner resolves the owner from the token. GetAdminID is empty when an
// admin authenticated through the users table has no admin_accounts row
// (internal/middleware/auth.go:128-137). The owner column is NOT NULL, so an
// empty owner must never reach a query — and matching owner = ” would serve
// one owner's layouts to another.
func (h *LayoutsHandler) requireOwner(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := h.owner(r)
	if id == "" {
		middleware.Forbidden(w)
		return "", false
	}
	return id, true
}

func (h *LayoutsHandler) List(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	list, err := h.svc.List(r.Context(), ownerID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, list)
}

// layoutBody is the create/update payload. There is deliberately no %s
// field — ownership comes from the token and a client-supplied one is ignored.
type layoutBody struct {
	Name      *string          `json:"name"`
	Layout    *json.RawMessage `json:"layout"`
	IsDefault *bool            `json:"is_default"`
	Sort      *int             `json:"sort"`
}

// validLayoutJSON rejects anything that is not a JSON object, so a stored
// layout the frontend cannot read never gets written.
func validLayoutJSON(raw json.RawMessage) bool {
	var obj map[string]any
	return json.Unmarshal(raw, &obj) == nil
}

func decodeLayoutBody(w http.ResponseWriter, r *http.Request) (layoutBody, bool) {
	var body layoutBody
	// Cap the read so an oversized payload is rejected before it is buffered.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLayoutBytes+1024)).Decode(&body); err != nil {
		middleware.BadRequest(w, "invalid JSON body")
		return body, false
	}
	if body.Layout != nil && !validLayoutJSON(*body.Layout) {
		middleware.BadRequest(w, "layout must be a JSON object")
		return body, false
	}
	return body, true
}

// writeLayoutErr maps service errors to responses. A layout owned by someone
// else is a 404, not a 403, so ids are not enumerable.
func writeLayoutErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		middleware.NotFound(w, "layout")
	case errors.Is(err, ErrDuplicateName):
		middleware.Error(w, http.StatusConflict, "DUPLICATE_NAME", err.Error())
	case errors.Is(err, ErrLayoutTooBig):
		middleware.Error(w, http.StatusRequestEntityTooLarge, "LAYOUT_TOO_LARGE", err.Error())
	default:
		middleware.InternalError(w)
	}
}

func (h *LayoutsHandler) Create(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	body, ok := decodeLayoutBody(w, r)
	if !ok {
		return
	}
	if body.Name == nil || strings.TrimSpace(*body.Name) == "" {
		middleware.BadRequest(w, "name is required")
		return
	}
	if body.Layout == nil {
		middleware.BadRequest(w, "layout is required and must be a JSON object")
		return
	}
	isDefault := body.IsDefault != nil && *body.IsDefault

	l, err := h.svc.Create(r.Context(), ownerID, *body.Name, *body.Layout, isDefault)
	if err != nil {
		writeLayoutErr(w, err)
		return
	}
	middleware.JSON(w, http.StatusCreated, l)
}

func (h *LayoutsHandler) Update(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	body, ok := decodeLayoutBody(w, r)
	if !ok {
		return
	}
	if body.Name != nil && strings.TrimSpace(*body.Name) == "" {
		middleware.BadRequest(w, "name cannot be blank")
		return
	}

	l, err := h.svc.Update(r.Context(), ownerID, chi.URLParam(r, "layoutId"),
		body.Name, body.Layout, body.IsDefault, body.Sort)
	if err != nil {
		writeLayoutErr(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, l)
}

func (h *LayoutsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), ownerID, chi.URLParam(r, "layoutId")); err != nil {
		writeLayoutErr(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}

func (h *LayoutsHandler) Reorder(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.IDs) == 0 {
		middleware.BadRequest(w, "ids must be a non-empty array of layout ids")
		return
	}
	if err := h.svc.Reorder(r.Context(), ownerID, body.IDs); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "ok"})
}
