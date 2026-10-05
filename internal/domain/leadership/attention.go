package leadership

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/domain/institution"
	"github.com/qwish/backend/internal/domain/learning"
	"github.com/qwish/backend/internal/middleware"
)

// The institute's decision dashboards for leadership role holders, under
// reports.read: the same queries and definitions as the institution admin's,
// limited to the caller's granted departments (a whole-institution role sees
// every class). ?department_id narrows within the grant; outside it is 403.

// reportScope is nil for whole-institution reach, else the department ids.
func reportScope(w http.ResponseWriter, r *http.Request, gs Grants) ([]string, bool) {
	all, depts, ok := requestedScope(r, gs, PermReportsRead)
	if !ok {
		middleware.Forbidden(w)
		return nil, false
	}
	if all {
		return nil, true
	}
	return depts, true
}

func (h *Handler) ClassAttention(w http.ResponseWriter, r *http.Request, gs Grants) {
	depts, ok := reportScope(w, r, gs)
	if !ok {
		return
	}
	days, ok := institution.ClassAttentionDays(r)
	if !ok {
		middleware.BadRequest(w, "days must be 7, 30 or 90")
		return
	}
	page, limit := institution.PageParams(r)
	out, err := institution.QueryClassAttention(r.Context(), h.db, middleware.GetInstitutionID(r), depts, days, limit, (page-1)*limit)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSONWithMeta(w, http.StatusOK, out, &middleware.Meta{Page: page, Limit: limit, Total: out.Totals.ClassesNeedingAttention})
}

func (h *Handler) ClassStudentsAttention(w http.ResponseWriter, r *http.Request, gs Grants) {
	depts, ok := reportScope(w, r, gs)
	if !ok {
		return
	}
	page, limit := institution.PageParams(r)
	out, total, err := institution.QueryClassStudentsAttention(r.Context(), h.db, middleware.GetInstitutionID(r), chi.URLParam(r, "classId"), depts, limit, (page-1)*limit)
	if errors.Is(err, institution.ErrClassNotFound) {
		middleware.NotFound(w, "class")
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSONWithMeta(w, http.StatusOK, out, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

func (h *Handler) LearningPriorities(w http.ResponseWriter, r *http.Request, gs Grants) {
	depts, ok := reportScope(w, r, gs)
	if !ok {
		return
	}
	from, staleBefore, ok := learning.Window(r)
	if !ok {
		middleware.BadRequest(w, "days must be 7, 30 or 90 and stale_after_days 1–3650")
		return
	}
	out, err := learning.QueryPriorities(r.Context(), h.db, middleware.GetInstitutionID(r), depts, from, staleBefore)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}

func (h *Handler) SupportSummary(w http.ResponseWriter, r *http.Request, gs Grants) {
	depts, ok := reportScope(w, r, gs)
	if !ok {
		return
	}
	out, err := learning.QuerySupportSummary(r.Context(), h.db, middleware.GetInstitutionID(r), depts)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}
