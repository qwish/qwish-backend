package activity

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	qdb "github.com/qwish/backend/internal/db"
	"github.com/qwish/backend/internal/domain/leadership"
	"github.com/qwish/backend/internal/domain/notification"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

type actor struct {
	id, inst string
	admin    bool
}

func who(r *http.Request) actor {
	return actor{id: middleware.GetUserID(r), inst: middleware.GetInstitutionID(r), admin: middleware.GetRole(r) == "institution_admin"}
}

// activityRow is an activity as organisers see it.
type activityRow struct {
	ID               string          `json:"id"`
	Kind             string          `json:"kind"`
	Title            string          `json:"title"`
	Description      string          `json:"description"`
	Status           string          `json:"status"`
	State            string          `json:"state"`
	CreatedBy        string          `json:"created_by"`
	CreatorName      string          `json:"created_by_name"`
	DraftQuestions   json.RawMessage `json:"draft_questions"`
	Questions        json.RawMessage `json:"questions"`
	VersionID        *string         `json:"version_id"`
	InstitutionWide  bool            `json:"institution_wide"`
	GroupIDs         []string        `json:"group_ids"`
	OpensAt          *time.Time      `json:"opens_at"`
	ClosesAt         *time.Time      `json:"closes_at"`
	AllowEdit        bool            `json:"allow_edit"`
	ResultVisibility string          `json:"result_visibility"`
	ReachEstimate    *int            `json:"reach_estimate"`
	ReachEstimatedAt *time.Time      `json:"reach_estimated_at"`
	RemindedAt       *time.Time      `json:"reminded_at"`
	Revision         int             `json:"revision"`
	PublishedAt      *time.Time      `json:"published_at"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	ResponseCount    int             `json:"response_count"`
}

const organiserCols = `a.id, a.kind, a.title, a.description, a.status, ` + stateSQL + `, a.created_by,
	COALESCE(NULLIF(u.display_name,''),u.full_name,''), a.draft_questions, v.questions, a.current_version_id,
	a.institution_wide, COALESCE((SELECT array_agg(group_id::text ORDER BY group_id) FROM activity_audience_groups WHERE activity_id=a.id),'{}'),
	a.opens_at, a.closes_at, a.allow_edit, a.result_visibility, a.reach_estimate, a.reach_estimated_at, a.reminded_at,
	a.revision, a.published_at, a.created_at, a.updated_at,
	(SELECT count(*) FROM activity_responses x WHERE x.activity_id=a.id AND x.status='submitted')
	FROM activities a JOIN users u ON u.id=a.created_by LEFT JOIN activity_versions v ON v.id=a.current_version_id`

// organiserScope: $1 institution, $2 caller, $3 caller is an institution admin.
const organiserScope = `a.institution_id=$1 AND (a.created_by=$2 OR $3)`

func scanActivity(row pgx.Row) (activityRow, error) {
	var a activityRow
	var versionQ []byte
	err := row.Scan(&a.ID, &a.Kind, &a.Title, &a.Description, &a.Status, &a.State, &a.CreatedBy, &a.CreatorName,
		&a.DraftQuestions, &versionQ, &a.VersionID, &a.InstitutionWide, &a.GroupIDs, &a.OpensAt, &a.ClosesAt,
		&a.AllowEdit, &a.ResultVisibility, &a.ReachEstimate, &a.ReachEstimatedAt, &a.RemindedAt, &a.Revision,
		&a.PublishedAt, &a.CreatedAt, &a.UpdatedAt, &a.ResponseCount)
	if versionQ != nil {
		a.Questions = versionQ
	} else {
		a.Questions = json.RawMessage("null")
	}
	return a, err
}

func (h *Handler) load(ctx context.Context, q pgx.Tx, me actor, id string) (activityRow, error) {
	sql := `SELECT ` + organiserCols + ` WHERE ` + organiserScope + ` AND a.id::text=$4`
	if q != nil {
		return scanActivity(q.QueryRow(ctx, sql+` FOR UPDATE OF a`, me.inst, me.id, me.admin, id))
	}
	return scanActivity(h.db.QueryRow(ctx, sql, me.inst, me.id, me.admin, id))
}

// ─── Reads ───────────────────────────────────────────────────────────────────

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	me := who(r)
	status := r.URL.Query().Get("status")
	kind := r.URL.Query().Get("kind")
	rows, err := h.db.Query(r.Context(), `SELECT `+organiserCols+` WHERE `+organiserScope+`
		AND ($4='' OR a.status=$4) AND ($5='' OR a.kind=$5) ORDER BY a.updated_at DESC LIMIT 200`,
		me.inst, me.id, me.admin, status, kind)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	out := []activityRow{}
	for rows.Next() {
		a, err := scanActivity(rows)
		if err != nil {
			middleware.InternalError(w)
			return
		}
		out = append(out, a)
	}
	if rows.Err() != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	a, err := h.load(r.Context(), nil, who(r), chi.URLParam(r, "id"))
	if err != nil {
		notFound(w)
		return
	}
	out := map[string]any{"activity": a, "identity_disclosure": IdentityDisclosure}
	if a.VersionID != nil {
		qs, _ := parseQuestions(a.Questions)
		res, err := h.results(r.Context(), a.ID, qs)
		if err != nil {
			middleware.InternalError(w)
			return
		}
		out["results"] = res
	}
	middleware.JSON(w, http.StatusOK, out)
}

func (h *Handler) AudienceEstimate(w http.ResponseWriter, r *http.Request) {
	me := who(r)
	a, err := h.load(r.Context(), nil, me, chi.URLParam(r, "id"))
	if err != nil {
		notFound(w)
		return
	}
	var n int
	if err := h.db.QueryRow(r.Context(), audienceCountSQL, a.ID, me.inst).Scan(&n); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"students": n, "as_of": time.Now().UTC()})
}

// ─── Authoring ───────────────────────────────────────────────────────────────

type draftInput struct {
	Revision         int        `json:"revision"`
	Kind             string     `json:"kind"`
	Title            string     `json:"title"`
	Description      string     `json:"description"`
	Questions        []Question `json:"questions"`
	GroupIDs         []string   `json:"group_ids"`
	InstitutionWide  bool       `json:"institution_wide"`
	OpensAt          *time.Time `json:"opens_at"`
	ClosesAt         *time.Time `json:"closes_at"`
	AllowEdit        bool       `json:"allow_edit"`
	ResultVisibility string     `json:"result_visibility"`
}

func (in *draftInput) normalise() error {
	in.Title = strings.TrimSpace(in.Title)
	if in.Kind != "form" && in.Kind != "poll" {
		return fmt.Errorf("kind must be form or poll")
	}
	if in.Title == "" || len([]rune(in.Title)) > 200 || len([]rune(in.Description)) > 4000 {
		return fmt.Errorf("title (1–200 characters) is required and description is at most 4000")
	}
	if in.Questions == nil {
		in.Questions = []Question{}
	}
	if len(in.Questions) > 0 {
		if err := ValidateQuestions(in.Kind, in.Questions); err != nil {
			return err
		}
	}
	in.GroupIDs = unique(in.GroupIDs)
	if len(in.GroupIDs) > 100 {
		return fmt.Errorf("at most 100 classes")
	}
	if in.ResultVisibility == "" {
		in.ResultVisibility = "after_close"
	}
	if in.ResultVisibility != "after_vote" && in.ResultVisibility != "after_close" && in.ResultVisibility != "organisers" {
		return fmt.Errorf("result_visibility must be after_vote, after_close or organisers")
	}
	if in.OpensAt != nil && in.ClosesAt != nil && !in.ClosesAt.After(*in.OpensAt) {
		return fmt.Errorf("closes_at must be after opens_at")
	}
	return nil
}

func unique(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// checkAudience enforces publication authority: teachers target classes they
// teach; an HOD also targets their department's classes; institution admins,
// Directors and Principals may publish institution-wide.
func checkAudience(ctx context.Context, tx pgx.Tx, me actor, groupIDs []string, wide bool) error {
	grants, err := leadership.Load(ctx, tx, me.id, me.inst)
	if err != nil {
		return err
	}
	pubAll, pubDepts := grants.Scope(leadership.PermActivitiesPublish)
	if pubDepts == nil {
		pubDepts = []string{}
	}
	if wide && !me.admin && !pubAll {
		return fmt.Errorf("only institution admins, Directors and Principals can publish to the whole institution")
	}
	if len(groupIDs) == 0 {
		return nil
	}
	var n int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM groups g WHERE g.id::text = ANY($1) AND g.institution_id=$2 AND g.archived_at IS NULL
		AND ($3 OR g.department_id::text = ANY($5) OR EXISTS(SELECT 1 FROM group_teachers gt WHERE gt.group_id=g.id AND gt.user_id=$4))`,
		groupIDs, me.inst, me.admin || pubAll, me.id, pubDepts).Scan(&n)
	if err != nil {
		return err
	}
	if n != len(groupIDs) {
		return fmt.Errorf("every class must be an active class you are allowed to publish to")
	}
	return nil
}

func setAudience(ctx context.Context, tx pgx.Tx, id string, groupIDs []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM activity_audience_groups WHERE activity_id=$1`, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO activity_audience_groups(activity_id,group_id) SELECT $1, unnest($2::uuid[])`, id, groupIDs)
	return err
}

func decodeStrict(body []byte, dst any) error {
	d := jsonx.NewDecoder(strings.NewReader(string(body)))
	d.DisallowUnknownFields()
	return d.Decode(dst)
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil {
		middleware.BadRequest(w, "request body is too large or unreadable")
		return nil, false
	}
	return body, true
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	me := who(r)
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var in draftInput
	if err := decodeStrict(body, &in); err != nil {
		middleware.BadRequest(w, "invalid activity body")
		return
	}
	if err := in.normalise(); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(r.Context())
	if err := checkAudience(r.Context(), tx, me, in.GroupIDs, in.InstitutionWide); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	var id string
	err = tx.QueryRow(r.Context(), `INSERT INTO activities(institution_id,kind,created_by,title,description,draft_questions,institution_wide,opens_at,closes_at,allow_edit,result_visibility)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
		me.inst, in.Kind, me.id, in.Title, in.Description, in.Questions, in.InstitutionWide, in.OpensAt, in.ClosesAt, in.AllowEdit, in.ResultVisibility).Scan(&id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if setAudience(r.Context(), tx, id, in.GroupIDs) != nil || audit(r.Context(), tx, id, me.id, "create", nil) != nil || tx.Commit(r.Context()) != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]any{"id": id, "revision": 1})
}

// Update replaces a draft. Once published, the schema and privacy settings are
// frozen: only title and description may be corrected, with an audit record.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	me := who(r)
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(r.Context())
	a, err := h.load(r.Context(), tx, me, chi.URLParam(r, "id"))
	if err != nil {
		notFound(w)
		return
	}
	if a.Status == "archived" {
		middleware.Error(w, http.StatusConflict, "CONFLICT", "archived activities cannot be edited")
		return
	}
	if a.Status != "draft" {
		var in struct {
			Revision    int    `json:"revision"`
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		if decodeStrict(body, &in) != nil {
			middleware.Error(w, http.StatusConflict, "CONFLICT", "published activities only accept title and description; duplicate it to change questions or settings")
			return
		}
		in.Title = strings.TrimSpace(in.Title)
		if in.Title == "" || len([]rune(in.Title)) > 200 || len([]rune(in.Description)) > 4000 {
			middleware.BadRequest(w, "title (1–200 characters) is required and description is at most 4000")
			return
		}
		if in.Revision != a.Revision {
			middleware.Error(w, http.StatusConflict, "STALE_REVISION", "this activity changed since you loaded it")
			return
		}
		_, err = tx.Exec(r.Context(), `UPDATE activities SET title=$2, description=$3, revision=revision+1, updated_at=now() WHERE id=$1`, a.ID, in.Title, in.Description)
		if err != nil || audit(r.Context(), tx, a.ID, me.id, "descriptive_edit", map[string]any{
			"title_before": a.Title, "title_after": in.Title, "description_changed": a.Description != in.Description,
		}) != nil || tx.Commit(r.Context()) != nil {
			middleware.InternalError(w)
			return
		}
		middleware.JSON(w, http.StatusOK, map[string]any{"id": a.ID, "revision": a.Revision + 1})
		return
	}
	var in draftInput
	if err := decodeStrict(body, &in); err != nil {
		middleware.BadRequest(w, "invalid activity body")
		return
	}
	if err := in.normalise(); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	if in.Revision != a.Revision {
		middleware.Error(w, http.StatusConflict, "STALE_REVISION", "this activity changed since you loaded it")
		return
	}
	if err := checkAudience(r.Context(), tx, me, in.GroupIDs, in.InstitutionWide); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE activities SET kind=$2,title=$3,description=$4,draft_questions=$5,institution_wide=$6,opens_at=$7,closes_at=$8,
		allow_edit=$9,result_visibility=$10,revision=revision+1,updated_at=now() WHERE id=$1`,
		a.ID, in.Kind, in.Title, in.Description, in.Questions, in.InstitutionWide, in.OpensAt, in.ClosesAt, in.AllowEdit, in.ResultVisibility)
	if err != nil || setAudience(r.Context(), tx, a.ID, in.GroupIDs) != nil || audit(r.Context(), tx, a.ID, me.id, "update", nil) != nil || tx.Commit(r.Context()) != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"id": a.ID, "revision": a.Revision + 1})
}

// Publish freezes the draft into a version and opens it (or schedules it, if
// opens_at is in the future). Retrying a publish that already succeeded returns
// the published activity rather than an error.
func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	me := who(r)
	var in struct {
		Revision int `json:"revision"`
	}
	d := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil {
		middleware.BadRequest(w, "revision is required")
		return
	}
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(r.Context())
	a, err := h.load(r.Context(), tx, me, chi.URLParam(r, "id"))
	if err != nil {
		notFound(w)
		return
	}
	if a.Status != "draft" {
		if a.Status == "published" && in.Revision == a.Revision-1 {
			middleware.JSON(w, http.StatusOK, map[string]any{"id": a.ID, "revision": a.Revision, "reach_estimate": a.ReachEstimate})
			return
		}
		middleware.Error(w, http.StatusConflict, "CONFLICT", "only drafts can be published")
		return
	}
	if in.Revision != a.Revision {
		middleware.Error(w, http.StatusConflict, "STALE_REVISION", "this activity changed since you loaded it")
		return
	}
	qs, err := parseQuestions(a.DraftQuestions)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if err := ValidateQuestions(a.Kind, qs); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	if !a.InstitutionWide && len(a.GroupIDs) == 0 {
		middleware.BadRequest(w, "choose at least one class before publishing")
		return
	}
	// Recheck authority: class assignments may have changed since the draft was saved.
	if err := checkAudience(r.Context(), tx, me, a.GroupIDs, a.InstitutionWide); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	if a.ClosesAt != nil && !a.ClosesAt.After(time.Now()) {
		middleware.BadRequest(w, "closes_at is in the past")
		return
	}
	var versionID string
	var reach int
	err = tx.QueryRow(r.Context(), `INSERT INTO activity_versions(activity_id,version,questions)
		SELECT $1, COALESCE(MAX(version),0)+1, $2 FROM activity_versions WHERE activity_id=$1 RETURNING id`, a.ID, qs).Scan(&versionID)
	if err == nil {
		err = tx.QueryRow(r.Context(), audienceCountSQL, a.ID, me.inst).Scan(&reach)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE activities SET status='published', current_version_id=$2, published_at=now(),
			reach_estimate=$3, reach_estimated_at=now(), revision=revision+1, updated_at=now() WHERE id=$1`, a.ID, versionID, reach)
	}
	if err != nil || audit(r.Context(), tx, a.ID, me.id, "publish", map[string]any{"version_id": versionID, "reach_estimate": reach}) != nil || tx.Commit(r.Context()) != nil {
		middleware.InternalError(w)
		return
	}
	h.notify(a.ID, me.inst, a.Kind, "new", false)
	middleware.JSON(w, http.StatusOK, map[string]any{"id": a.ID, "revision": a.Revision + 1, "reach_estimate": reach})
}

// notify fans an in-app (and push) notification out to the current audience.
// The reference dedups retries. Payloads stay generic: no titles or answers.
//
// ponytail: fan-out runs after commit, in-process; a crash mid-way leaves some
// students un-notified, but the activity is already in their feed. Move to a
// durable outbox when the notification service gets one.
func (h *Handler) notify(activityID, inst, kind, event string, onlyNonResponders bool) {
	if h.notif == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		rows, err := h.db.Query(ctx, `SELECT u.id FROM users u, activities a
			WHERE a.id=$1 AND `+qdb.LiveMemberSQL("u.id", "$2")+` AND u.role='student' AND u.deleted_at IS NULL
			AND (a.institution_wide OR EXISTS(SELECT 1 FROM activity_audience_groups ag
				JOIN groups g ON g.id=ag.group_id AND g.archived_at IS NULL
				JOIN group_students gs ON gs.group_id=ag.group_id WHERE ag.activity_id=a.id AND gs.user_id=u.id))
			AND (NOT $3 OR NOT EXISTS(SELECT 1 FROM activity_responses x WHERE x.activity_id=a.id AND x.student_id=u.id AND x.status='submitted'))
			AND COALESCE((SELECT np.push_assignments FROM notification_preferences np WHERE np.user_id=u.id), true)`,
			activityID, inst, onlyNonResponders)
		if err != nil {
			return
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		title := map[string]string{"form": "New form from your teacher", "poll": "New poll from your teacher"}[kind]
		body := "Open Qwish to respond."
		if event == "reminder" {
			title, body = "Reminder: response needed", "A form or poll is still waiting for your response."
		}
		for _, id := range ids {
			h.notif.Emit(ctx, id, "activity", title, body,
				notification.WithIcon("activity"), notification.WithColor("indigo"),
				notification.WithReference("activity:"+activityID+":"+event))
		}
	}()
}

func (h *Handler) Close(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, "close", `status='closed'`, `a.status IN ('published','closed')`)
}

func (h *Handler) Archive(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, "archive", `status='archived'`, `true`)
}

// transition is idempotent: repeating it on an activity already in the target
// status succeeds.
func (h *Handler) transition(w http.ResponseWriter, r *http.Request, action, set, allowed string) {
	me := who(r)
	var id string
	err := h.db.QueryRow(r.Context(), `UPDATE activities a SET `+set+`, revision=revision+1, updated_at=now()
		WHERE `+organiserScope+` AND a.id::text=$4 AND `+allowed+` RETURNING a.id`, me.inst, me.id, me.admin, chi.URLParam(r, "id")).Scan(&id)
	if isNoRows(err) {
		middleware.Error(w, http.StatusConflict, "CONFLICT", "activity not found or cannot be "+action+"d now")
		return
	}
	if err != nil || audit(r.Context(), h.db, id, me.id, action, nil) != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"id": id})
}

// Duplicate copies an activity into a new draft: the route to change a
// published schema. Dates and audience authority are rechecked on publish.
func (h *Handler) Duplicate(w http.ResponseWriter, r *http.Request) {
	me := who(r)
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(r.Context())
	a, err := h.load(r.Context(), tx, me, chi.URLParam(r, "id"))
	if err != nil {
		notFound(w)
		return
	}
	qs := a.DraftQuestions
	if a.VersionID != nil {
		qs = a.Questions
	}
	title := []rune("Copy of " + a.Title)
	if len(title) > 200 {
		title = title[:200]
	}
	var id string
	err = tx.QueryRow(r.Context(), `INSERT INTO activities(institution_id,kind,created_by,title,description,draft_questions,institution_wide,allow_edit,result_visibility)
		VALUES ($1,$2,$3,$4,$5,$6,$7 AND $8,$9,$10) RETURNING id`,
		me.inst, a.Kind, me.id, string(title), a.Description, qs, a.InstitutionWide, me.admin, a.AllowEdit, a.ResultVisibility).Scan(&id)
	if err == nil {
		// Keep only classes the duplicating organiser may publish to.
		_, err = tx.Exec(r.Context(), `INSERT INTO activity_audience_groups(activity_id,group_id)
			SELECT $1, ag.group_id FROM activity_audience_groups ag JOIN groups g ON g.id=ag.group_id AND g.archived_at IS NULL
			WHERE ag.activity_id=$2 AND ($3 OR EXISTS(SELECT 1 FROM group_teachers gt WHERE gt.group_id=g.id AND gt.user_id=$4))`,
			id, a.ID, me.admin, me.id)
	}
	if err != nil || audit(r.Context(), tx, id, me.id, "duplicate", map[string]any{"from": a.ID}) != nil || tx.Commit(r.Context()) != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]any{"id": id, "revision": 1})
}

// Remind notifies current non-responders once per activity.
func (h *Handler) Remind(w http.ResponseWriter, r *http.Request) {
	me := who(r)
	var id, kind string
	err := h.db.QueryRow(r.Context(), `UPDATE activities a SET reminded_at=now(), updated_at=now()
		WHERE `+organiserScope+` AND a.id::text=$4 AND a.reminded_at IS NULL AND (`+stateSQL+`)='open'
		RETURNING a.id, a.kind`, me.inst, me.id, me.admin, chi.URLParam(r, "id")).Scan(&id, &kind)
	if isNoRows(err) {
		middleware.Error(w, http.StatusConflict, "CONFLICT", "a reminder was already sent, or the activity is not open")
		return
	}
	if err != nil || audit(r.Context(), h.db, id, me.id, "remind", nil) != nil {
		middleware.InternalError(w)
		return
	}
	h.notify(id, me.inst, kind, "reminder", true)
	middleware.JSON(w, http.StatusOK, map[string]string{"id": id})
}

// ─── Responses ───────────────────────────────────────────────────────────────

type responseRow struct {
	ID          string          `json:"id"`
	StudentID   string          `json:"student_id"`
	Respondent  json.RawMessage `json:"respondent"`
	Answers     json.RawMessage `json:"answers"`
	VersionID   string          `json:"version_id"`
	SubmittedAt *time.Time      `json:"submitted_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// submittedResponses never includes drafts or withdrawn responses.
func (h *Handler) submittedResponses(ctx context.Context, activityID string, limit, offset int) ([]responseRow, error) {
	rows, err := h.db.Query(ctx, `SELECT id, student_id, respondent, answers, version_id, submitted_at, updated_at
		FROM activity_responses WHERE activity_id=$1 AND status='submitted'
		ORDER BY submitted_at, id LIMIT $2 OFFSET $3`, activityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []responseRow{}
	for rows.Next() {
		var v responseRow
		if err := rows.Scan(&v.ID, &v.StudentID, &v.Respondent, &v.Answers, &v.VersionID, &v.SubmittedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (h *Handler) Responses(w http.ResponseWriter, r *http.Request) {
	me := who(r)
	a, err := h.load(r.Context(), nil, me, chi.URLParam(r, "id"))
	if err != nil {
		notFound(w)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	list, err := h.submittedResponses(r.Context(), a.ID, limit, offset)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	var eligible int
	if err := h.db.QueryRow(r.Context(), audienceCountSQL, a.ID, me.inst).Scan(&eligible); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]any{
		"responses": list,
		"summary": map[string]any{
			"submitted": a.ResponseCount, "eligible_now": eligible, "eligible_as_of": time.Now().UTC(),
			"reach_estimate": a.ReachEstimate, "reach_estimated_at": a.ReachEstimatedAt,
		},
	})
}

// ExportCSV streams submitted responses. Access is checked on every request;
// no export file is cached, so a revoked organiser cannot fetch one later.
func (h *Handler) ExportCSV(w http.ResponseWriter, r *http.Request) {
	me := who(r)
	a, err := h.load(r.Context(), nil, me, chi.URLParam(r, "id"))
	if err != nil || a.VersionID == nil {
		notFound(w)
		return
	}
	qs, err := parseQuestions(a.Questions)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	// ponytail: whole export in memory, capped at 10k rows; page through
	// submittedResponses when an audience gets bigger than that.
	list, err := h.submittedResponses(r.Context(), a.ID, 10000, 0)
	if err != nil || audit(r.Context(), h.db, a.ID, me.id, "export", map[string]any{"rows": len(list)}) != nil {
		middleware.InternalError(w)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="responses.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	cw := csv.NewWriter(w)
	header := []string{"Name", "Roll number", "Classes", "Submitted at"}
	for _, q := range qs {
		header = append(header, csvCell(q.Label))
	}
	_ = cw.Write(header)
	for _, resp := range list {
		_ = cw.Write(exportRow(qs, resp))
	}
	cw.Flush()
}

func exportRow(qs []Question, resp responseRow) []string {
	var who struct {
		Name    string   `json:"name"`
		Roll    string   `json:"roll_number"`
		Classes []string `json:"classes"`
	}
	_ = json.Unmarshal(resp.Respondent, &who)
	var answers map[string]json.RawMessage
	_ = json.Unmarshal(resp.Answers, &answers)
	submitted := ""
	if resp.SubmittedAt != nil {
		submitted = resp.SubmittedAt.UTC().Format(time.RFC3339)
	}
	row := []string{csvCell(who.Name), csvCell(who.Roll), csvCell(strings.Join(who.Classes, "; ")), submitted}
	for _, q := range qs {
		row = append(row, csvCell(answerText(q, answers[q.ID])))
	}
	return row
}

// answerText renders one answer for export, mapping option ids to labels.
func answerText(q Question, raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	label := func(id string) string {
		for _, o := range q.Options {
			if o.ID == id {
				return o.Label
			}
		}
		return id
	}
	switch q.Type {
	case "single_choice":
		var s string
		_ = json.Unmarshal(raw, &s)
		return label(s)
	case "multiple_choice":
		var picks []string
		_ = json.Unmarshal(raw, &picks)
		for i := range picks {
			picks[i] = label(picks[i])
		}
		return strings.Join(picks, "; ")
	case "acknowledgement":
		var b bool
		_ = json.Unmarshal(raw, &b)
		if b {
			return "Yes"
		}
		return "No"
	default:
		var s any
		_ = json.Unmarshal(raw, &s)
		return fmt.Sprint(s)
	}
}
