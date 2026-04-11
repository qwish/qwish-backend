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
	viewerID := middleware.GetUserID(r)
	if viewerID != targetID {
		h.svc.RecordProfileView(r.Context(), viewerID, targetID)
	}
	middleware.JSON(w, http.StatusOK, profile)
}

// GET /api/v1/users/me/profile-views
func (h *Handler) GetMyProfileViews(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	stats, err := h.svc.GetProfileViews(r.Context(), userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, stats)
}

// GET /api/v1/users/me/rank
func (h *Handler) GetMyRank(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	instID := middleware.GetInstitutionID(r)
	rank, err := h.svc.GetRank(r.Context(), userID, instID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, rank)
}

// GET /api/v1/users/me/milestones
func (h *Handler) GetMyMilestones(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	milestones, err := h.svc.GetMilestones(r.Context(), userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, milestones)
}

// GET /api/v1/users/me/education
func (h *Handler) GetMyEducation(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	list, err := h.svc.GetEducation(r.Context(), userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, list)
}

// POST /api/v1/users/me/education
func (h *Handler) AddMyEducation(w http.ResponseWriter, r *http.Request) {
	var req Education
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if req.InstitutionName == "" {
		middleware.BadRequest(w, "institution_name is required")
		return
	}
	userID := middleware.GetUserID(r)
	out, err := h.svc.AddEducation(r.Context(), userID, req)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, out)
}

// DELETE /api/v1/users/me/education/:id
func (h *Handler) DeleteMyEducation(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	edID := chi.URLParam(r, "id")
	if err := h.svc.DeleteEducation(r.Context(), userID, edID); err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/users/me/skills
func (h *Handler) GetMySkills(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	skills, err := h.svc.GetSkills(r.Context(), userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, skills)
}

// POST /api/v1/users/me/skills
func (h *Handler) AddMySkill(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Skill string `json:"skill"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Skill == "" {
		middleware.BadRequest(w, "skill is required")
		return
	}
	userID := middleware.GetUserID(r)
	if err := h.svc.AddSkill(r.Context(), userID, req.Skill); err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/v1/users/me/skills/:skill
func (h *Handler) DeleteMySkill(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	skill := chi.URLParam(r, "skill")
	if err := h.svc.DeleteSkill(r.Context(), userID, skill); err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PATCH /api/v1/users/me/domain
func (h *Handler) UpdateMyDomain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	userID := middleware.GetUserID(r)
	if err := h.svc.UpdateDomain(r.Context(), userID, req.Domain); err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
