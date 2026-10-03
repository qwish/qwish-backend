package teacher

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

// POST /api/v1/teacher/remedial-groups
//
// A remedial group is a class with kind='remedial': additive to the main class,
// no grade, joining off. Students only ever see the name the teacher chose.
func (h *Handler) CreateRemedialGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourceClassID string   `json:"source_class_id"`
		ConceptID     string   `json:"concept_id"`
		Name          string   `json:"name"`
		StudentIDs    []string `json:"student_ids"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 80 || len(req.StudentIDs) == 0 || len(req.StudentIDs) > 200 || req.SourceClassID == "" || req.ConceptID == "" {
		middleware.BadRequest(w, "name (1-80 characters), source_class_id, concept_id and 1-200 student_ids are required")
		return
	}
	for _, id := range append([]string{req.SourceClassID, req.ConceptID}, req.StudentIDs...) {
		if _, err := uuid.Parse(id); err != nil {
			middleware.BadRequest(w, "ids must be uuids")
			return
		}
	}
	teacherID, instID := middleware.GetUserID(r), middleware.GetInstitutionID(r)
	ctx := r.Context()

	tx, err := h.db.Begin(ctx)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(ctx)

	var dept *string
	err = tx.QueryRow(ctx, `SELECT g.department_id::text FROM groups g
		JOIN group_teachers gt ON gt.group_id=g.id AND gt.user_id=$2
		WHERE g.id=$1 AND g.institution_id=$3 AND g.archived_at IS NULL`, req.SourceClassID, teacherID, instID).Scan(&dept)
	if errors.Is(err, pgx.ErrNoRows) {
		middleware.Error(w, http.StatusForbidden, "NOT_YOUR_CLASS", "You can only create groups from classes you teach.")
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	var inClass int
	if err = tx.QueryRow(ctx, `SELECT count(DISTINCT user_id) FROM group_students WHERE group_id=$1 AND user_id::text = ANY($2)`,
		req.SourceClassID, req.StudentIDs).Scan(&inClass); err != nil {
		middleware.InternalError(w)
		return
	}
	if inClass != len(uniqueStrings(req.StudentIDs)) {
		middleware.BadRequest(w, "every student must be in the source class")
		return
	}
	var hasEvidence bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM learning_evidence WHERE concept_id=$1 AND institution_id=$2)`,
		req.ConceptID, instID).Scan(&hasEvidence); err != nil {
		middleware.InternalError(w)
		return
	}
	if !hasEvidence {
		middleware.BadRequest(w, "concept has no evidence at this institute")
		return
	}

	var id, code string
	if err = tx.QueryRow(ctx, `INSERT INTO groups (institution_id, name, invite_code, kind, joining_enabled, department_id,
			source_group_id, source_concept_id, created_by)
		VALUES ($1,$2,upper(substr(md5(random()::text||clock_timestamp()::text),1,8)),'remedial',false,$3::uuid,$4,$5,$6)
		RETURNING id, invite_code`, instID, req.Name, dept, req.SourceClassID, req.ConceptID, teacherID).Scan(&id, &code); err != nil {
		middleware.InternalError(w)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO group_teachers (group_id, user_id) VALUES ($1,$2)`, id, teacherID); err != nil {
		middleware.InternalError(w)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO group_students (group_id, user_id) SELECT $1, unnest($2::uuid[]) ON CONFLICT DO NOTHING`,
		id, uniqueStrings(req.StudentIDs)); err != nil {
		middleware.InternalError(w)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]any{"id": id, "name": req.Name, "invite_code": code, "member_count": inClass})
}

type evidenceCount struct {
	Correct int `json:"correct"`
	Errors  int `json:"errors"`
}

// GET /api/v1/teacher/remedial-groups/{groupId}/progress
func (h *Handler) RemedialProgress(w http.ResponseWriter, r *http.Request) {
	groupID, teacherID, instID := chi.URLParam(r, "groupId"), middleware.GetUserID(r), middleware.GetInstitutionID(r)
	if _, err := uuid.Parse(groupID); err != nil {
		middleware.NotFound(w, "group")
		return
	}
	ctx := r.Context()
	var conceptID, title string
	var created time.Time
	err := h.db.QueryRow(ctx, `SELECT c.id::text, c.title, g.created_at FROM groups g
		JOIN group_teachers gt ON gt.group_id=g.id AND gt.user_id=$2
		JOIN curriculum_concepts c ON c.id=g.source_concept_id
		WHERE g.id=$1 AND g.kind='remedial' AND g.institution_id=$3`, groupID, teacherID, instID).Scan(&conceptID, &title, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		middleware.NotFound(w, "group")
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	rows, err := h.db.Query(ctx, `SELECT u.id::text, COALESCE(NULLIF(u.display_name,''), u.full_name, ''),
		count(*) FILTER (WHERE le.occurred_at <  $3 AND le.is_correct),
		count(*) FILTER (WHERE le.occurred_at <  $3 AND NOT le.is_correct),
		count(*) FILTER (WHERE le.occurred_at >= $3 AND le.is_correct),
		count(*) FILTER (WHERE le.occurred_at >= $3 AND NOT le.is_correct)
		FROM group_students gs JOIN users u ON u.id=gs.user_id
		LEFT JOIN learning_evidence le ON le.user_id=u.id AND le.concept_id=$2 AND le.institution_id=$4 AND NOT le.timed_out
		WHERE gs.group_id=$1
		GROUP BY u.id, u.display_name, u.full_name ORDER BY 2`, groupID, conceptID, created, instID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type student struct {
		ID     string        `json:"student_id"`
		Name   string        `json:"name"`
		Before evidenceCount `json:"before"`
		After  evidenceCount `json:"after"`
	}
	out := []student{}
	for rows.Next() {
		var s student
		if err := rows.Scan(&s.ID, &s.Name, &s.Before.Correct, &s.Before.Errors, &s.After.Correct, &s.After.Errors); err != nil {
			middleware.InternalError(w)
			return
		}
		out = append(out, s)
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"concept_id": conceptID, "concept_title": title, "created_at": created, "students": out})
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
