package institution

import (
	"fmt"
	"net/http"
	"net/mail"
	"strings"

	"github.com/qwish/backend/internal/domain/auth"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

// POST /api/v1/institution/teachers
// Adds a teacher directly: an active teacher account in this institution and
// an email saying they are invited. There is no password and no link to
// accept — the teacher signs in with email + one-time code, and that first
// sign-in attaches to this account (auth.GetUserForLogin matches the verified
// email). Assigning classes stays a separate step.
func (h *Handler) AddTeacher(w http.ResponseWriter, r *http.Request) {
	instID, actor := middleware.GetInstitutionID(r), middleware.GetUserID(r)
	var req struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Email = auth.NormalizeEmail(req.Email)
	if a, err := mail.ParseAddress(req.Email); req.Name == "" || len(req.Name) > 120 || err != nil || a.Address != req.Email {
		middleware.BadRequest(w, "name (at most 120 characters) and a valid email are required")
		return
	}
	if taken := auth.EmailIdentityIn(r.Context(), h.db, req.Email); taken != nil {
		middleware.Error(w, http.StatusConflict, "EMAIL_ALREADY_REGISTERED", taken.Human())
		return
	}
	var instName string
	if err := h.db.QueryRow(r.Context(), `SELECT name FROM institutions WHERE id=$1 AND status='verified'`, instID).Scan(&instName); err != nil {
		middleware.Error(w, http.StatusUnprocessableEntity, "NOT_VERIFIED", "the institution must be verified before adding teachers")
		return
	}
	// The Supabase identity does not exist yet; a placeholder UID holds the row
	// until the first OTP sign-in replaces it with the verified one.
	var userID string
	if err := h.db.QueryRow(r.Context(), `INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id, status)
		VALUES (gen_random_uuid(), $1, $1, $2, 'teacher', $3, 'active') RETURNING id`,
		req.Name, req.Email, instID).Scan(&userID); err != nil {
		middleware.Error(w, http.StatusConflict, "EMAIL_ALREADY_REGISTERED", "this email already belongs to a Qwish account")
		return
	}
	h.db.Exec(r.Context(), `INSERT INTO streaks (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, userID)
	// A pending invite to the same address is superseded by the account.
	h.db.Exec(r.Context(), `UPDATE teacher_invites SET status='revoked' WHERE institution_id=$1 AND lower(btrim(email))=$2 AND status='pending'`, instID, req.Email)
	logAuditInst(r.Context(), h.db, actor, instID, "add_teacher", "user", userID, req.Email)

	sent := false
	if h.notif != nil {
		if err := h.notif.SendAccountInvite(r.Context(), req.Email, req.Name, "a teacher", instName, h.teacherURL, "teacher_added:"+userID); err != nil {
			fmt.Printf("[institution] add-teacher invite email failed: %v\n", err)
		} else {
			sent = true
		}
	}
	middleware.JSON(w, http.StatusCreated, map[string]any{"id": userID, "email": req.Email, "invite_sent": sent})
}
