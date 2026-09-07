package topicrequest

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	db *pgxpool.Pool
}

func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db}
}

type TopicRequest struct {
	ID          string    `json:"id"`
	StudentID   string    `json:"student_id"`
	Topic       string    `json:"topic"`
	Subject     *string   `json:"subject,omitempty"`
	Description *string   `json:"description,omitempty"`
	Status      string    `json:"status"`
	AssignedTo  *string   `json:"assigned_to,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type updateRequest struct {
	Status     string  `json:"status"`
	AssignedTo *string `json:"assigned_to"`
}

// POST /api/v1/topic-requests
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Topic       string  `json:"topic"`
		Subject     *string `json:"subject"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Topic == "" {
		middleware.BadRequest(w, "topic is required")
		return
	}
	userID := middleware.GetUserID(r)
	instID := middleware.GetInstitutionID(r)

	var tr TopicRequest
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO topic_requests (student_id, institution_id, topic, subject, description)
		 VALUES ($1,$2,$3,$4,$5)
		 RETURNING id, student_id, topic, subject, description, status, assigned_to, created_at`,
		userID, instID, req.Topic, req.Subject, req.Description,
	).Scan(&tr.ID, &tr.StudentID, &tr.Topic, &tr.Subject, &tr.Description, &tr.Status, &tr.AssignedTo, &tr.CreatedAt)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, tr)
}

// GET /api/v1/topic-requests/mine
func (h *Handler) ListMine(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	rows, err := h.db.Query(r.Context(),
		`SELECT id, student_id, topic, subject, description, status, assigned_to, created_at
		 FROM topic_requests WHERE student_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	var list []TopicRequest
	for rows.Next() {
		var tr TopicRequest
		rows.Scan(&tr.ID, &tr.StudentID, &tr.Topic, &tr.Subject, &tr.Description, &tr.Status, &tr.AssignedTo, &tr.CreatedAt)
		list = append(list, tr)
	}
	if list == nil {
		list = []TopicRequest{}
	}
	middleware.JSON(w, http.StatusOK, list)
}

// GET /api/v1/teacher/topic-requests
func (h *Handler) TeacherList(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	offset := (page - 1) * limit

	status := r.URL.Query().Get("status")
	args := []interface{}{instID}
	where := `institution_id=$1`
	if status != "" {
		where += ` AND status=$2`
		args = append(args, status)
	}

	var total int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM topic_requests WHERE `+where, args...).Scan(&total)
	args = append(args, limit, offset)
	n := len(args)

	rows, err := h.db.Query(r.Context(),
		`SELECT id, student_id, topic, subject, description, status, assigned_to, created_at FROM topic_requests WHERE `+where+
			" ORDER BY created_at DESC LIMIT $"+strconv.Itoa(n-1)+" OFFSET $"+strconv.Itoa(n),
		args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	var list []TopicRequest
	for rows.Next() {
		var tr TopicRequest
		rows.Scan(&tr.ID, &tr.StudentID, &tr.Topic, &tr.Subject, &tr.Description, &tr.Status, &tr.AssignedTo, &tr.CreatedAt)
		list = append(list, tr)
	}
	if list == nil {
		list = []TopicRequest{}
	}
	middleware.JSONWithMeta(w, http.StatusOK, list, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// PATCH /api/v1/teacher/topic-requests/:requestId
func (h *Handler) TeacherUpdate(w http.ResponseWriter, r *http.Request) {
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request")
		return
	}
	if req.Status != "" && req.Status != "pending" && req.Status != "in_progress" && req.Status != "done" {
		middleware.BadRequest(w, "invalid status")
		return
	}
	teacherID := middleware.GetUserID(r)
	if req.AssignedTo != nil && *req.AssignedTo != "" && *req.AssignedTo != teacherID {
		middleware.Forbidden(w)
		return
	}
	reqID := chi.URLParam(r, "requestId")
	result, err := h.db.Exec(r.Context(),
		`UPDATE topic_requests
		 SET status=CASE WHEN $1 != '' THEN $1 ELSE status END,
		     assigned_to=CASE
		       WHEN $2::text IS NOT NULL THEN NULLIF($2, '')::uuid
		       WHEN $1 != '' AND assigned_to IS NULL THEN $5::uuid
		       ELSE assigned_to
		     END
		 WHERE id=$3 AND institution_id=$4 AND (assigned_to IS NULL OR assigned_to=$5)`,
		req.Status, req.AssignedTo, reqID, middleware.GetInstitutionID(r), teacherID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if result.RowsAffected() == 0 {
		middleware.NotFound(w, "topic request")
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "updated"})
}

// PATCH /api/v1/institution/topic-requests/:requestId
func (h *Handler) InstitutionUpdate(w http.ResponseWriter, r *http.Request) {
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request")
		return
	}
	if req.Status != "" && req.Status != "pending" && req.Status != "in_progress" && req.Status != "done" {
		middleware.BadRequest(w, "invalid status")
		return
	}
	reqID := chi.URLParam(r, "requestId")
	result, err := h.db.Exec(r.Context(),
		`UPDATE topic_requests
		 SET status=CASE WHEN $1 != '' THEN $1 ELSE status END,
		     assigned_to=CASE WHEN $2::text IS NULL THEN assigned_to ELSE NULLIF($2, '')::uuid END
		 WHERE id=$3 AND institution_id=$4
		   AND ($2::text IS NULL OR $2='' OR EXISTS (
		     SELECT 1 FROM users u
		     WHERE u.id=$2::uuid AND u.institution_id=$4 AND u.role='teacher' AND u.status='active'
		   ))`,
		req.Status, req.AssignedTo, reqID, middleware.GetInstitutionID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if result.RowsAffected() == 0 {
		middleware.NotFound(w, "topic request")
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "updated"})
}
