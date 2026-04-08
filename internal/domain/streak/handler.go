package streak

import (
	"net/http"

	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// GET /api/v1/users/me/streak
func (h *Handler) GetStreak(w http.ResponseWriter, r *http.Request) {
	info, err := h.svc.GetInfo(r.Context(), middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, info)
}
