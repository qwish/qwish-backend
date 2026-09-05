package curriculum

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Route registration stays beside the handlers so integration tests exercise
// the same role checks and paths used by the production router.
func (h *Handler) InstitutionRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(academicScope("institution_admin"))
		r.Get("/academic-years", h.ListYears)
		r.Post("/academic-years", h.CreateYear)
		r.Get("/curricula", h.ListVersions)
		r.Post("/curricula", h.CreateCurriculum)
		r.Post("/curricula/{curriculumId}/versions", h.CreateVersion)
		r.Get("/curriculum-versions/{versionId}", h.GetVersion)
		r.Put("/curriculum-versions/{versionId}", h.UpdateVersion)
		r.Post("/curriculum-versions/{versionId}/publish", h.PublishVersion)
		r.Get("/groups/{groupId}/curricula", h.ListAssignments)
		r.Post("/groups/{groupId}/curricula", h.Assign)
		r.Delete("/groups/{groupId}/curricula/{assignmentId}", h.EndAssignment)
	})
}

func (h *Handler) TeacherRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(academicScope("teacher"))
		r.Get("/classes/{groupId}/curricula", h.ListAssignments)
		r.Get("/curriculum-versions/{versionId}", h.GetVersion)
	})
}

func academicScope(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if middleware.GetRole(r) != role || !validID(middleware.GetInstitutionID(r)) || !validID(middleware.GetUserID(r)) {
				middleware.Forbidden(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func validID(id string) bool { parsed, err := uuid.Parse(id); return err == nil && parsed != uuid.Nil }
func actor(r *http.Request) Actor {
	return Actor{InstitutionID: middleware.GetInstitutionID(r), ID: middleware.GetUserID(r)}
}
func teacherID(r *http.Request) string {
	if middleware.GetRole(r) == "teacher" {
		return middleware.GetUserID(r)
	}
	return ""
}

func pathID(w http.ResponseWriter, r *http.Request, key string) (string, bool) {
	id := chi.URLParam(r, key)
	if !validID(id) {
		middleware.BadRequest(w, key+" must be a UUID")
		return "", false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		middleware.BadRequest(w, "invalid JSON body or unsupported fields")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		middleware.BadRequest(w, "body must contain one JSON object")
		return false
	}
	return true
}

func replyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		middleware.NotFound(w, "academic resource")
	case errors.Is(err, ErrPublished):
		middleware.Error(w, http.StatusConflict, "VERSION_PUBLISHED", err.Error())
	case errors.Is(err, ErrRevision):
		middleware.Error(w, http.StatusConflict, "REVISION_CONFLICT", err.Error())
	case errors.Is(err, ErrConflict):
		middleware.Error(w, http.StatusConflict, "ACADEMIC_CONFLICT", err.Error())
	case errors.Is(err, ErrIncomplete):
		middleware.Error(w, http.StatusUnprocessableEntity, "CURRICULUM_INCOMPLETE", err.Error())
	default:
		log.Printf("curriculum: %v", err)
		middleware.InternalError(w)
	}
}

func (h *Handler) ListYears(w http.ResponseWriter, r *http.Request) {
	data, err := h.svc.ListYears(r.Context(), middleware.GetInstitutionID(r))
	if err != nil {
		replyError(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, data)
}
func (h *Handler) CreateYear(w http.ResponseWriter, r *http.Request) {
	var in YearInput
	if !decode(w, r, &in) {
		return
	}
	if err := in.Validate(); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	data, err := h.svc.CreateYear(r.Context(), actor(r), in)
	if err != nil {
		replyError(w, err)
		return
	}
	middleware.JSON(w, http.StatusCreated, data)
}
func (h *Handler) ListVersions(w http.ResponseWriter, r *http.Request) {
	page, limit := 1, 20
	for key, dest := range map[string]*int{"page": &page, "limit": &limit} {
		if raw := r.URL.Query().Get(key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 100000 {
				middleware.BadRequest(w, "invalid pagination")
				return
			}
			*dest = n
		}
	}
	if limit > 50 {
		middleware.BadRequest(w, "limit must be at most 50")
		return
	}
	data, total, err := h.svc.ListVersions(r.Context(), middleware.GetInstitutionID(r), page, limit)
	if err != nil {
		replyError(w, err)
		return
	}
	middleware.JSONWithMeta(w, http.StatusOK, data, &middleware.Meta{Page: page, Limit: limit, Total: total})
}
func (h *Handler) CreateCurriculum(w http.ResponseWriter, r *http.Request) {
	var in CreateInput
	if !decode(w, r, &in) {
		return
	}
	if err := validText(&in.Name, "name", 160); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	if err := in.VersionInput.Validate(); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	id, err := h.svc.CreateVersion(r.Context(), actor(r), "", in.Name, in.VersionInput)
	if err != nil {
		replyError(w, err)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": id})
}
func (h *Handler) CreateVersion(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "curriculumId")
	if !ok {
		return
	}
	var in VersionInput
	if !decode(w, r, &in) {
		return
	}
	if err := in.Validate(); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	versionID, err := h.svc.CreateVersion(r.Context(), actor(r), id, "", in)
	if err != nil {
		replyError(w, err)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": versionID})
}
func (h *Handler) GetVersion(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "versionId")
	if !ok {
		return
	}
	data, err := h.svc.GetVersion(r.Context(), middleware.GetInstitutionID(r), id, teacherID(r))
	if err != nil {
		replyError(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, data)
}
func (h *Handler) UpdateVersion(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "versionId")
	if !ok {
		return
	}
	var in struct {
		Revision int `json:"revision"`
		VersionInput
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Revision < 1 {
		middleware.BadRequest(w, "revision is required")
		return
	}
	if err := in.VersionInput.Validate(); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	if err := h.svc.UpdateVersion(r.Context(), actor(r), id, in.Revision, in.VersionInput); err != nil {
		replyError(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]int{"revision": in.Revision + 1})
}
func (h *Handler) PublishVersion(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "versionId")
	if !ok {
		return
	}
	var in struct {
		Revision int `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Revision < 1 {
		middleware.BadRequest(w, "revision is required")
		return
	}
	if err := h.svc.PublishVersion(r.Context(), actor(r), id, in.Revision); err != nil {
		replyError(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]int{"revision": in.Revision + 1})
}
func (h *Handler) ListAssignments(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "groupId")
	if !ok {
		return
	}
	data, err := h.svc.ListAssignments(r.Context(), middleware.GetInstitutionID(r), id, teacherID(r))
	if err != nil {
		replyError(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, data)
}
func (h *Handler) Assign(w http.ResponseWriter, r *http.Request) {
	groupID, ok := pathID(w, r, "groupId")
	if !ok {
		return
	}
	var in AssignmentInput
	if !decode(w, r, &in) {
		return
	}
	if !validID(in.AcademicYearID) || !validID(in.VersionID) {
		middleware.BadRequest(w, "academic_year_id and version_id must be UUIDs")
		return
	}
	id, err := h.svc.Assign(r.Context(), actor(r), groupID, in)
	if err != nil {
		replyError(w, err)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": id})
}
func (h *Handler) EndAssignment(w http.ResponseWriter, r *http.Request) {
	groupID, ok := pathID(w, r, "groupId")
	if !ok {
		return
	}
	id, ok := pathID(w, r, "assignmentId")
	if !ok {
		return
	}
	if err := h.svc.EndAssignment(r.Context(), actor(r), groupID, id); err != nil {
		replyError(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "ended"})
}
