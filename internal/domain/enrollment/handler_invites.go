package enrollment

import (
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

type inviteRequest struct {
	Emails []string `json:"emails"`
}

func decodeInvites(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	var req inviteRequest
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Emails) == 0 || len(req.Emails) > 500 {
		middleware.BadRequest(w, "emails must list 1 to 500 addresses")
		return nil, false
	}
	return req.Emails, true
}

func inviteFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInviteNotFound):
		middleware.NotFound(w, "invite")
	case errors.Is(err, ErrNotYourClass):
		middleware.Error(w, 403, "NOT_YOUR_CLASS", "You can only manage invites for classes you teach.")
	default:
		joinError(w, err)
	}
}

// Teacher side, bounded to classes the teacher is assigned to.
func (h *TeacherHandler) CreateInvites(w http.ResponseWriter, r *http.Request) {
	classID := chi.URLParam(r, "classId")
	if ok, err := h.svc.TeacherOwnsClass(r.Context(), middleware.GetUserID(r), classID); err != nil || !ok {
		inviteFail(w, ErrNotYourClass)
		return
	}
	emails, ok := decodeInvites(w, r)
	if !ok {
		return
	}
	b, err := h.svc.CreateInvites(r.Context(), middleware.GetInstitutionID(r), classID, middleware.GetUserID(r), emails)
	if err != nil {
		log.Printf("create invites: %v", err)
		middleware.InternalError(w)
		return
	}
	h.svc.notifyInvites(r.Context(), b.Created)
	middleware.JSON(w, http.StatusCreated, b)
}

func (h *TeacherHandler) ListInvites(w http.ResponseWriter, r *http.Request) {
	classID := chi.URLParam(r, "classId")
	if ok, err := h.svc.TeacherOwnsClass(r.Context(), middleware.GetUserID(r), classID); err != nil || !ok {
		inviteFail(w, ErrNotYourClass)
		return
	}
	out, err := h.svc.ListClassInvites(r.Context(), middleware.GetInstitutionID(r), classID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}

func (h *TeacherHandler) RevokeInvite(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "inviteId")
	g, err := h.svc.InviteGroup(r.Context(), id)
	if err != nil {
		inviteFail(w, err)
		return
	}
	if ok, err := h.svc.TeacherOwnsClass(r.Context(), middleware.GetUserID(r), g); err != nil || !ok {
		inviteFail(w, ErrNotYourClass)
		return
	}
	if err := h.svc.RevokeInvite(r.Context(), middleware.GetInstitutionID(r), id); err != nil {
		inviteFail(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "invite revoked"})
}

// Institution side: any class in the institute.
func (h *InstitutionHandler) CreateInvites(w http.ResponseWriter, r *http.Request) {
	emails, ok := decodeInvites(w, r)
	if !ok {
		return
	}
	b, err := h.svc.CreateInvites(r.Context(), middleware.GetInstitutionID(r), chi.URLParam(r, "groupId"), middleware.GetUserID(r), emails)
	if errors.Is(err, ErrNotFound) {
		middleware.NotFound(w, "class")
		return
	}
	if err != nil {
		log.Printf("create invites: %v", err)
		middleware.InternalError(w)
		return
	}
	h.svc.notifyInvites(r.Context(), b.Created)
	middleware.JSON(w, http.StatusCreated, b)
}

func (h *InstitutionHandler) ListInvites(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.ListClassInvites(r.Context(), middleware.GetInstitutionID(r), chi.URLParam(r, "groupId"))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}

func (h *InstitutionHandler) RevokeInvite(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.RevokeInvite(r.Context(), middleware.GetInstitutionID(r), chi.URLParam(r, "inviteId")); err != nil {
		inviteFail(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "invite revoked"})
}

// Student side.
func (h *StudentHandler) MyInvites(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.MyInvites(r.Context(), middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}

func (h *StudentHandler) AcceptInvite(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.AcceptInvite(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "inviteId"))
	if err != nil {
		inviteFail(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, res)
}
