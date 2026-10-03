package institution

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

// staleAfterDays is when an open item counts as "older than a week".
const staleAfterDays = 7

// actionItemsSQL is every open item waiting on the institution, one row per
// item: type, id, title, subtitle, waiting-since, a destination id for the
// client to link, and a fixed owner label when someone outside the institution
// holds it.
//
// $1 institution. Each branch is an ordinary query over its own table; the
// union is sorted and paged by the caller.
const actionItemsSQL = `
SELECT 'teacher_verification' AS item_type, u.id::text AS item_id, COALESCE(NULLIF(u.display_name,''), u.full_name) AS title,
       'Joined ' || to_char(u.created_at, 'DD Mon') || ' · ' || u.email AS subtitle, u.created_at AS waiting_since,
       u.id::text AS link_id, NULL::text AS fixed_owner
  FROM users u
 WHERE u.institution_id=$1 AND u.role='teacher' AND u.status='pending' AND u.deleted_at IS NULL
UNION ALL
SELECT 'edit_request', sr.id::text,
       initcap(replace(sr.field,'_',' ')) || ' correction — ' || COALESCE(NULLIF(su.display_name,''), e.full_name),
       COALESCE(sr.current_value,'not set') || ' → ' || sr.proposed_value, sr.created_at, sr.id::text, NULL
  FROM student_edit_requests sr JOIN enrollments e ON e.id=sr.enrollment_id LEFT JOIN users su ON su.id=e.user_id
 WHERE e.institution_id=$1 AND sr.status='pending'
UNION ALL
SELECT 'unclaimed_records', COALESCE(e.grade,'') || '|' || COALESCE(e.section,''),
       CASE WHEN e.grade IS NULL AND e.section IS NULL THEN 'No grade or section'
            ELSE concat_ws(' · ', 'Grade ' || e.grade, 'Section ' || e.section) END,
       COUNT(*) || ' roster records still unclaimed', MIN(e.created_at),
       COALESCE(e.grade,'') || '|' || COALESCE(e.section,''), NULL
  FROM enrollments e
 WHERE e.institution_id=$1 AND e.status='pending_claim'
 GROUP BY e.grade, e.section
UNION ALL
SELECT 'topic_request', tr.id::text, '“' || tr.topic || '”',
       'Topic request' || COALESCE(' · ' || tr.subject, ''), tr.created_at, tr.id::text, NULL
  FROM topic_requests tr
 WHERE tr.institution_id=$1 AND tr.status IN ('pending','in_progress') AND tr.created_at < now() - INTERVAL '5 days'`

// GET /institution/action-centre?filter=all|mine|unowned|stale&page=&limit=
func (h *Handler) ActionCentre(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	me := middleware.GetUserID(r)
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}

	filters := map[string]string{
		"":        "TRUE",
		"all":     "TRUE",
		"mine":    "owner_id = $2::uuid",
		"unowned": "owner_id IS NULL AND fixed_owner IS NULL",
		"stale":   fmt.Sprintf("waiting_since < now() - INTERVAL '%d days'", staleAfterDays),
	}
	cond, ok := filters[q.Get("filter")]
	if !ok {
		middleware.BadRequest(w, "filter must be all, mine, unowned or stale")
		return
	}

	// One flattened set of open items with their owners. $2 (the caller) is
	// always referenced, so every query below binds the same parameters.
	items := `WITH i AS (` + actionItemsSQL + `),
	  a AS (SELECT i.*, o.owner_id, COALESCE(NULLIF(ou.display_name,''), ou.full_name) AS owner_name
	          FROM i
	          LEFT JOIN action_item_owners o ON o.institution_id=$1 AND o.item_type=i.item_type AND o.item_id=i.item_id
	          LEFT JOIN users ou ON ou.id=o.owner_id
	         WHERE $2::uuid IS NOT NULL)`

	// Counts per filter, so the pills are exact across pages.
	var all, mine, unowned, stale int
	if err := h.db.QueryRow(r.Context(), items+` SELECT COUNT(*),
		COUNT(*) FILTER (WHERE `+filters["mine"]+`),
		COUNT(*) FILTER (WHERE `+filters["unowned"]+`),
		COUNT(*) FILTER (WHERE `+filters["stale"]+`) FROM a`, instID, me).
		Scan(&all, &mine, &unowned, &stale); err != nil {
		middleware.InternalError(w)
		return
	}

	rows, err := h.db.Query(r.Context(), items+`
		SELECT item_type, item_id, title, subtitle, waiting_since, link_id, fixed_owner, owner_id, owner_name
		  FROM a WHERE `+cond+`
		 ORDER BY waiting_since ASC, item_type, item_id LIMIT $3 OFFSET $4`,
		instID, me, limit, (page-1)*limit)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type item struct {
		Type         string    `json:"type"`
		ID           string    `json:"id"`
		Title        string    `json:"title"`
		Subtitle     string    `json:"subtitle"`
		WaitingSince time.Time `json:"waiting_since"`
		LinkID       string    `json:"link_id"`
		OwnerID      *string   `json:"owner_id"`
		OwnerName    *string   `json:"owner_name"`
		// Assignable is false when the next step is someone else's (the student).
		Assignable bool `json:"assignable"`
	}
	list := []item{}
	for rows.Next() {
		var it item
		var fixed *string
		if err := rows.Scan(&it.Type, &it.ID, &it.Title, &it.Subtitle, &it.WaitingSince, &it.LinkID, &fixed,
			&it.OwnerID, &it.OwnerName); err != nil {
			middleware.InternalError(w)
			return
		}
		it.Assignable = fixed == nil
		if fixed != nil {
			it.OwnerName = fixed
		}
		list = append(list, it)
	}

	// Per-queue totals and the oldest item in each, for the summary cards.
	type queue struct {
		Type   string     `json:"type"`
		Count  int        `json:"count"`
		Oldest *time.Time `json:"oldest"`
	}
	queues := []queue{}
	qrows, err := h.db.Query(r.Context(), `WITH i AS (`+actionItemsSQL+`)
		SELECT t.type, COUNT(i.item_id), MIN(i.waiting_since)
		  FROM unnest(ARRAY['teacher_verification','edit_request','unclaimed_records','topic_request']) t(type)
		  LEFT JOIN i ON i.item_type=t.type
		 GROUP BY t.type ORDER BY array_position(ARRAY['teacher_verification','edit_request','unclaimed_records','topic_request'], t.type)`,
		instID)
	if err == nil {
		defer qrows.Close()
		for qrows.Next() {
			var qq queue
			qrows.Scan(&qq.Type, &qq.Count, &qq.Oldest)
			queues = append(queues, qq)
		}
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"items":  list,
		"queues": queues,
		"counts": map[string]int{"all": all, "mine": mine, "unowned": unowned, "stale": stale},
		"page":   page, "limit": limit,
		"stale_after_days": staleAfterDays,
		"updated_at":       time.Now().UTC(),
	})
}

// PUT /institution/action-centre/owner {item_type, item_id, owner_id|null}
// Assigns an item to someone at the institution, or clears it with null.
func (h *Handler) SetActionOwner(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	var req struct {
		ItemType string  `json:"item_type"`
		ItemID   string  `json:"item_id"`
		OwnerID  *string `json:"owner_id"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || req.ItemType == "" || req.ItemID == "" {
		middleware.BadRequest(w, "item_type and item_id are required")
		return
	}
	// The item must be one of this institution's open items; this also
	// validates item_type.
	var exists bool
	if err := h.db.QueryRow(r.Context(), `WITH i AS (`+actionItemsSQL+`)
		SELECT EXISTS(SELECT 1 FROM i WHERE i.item_type=$2 AND i.item_id=$3 AND i.fixed_owner IS NULL)`,
		instID, req.ItemType, req.ItemID).Scan(&exists); err != nil || !exists {
		middleware.NotFound(w, "open action item")
		return
	}
	if req.OwnerID == nil {
		h.db.Exec(r.Context(), `DELETE FROM action_item_owners WHERE institution_id=$1 AND item_type=$2 AND item_id=$3`,
			instID, req.ItemType, req.ItemID)
		middleware.JSON(w, http.StatusOK, map[string]interface{}{"owner_id": nil})
		return
	}
	// Owners are this institution's admins and teachers.
	var ok bool
	h.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND institution_id=$2
		AND role IN ('institution_admin','teacher') AND deleted_at IS NULL)`, *req.OwnerID, instID).Scan(&ok)
	if !ok {
		middleware.BadRequest(w, "the owner must be an admin or teacher at your institution")
		return
	}
	if _, err := h.db.Exec(r.Context(), `INSERT INTO action_item_owners(institution_id,item_type,item_id,owner_id)
		VALUES($1,$2,$3,$4) ON CONFLICT (institution_id,item_type,item_id)
		DO UPDATE SET owner_id=EXCLUDED.owner_id, assigned_at=now()`, instID, req.ItemType, req.ItemID, *req.OwnerID); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]interface{}{"owner_id": *req.OwnerID})
}
