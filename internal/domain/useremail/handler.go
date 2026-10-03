package useremail

import (
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidEmail):
		middleware.Error(w, 400, "EMAIL_INVALID", "Enter a valid, permanent email address.")
	case errors.Is(err, ErrIsLoginEmail):
		middleware.Error(w, 400, "EMAIL_IS_LOGIN", "That is already your sign-in email.")
	case errors.Is(err, ErrEmailTaken):
		middleware.Error(w, 409, "EMAIL_TAKEN", "That email is already verified on another account.")
	case errors.Is(err, ErrBadCode):
		middleware.Error(w, 400, "CODE_INVALID", "That code is wrong or has expired.")
	case errors.Is(err, ErrTooManyAttempts):
		middleware.Error(w, 429, "CODE_LOCKED", "Too many attempts. Request a new code.")
	case errors.Is(err, ErrNotFound):
		middleware.NotFound(w, "email")
	default:
		log.Printf("useremail: %v", err)
		middleware.InternalError(w)
	}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.List(r.Context(), middleware.GetUserID(r))
	if err != nil {
		fail(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}

func (h *Handler) Add(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		middleware.BadRequest(w, "email is required")
		return
	}
	e, err := h.svc.Add(r.Context(), middleware.GetUserID(r), req.Email)
	if err != nil {
		fail(w, err)
		return
	}
	middleware.JSON(w, http.StatusCreated, e)
}

func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		middleware.BadRequest(w, "code is required")
		return
	}
	e, err := h.svc.Verify(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "emailId"), req.Code)
	if err != nil {
		fail(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, e)
}

func (h *Handler) Resend(w http.ResponseWriter, r *http.Request) {
	e, err := h.svc.Resend(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "emailId"))
	if err != nil {
		fail(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, e)
}

func (h *Handler) Remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Remove(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "emailId")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
