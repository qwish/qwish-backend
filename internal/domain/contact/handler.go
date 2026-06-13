package contact

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/notification"
	"github.com/qwish/backend/internal/middleware"
)

// validTopics is the set of accepted contact form topics.
// Keep in sync with the CHECK constraint in 015_contact_topic_demo.sql and the
// brand-website contact form (qwish-brand-website ContactClient.tsx).
var validTopics = map[string]bool{
	"institution": true,
	"partnership": true,
	"recruiter":   true,
	"calibration": true,
	"press":       true,
	"support":     true,
	"demo":        true,
	"other":       true,
}

// Handler holds the database pool for all contact-form operations.
type Handler struct {
	db       *pgxpool.Pool
	notif    *notification.Service
	brandURL string // marketing site base; institution apply links point here
}

// NewHandler creates a new contact Handler. notif may be nil (emails are then
// skipped); brandURL is the marketing-site base used to build apply links.
func NewHandler(db *pgxpool.Pool, notif *notification.Service, brandURL string) *Handler {
	return &Handler{db: db, notif: notif, brandURL: strings.TrimRight(brandURL, "/")}
}

// ──────────────────────────────────────────────
// POST /api/v1/contact  (public, no auth required)
// ──────────────────────────────────────────────

// Submit stores a new contact form submission.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Topic    string          `json:"topic"`
		Name     string          `json:"name"`
		Email    string          `json:"email"`
		Phone    string          `json:"phone"`
		Message  string          `json:"message"`
		Metadata json.RawMessage `json:"metadata"` // optional topic-specific fields
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}

	// Validate required fields
	req.Topic = strings.TrimSpace(req.Topic)
	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Message = strings.TrimSpace(req.Message)

	if req.Topic == "" || req.Name == "" || req.Email == "" || req.Message == "" {
		middleware.BadRequest(w, "topic, name, email, and message are required")
		return
	}
	if !validTopics[req.Topic] {
		middleware.BadRequest(w, "topic must be one of: institution, partnership, recruiter, calibration, press, support, demo, other")
		return
	}

	// Normalise optional metadata
	var metadata interface{} = nil
	if len(req.Metadata) > 0 && string(req.Metadata) != "null" {
		metadata = req.Metadata
	}

	var id string
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO contact_submissions (topic, name, email, phone, message, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id`,
		req.Topic,
		req.Name,
		req.Email,
		nullStr(req.Phone),
		req.Message,
		metadata,
	).Scan(&id)
	if err != nil {
		middleware.InternalError(w)
		return
	}

	middleware.JSON(w, http.StatusCreated, map[string]string{
		"id":      id,
		"message": "Your message has been received. We'll get back to you at " + req.Email + ".",
	})
}

// ──────────────────────────────────────────────
// GET /api/v1/admin/contact-submissions
// Roles: super_admin, moderator, support_agent
// ──────────────────────────────────────────────

// List returns paginated contact submissions, filterable by topic and status.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	topic := q.Get("topic")
	status := q.Get("status")

	// Build dynamic WHERE clause
	args := []interface{}{}
	where := []string{}
	idx := 1

	if topic != "" {
		where = append(where, "topic = $"+itoa(idx))
		args = append(args, topic)
		idx++
	}
	if status != "" {
		where = append(where, "status = $"+itoa(idx))
		args = append(args, status)
		idx++
	}

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, topic, name, email, phone, message, metadata, status, resolved_at, created_at
		 FROM contact_submissions
		 `+whereSQL+`
		 ORDER BY created_at DESC
		 LIMIT 100`,
		args...,
	)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type row struct {
		ID         string          `json:"id"`
		Topic      string          `json:"topic"`
		Name       string          `json:"name"`
		Email      string          `json:"email"`
		Phone      *string         `json:"phone,omitempty"`
		Message    string          `json:"message"`
		Metadata   json.RawMessage `json:"metadata,omitempty"`
		Status     string          `json:"status"`
		ResolvedAt *time.Time      `json:"resolved_at,omitempty"`
		CreatedAt  time.Time       `json:"created_at"`
	}

	results := []row{}
	for rows.Next() {
		var s row
		if err := rows.Scan(
			&s.ID, &s.Topic, &s.Name, &s.Email, &s.Phone,
			&s.Message, &s.Metadata, &s.Status, &s.ResolvedAt, &s.CreatedAt,
		); err != nil {
			middleware.InternalError(w)
			return
		}
		results = append(results, s)
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"submissions": results,
		"count":       len(results),
	})
}

// ──────────────────────────────────────────────
// POST /api/v1/admin/contact-submissions/{id}/resolve
// Roles: super_admin, moderator, support_agent
// ──────────────────────────────────────────────

// Resolve updates the status of a contact submission.
func (h *Handler) Resolve(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req struct {
		Status string `json:"status"` // resolved | spam | in_progress
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}

	allowed := map[string]bool{"in_progress": true, "resolved": true, "spam": true}
	if !allowed[req.Status] {
		middleware.BadRequest(w, "status must be one of: in_progress, resolved, spam")
		return
	}

	tag, err := h.db.Exec(r.Context(),
		`UPDATE contact_submissions
		 SET status      = $1,
		     resolved_at = CASE WHEN $1 IN ('resolved','spam') THEN now() ELSE resolved_at END
		 WHERE id = $2`,
		req.Status, id,
	)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "submission")
		return
	}

	middleware.JSON(w, http.StatusOK, map[string]string{"status": req.Status})
}

// ──────────────────────────────────────────────
// POST /api/v1/admin/contact-submissions/{id}/invite
// Roles: super_admin, moderator, support_agent
//
// Emails the submitter a "bring Qwish to your institution" invite with a link to
// the public application form, pre-filled with their org/name/email. A "new"
// submission is bumped to "in_progress" since it's now been actioned.
// ──────────────────────────────────────────────

func (h *Handler) InviteToApply(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var name, email, status string
	var metadata json.RawMessage
	err := h.db.QueryRow(r.Context(),
		`SELECT name, email, status, metadata FROM contact_submissions WHERE id = $1`,
		id,
	).Scan(&name, &email, &status, &metadata)
	if err != nil {
		middleware.NotFound(w, "submission")
		return
	}

	applyLink := h.buildApplyLink(name, email, metadata)

	if h.notif != nil {
		if err := h.notif.SendInstitutionInvite(r.Context(), email, name, applyLink, "institution_invite:"+id); err != nil {
			middleware.Error(w, http.StatusBadGateway, "EMAIL_FAILED", "failed to send invite email")
			return
		}
	}

	// Move it out of the "new" bucket — it's been actioned.
	if status == "new" {
		h.db.Exec(r.Context(),
			`UPDATE contact_submissions SET status='in_progress' WHERE id=$1 AND status='new'`, id)
		status = "in_progress"
	}

	middleware.JSON(w, http.StatusOK, map[string]string{
		"message": "invite sent",
		"status":  status,
	})
}

// buildApplyLink constructs the institution application URL, pre-filling org
// (from submission metadata), name and email as query params when present.
func (h *Handler) buildApplyLink(name, email string, metadata json.RawMessage) string {
	q := url.Values{}
	if org := organisationFrom(metadata); org != "" {
		q.Set("org", org)
	}
	if name != "" {
		q.Set("name", name)
	}
	if email != "" {
		q.Set("email", email)
	}
	link := h.brandURL + "/institutions/apply"
	if enc := q.Encode(); enc != "" {
		link += "?" + enc
	}
	return link
}

// organisationFrom pulls the optional "organisation" field out of a submission's
// metadata blob. Returns "" when absent or unparseable.
func organisationFrom(metadata json.RawMessage) string {
	if len(metadata) == 0 {
		return ""
	}
	var m struct {
		Organisation string `json:"organisation"`
	}
	if err := json.Unmarshal(metadata, &m); err != nil {
		return ""
	}
	return strings.TrimSpace(m.Organisation)
}

// ──────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────

func nullStr(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
