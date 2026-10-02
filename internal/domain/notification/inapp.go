package notification

import (
	"context"
	"encoding/json"
	"fmt"
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

// pusherAdapter is the callback shape used by SetPusher: deliver one push to
// the given user. Decoupled from the push package via this function type so
// the notification package has no upstream import on push.
type pusherAdapter func(ctx context.Context, userID, title, body string, data map[string]string)

// SetPusher registers a delivery callback for outbound mobile push. Pass a
// closure that adapts your concrete pusher (e.g. push.Service.SendToUser).
func (s *Service) SetPusher(fn func(ctx context.Context, userID, title, body string, data map[string]string)) {
	s.push = pusherAdapter(fn)
	// A fixed worker pool, not a goroutine per push: an announcement to every
	// student used to start one goroutine per recipient, each taking pool
	// connections for its preference/token reads and starving API requests.
	// ponytail: in-process queue, lost on restart; push is best-effort anyway.
	s.pushQ = make(chan pushJob, 1024)
	for range 4 {
		go func() {
			for j := range s.pushQ {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				s.push(ctx, j.userID, j.title, j.body, j.data)
				cancel()
			}
		}()
	}
}

type pushJob struct {
	userID, title, body string
	data                map[string]string
}

// Emit writes a single in-app notification row. Best-effort — errors are swallowed
// so callers in the hot path (attempt complete, streak update) do not break.
// When a pusher is registered, Emit also fans the notification out as a mobile
// push so users see it on the lock screen even when the app is closed.
func (s *Service) Emit(ctx context.Context, userID, kind, title, body string, opts ...EmitOpt) {
	if s.db == nil || userID == "" {
		return
	}
	o := emitOpts{}
	for _, fn := range opts {
		fn(&o)
	}

	var notifID string
	var createdAt time.Time
	err := s.db.QueryRow(ctx,
		`INSERT INTO user_notifications (user_id, kind, title, body, icon, color, reference)
		 VALUES ($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),NULLIF($7,''))
		 ON CONFLICT DO NOTHING
		 RETURNING id, created_at`,
		userID, kind, title, body, o.icon, o.color, o.reference).Scan(&notifID, &createdAt)

	if err == nil {
		var iconPtr *string
		if o.icon != "" {
			iconPtr = &o.icon
		}
		var colorPtr *string
		if o.color != "" {
			colorPtr = &o.color
		}
		var refPtr *string
		if o.reference != "" {
			refPtr = &o.reference
		}
		s.Publish(userID, Notification{
			ID:        notifID,
			Kind:      kind,
			Title:     title,
			Body:      body,
			Icon:      iconPtr,
			Color:     colorPtr,
			Reference: refPtr,
			CreatedAt: createdAt,
		})
	}

	// Push only when this call inserted the durable notification. In particular,
	// an idempotent assignment reminder must not produce duplicate lock-screen
	// pushes after ON CONFLICT DO NOTHING.
	if err == nil && s.push != nil {
		data := map[string]string{"kind": kind}
		if o.reference != "" {
			data["reference"] = o.reference
		}
		if kind == "assignment" {
			data["deep_link"] = "qwish://assignments"
		}
		if id, _, ok := strings.Cut(strings.TrimPrefix(o.reference, "activity:"), ":"); kind == "activity" && ok {
			data["deep_link"] = "qwish://activities/" + id
		}
		// Queued so push latency never blocks the request that triggered Emit;
		// a full queue backpressures bulk senders like the announcement cron.
		s.pushQ <- pushJob{userID, title, body, data}
	}
}

type emitOpts struct {
	icon, color, reference string
}

type EmitOpt func(*emitOpts)

func WithIcon(v string) EmitOpt      { return func(o *emitOpts) { o.icon = v } }
func WithColor(v string) EmitOpt     { return func(o *emitOpts) { o.color = v } }
func WithReference(v string) EmitOpt { return func(o *emitOpts) { o.reference = v } }

func (s *Service) List(ctx context.Context, userID string, page, limit int) ([]Notification, int, int, error) {
	offset := (page - 1) * limit
	// Both counts scan the same rows, so one aggregate with a FILTER replaces
	// two round trips and two index scans. This endpoint is polled on every app
	// open, which makes it one of the highest-frequency reads in the API.
	var total, unread int
	s.db.QueryRow(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE read_at IS NULL)
		 FROM user_notifications WHERE user_id=$1`, userID).Scan(&total, &unread)

	rows, err := s.db.Query(ctx,
		`SELECT id, kind, title, body, icon, color, reference, read_at, created_at
		 FROM user_notifications
		 WHERE user_id=$1
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	list := []Notification{}
	for rows.Next() {
		var n Notification
		rows.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &n.Icon, &n.Color, &n.Reference, &n.ReadAt, &n.CreatedAt)
		list = append(list, n)
	}
	return list, total, unread, nil
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
	list, total, unread, err := h.svc.List(r.Context(), userID, page, limit)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSONWithMeta(w, http.StatusOK, map[string]interface{}{
		"items":  list,
		"unread": unread,
	}, &middleware.Meta{Page: page, Limit: limit, Total: total})
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

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case n := <-ch:
			data, err := json.Marshal(n)
			if err == nil {
				fmt.Fprintf(w, "data: %s\n\n", string(data))
				flusher.Flush()
			}
		case <-ticker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
