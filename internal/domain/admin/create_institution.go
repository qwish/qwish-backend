package admin

import (
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qwish/backend/internal/domain/auth"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

// validEmail is a single bare address, as the enrollment and user-email
// services check it.
func validEmail(addr string) bool {
	a, err := mail.ParseAddress(addr)
	return err == nil && a.Address == addr
}

// POST /api/v1/admin/institutions
// Creates a verified institution and its admin account in one step, then
// emails the admin that they are invited. There is no password: the admin
// signs in with email + one-time code, and that first sign-in attaches to this
// account (auth.GetUserForLogin matches the verified email).
func (h *Handler) CreateInstitution(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"name"`
		Type         string `json:"type"`
		ContactEmail string `json:"contact_email"`
		Timezone     string `json:"timezone"`
		AdminName    string `json:"admin_name"`
		AdminEmail   string `json:"admin_email"` // defaults to contact_email
		Phone        string `json:"phone"`
		Website      string `json:"website"`
		City         string `json:"city"`
		State        string `json:"state"`
		Country      string `json:"country"`
	}
	if err := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.ContactEmail = auth.NormalizeEmail(req.ContactEmail)
	req.AdminEmail = auth.NormalizeEmail(req.AdminEmail)
	if req.AdminEmail == "" {
		req.AdminEmail = req.ContactEmail
	}
	req.AdminName = strings.TrimSpace(req.AdminName)
	if req.AdminName == "" {
		req.AdminName = req.Name + " Admin"
	}
	if req.Timezone == "" {
		req.Timezone = "Asia/Kolkata"
	}
	switch {
	case req.Name == "" || len(req.Name) > 200:
		middleware.BadRequest(w, "name is required (at most 200 characters)")
		return
	case req.Type != "school" && req.Type != "college" && req.Type != "tuition":
		middleware.BadRequest(w, "type must be school, college or tuition")
		return
	case !validEmail(req.ContactEmail) || !validEmail(req.AdminEmail):
		middleware.BadRequest(w, "contact_email and admin_email must be valid email addresses")
		return
	}
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		middleware.BadRequest(w, "timezone must be an IANA zone such as Asia/Kolkata")
		return
	}
	// One address is one Qwish account.
	if taken := auth.EmailIdentityIn(r.Context(), h.db, req.AdminEmail); taken != nil {
		middleware.Error(w, http.StatusConflict, "EMAIL_ALREADY_REGISTERED", taken.Human())
		return
	}

	adminID := middleware.GetAdminID(r)
	var verifiedBy *string
	if adminID != "" {
		verifiedBy = &adminID
	}
	nullable := func(s string) *string {
		if s = strings.TrimSpace(s); s == "" {
			return nil
		}
		return &s
	}
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(r.Context())
	var instID string
	if err := tx.QueryRow(r.Context(), `INSERT INTO institutions
		(name, type, contact_email, timezone, status, verified_at, verified_by, student_referral_code, teacher_referral_code,
		 onboarding_admin_name, onboarding_phone, onboarding_website, onboarding_city, onboarding_state, onboarding_country)
		VALUES ($1,$2,$3,$4,'verified',now(),$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`,
		req.Name, req.Type, req.ContactEmail, req.Timezone, verifiedBy,
		"S"+uuid.New().String()[:7], "T"+uuid.New().String()[:7],
		req.AdminName, nullable(req.Phone), nullable(req.Website), nullable(req.City), nullable(req.State), nullable(req.Country),
	).Scan(&instID); err != nil {
		middleware.InternalError(w)
		return
	}
	// The Supabase identity does not exist yet; a placeholder UID holds the row
	// until the first OTP sign-in replaces it with the verified one.
	var userID string
	if err := tx.QueryRow(r.Context(), `INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id, status)
		VALUES (gen_random_uuid(), $1, $1, $2, 'institution_admin', $3, 'active') RETURNING id`,
		req.AdminName, req.AdminEmail, instID).Scan(&userID); err != nil {
		middleware.Error(w, http.StatusConflict, "EMAIL_ALREADY_REGISTERED", "this email already belongs to a Qwish account")
		return
	}
	if _, err := tx.Exec(r.Context(), `INSERT INTO streaks (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, userID); err != nil {
		middleware.InternalError(w)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		middleware.InternalError(w)
		return
	}
	logAudit(r.Context(), h.db, adminID, "create_institution", "institution", instID, "admin_email="+req.AdminEmail)

	sent := false
	if h.notif != nil {
		loginURL := ""
		if h.cfg != nil {
			loginURL = h.cfg.InstituteURL
		}
		if err := h.notif.SendAccountInvite(r.Context(), req.AdminEmail, req.AdminName, "the institute admin", req.Name, loginURL, "institution_admin_invite:"+userID); err != nil {
			fmt.Printf("[admin] create-institution invite email failed: %v\n", err)
		} else {
			sent = true
		}
	}
	middleware.JSON(w, http.StatusCreated, map[string]any{
		"id": instID, "admin_user_id": userID, "admin_email": req.AdminEmail, "invite_sent": sent,
	})
}
