package offline

import (
	"encoding/json"
	"net/http"

	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// GET /api/v1/offline/pack[?since=<version>]
// Returns the offline practice bundle. When `since` matches the current
// version the body carries an empty quiz list and changed=false so the client
// can keep its cache.
func (h *Handler) GetPack(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	since := r.URL.Query().Get("since")
	pack, changed, err := h.svc.BuildPack(r.Context(), instID, since)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, struct {
		*Pack
		Changed bool `json:"changed"`
	}{Pack: pack, Changed: changed})
}

// POST /api/v1/offline/sync — batch upload of offline practice sessions.
func (h *Handler) Sync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Results []SyncResult `json:"results"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if len(req.Results) == 0 {
		middleware.BadRequest(w, "results is required")
		return
	}
	if len(req.Results) > 200 {
		middleware.BadRequest(w, "too many results in one batch (max 200)")
		return
	}
	userID := middleware.GetUserID(r)
	stored, err := h.svc.Sync(r.Context(), userID, req.Results)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]int{
		"received": len(req.Results),
		"stored":   stored,
	})
}
