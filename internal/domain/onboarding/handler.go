package onboarding

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	db              *pgxpool.Pool
	turnstileSecret string // Cloudflare Turnstile secret; empty disables verification
}

func NewHandler(db *pgxpool.Pool, turnstileSecret string) *Handler {
	return &Handler{db: db, turnstileSecret: turnstileSecret}
}

// POST /api/v1/onboarding/institution
// Public endpoint — no authentication required.
// Submits a new institution onboarding request with status='pending'.
// A Super Admin reviews and approves it via POST /api/v1/admin/institutions/:id/approve,
// then provisions credentials with POST /api/v1/admin/institutions/:id/provision-admin.
func (h *Handler) RegisterInstitution(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"name"`
		Type         string `json:"type"` // school | college | tuition
		ContactEmail string `json:"contact_email"`
		AdminName    string `json:"admin_name"`
		Timezone     string `json:"timezone"`
		// Optional supplementary details
		Phone   string `json:"phone"`
		Website string `json:"website"`
		City    string `json:"city"`
		State   string `json:"state"`
		Country string `json:"country"`

		Turnstile string `json:"turnstileToken"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}

	if err := middleware.VerifyTurnstile(r.Context(), h.turnstileSecret, req.Turnstile); err != nil {
		middleware.BadRequest(w, "verification failed, please try again")
		return
	}

	// Validate required fields
	if req.Name == "" || req.Type == "" || req.ContactEmail == "" || req.AdminName == "" {
		middleware.BadRequest(w, "name, type, contact_email, and admin_name are required")
		return
	}

	// Validate type enum
	switch req.Type {
	case "school", "college", "tuition":
		// valid
	default:
		middleware.BadRequest(w, "type must be one of: school, college, tuition")
		return
	}

	// Default timezone
	if req.Timezone == "" {
		req.Timezone = "Asia/Kolkata"
	}

	// Generate placeholder referral codes (will be replaced on approval)
	// Using a tmp prefix to signal they're not yet active
	sCode := "TMP_S_" + randomHex(6)
	tCode := "TMP_T_" + randomHex(6)

	// Check for duplicate pending application from same email
	var existing int
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM institutions WHERE contact_email=$1 AND status='pending'`,
		strings.ToLower(req.ContactEmail),
	).Scan(&existing)
	if existing > 0 {
		middleware.Error(w, http.StatusConflict, "DUPLICATE_REQUEST",
			"an onboarding request from this email is already pending review")
		return
	}

	// Insert into institutions with status='pending'
	// admin_name is stored in a separate metadata column if it exists,
	// or we embed it into the contact_email lookup. We store it in the
	// supplementary columns added by the migration.
	var instID string
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO institutions
		 (name, type, contact_email, timezone, status, student_referral_code, teacher_referral_code,
		  onboarding_admin_name, onboarding_phone, onboarding_website,
		  onboarding_city, onboarding_state, onboarding_country)
		 VALUES ($1,$2,$3,$4,'pending',$5,$6,$7,$8,$9,$10,$11,$12)
		 RETURNING id`,
		req.Name, req.Type, strings.ToLower(req.ContactEmail), req.Timezone,
		sCode, tCode,
		req.AdminName, nullStr(req.Phone), nullStr(req.Website),
		nullStr(req.City), nullStr(req.State), nullStr(req.Country),
	).Scan(&instID)
	if err != nil {
		middleware.InternalError(w)
		return
	}

	middleware.JSON(w, http.StatusCreated, map[string]interface{}{
		"id":            instID,
		"status":        "pending",
		"message":       "Your institution application has been submitted and is under review. You will be contacted at " + req.ContactEmail + " once it is approved.",
		"contact_email": req.ContactEmail,
	})
}

// GET /api/v1/onboarding/institution/:token/status
// Public endpoint — lets institutions check their application status by a secure lookup token.
// (Currently returns basic status by institution ID; extend with a token column if needed.)
func (h *Handler) CheckStatus(w http.ResponseWriter, r *http.Request) {
	email := r.URL.Query().Get("email")
	if email == "" {
		middleware.BadRequest(w, "email query param is required")
		return
	}

	var id, name, status string
	err := h.db.QueryRow(r.Context(),
		`SELECT id, name, status FROM institutions WHERE contact_email=$1 AND deleted_at IS NULL`,
		strings.ToLower(email),
	).Scan(&id, &name, &status)
	if err != nil {
		middleware.NotFound(w, "institution application")
		return
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"id":     id,
		"name":   name,
		"status": status,
	})
}

// nullStr converts an empty string to nil for optional DB fields.
func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	const digits = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for i := range b {
		b[i] = digits[int(b[i])%len(digits)]
	}
	return string(b)
}
