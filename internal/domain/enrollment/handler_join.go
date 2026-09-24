package enrollment

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/qwish/backend/internal/middleware"
)

type joinRequest struct {
	Code     string `json:"code"`
	Kind     string `json:"kind"`
	TargetID string `json:"target_id"`
}

func readJoinRequest(w http.ResponseWriter, r *http.Request) (joinRequest, bool) {
	var req joinRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&req); err != nil {
		middleware.BadRequest(w, "enter the code your institution shared")
		return req, false
	}
	req.Code = strings.TrimSpace(req.Code)
	if len(req.Code) == 0 || len(req.Code) > 128 {
		middleware.BadRequest(w, "enter a valid code")
		return req, false
	}
	return req, true
}

func joinError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrJoinCodeInvalid):
		middleware.Error(w, 400, "JOIN_CODE_INVALID", "We couldn't identify that code. Check it with your institution and try again.")
	case errors.Is(err, ErrClaimCodeUsed):
		middleware.Error(w, 409, "CLAIM_CODE_USED", "This enrollment is already connected. Sign in to the account you used, or ask your institution for help.")
	case errors.Is(err, ErrEnrollmentExists):
		middleware.Error(w, 409, "ENROLLMENT_EXISTS", "You already have an institution enrollment. Your current enrollment has not been changed. Ask your institution for help.")
	case errors.Is(err, ErrJoinSuspended):
		middleware.Error(w, 403, "JOIN_SUSPENDED", "Your enrollment is suspended. Contact your institution to restore access.")
	case errors.Is(err, ErrJoinChanged):
		middleware.Error(w, 409, "JOIN_CHANGED", "This invitation has changed. Enter your code again to review the current details.")
	case errors.Is(err, ErrJoinRole):
		middleware.Error(w, 403, "JOIN_ROLE", "Only student accounts can use this invitation.")
	default:
		log.Printf("student join: %v", err)
		middleware.InternalError(w)
	}
}

func (h *StudentHandler) PreviewJoin(w http.ResponseWriter, r *http.Request) {
	req, ok := readJoinRequest(w, r)
	if !ok {
		return
	}
	p, err := h.svc.PreviewJoin(r.Context(), middleware.GetUserID(r), req.Code)
	if err != nil {
		joinError(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, p)
}

func (h *StudentHandler) ConfirmJoin(w http.ResponseWriter, r *http.Request) {
	req, ok := readJoinRequest(w, r)
	if !ok {
		return
	}
	if req.TargetID == "" || (req.Kind != "claim" && req.Kind != "class" && req.Kind != "institution") {
		middleware.BadRequest(w, "review your invitation before joining")
		return
	}
	result, err := h.svc.ConfirmJoin(r.Context(), middleware.GetUserID(r), req.Code, req.Kind, req.TargetID)
	if err != nil {
		joinError(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, result)
}
