package user

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// GET /api/v1/users/me
func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	profile, err := h.svc.GetProfile(r.Context(), userID)
	if err != nil {
		middleware.NotFound(w, "user")
		return
	}
	middleware.JSON(w, http.StatusOK, profile)
}

// PATCH /api/v1/users/me
func (h *Handler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DisplayName *string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	userID := middleware.GetUserID(r)
	if req.DisplayName != nil {
		if err := h.svc.UpdateDisplayName(r.Context(), userID, *req.DisplayName); err != nil {
			middleware.InternalError(w)
			return
		}
	}
	profile, _ := h.svc.GetProfile(r.Context(), userID)
	middleware.JSON(w, http.StatusOK, profile)
}

// GET /api/v1/users/me/stats
func (h *Handler) GetMyStats(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	stats, err := h.svc.GetStats(r.Context(), userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, stats)
}

// GET /api/v1/users/me/badges
func (h *Handler) GetMyBadges(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	badges, err := h.svc.GetBadges(r.Context(), userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, badges)
}

// GET /api/v1/users/me/attempts
func (h *Handler) GetMyAttempts(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	attempts, total, err := h.svc.GetAttempts(r.Context(), userID, page, limit)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSONWithMeta(w, http.StatusOK, attempts, &middleware.Meta{
		Page: page, Limit: limit, Total: total,
	})
}

// GET /api/v1/users/:userId/profile
func (h *Handler) GetPublicProfile(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "userId")
	profile, err := h.svc.GetPublicProfile(r.Context(), targetID)
	if err != nil {
		middleware.NotFound(w, "user")
		return
	}
	middleware.JSON(w, http.StatusOK, profile)
}

// DELETE /api/v1/users/me
func (h *Handler) DeleteMe(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if err := h.svc.SoftDelete(r.Context(), userID); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "account deleted"})
}
