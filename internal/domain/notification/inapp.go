package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/db"
	"github.com/qwish/backend/internal/jobs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/middleware"
)

// ── In-app notification types ────────────────────────────────────────────────

type Notification struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Icon      *string    `json:"icon,omitempty"`
	Color     *string    `json:"color,omitempty"`
	Reference *string    `json:"reference,omitempty"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// ── Service: emit + query ────────────────────────────────────────────────────

// Push delivery is a durable job, written atomically with the notification.
type pusherAdapter func(context.Context, string, string, string, map[string]string) error

func (s *Service) SetPusher(fn func(context.Context, string, string, string, map[string]string) error) {
	s.push = fn
}

type pushJob struct {
	UserID string            `json:"user_id"`
	Title  string            `json:"title"`
	Body   string            `json:"body"`
	Data   map[string]string `json:"data"`
}

func (s *Service) Emit(ctx context.Context, userID, kind, title, body string, opts ...EmitOpt) {
	if s == nil || s.db == nil || userID == "" {
		return
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		log.Printf("notification begin: %v", err)
		return
	}
	defer tx.Rollback(context.Background())
	if err = s.EmitTx(ctx, tx, userID, kind, title, body, opts...); err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		log.Printf("notification emit: %v", err)
	}
}

// EmitTx returns errors so authoritative callers can roll back the whole event.
func (s *Service) EmitTx(ctx context.Context, tx pgx.Tx, userID, kind, title, body string, opts ...EmitOpt) error {
	o := emitOpts{}
	for _, fn := range opts {
		fn(&o)
	}
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO user_notifications(user_id,kind,title,body,icon,color,reference)
 VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),NULLIF($7,'')) ON CONFLICT DO NOTHING RETURNING id`, userID, kind, title, body, o.icon, o.color, o.reference).Scan(&id)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if s.push == nil {
		return nil
	}
	data := map[string]string{"kind": kind, "notification_id": id}
	if o.reference != "" {
		data["reference"] = o.reference
	}
	if kind == "assignment" {
		data["deep_link"] = "qwish://assignments"
	}
	if activity, _, ok := strings.Cut(strings.TrimPrefix(o.reference, "activity:"), ":"); kind == "activity" && ok {
		data["deep_link"] = "qwish://activities/" + activity
	}
	return jobs.Enqueue(ctx, tx, "push", id, userID, pushJob{userID, title, body, data})
}

func (s *Service) RegisterJobs(q *jobs.Queue) {
	q.Register("push", func(ctx context.Context, j jobs.Job) (any, error) {
		var p pushJob
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return nil, err
		}
		if s.push == nil {
			return nil, fmt.Errorf("push is not configured")
		}
		return nil, s.push(ctx, p.UserID, p.Title, p.Body, p.Data)
	})
	q.Register("email", func(ctx context.Context, j jobs.Job) (any, error) {
		var p queuedEmail
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return nil, err
		}
		if strings.HasPrefix(p.Reference, "announcement:") {
			var active bool
			err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM announcements WHERE id::text=$1 AND status='sent')`, strings.TrimPrefix(p.Reference, "announcement:")).Scan(&active)
			if err != nil || !active {
				return nil, err
			}
		}
		return nil, s.deliverEmail(ctx, p.To, p.Subject, p.HTML, j.ID, p.Reference)
	})
}

// StartBroadcast relays committed notifications to SSE clients on every replica.
func (s *Service) StartBroadcast(ctx context.Context, pool *pgxpool.Pool) {
	db.Listen(ctx, pool, "qwish_notifications", nil, func(ctx context.Context, id string) {
		var userID string
		var n Notification
		err := pool.QueryRow(ctx, `SELECT user_id,id,kind,title,body,icon,color,reference,read_at,created_at FROM user_notifications WHERE id=$1`, id).Scan(&userID, &n.ID, &n.Kind, &n.Title, &n.Body, &n.Icon, &n.Color, &n.Reference, &n.ReadAt, &n.CreatedAt)
		if err == nil {
			s.Publish(userID, n)
		} else {
			log.Printf("notification broadcast: %v", err)
		}
	})
}

type emitOpts struct {
	icon, color, reference string
}

type EmitOpt func(*emitOpts)

func WithIcon(v string) EmitOpt      { return func(o *emitOpts) { o.icon = v } }
func WithColor(v string) EmitOpt     { return func(o *emitOpts) { o.color = v } }
func WithReference(v string) EmitOpt { return func(o *emitOpts) { o.reference = v } }

func (s *Service) List(ctx context.Context, userID string, page, limit int) ([]Notification, int, int, error) {
	list, total, unread, _, err := s.ListCursor(ctx, userID, page, limit, "", false)
	return list, total, unread, err
}
func (s *Service) ListCursor(ctx context.Context, userID string, page, limit int, cursor string, cursorMode bool) ([]Notification, int, int, string, error) {
	offset := (page - 1) * limit
	// Both counts scan the same rows, so one aggregate with a FILTER replaces
	// two round trips and two index scans. This endpoint is polled on every app
	// open, which makes it one of the highest-frequency reads in the API.
	var total, unread int
	if err := s.db.QueryRow(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE read_at IS NULL)
		 FROM user_notifications WHERE user_id=$1`, userID).Scan(&total, &unread); err != nil {
		return nil, 0, 0, "", err
	}

	predicate := ""
	args := []any{userID, limit + 1, offset}
	if cursorMode {
		args[2] = 0
	}
	if cursor != "" {
		c, err := middleware.DecodeCursor(cursor)
		if err != nil {
			return nil, 0, 0, "", err
		}
		predicate = " AND (created_at,id)<($4,$5::uuid)"
		args = append(args, c.Time, c.ID)
	}
	rows, err := s.db.Query(ctx, `SELECT id,kind,title,body,icon,color,reference,read_at,created_at FROM user_notifications WHERE user_id=$1`+predicate+` ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, args...)

	if err != nil {
		return nil, 0, 0, "", err
	}
	defer rows.Close()

	list := []Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &n.Icon, &n.Color, &n.Reference, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, 0, 0, "", err
		}
		list = append(list, n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, "", err
	}
	next := ""
	if len(list) > limit {
		list = list[:limit]
		if cursorMode {
			last := list[len(list)-1]
			next = middleware.EncodeCursor(last.CreatedAt, last.ID)
		}
	}
	return list, total, unread, next, nil
}

func (s *Service) MarkRead(ctx context.Context, userID, notifID string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE user_notifications SET read_at = now() WHERE id=$1 AND user_id=$2 AND read_at IS NULL`,
		notifID, userID)
	return err
}

func (s *Service) MarkAllRead(ctx context.Context, userID string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE user_notifications SET read_at = now() WHERE user_id=$1 AND read_at IS NULL`, userID)
	return err
}

func (s *Service) UnreadCount(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_notifications WHERE user_id=$1 AND read_at IS NULL`, userID,
	).Scan(&n)
	return n, err
}

// ── Handler ──────────────────────────────────────────────────────────────────

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// GET /api/v1/users/me/notifications
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor != "" {
		if _, err := middleware.DecodeCursor(cursor); err != nil {
			middleware.BadRequest(w, err.Error())
			return
		}
	}
	list, total, unread, next, err := h.svc.ListCursor(r.Context(), userID, page, limit, cursor, r.URL.Query().Get("pagination") == "cursor" || cursor != "")
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSONWithMeta(w, http.StatusOK, map[string]interface{}{
		"items":  list,
		"unread": unread,
	}, &middleware.Meta{Page: page, Limit: limit, Total: total, Cursor: next})
}

// GET /api/v1/users/me/notifications/unread-count
func (h *Handler) UnreadCount(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	n, err := h.svc.UnreadCount(r.Context(), userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]int{"unread": n})
}

// PATCH /api/v1/users/me/notifications/{id}/read
func (h *Handler) MarkRead(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	id := chi.URLParam(r, "id")
	if err := h.svc.MarkRead(r.Context(), userID, id); err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PATCH /api/v1/users/me/notifications/read-all
func (h *Handler) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if err := h.svc.MarkAllRead(r.Context(), userID); err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/users/me/notifications/stream
func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// The server's 30s WriteTimeout would otherwise cut every stream just after
	// its first ping, and each reconnect re-runs the auth query. The ticker
	// below and client disconnects (r.Context) bound the stream instead.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	userID := middleware.GetUserID(r)
	ch := h.svc.Subscribe(userID)
	defer h.svc.Unsubscribe(userID, ch)

	fmt.Fprintf(w, "event: connected\ndata: {}\n\n")
	flusher.Flush()
	if after := r.Header.Get("Last-Event-ID"); after != "" {
		rows, err := h.svc.db.Query(r.Context(), `SELECT n.id,n.kind,n.title,n.body,n.icon,n.color,n.reference,n.read_at,n.created_at
   FROM user_notifications n WHERE n.user_id=$1 AND (n.created_at,n.id)>(SELECT created_at,id FROM user_notifications WHERE id::text=$2 AND user_id=$1)
   ORDER BY n.created_at,n.id LIMIT 101`, userID, after)
		if err != nil {
			return
		}
		count := 0
		for rows.Next() {
			var n Notification
			if err = rows.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &n.Icon, &n.Color, &n.Reference, &n.ReadAt, &n.CreatedAt); err != nil {
				rows.Close()
				return
			}
			count++
			if count > 100 {
				break
			}
			raw, _ := json.Marshal(n)
			if _, err = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", n.ID, raw); err != nil {
				rows.Close()
				return
			}
		}
		rows.Close()
		fmt.Fprint(w, "event: resync\ndata: {}\n\n")
		flusher.Flush()
	}
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case n := <-ch:
			data, err := json.Marshal(n)
			if err == nil {
				if _, err := fmt.Fprintf(w, "id: %s\ndata: %s\n\n", n.ID, string(data)); err != nil {
					return
				}
				flusher.Flush()
			}
		case <-ticker.C:
			if _, err := fmt.Fprintf(w, ": ping\nevent: resync\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
