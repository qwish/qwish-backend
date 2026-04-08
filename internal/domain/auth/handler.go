package auth

import (
	"encoding/json"
	"net/http"

	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// POST /api/v1/auth/signup
func (h *Handler) Signup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FullName     string `json:"full_name"`
		Email        string `json:"email"`
		Password     string `json:"password"`
		ReferralCode string `json:"referral_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if req.FullName == "" || req.Email == "" || req.Password == "" || req.ReferralCode == "" {
		middleware.BadRequest(w, "full_name, email, password, and referral_code are required")
		return
	}

	instID, role, err := h.svc.FindInstitutionByReferralCode(r.Context(), req.ReferralCode)
	if err != nil {
		middleware.BadRequest(w, "invalid or inactive referral code")
		return
	}

	authResp, err := h.svc.SupabaseSignUp(r.Context(), req.Email, req.Password)
	if err != nil {
		middleware.Error(w, http.StatusConflict, "EMAIL_IN_USE", "email already registered or "+err.Error())
		return
	}

	user, err := h.svc.CreateUser(r.Context(), authResp.User.ID, req.FullName, req.Email, role, instID)
	if err != nil {
		middleware.InternalError(w)
		return
	}

	instName := h.svc.GetInstitutionName(r.Context(), instID)

	middleware.JSON(w, http.StatusCreated, map[string]interface{}{
		"user": map[string]interface{}{
			"id":           user.ID,
			"full_name":    user.FullName,
			"display_name": user.DisplayName,
			"email":        user.Email,
			"role":         user.Role,
			"institution":  map[string]string{"id": instID, "name": instName},
		},
		"access_token":  authResp.AccessToken,
		"refresh_token": authResp.RefreshToken,
	})
}

// POST /api/v1/auth/login
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if req.Email == "" || req.Password == "" {
		middleware.BadRequest(w, "email and password are required")
		return
	}

	authResp, err := h.svc.SupabaseLogin(r.Context(), req.Email, req.Password)
	if err != nil {
		middleware.Error(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid email or password")
		return
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"access_token":  authResp.AccessToken,
		"refresh_token": authResp.RefreshToken,
	})
}

// POST /api/v1/auth/logout
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	token := ""
	if len(authHeader) > 7 {
		token = authHeader[7:]
	}
	h.svc.SupabaseLogout(r.Context(), token)
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "logged out"})
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

// POST /api/v1/auth/forgot-password
func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		middleware.BadRequest(w, "email is required")
		return
	}
	// Supabase sends OTP email automatically
	h.svc.SendPasswordResetOTP(r.Context(), req.Email)
	// Always return success to prevent email enumeration
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "if that email is registered, a reset link has been sent"})
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

	userID := middleware.GetUserID(r)
	if err := h.svc.UpdateUserInstitution(r.Context(), userID, req.ReferralCode); err != nil {
		middleware.BadRequest(w, "invalid or inactive referral code")
		return
	}

	middleware.JSON(w, http.StatusOK, map[string]string{"message": "institution updated"})
}
