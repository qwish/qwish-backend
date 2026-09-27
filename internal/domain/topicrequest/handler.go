package topicrequest

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/jsonx"
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
	// Filled by the staff listing only.
	StudentName    string  `json:"student_name,omitempty"`
	AssignedToName *string `json:"assigned_to_name,omitempty"`
	// RequesterCount is how many open requests in the institution ask for the
	// same topic and subject (this one included); OtherRequesters names a few.
	RequesterCount  int      `json:"requester_count,omitempty"`
	OtherRequesters []string `json:"other_requesters,omitempty"`
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
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || req.Topic == "" {
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
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	where, args, bad := topicListWhere(instID, q.Get("status"), q.Get("assigned"), q.Get("search"))
	if bad != "" {
		middleware.BadRequest(w, bad)
		return
	}

	var total int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM topic_requests tr LEFT JOIN users su ON su.id=tr.student_id WHERE `+where, args...).Scan(&total)
	args = append(args, limit, offset)
	n := len(args)

	// Open work reads oldest first; everything else newest first.
	order := "tr.created_at DESC"
	if s := q.Get("status"); s == "open" || s == "pending" || s == "in_progress" {
		order = "tr.created_at ASC"
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT tr.id, tr.student_id, tr.topic, tr.subject, tr.description, tr.status, tr.assigned_to, tr.created_at,
		        COALESCE(NULLIF(su.display_name,''), su.full_name, ''),
		        (SELECT COALESCE(NULLIF(t.display_name,''), t.full_name) FROM users t WHERE t.id=tr.assigned_to),
		        (SELECT COUNT(*) FROM topic_requests o WHERE o.institution_id=tr.institution_id AND o.status<>'done'
		           AND lower(btrim(o.topic))=lower(btrim(tr.topic)) AND o.subject IS NOT DISTINCT FROM tr.subject),
		        COALESCE((SELECT array_agg(n) FROM (SELECT COALESCE(NULLIF(ou.display_name,''), ou.full_name) AS n
		           FROM topic_requests o JOIN users ou ON ou.id=o.student_id
		          WHERE o.institution_id=tr.institution_id AND o.status<>'done' AND o.id<>tr.id
		            AND lower(btrim(o.topic))=lower(btrim(tr.topic)) AND o.subject IS NOT DISTINCT FROM tr.subject
		          ORDER BY o.created_at LIMIT 3) x), '{}')
		 FROM topic_requests tr LEFT JOIN users su ON su.id=tr.student_id
		 WHERE `+where+" ORDER BY "+order+", tr.id LIMIT $"+strconv.Itoa(n-1)+" OFFSET $"+strconv.Itoa(n),
		args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	list := []TopicRequest{}
	for rows.Next() {
		var tr TopicRequest
		rows.Scan(&tr.ID, &tr.StudentID, &tr.Topic, &tr.Subject, &tr.Description, &tr.Status, &tr.AssignedTo, &tr.CreatedAt,
			&tr.StudentName, &tr.AssignedToName, &tr.RequesterCount, &tr.OtherRequesters)
		list = append(list, tr)
	}
	middleware.JSONWithMeta(w, http.StatusOK, list, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// topicListWhere builds the staff listing filter. status accepts a single
// state or "open" (pending + in progress); assigned=none keeps unassigned
// requests; search matches topic, subject, description or the student's name.
func topicListWhere(instID, status, assigned, search string) (string, []interface{}, string) {
	where := `tr.institution_id=$1`
	args := []interface{}{instID}
	switch status {
	case "":
	case "open":
		where += ` AND tr.status IN ('pending','in_progress')`
	case "pending", "in_progress", "done":
		args = append(args, status)
		where += ` AND tr.status=$` + strconv.Itoa(len(args))
	default:
		return "", nil, "status must be pending, in_progress, done or open"
	}
	switch assigned {
	case "":
	case "none":
		where += ` AND tr.assigned_to IS NULL`
	default:
		return "", nil, "assigned must be none or empty"
	}
	if search != "" {
		args = append(args, "%"+search+"%")
		p := `$` + strconv.Itoa(len(args))
		where += ` AND (tr.topic ILIKE ` + p + ` OR tr.subject ILIKE ` + p + ` OR tr.description ILIKE ` + p +
			` OR su.display_name ILIKE ` + p + ` OR su.full_name ILIKE ` + p + `)`
	}
	return where, args, ""
}

// GET /institution/topic-requests/counts
func (h *Handler) Counts(w http.ResponseWriter, r *http.Request) {
	var open, unassigned, done int
	h.db.QueryRow(r.Context(), `SELECT
		COUNT(*) FILTER (WHERE status IN ('pending','in_progress')),
		COUNT(*) FILTER (WHERE status IN ('pending','in_progress') AND assigned_to IS NULL),
		COUNT(*) FILTER (WHERE status='done')
		FROM topic_requests WHERE institution_id=$1`, middleware.GetInstitutionID(r)).Scan(&open, &unassigned, &done)
	middleware.JSON(w, http.StatusOK, map[string]int{"open": open, "unassigned": unassigned, "done": done})
}

// PATCH /api/v1/teacher/topic-requests/:requestId
func (h *Handler) TeacherUpdate(w http.ResponseWriter, r *http.Request) {
	var req updateRequest
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
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
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
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
