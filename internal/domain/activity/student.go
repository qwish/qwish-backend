package activity

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

type feedItem struct {
	ID            string     `json:"id"`
	Kind          string     `json:"kind"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	State         string     `json:"state"`
	OrganiserName string     `json:"organiser_name"`
	OpensAt       *time.Time `json:"opens_at"`
	ClosesAt      *time.Time `json:"closes_at"`
	MyStatus      *string    `json:"my_status"`
	SubmittedAt   *time.Time `json:"submitted_at"`
}

// Feed lists activities for the student: everything currently addressed to
// them, plus anything they already responded to. filter: forms|polls|mine.
func (h *Handler) Feed(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("filter")
	rows, err := h.db.Query(r.Context(), `SELECT a.id, a.kind, a.title, a.description, `+stateSQL+`,
		COALESCE(NULLIF(u.display_name,''),u.full_name,''), a.opens_at, a.closes_at, x.status, x.submitted_at
		FROM activities a JOIN users u ON u.id=a.created_by
		LEFT JOIN activity_responses x ON x.activity_id=a.id AND x.student_id=$2
		WHERE ((a.status IN ('published','closed') AND `+eligibleSQL+`) OR (a.status<>'draft' AND x.status='submitted'))
		AND ($1='' OR ($1='forms' AND a.kind='form') OR ($1='polls' AND a.kind='poll') OR ($1='mine' AND x.status IN ('draft','submitted')))
		ORDER BY (`+stateSQL+`)='open' DESC, a.closes_at NULLS LAST, a.published_at DESC LIMIT 100`,
		filter, middleware.GetUserID(r), middleware.GetInstitutionID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	out := []feedItem{}
	for rows.Next() {
		var v feedItem
		if rows.Scan(&v.ID, &v.Kind, &v.Title, &v.Description, &v.State, &v.OrganiserName, &v.OpensAt, &v.ClosesAt, &v.MyStatus, &v.SubmittedAt) != nil {
			middleware.InternalError(w)
			return
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}

type studentActivity struct {
	ID               string          `json:"id"`
	Kind             string          `json:"kind"`
	Title            string          `json:"title"`
	Description      string          `json:"description"`
	State            string          `json:"state"`
	OrganiserName    string          `json:"organiser_name"`
	OpensAt          *time.Time      `json:"opens_at"`
	ClosesAt         *time.Time      `json:"closes_at"`
	AllowEdit        bool            `json:"allow_edit"`
	ResultVisibility string          `json:"result_visibility"`
	VersionID        string          `json:"version_id"`
	Questions        json.RawMessage `json:"questions"`
	Eligible         bool            `json:"eligible"`
	status           string
}

type myResponse struct {
	ID          string          `json:"id"`
	Status      string          `json:"status"`
	Answers     json.RawMessage `json:"answers"`
	SubmittedAt *time.Time      `json:"submitted_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// loadForStudent returns the activity if the student may see it: in its
// current audience, or holding their own submitted response. With a tx, the
// activity row is share-locked so a concurrent close serialises with the write.
func loadForStudent(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id, studentID, inst string, lock bool) (studentActivity, *myResponse, error) {
	var a studentActivity
	sql := `SELECT a.id, a.kind, a.title, a.description, ` + stateSQL + `, COALESCE(NULLIF(u.display_name,''),u.full_name,''),
		a.opens_at, a.closes_at, a.allow_edit, a.result_visibility, v.id, v.questions, ` + eligibleSQL + `, a.status
		FROM activities a JOIN users u ON u.id=a.created_by JOIN activity_versions v ON v.id=a.current_version_id
		WHERE a.id::text=$1 AND a.status<>'draft'`
	if lock {
		sql += ` FOR SHARE OF a`
	}
	err := q.QueryRow(ctx, sql, id, studentID, inst).Scan(&a.ID, &a.Kind, &a.Title, &a.Description, &a.State, &a.OrganiserName,
		&a.OpensAt, &a.ClosesAt, &a.AllowEdit, &a.ResultVisibility, &a.VersionID, &a.Questions, &a.Eligible, &a.status)
	if err != nil {
		return a, nil, err
	}
	a.Eligible = a.Eligible && a.status != "archived"
	var mine myResponse
	err = q.QueryRow(ctx, `SELECT id, status, answers, submitted_at, updated_at FROM activity_responses WHERE activity_id=$1 AND student_id=$2`,
		a.ID, studentID).Scan(&mine.ID, &mine.Status, &mine.Answers, &mine.SubmittedAt, &mine.UpdatedAt)
	var resp *myResponse
	if err == nil {
		resp = &mine
	} else if !isNoRows(err) {
		return a, nil, err
	}
	if !a.Eligible && (resp == nil || resp.Status != "submitted") {
		return a, nil, pgx.ErrNoRows
	}
	return a, resp, nil
}

// resultsVisible applies the poll's result policy for a student.
func resultsVisible(a studentActivity, mine *myResponse) bool {
	if a.Kind != "poll" {
		return false
	}
	switch a.ResultVisibility {
	case "after_vote":
		return mine != nil && mine.Status == "submitted"
	case "after_close":
		return a.State == "closed" || a.State == "archived"
	}
	return false
}

func (h *Handler) StudentGet(w http.ResponseWriter, r *http.Request) {
	a, mine, err := loadForStudent(r.Context(), h.db, chi.URLParam(r, "id"), middleware.GetUserID(r), middleware.GetInstitutionID(r), false)
	if err != nil {
		if isNoRows(err) {
			notFound(w)
		} else {
			middleware.InternalError(w)
		}
		return
	}
	out := map[string]any{"activity": a, "my_response": mine, "identity_disclosure": IdentityDisclosure, "results": nil}
	if resultsVisible(a, mine) {
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

type answersInput struct {
	Answers map[string]json.RawMessage `json:"answers"`
}

// beginWrite opens a transaction, loads the activity for an eligible student
// while it is open, validates the answers and locks (creating if needed) the
// student's single response row.
func (h *Handler) beginWrite(w http.ResponseWriter, r *http.Request, submit bool) (pgx.Tx, studentActivity, *myResponse, string, bool) {
	var in answersInput
	d := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || in.Answers == nil {
		middleware.BadRequest(w, "answers is required")
		return nil, studentActivity{}, nil, "", false
	}
	studentID := middleware.GetUserID(r)
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return nil, studentActivity{}, nil, "", false
	}
	fail := func(f func()) (pgx.Tx, studentActivity, *myResponse, string, bool) {
		tx.Rollback(r.Context())
		f()
		return nil, studentActivity{}, nil, "", false
	}
	a, _, err := loadForStudent(r.Context(), tx, chi.URLParam(r, "id"), studentID, middleware.GetInstitutionID(r), true)
	if err != nil || !a.Eligible {
		return fail(func() {
			if err == nil || isNoRows(err) {
				notFound(w)
			} else {
				middleware.InternalError(w)
			}
		})
	}
	if a.State != "open" {
		return fail(func() {
			middleware.Error(w, http.StatusConflict, "NOT_OPEN", "this "+a.Kind+" is "+a.State)
		})
	}
	qs, err := parseQuestions(a.Questions)
	if err != nil {
		return fail(func() { middleware.InternalError(w) })
	}
	if err := ValidateAnswers(qs, in.Answers, submit); err != nil {
		return fail(func() { middleware.BadRequest(w, err.Error()) })
	}
	answers, _ := json.Marshal(in.Answers)
	// One row per student per activity: create it if missing, then lock it.
	_, err = tx.Exec(r.Context(), `INSERT INTO activity_responses(activity_id,version_id,student_id,status) VALUES ($1,$2,$3,'draft')
		ON CONFLICT (activity_id,student_id) DO NOTHING`, a.ID, a.VersionID, studentID)
	var mine myResponse
	var same bool
	if err == nil {
		err = tx.QueryRow(r.Context(), `SELECT id, status, answers, submitted_at, updated_at, answers=$3::jsonb
			FROM activity_responses WHERE activity_id=$1 AND student_id=$2 FOR UPDATE`, a.ID, studentID, string(answers)).
			Scan(&mine.ID, &mine.Status, &mine.Answers, &mine.SubmittedAt, &mine.UpdatedAt, &same)
	}
	if err != nil {
		return fail(func() { middleware.InternalError(w) })
	}
	if same {
		answers = nil // signals "identical to what is stored"
	}
	return tx, a, &mine, string(answers), true
}

func history(ctx context.Context, tx pgx.Tx, mine *myResponse) error {
	_, err := tx.Exec(ctx, `INSERT INTO activity_response_history(response_id,answers,status) VALUES ($1,$2,$3)`, mine.ID, mine.Answers, mine.Status)
	return err
}

// Submit records the student's response. Retrying an identical submission
// returns the existing receipt; changing a submitted response needs allow_edit.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	tx, a, mine, answers, ok := h.beginWrite(w, r, true)
	if !ok {
		return
	}
	defer tx.Rollback(r.Context())
	ctx := r.Context()
	if mine.Status == "submitted" {
		if answers == "" {
			middleware.JSON(w, http.StatusOK, mine)
			return
		}
		if !a.AllowEdit {
			middleware.Error(w, http.StatusConflict, "ALREADY_SUBMITTED", "you have already responded and this "+a.Kind+" does not allow changes")
			return
		}
		if history(ctx, tx, mine) != nil {
			middleware.InternalError(w)
			return
		}
	}
	if answers == "" {
		answers = string(mine.Answers)
	}
	inst := middleware.GetInstitutionID(r)
	err := tx.QueryRow(ctx, `UPDATE activity_responses x SET answers=$2::jsonb, status='submitted', version_id=$3,
		submitted_at=COALESCE(x.submitted_at, now()), updated_at=now(),
		respondent=(SELECT jsonb_build_object(
			'name', COALESCE(NULLIF(u.display_name,''),u.full_name,''),
			'roll_number', (SELECT e.roll_number FROM enrollments e WHERE e.user_id=u.id AND e.institution_id=$4 AND e.ended_at IS NULL LIMIT 1),
			'classes', COALESCE((SELECT jsonb_agg(g.name ORDER BY g.name) FROM group_students gs JOIN groups g ON g.id=gs.group_id
				WHERE gs.user_id=u.id AND g.institution_id=$4 AND g.archived_at IS NULL), '[]'::jsonb))
			FROM users u WHERE u.id=x.student_id)
		WHERE x.id=$1 RETURNING status, answers, submitted_at, updated_at`, mine.ID, answers, a.VersionID, inst).
		Scan(&mine.Status, &mine.Answers, &mine.SubmittedAt, &mine.UpdatedAt)
	if err != nil || tx.Commit(ctx) != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, mine)
}

// SaveDraft stores partial form answers. Drafts are never visible to organisers.
func (h *Handler) SaveDraft(w http.ResponseWriter, r *http.Request) {
	tx, a, mine, answers, ok := h.beginWrite(w, r, false)
	if !ok {
		return
	}
	defer tx.Rollback(r.Context())
	if a.Kind == "poll" {
		middleware.BadRequest(w, "polls are submitted directly")
		return
	}
	if mine.Status == "submitted" {
		middleware.Error(w, http.StatusConflict, "ALREADY_SUBMITTED", "this form is already submitted")
		return
	}
	if answers == "" {
		answers = string(mine.Answers)
	}
	err := tx.QueryRow(r.Context(), `UPDATE activity_responses SET answers=$2::jsonb, status='draft', version_id=$3, updated_at=now()
		WHERE id=$1 RETURNING status, answers, updated_at`, mine.ID, answers, a.VersionID).Scan(&mine.Status, &mine.Answers, &mine.UpdatedAt)
	if err != nil || tx.Commit(r.Context()) != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, mine)
}

// Withdraw retracts a submitted response while the activity is open, if allowed.
func (h *Handler) Withdraw(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	studentID := middleware.GetUserID(r)
	tx, err := h.db.Begin(ctx)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(ctx)
	a, _, err := loadForStudent(ctx, tx, chi.URLParam(r, "id"), studentID, middleware.GetInstitutionID(r), true)
	if err != nil || !a.Eligible {
		if err == nil || isNoRows(err) {
			notFound(w)
		} else {
			middleware.InternalError(w)
		}
		return
	}
	if !a.AllowEdit || a.State != "open" {
		middleware.Error(w, http.StatusConflict, "CONFLICT", "responses to this "+a.Kind+" cannot be withdrawn")
		return
	}
	var mine myResponse
	err = tx.QueryRow(ctx, `SELECT id, status, answers, submitted_at, updated_at FROM activity_responses
		WHERE activity_id=$1 AND student_id=$2 FOR UPDATE`, a.ID, studentID).Scan(&mine.ID, &mine.Status, &mine.Answers, &mine.SubmittedAt, &mine.UpdatedAt)
	if isNoRows(err) || (err == nil && mine.Status != "submitted") {
		middleware.JSON(w, http.StatusOK, map[string]string{"status": "withdrawn"})
		return
	}
	if err != nil || history(ctx, tx, &mine) != nil {
		middleware.InternalError(w)
		return
	}
	_, err = tx.Exec(ctx, `UPDATE activity_responses SET status='withdrawn', updated_at=now() WHERE id=$1`, mine.ID)
	if err != nil || tx.Commit(ctx) != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "withdrawn"})
}
