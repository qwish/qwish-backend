package enrollment

import (
	"log"
	"net/http"

	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

type StudentHandler struct{ svc *Service }

func NewStudentHandler(svc *Service) *StudentHandler { return &StudentHandler{svc: svc} }

// POST /api/v1/students/claim  {claim_code}
func (h *StudentHandler) Claim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClaimCode string `json:"claim_code"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || req.ClaimCode == "" {
		middleware.BadRequest(w, "claim_code is required")
		return
	}

	e, err := h.svc.Claim(r.Context(), middleware.GetUserID(r), req.ClaimCode)
	if err != nil {
		joinError(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, e)
}

// POST /api/v1/students/join-class  {invite_code}
func (h *StudentHandler) JoinClass(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InviteCode string `json:"invite_code"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || req.InviteCode == "" {
		middleware.BadRequest(w, "invite_code is required")
		return
	}

	e, err := h.svc.JoinByClassCode(r.Context(), middleware.GetUserID(r), req.InviteCode)
	if err != nil {
		joinError(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, e)
}

// GET /api/v1/users/me/enrollment
//
// Returns null for a student with no institution. numpie keys its shell off
// this: null hides institution navigation and shows the join prompt.
func (h *StudentHandler) Mine(w http.ResponseWriter, r *http.Request) {
	e, err := h.svc.ActiveByUser(r.Context(), middleware.GetUserID(r))
	if err != nil {
		log.Printf("Mine: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, e)
}
