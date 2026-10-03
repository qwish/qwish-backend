package enrollment

import (
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

type DomainHandler struct{ svc *Service }

func NewDomainHandler(svc *Service) *DomainHandler { return &DomainHandler{svc: svc} }

func (h *DomainHandler) List(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.ListDomains(r.Context(), chi.URLParam(r, "institutionId"))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}

func (h *DomainHandler) Add(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain string `json:"domain"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "domain is required")
		return
	}
	d, err := h.svc.AddDomain(r.Context(), chi.URLParam(r, "institutionId"), req.Domain, middleware.GetAdminID(r))
	switch {
	case errors.Is(err, ErrDomainInvalid):
		middleware.Error(w, 400, "DOMAIN_INVALID", "Enter a domain like college.edu.")
	case errors.Is(err, ErrDomainFreeMail):
		middleware.Error(w, 400, "DOMAIN_FREE_MAIL", "Free and disposable mail domains cannot belong to an institute.")
	case errors.Is(err, ErrDomainTaken):
		middleware.Error(w, 409, "DOMAIN_TAKEN", "That domain is verified for another institute.")
	case err != nil:
		log.Printf("add domain: %v", err)
		middleware.InternalError(w)
	default:
		middleware.JSON(w, http.StatusCreated, d)
	}
}

func (h *DomainHandler) Remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.RemoveDomain(r.Context(), chi.URLParam(r, "institutionId"), chi.URLParam(r, "domain")); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "domain removed"})
}
