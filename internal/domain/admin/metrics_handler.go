package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

type MetricsHandler struct{ svc *MetricsService }

func NewMetricsHandler(db *pgxpool.Pool) *MetricsHandler {
	return &MetricsHandler{svc: NewMetricsService(db)}
}

const bucketTimezone = "Asia/Kolkata"

// Catalog advertises the metric vocabulary so the UI builds every picker from
// the server rather than its own copy. Changes only on deploy.
func (h *MetricsHandler) Catalog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, max-age=300")
	middleware.JSON(w, http.StatusOK, map[string]any{
		"metrics":       Catalog(),
		"granularities": Granularities,
		"timezone":      bucketTimezone,
	})
}

// resolveInstitution reads and validates the optional institution_id filter.
// Returns ok=false when it has already written an error response.
func (h *MetricsHandler) resolveInstitution(w http.ResponseWriter, r *http.Request) (*string, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("institution_id"))
	if raw == "" {
		return nil, true
	}
	exists, err := h.svc.InstitutionExists(r.Context(), raw)
	if err != nil {
		// An unparseable uuid surfaces here as a query error, not a 500 — the
		// caller sent a bad filter, so say so.
		middleware.BadRequest(w, "institution_id must be a valid uuid")
		return nil, false
	}
	if !exists {
		middleware.NotFound(w, "institution")
		return nil, false
	}
	return &raw, true
}

func (h *MetricsHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	win, err := ResolveWindow(q.Get("from"), q.Get("to"), q.Get("granularity"), time.Now())
	if err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}

	compare := q.Get("compare")
	switch compare {
	case "", "previous", "year":
	default:
		middleware.BadRequest(w, "compare must be 'previous' or 'year'")
		return
	}

	instID, ok := h.resolveInstitution(w, r)
	if !ok {
		return
	}

	var ids []string
	if raw := strings.TrimSpace(q.Get("metrics")); raw != "" {
		ids = strings.Split(raw, ",")
	}
	sel, dropped, err := SelectMetrics(ids, instID != nil)
	if err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}

	series, err := h.svc.Series(r.Context(), sel, win, instID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	totals, err := h.svc.Totals(r.Context(), sel, win, instID)
	if err != nil {
		middleware.InternalError(w)
		return
	}

	data := map[string]any{
		"from":        win.From.Format(dateLayout),
		"to":          win.To.Format(dateLayout),
		"granularity": win.Gran,
		"timezone":    bucketTimezone,
		"series":      series,
		"totals":      totals,
	}
	if instID != nil {
		data["institution_id"] = *instID
	} else {
		data["institution_id"] = nil
	}
	// dropped is present only when something was excluded, so the UI can treat
	// its presence as "tell the admin" rather than checking for an empty array.
	if len(dropped) > 0 {
		data["dropped"] = dropped
	}

	if compare != "" {
		prev := win.Previous()
		if compare == "year" {
			prev = win.LastYear()
		}
		prevTotals, err := h.svc.Totals(r.Context(), sel, prev, instID)
		if err != nil {
			middleware.InternalError(w)
			return
		}
		prevSeries, err := h.svc.Series(r.Context(), sel, prev, instID)
		if err != nil {
			middleware.InternalError(w)
			return
		}
		data["previous"] = prevTotals
		data["previous_series"] = prevSeries
		data["previous_from"] = prev.From.Format(dateLayout)
		data["previous_to"] = prev.To.Format(dateLayout)
	}

	middleware.JSON(w, http.StatusOK, data)
}

func (h *MetricsHandler) Distributions(w http.ResponseWriter, r *http.Request) {
	instID, ok := h.resolveInstitution(w, r)
	if !ok {
		return
	}
	data, err := h.svc.Distributions(r.Context(), instID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, data)
}
