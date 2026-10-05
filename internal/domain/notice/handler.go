package notice

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func actor(r *http.Request) Actor {
	return Actor{UserID: middleware.GetUserID(r), InstitutionID: middleware.GetInstitutionID(r),
		Admin: middleware.GetRole(r) == "institution_admin"}
}

func (h *Handler) Audiences(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Audiences(r.Context(), actor(r))
	if err != nil {
		log.Printf("notice audiences: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}

func (h *Handler) Send(w http.ResponseWriter, r *http.Request) {
	var d Draft
	if err := jsonx.NewDecoder(r.Body).Decode(&d); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	n, err := h.svc.Send(r.Context(), actor(r), d)
	switch {
	case errors.Is(err, ErrInvalid):
		middleware.BadRequest(w, err.Error())
	case errors.Is(err, ErrForbidden):
		middleware.Error(w, http.StatusForbidden, "NOTICE_AUDIENCE", "You can't send notices to one or more of the chosen classes or departments.")
	case err != nil:
		log.Printf("notice send: %v", err)
		middleware.InternalError(w)
	default:
		middleware.JSON(w, http.StatusCreated, n)
	}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	out, err := h.svc.List(r.Context(), actor(r), limit, (page-1)*limit)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}

// GET /users/me/notices?page=&limit= — notices delivered to the caller.
func (h *Handler) Mine(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 50 {
		limit = 20
	}
	list, total, err := h.svc.Received(r.Context(), middleware.GetUserID(r), limit, (page-1)*limit)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSONWithMeta(w, http.StatusOK, list, &middleware.Meta{Page: page, Limit: limit, Total: total})
}
