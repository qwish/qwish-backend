package metrics

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

// ErrBadScopeRequest wraps a resolver failure the caller caused, so the handler
// answers 400 with the resolver's message instead of an opaque 500.
var ErrBadScopeRequest = errors.New("bad scope request")

// ScopeNote reports what the caller asked for versus what was actually applied.
// A teacher with no classes is answered institution-wide; without this field
// they would read those numbers as their own class's.
type ScopeNote struct {
	Requested ScopeKind `json:"requested"`
	Effective ScopeKind `json:"effective"`
	Reason    string    `json:"reason"`
}

// ScopeResolver derives the caller's scope from their token and, for teachers,
// the `scope` query parameter. It never reads an id from the query string.
type ScopeResolver func(r *http.Request) (Scope, ScopeNote, error)

type Handler struct {
	svc     *MetricsService
	resolve ScopeResolver
}

func NewHandler(db *pgxpool.Pool, resolve ScopeResolver) *Handler {
	return &Handler{svc: NewMetricsService(db), resolve: resolve}
}

// failed logs the cause before the opaque 500. Without this a query error is
// invisible — the client sees "an unexpected error occurred" and the server
// says nothing.
func failed(w http.ResponseWriter, what string, err error) {
	log.Printf("analytics: %s: %v", what, err)
	middleware.InternalError(w)
}

// scope runs the resolver and writes the error response itself. ok=false means
// a response has already been written.
func (h *Handler) scope(w http.ResponseWriter, r *http.Request) (Scope, ScopeNote, bool) {
	sc, note, err := h.resolve(r)
	if err != nil {
		if errors.Is(err, ErrBadScopeRequest) {
			middleware.BadRequest(w, err.Error())
		} else {
			failed(w, "resolve scope", err)
		}
		return Scope{}, ScopeNote{}, false
	}
	return sc, note, true
}

// Catalog advertises the metric vocabulary the caller's scope can actually
// answer, so every picker is built from the server rather than its own copy.
func (h *Handler) Catalog(w http.ResponseWriter, r *http.Request) {
	sc, note, ok := h.scope(w, r)
	if !ok {
		return
	}

	all := Catalog()
	visible := make([]MetricDef, 0, len(all))
	for _, m := range all {
		if m.answers(sc.Kind) {
			visible = append(visible, m)
		}
	}

	w.Header().Set("Cache-Control", "private, max-age=300")
	middleware.JSON(w, http.StatusOK, map[string]any{
		"metrics":       visible,
		"granularities": Granularities,
		"timezone":      BucketTimezone,
		"scope":         note,
	})
}

func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
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

	sc, note, ok := h.scope(w, r)
	if !ok {
		return
	}

	var ids []string
	if raw := strings.TrimSpace(q.Get("metrics")); raw != "" {
		ids = strings.Split(raw, ",")
	}
	sel, dropped, err := SelectMetrics(ids, sc.Kind)
	if err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}

	series, err := h.svc.Series(r.Context(), sel, win, sc)
	if err != nil {
		failed(w, "series", err)
		return
	}
	totals, err := h.svc.Totals(r.Context(), sel, win, sc)
	if err != nil {
		failed(w, "totals", err)
		return
	}

	data := map[string]any{
		"from":        win.From.Format(DateLayout),
		"to":          win.To.Format(DateLayout),
		"granularity": win.Gran,
		"timezone":    BucketTimezone,
		"series":      series,
		"totals":      totals,
		"scope":       note,
	}
	// institution_id is kept for the super-admin console, which reads it today.
	if sc.Kind == ScopeInstitution {
		data["institution_id"] = sc.ID
	} else {
		data["institution_id"] = nil
	}
	// dropped is present only when something was excluded, so the UI can treat
	// its presence as "tell the caller" rather than checking for an empty array.
	if len(dropped) > 0 {
		data["dropped"] = dropped
	}

	if compare != "" {
		prev := win.Previous()
		if compare == "year" {
			prev = win.LastYear()
		}
		prevTotals, err := h.svc.Totals(r.Context(), sel, prev, sc)
		if err != nil {
			failed(w, "previous totals", err)
			return
		}
		prevSeries, err := h.svc.Series(r.Context(), sel, prev, sc)
		if err != nil {
			failed(w, "previous series", err)
			return
		}
		data["previous"] = prevTotals
		data["previous_series"] = prevSeries
		data["previous_from"] = prev.From.Format(DateLayout)
		data["previous_to"] = prev.To.Format(DateLayout)
	}

	middleware.JSON(w, http.StatusOK, data)
}

func (h *Handler) Distributions(w http.ResponseWriter, r *http.Request) {
	sc, note, ok := h.scope(w, r)
	if !ok {
		return
	}
	shapes, dropped, err := h.svc.Distributions(r.Context(), sc)
	if err != nil {
		failed(w, "distributions", err)
		return
	}
	data := map[string]any{"scope": note}
	for k, v := range shapes {
		data[k] = v
	}
	if len(dropped) > 0 {
		data["dropped"] = dropped
	}
	middleware.JSON(w, http.StatusOK, data)
}

func (h *Handler) PointsLiability(w http.ResponseWriter, r *http.Request) {
	sc, note, ok := h.scope(w, r)
	if !ok {
		return
	}
	data, err := h.svc.PointsLiability(r.Context(), sc)
	if err != nil {
		if errors.Is(err, ErrScopeUnsupported) {
			middleware.BadRequest(w,
				"points liability cannot be scoped to your quizzes; the points ledger has no quiz linkage")
			return
		}
		failed(w, "points liability", err)
		return
	}
	data["scope"] = note
	middleware.JSON(w, http.StatusOK, data)
}
