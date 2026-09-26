package featureonboarding

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

var allowedFeatures = map[string]struct{}{
	"today": {}, "classes": {}, "curriculum": {}, "assessment_creation": {},
	"question_import": {}, "publishing_assignment": {}, "results": {},
	"learning_insights": {}, "follow_up_practice": {}, "follow_up_outcomes": {},
	"inbox": {}, "reports_exports": {}, "custom_dashboards": {}, "settings": {},
}

type Handler struct{ db *pgxpool.Pool }

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

type Progress struct {
	FeatureKey  string     `json:"feature_key"`
	Status      string     `json:"status"`
	LastStep    int        `json:"last_step"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	DismissedAt *time.Time `json:"dismissed_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `SELECT feature_key,status,last_step,started_at,completed_at,dismissed_at,updated_at
		FROM teacher_feature_onboarding WHERE teacher_id=$1 AND institution_id=$2 ORDER BY updated_at DESC`,
		middleware.GetUserID(r), middleware.GetInstitutionID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	result := []Progress{}
	for rows.Next() {
		var item Progress
		if err := rows.Scan(&item.FeatureKey, &item.Status, &item.LastStep, &item.StartedAt, &item.CompletedAt, &item.DismissedAt, &item.UpdatedAt); err != nil {
			middleware.InternalError(w)
			return
		}
		result = append(result, item)
	}
	if rows.Err() != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, result)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	featureKey := chi.URLParam(r, "featureKey")
	if _, ok := allowedFeatures[featureKey]; !ok {
		middleware.BadRequest(w, "unknown onboarding feature")
		return
	}
	var in struct {
		Status   string `json:"status"`
		LastStep int    `json:"last_step"`
	}
	decoder := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || (in.Status != "started" && in.Status != "dismissed" && in.Status != "completed") || in.LastStep < 0 || in.LastStep > 50 {
		middleware.BadRequest(w, "status and a valid last_step are required")
		return
	}
	var item Progress
	err := h.db.QueryRow(r.Context(), `INSERT INTO teacher_feature_onboarding
		(teacher_id,institution_id,feature_key,status,last_step,completed_at,dismissed_at)
		VALUES ($1,$2,$3,$4,$5,CASE WHEN $4='completed' THEN now() END,CASE WHEN $4='dismissed' THEN now() END)
		ON CONFLICT (teacher_id,institution_id,feature_key) DO UPDATE SET
		status=EXCLUDED.status,last_step=EXCLUDED.last_step,
		completed_at=CASE WHEN EXCLUDED.status='completed' THEN now() END,
		dismissed_at=CASE WHEN EXCLUDED.status='dismissed' THEN now() END,updated_at=now()
		RETURNING feature_key,status,last_step,started_at,completed_at,dismissed_at,updated_at`,
		middleware.GetUserID(r), middleware.GetInstitutionID(r), featureKey, in.Status, in.LastStep).
		Scan(&item.FeatureKey, &item.Status, &item.LastStep, &item.StartedAt, &item.CompletedAt, &item.DismissedAt, &item.UpdatedAt)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, item)
}
