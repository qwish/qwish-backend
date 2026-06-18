package auth

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// POST /api/v1/auth/send-otp
// Sends a 6-digit OTP to the given email. Always returns success to prevent enumeration.
func (h *Handler) SendOTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		middleware.BadRequest(w, "email is required")
		return
	}
	isNewUser := !h.svc.UserExistsByEmail(r.Context(), req.Email)
	h.svc.SupabaseSendOTP(r.Context(), req.Email)
	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"message":      "OTP sent",
		"is_new_user":  isNewUser,
	})
}

// POST /api/v1/auth/verify-otp
// Verifies the OTP and returns tokens. Does not create a profile.
// is_new_user=true means the client should redirect to the create-profile step.
func (h *Handler) VerifyOTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
		OTP   string `json:"otp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if req.Email == "" || req.OTP == "" {
		middleware.BadRequest(w, "email and otp are required")
		return
	}

	authResp, err := h.svc.SupabaseVerifyOTP(r.Context(), req.Email, req.OTP)
	if err != nil {
		middleware.Error(w, http.StatusUnauthorized, "INVALID_OTP", "invalid or expired OTP")
		return
	}

	uid := authResp.User.ID
	if uid == "" {
		middleware.InternalError(w)
		return
	}

	existingUser, err := h.svc.GetUserBySupabaseUID(r.Context(), uid)
	if err == nil {
		middleware.JSON(w, http.StatusOK, map[string]interface{}{
			"user": map[string]interface{}{
				"id":           existingUser.ID,
				"full_name":    existingUser.FullName,
				"display_name": existingUser.DisplayName,
				"email":        existingUser.Email,
				"role":         existingUser.Role,
			},
			"access_token":  authResp.AccessToken,
			"refresh_token": authResp.RefreshToken,
			"is_new_user":   false,
		})
		return
	}

	// Not in users — check admin_accounts (invited admins have no users row)
	if admin, aerr := h.svc.GetAdminForLogin(r.Context(), uid, req.Email); aerr == nil {
		switch admin.Status {
		case "active":
			// proceed
		case "pending", "invite_failed":
			// First successful OTP = invite accepted. Promote to active so the
			// admin isn't blocked before reaching the middleware that would.
			h.svc.ActivateAdmin(r.Context(), admin.ID)
		default: // suspended (deleted rows excluded by GetAdminForLogin)
			middleware.Error(w, http.StatusForbidden, "ACCOUNT_SUSPENDED", "account is suspended")
			return
		}
		middleware.JSON(w, http.StatusOK, map[string]interface{}{
			"user": map[string]interface{}{
				"id":           admin.ID,
				"full_name":    admin.Name,
				"display_name": admin.Name,
				"email":        admin.Email,
				"role":         admin.Role,
			},
			"access_token":  authResp.AccessToken,
			"refresh_token": authResp.RefreshToken,
			"is_new_user":   false,
		})
		return
	}

	// New user — return tokens so they can call create-profile next
	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"access_token":  authResp.AccessToken,
		"refresh_token": authResp.RefreshToken,
		"is_new_user":   true,
	})
}

// POST /api/v1/auth/create-profile
// Creates a profile for a newly verified user. Requires auth.
// Body: { full_name, referral_code? }
func (h *Handler) CreateProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FullName     string `json:"full_name"`
		ReferralCode string `json:"referral_code"`
		InviteToken  string `json:"invite_token"` // teacher email-invite token
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if req.FullName == "" {
		middleware.BadRequest(w, "full_name is required")
		return
	}

	uid := middleware.GetSupabaseUID(r)
	email := middleware.GetEmail(r)

	var instID *string
	role := "student"
	var acceptedInviteID string
	switch {
	case req.InviteToken != "":
		inv, err := h.svc.GetTeacherInviteByToken(r.Context(), req.InviteToken)
		if err != nil {
			middleware.NotFound(w, "invite")
			return
		}
		if inv.Status != "pending" {
			middleware.Error(w, http.StatusGone, "INVITE_"+strings.ToUpper(inv.Status),
				"this invite is "+inv.Status)
			return
		}
		// The invite is bound to the email it was sent to; the authenticated
		// Supabase session must match it.
		if !strings.EqualFold(inv.Email, email) {
			middleware.Error(w, http.StatusForbidden, "INVITE_EMAIL_MISMATCH",
				"this invite was issued for a different email address")
			return
		}
		instID = &inv.InstitutionID
		role = "teacher"
		acceptedInviteID = inv.ID
	case req.ReferralCode != "":
		id, assignedRole, err := h.svc.FindInstitutionByReferralCode(r.Context(), req.ReferralCode)
		if err != nil {
			middleware.BadRequest(w, "invalid or inactive referral code")
			return
		}
		instID = &id
		role = assignedRole
	}

	newUser, err := h.svc.CreateUser(r.Context(), uid, req.FullName, email, role, instID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			middleware.Error(w, http.StatusConflict, "USER_ALREADY_EXISTS", "profile already created")
			return
		}
		log.Printf("CreateProfile: CreateUser error: %v", err)
		middleware.InternalError(w)
		return
	}

	if acceptedInviteID != "" {
		if err := h.svc.MarkTeacherInviteAccepted(r.Context(), acceptedInviteID); err != nil {
			log.Printf("CreateProfile: failed to mark teacher invite %s accepted: %v", acceptedInviteID, err)
		}
	}

	var instData interface{}
	if instID != nil {
		instData = map[string]string{"id": *instID, "name": h.svc.GetInstitutionName(r.Context(), *instID)}
	}

	middleware.JSON(w, http.StatusCreated, map[string]interface{}{
		"user": map[string]interface{}{
			"id":           newUser.ID,
			"full_name":    newUser.FullName,
			"display_name": newUser.DisplayName,
			"email":        newUser.Email,
			"role":         newUser.Role,
			"institution":  instData,
		},
	})
}

// GET /api/v1/auth/teacher-invite?token=<token>
// Public. Lets the teacher-signup page validate an invite link and prefill the
// form. Returns the invite's email, optional name, institution and status.
func (h *Handler) GetTeacherInvite(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		middleware.BadRequest(w, "token is required")
		return
	}
	inv, err := h.svc.GetTeacherInviteByToken(r.Context(), token)
	if err != nil {
		middleware.NotFound(w, "invite")
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"email":            inv.Email,
		"name":             inv.Name,
		"institution_name": inv.InstitutionName,
		"status":           inv.Status,
		"expires_at":       inv.ExpiresAt,
	})
}

// POST /api/v1/auth/refresh
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
		middleware.BadRequest(w, "refresh_token is required")
		return
	}

	authResp, err := h.svc.SupabaseRefresh(r.Context(), req.RefreshToken)
	if err != nil {
		middleware.Error(w, http.StatusUnauthorized, "INVALID_TOKEN", "invalid or expired refresh token")
		return
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"access_token":  authResp.AccessToken,
		"refresh_token": authResp.RefreshToken,
	})
}

// POST /api/v1/auth/logout
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	token := ""
	if auth := r.Header.Get("Authorization"); len(auth) > 7 {
		token = auth[7:]
	}
	h.svc.SupabaseLogout(r.Context(), token)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

// PATCH /api/v1/auth/referral-code
func (h *Handler) UpdateReferralCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReferralCode string `json:"referral_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ReferralCode == "" {
		middleware.BadRequest(w, "referral_code is required")
		return
	}

	if err := h.svc.UpdateUserInstitution(r.Context(), middleware.GetUserID(r), req.ReferralCode); err != nil {
		middleware.BadRequest(w, "invalid or inactive referral code")
		return
	}

	middleware.JSON(w, http.StatusOK, map[string]string{"message": "institution updated"})
}
