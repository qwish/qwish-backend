package enrollment

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

type joinRequest struct {
	Code     string `json:"code"`
	Kind     string `json:"kind"`
	TargetID string `json:"target_id"`
}

func readJoinRequest(w http.ResponseWriter, r *http.Request) (joinRequest, bool) {
	var req joinRequest
	if err := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&req); err != nil {
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
		middleware.Error(w, 400, "JOIN_CODE_INVALID", "We couldn't find a class with that code. Check it with your teacher.")
	case errors.Is(err, ErrJoinClosed):
		middleware.Error(w, 403, "JOIN_CLOSED", "This class is invite-only. Ask your teacher for an invite, or add your school or college email in Profile → Emails.")
	case errors.Is(err, ErrInstituteCap):
		middleware.Error(w, 409, "INSTITUTE_LIMIT", "You already belong to 2 institutes. Leave one in Profile before joining another.")
	case errors.Is(err, ErrJoinSuspended):
		middleware.Error(w, 403, "JOIN_SUSPENDED", "Your enrollment at this institute is suspended. Contact the institute.")
	case errors.Is(err, ErrJoinChanged):
		middleware.Error(w, 409, "JOIN_CHANGED", "This class has changed. Enter the code again.")
	case errors.Is(err, ErrJoinRole):
		middleware.Error(w, 403, "JOIN_ROLE", "Only student accounts can join classes.")
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

// ConfirmJoin ignores any client-sent kind: every code is a class code now.
func (h *StudentHandler) ConfirmJoin(w http.ResponseWriter, r *http.Request) {
	req, ok := readJoinRequest(w, r)
	if !ok {
		return
	}
	if req.TargetID == "" {
		middleware.BadRequest(w, "review your invitation before joining")
		return
	}
	result, err := h.svc.ConfirmJoin(r.Context(), middleware.GetUserID(r), req.Code, req.TargetID)
	if err != nil {
		joinError(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, result)
}
