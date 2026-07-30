package admin

import (
	"context"
	"encoding/json"
	"errors"
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

type LayoutsService struct{ db *pgxpool.Pool }

func NewLayoutsService(db *pgxpool.Pool) *LayoutsService { return &LayoutsService{db: db} }

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

func (s *LayoutsService) List(ctx context.Context, adminID string) ([]Layout, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+layoutCols+` FROM admin_dashboard_layouts
		  WHERE admin_id = $1 ORDER BY sort, created_at`, adminID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Layout{} // never nil — serialises as [] for a fresh admin
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
	ctx context.Context, adminID, name string, layout json.RawMessage, isDefault bool,
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
		if _, err := tx.Exec(ctx,
			`UPDATE admin_dashboard_layouts SET is_default = false, updated_at = now()
			  WHERE admin_id = $1 AND is_default`, adminID); err != nil {
			return Layout{}, err
		}
	}

	l, err := scanLayout(tx.QueryRow(ctx,
		`INSERT INTO admin_dashboard_layouts (admin_id, name, layout, is_default, sort)
		 VALUES ($1, $2, $3, $4,
		   COALESCE((SELECT MAX(sort) + 1 FROM admin_dashboard_layouts WHERE admin_id = $1), 0))
		 RETURNING `+layoutCols,
		adminID, strings.TrimSpace(name), []byte(layout), isDefault))
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
	ctx context.Context, adminID, id string,
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
	// carries admin_id, so another admin's row is never reachable.
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM admin_dashboard_layouts WHERE id = $1 AND admin_id = $2)`,
		id, adminID).Scan(&exists); err != nil {
		return Layout{}, err
	}
	if !exists {
		return Layout{}, ErrNotFound
	}

	if isDefault != nil && *isDefault {
		if _, err := tx.Exec(ctx,
			`UPDATE admin_dashboard_layouts SET is_default = false, updated_at = now()
			  WHERE admin_id = $1 AND is_default AND id <> $2`, adminID, id); err != nil {
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

	l, err := scanLayout(tx.QueryRow(ctx,
		`UPDATE admin_dashboard_layouts SET
		   name       = COALESCE($3::text,    name),
		   layout     = COALESCE($4::jsonb,   layout),
		   is_default = COALESCE($5::boolean, is_default),
		   sort       = COALESCE($6::int,     sort),
		   updated_at = now()
		 WHERE id = $1 AND admin_id = $2
		 RETURNING `+layoutCols,
		id, adminID, nameArg, layoutArg, defaultArg, sortArg))
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
func (s *LayoutsService) Delete(ctx context.Context, adminID, id string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var wasDefault bool
	err = tx.QueryRow(ctx,
		`DELETE FROM admin_dashboard_layouts
		  WHERE id = $1 AND admin_id = $2
		  RETURNING is_default`, id, adminID).Scan(&wasDefault)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	if wasDefault {
		if _, err := tx.Exec(ctx,
			`UPDATE admin_dashboard_layouts SET is_default = true, updated_at = now()
			  WHERE id = (
			    SELECT id FROM admin_dashboard_layouts
			     WHERE admin_id = $1 ORDER BY sort, created_at LIMIT 1
			  )`, adminID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Reorder rewrites sort for the given ids in one statement, so dragging a
// layout in the switcher is a single write rather than N. The admin_id
// predicate means ids belonging to another admin are silently ignored.
func (s *LayoutsService) Reorder(ctx context.Context, adminID string, ids []string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE admin_dashboard_layouts AS l
		    SET sort = v.ord, updated_at = now()
		   FROM (SELECT id, ord FROM unnest($2::uuid[]) WITH ORDINALITY AS t(id, ord)) AS v
		  WHERE l.id = v.id AND l.admin_id = $1`, adminID, ids)
	return err
}

type LayoutsHandler struct{ svc *LayoutsService }

func NewLayoutsHandler(db *pgxpool.Pool) *LayoutsHandler {
	return &LayoutsHandler{svc: NewLayoutsService(db)}
}

// requireAdmin resolves the owner from the token. GetAdminID is empty when an
// admin authenticated through the users table has no admin_accounts row
// (internal/middleware/auth.go:128-137). admin_id is NOT NULL, so an empty
// owner must never reach a query — and matching admin_id = ” would serve one
// admin's layouts to another.
func requireAdmin(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := middleware.GetAdminID(r)
	if id == "" {
		middleware.Forbidden(w)
		return "", false
	}
	return id, true
}

func (h *LayoutsHandler) List(w http.ResponseWriter, r *http.Request) {
	adminID, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	list, err := h.svc.List(r.Context(), adminID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, list)
}

// layoutBody is the create/update payload. There is deliberately no admin_id
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
	adminID, ok := requireAdmin(w, r)
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

	l, err := h.svc.Create(r.Context(), adminID, *body.Name, *body.Layout, isDefault)
	if err != nil {
		writeLayoutErr(w, err)
		return
	}
	middleware.JSON(w, http.StatusCreated, l)
}

func (h *LayoutsHandler) Update(w http.ResponseWriter, r *http.Request) {
	adminID, ok := requireAdmin(w, r)
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

	l, err := h.svc.Update(r.Context(), adminID, chi.URLParam(r, "layoutId"),
		body.Name, body.Layout, body.IsDefault, body.Sort)
	if err != nil {
		writeLayoutErr(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, l)
}

func (h *LayoutsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	adminID, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), adminID, chi.URLParam(r, "layoutId")); err != nil {
		writeLayoutErr(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "deleted"})
}

func (h *LayoutsHandler) Reorder(w http.ResponseWriter, r *http.Request) {
	adminID, ok := requireAdmin(w, r)
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
	if err := h.svc.Reorder(r.Context(), adminID, body.IDs); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "ok"})
}
