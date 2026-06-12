package studygroup

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// ── Groups ────────────────────────────────────────────────────────────────────

// POST /api/v1/study-groups
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string  `json:"name"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		middleware.BadRequest(w, "name is required")
		return
	}
	g, err := h.svc.Create(r.Context(), middleware.GetUserID(r), req.Name, req.Description)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, g)
}

// GET /api/v1/study-groups
func (h *Handler) ListMine(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListMine(r.Context(), middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, list)
}

// GET /api/v1/study-groups/{groupId}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	g, err := h.svc.Get(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "groupId"))
	if errors.Is(err, ErrNotFound) {
		middleware.NotFound(w, "study group")
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, g)
}

// POST /api/v1/study-groups/join  {invite_code}
func (h *Handler) Join(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InviteCode string `json:"invite_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.InviteCode) == "" {
		middleware.BadRequest(w, "invite_code is required")
		return
	}
	g, err := h.svc.JoinByCode(r.Context(), middleware.GetUserID(r), strings.TrimSpace(req.InviteCode))
	if errors.Is(err, ErrNotFound) {
		middleware.NotFound(w, "study group")
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, g)
}

// POST /api/v1/study-groups/{groupId}/leave
func (h *Handler) Leave(w http.ResponseWriter, r *http.Request) {
	err := h.svc.Leave(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "groupId"))
	if errors.Is(err, ErrNotFound) {
		middleware.NotFound(w, "study group")
		return
	}
	if errors.Is(err, ErrForbidden) {
		middleware.Error(w, http.StatusForbidden, "OWNER_CANNOT_LEAVE", "the owner must archive the group instead of leaving")
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/v1/study-groups/{groupId}
func (h *Handler) Archive(w http.ResponseWriter, r *http.Request) {
	err := h.svc.Archive(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "groupId"))
	if errors.Is(err, ErrForbidden) {
		middleware.Forbidden(w)
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/study-groups/{groupId}/leaderboard
func (h *Handler) Leaderboard(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Leaderboard(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "groupId"))
	if errors.Is(err, ErrForbidden) {
		middleware.Forbidden(w)
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, list)
}

// ── Follows ─────────────────────────────────────────────────────────────────

// POST /api/v1/users/{userId}/follow
func (h *Handler) Follow(w http.ResponseWriter, r *http.Request) {
	err := h.svc.Follow(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "userId"))
	if errors.Is(err, ErrSelf) {
		middleware.BadRequest(w, "you cannot follow yourself")
		return
	}
	if errors.Is(err, ErrNotFound) {
		middleware.NotFound(w, "user")
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/v1/users/{userId}/follow
func (h *Handler) Unfollow(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Unfollow(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "userId")); err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/users/me/following
func (h *Handler) Following(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Following(r.Context(), middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, list)
}

// GET /api/v1/users/me/followers
func (h *Handler) Followers(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Followers(r.Context(), middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, list)
}
