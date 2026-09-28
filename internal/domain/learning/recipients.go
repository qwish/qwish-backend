package learning

// Teacher-panel roadmap: per-student assignment state.
//   R2  GET  /teacher/assignments/{id}/recipients, POST /teacher/assignments/{id}/remind
//   R3  PATCH /teacher/assignments/{id}/recipients  (extend | excuse | unexcuse)
//   R13 PATCH /teacher/assignments/{id}             (reschedule open / due)

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/domain/notification"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

// ownsAssignment: the assignment is in the caller's institution and the caller
// teaches its class.
func (h *Handler) ownsAssignment(r *http.Request, assignmentID string) bool {
	var ok bool
	err := h.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM learning_assignments a
		JOIN group_teachers gt ON gt.group_id=a.group_id AND gt.user_id=$3
		WHERE a.id::text=$1 AND a.institution_id=$2)`, assignmentID, middleware.GetInstitutionID(r), middleware.GetUserID(r)).Scan(&ok)
	return err == nil && ok
}

type Recipient struct {
	StudentID    string     `json:"student_id"`
	StudentName  string     `json:"student_name"`
	Status       string     `json:"status"`
	DueAt        *time.Time `json:"due_at"`
	Note         *string    `json:"note"`
	SubmittedAt  *time.Time `json:"submitted_at"`
	LastActiveAt *time.Time `json:"last_active_at"`
}

// recipientStatus mirrors how ListAssignments counts overdue, honouring a
// per-student extension.
const recipientStatus = `CASE WHEN ar.status IN ('assigned','started') AND COALESCE(ar.due_at_override,a.due_at) < now() THEN 'overdue' ELSE ar.status END`

func (h *Handler) ListRecipients(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "assignmentId")
	if !h.ownsAssignment(r, id) {
		middleware.NotFound(w, "assignment")
		return
	}
	var lastReminded *time.Time
	if err := h.db.QueryRow(r.Context(), `SELECT last_reminded_at FROM learning_assignments WHERE id=$1`, id).Scan(&lastReminded); err != nil {
		middleware.InternalError(w)
		return
	}
	rows, err := h.db.Query(r.Context(), `SELECT ar.student_id, COALESCE(NULLIF(u.display_name,''),u.full_name,''),
		`+recipientStatus+`, ar.due_at_override, ar.teacher_note, ar.submitted_at, u.last_active_at
		FROM learning_assignment_recipients ar
		JOIN learning_assignments a ON a.id=ar.assignment_id
		JOIN users u ON u.id=ar.student_id AND u.deleted_at IS NULL
		WHERE ar.assignment_id=$1 AND ar.status<>'withdrawn'
		ORDER BY 2`, id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	list := []Recipient{}
	for rows.Next() {
		var item Recipient
		if err := rows.Scan(&item.StudentID, &item.StudentName, &item.Status, &item.DueAt, &item.Note, &item.SubmittedAt, &item.LastActiveAt); err != nil {
			middleware.InternalError(w)
			return
		}
		list = append(list, item)
	}
	if rows.Err() != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]interface{}{"recipients": list, "last_reminded_at": lastReminded})
}

type studentIDsInput struct {
	StudentIDs []string `json:"student_ids"`
}

// Remind nudges non-submitters. One reminder per assignment per hour.
func (h *Handler) RemindRecipients(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "assignmentId")
	var in studentIDsInput
	decoder := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || len(in.StudentIDs) == 0 || len(in.StudentIDs) > 500 {
		middleware.BadRequest(w, "student_ids is required")
		return
	}
	if !h.ownsAssignment(r, id) {
		middleware.NotFound(w, "assignment")
		return
	}
	var now time.Time
	var title string
	err := h.db.QueryRow(r.Context(), `UPDATE learning_assignments a SET last_reminded_at=now()
		FROM quizzes q WHERE a.id=$1 AND q.id=a.quiz_id AND a.status='published'
		AND (a.last_reminded_at IS NULL OR a.last_reminded_at < now()-interval '1 hour')
		RETURNING a.last_reminded_at, q.title`, id).Scan(&now, &title)
	if err != nil {
		middleware.Error(w, http.StatusTooManyRequests, "RATE_LIMITED", "A reminder was sent less than an hour ago.")
		return
	}
	rows, err := h.db.Query(r.Context(), `SELECT ar.student_id FROM learning_assignment_recipients ar
		WHERE ar.assignment_id=$1 AND ar.student_id::text = ANY($2::text[]) AND ar.status IN ('assigned','started','overdue')`, id, in.StudentIDs)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	targets := []string{}
	for rows.Next() {
		var sid string
		if rows.Scan(&sid) == nil {
			targets = append(targets, sid)
		}
	}
	rows.Close()
	if h.notif != nil {
		ref := "assignment:" + id + ":reminder:" + now.UTC().Format("2006010215")
		for _, sid := range targets {
			h.notif.Emit(r.Context(), sid, "assignment", "Reminder from your teacher", title+" is still waiting for you.",
				notification.WithIcon("assignment"), notification.WithColor("indigo"), notification.WithReference(ref))
		}
	}
	middleware.JSON(w, http.StatusOK, map[string]interface{}{"sent": len(targets), "last_reminded_at": now})
}

type recipientsUpdate struct {
	StudentIDs []string `json:"student_ids"`
	Action     string   `json:"action"`
	DueAt      *string  `json:"due_at"`
	Note       *string  `json:"note"`
}

func (h *Handler) UpdateRecipients(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "assignmentId")
	var in recipientsUpdate
	decoder := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || len(in.StudentIDs) == 0 || len(in.StudentIDs) > 500 || (in.Note != nil && len(*in.Note) > 500) {
		middleware.BadRequest(w, "student_ids and a valid action are required")
		return
	}
	if !h.ownsAssignment(r, id) {
		middleware.NotFound(w, "assignment")
		return
	}
	var sql string
	args := []interface{}{id, in.StudentIDs}
	switch in.Action {
	case "extend":
		if in.DueAt == nil {
			middleware.BadRequest(w, "due_at is required to extend")
			return
		}
		due, err := time.Parse(time.RFC3339, *in.DueAt)
		if err != nil || !due.After(time.Now()) {
			middleware.BadRequest(w, "due_at must be a future RFC3339 timestamp")
			return
		}
		note := ""
		if in.Note != nil {
			note = strings.TrimSpace(*in.Note)
		}
		sql = `UPDATE learning_assignment_recipients SET due_at_override=$3, teacher_note=NULLIF($4,''),
			status=CASE WHEN status='overdue' THEN CASE WHEN attempt_id IS NULL THEN 'assigned' ELSE 'started' END ELSE status END
			WHERE assignment_id=$1 AND student_id::text = ANY($2::text[]) AND status IN ('assigned','started','overdue')`
		args = append(args, due, note)
	case "excuse":
		sql = `UPDATE learning_assignment_recipients SET status='excused'
			WHERE assignment_id=$1 AND student_id::text = ANY($2::text[]) AND status IN ('assigned','started','overdue')`
	case "unexcuse":
		sql = `UPDATE learning_assignment_recipients SET status=CASE WHEN submitted_at IS NOT NULL THEN 'submitted' WHEN attempt_id IS NOT NULL THEN 'started' ELSE 'assigned' END
			WHERE assignment_id=$1 AND student_id::text = ANY($2::text[]) AND status='excused'`
	default:
		middleware.BadRequest(w, "action must be extend, excuse or unexcuse")
		return
	}
	tag, err := h.db.Exec(r.Context(), sql, args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]int64{"updated": tag.RowsAffected()})
}

// UpdateAssignment reschedules the open time and/or the class-wide due date.
// Keys present with null clear the value; absent keys are left alone.
func (h *Handler) UpdateAssignment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "assignmentId")
	var raw map[string]*string
	decoder := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	if decoder.Decode(&raw) != nil {
		middleware.BadRequest(w, "invalid body")
		return
	}
	for k := range raw {
		if k != "available_at" && k != "due_at" {
			middleware.BadRequest(w, "only available_at and due_at can change")
			return
		}
	}
	if !h.ownsAssignment(r, id) {
		middleware.NotFound(w, "assignment")
		return
	}
	var curAvail, curDue *time.Time
	if err := h.db.QueryRow(r.Context(), `SELECT available_at,due_at FROM learning_assignments WHERE id=$1 AND status='published'`, id).Scan(&curAvail, &curDue); err != nil {
		middleware.BadRequest(w, "only published assignments can be rescheduled")
		return
	}
	parse := func(key string, cur *time.Time) (*time.Time, bool) {
		v, ok := raw[key]
		if !ok {
			return cur, true
		}
		if v == nil || *v == "" {
			return nil, true
		}
		t, err := time.Parse(time.RFC3339, *v)
		return &t, err == nil
	}
	avail, ok1 := parse("available_at", curAvail)
	due, ok2 := parse("due_at", curDue)
	if !ok1 || !ok2 {
		middleware.BadRequest(w, "timestamps must be RFC3339")
		return
	}
	if due != nil {
		start := time.Now()
		if avail != nil && avail.After(start) {
			start = *avail
		}
		if _, changed := raw["due_at"]; changed && !due.After(start) {
			middleware.BadRequest(w, "due_at must be in the future and after available_at")
			return
		}
	}
	if _, err := h.db.Exec(r.Context(), `UPDATE learning_assignments SET available_at=$2,due_at=$3 WHERE id=$1`, id, avail, due); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
