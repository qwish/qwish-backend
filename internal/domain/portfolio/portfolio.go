// Package portfolio is the staff side of the student portfolio
// (plans/student-portfolio-and-achievements.md): the full student profile as
// teachers and institution admins see it, and the teacher review workflow.
//
// Staff only ever see submitted revisions — never live drafts — and only
// revisions submitted while the student belonged to the caller's institution.
// Restricted fields (internship supervisor contact) are stripped from every
// staff view.
package portfolio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/qwish/backend/internal/domain/notification"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	db    *pgxpool.Pool
	notif *notification.Service
}

func NewHandler(db *pgxpool.Pool, notif *notification.Service) *Handler {
	return &Handler{db: db, notif: notif}
}

// TeacherRoutes mounts under /teacher.
func (h *Handler) TeacherRoutes(r chi.Router) {
	r.Get("/students/{userId}/profile", h.TeacherProfile)
	r.Get("/portfolio-reviews", h.Queue)
	r.Post("/portfolio-reviews/{revisionId}/decision", h.Decide)
}

// InstitutionRoutes mounts under /institution.
func (h *Handler) InstitutionRoutes(r chi.Router) {
	r.Get("/students/{userId}/profile", h.InstitutionProfile)
}

// teacherScopeSQL: student $1 is visible to teacher $3 in institution $2 —
// the same rule as the teacher student list (unassigned teachers see the
// whole institution; assigned teachers see students sharing a class).
const teacherScopeSQL = `EXISTS(SELECT 1 FROM users s WHERE s.id=$1 AND s.institution_id=$2 AND s.role='student' AND s.deleted_at IS NULL
	AND (NOT EXISTS(SELECT 1 FROM group_teachers WHERE user_id=$3)
	  OR EXISTS(SELECT 1 FROM group_students gs JOIN group_teachers gt ON gt.group_id=gs.group_id WHERE gs.user_id=s.id AND gt.user_id=$3)))`

// institutionScopeSQL mirrors the institution student detail: an active or
// suspended enrollment in institution $2.
const institutionScopeSQL = `EXISTS(SELECT 1 FROM users s JOIN enrollments e ON e.user_id=s.id
	WHERE s.id=$1 AND e.institution_id=$2 AND s.role='student' AND s.deleted_at IS NULL AND e.status IN ('active','suspended'))`

// restricted detail keys never shown to staff.
var restrictedDetails = []string{"supervisor"}

// staffContent turns a stored revision snapshot into what staff may see.
func staffContent(raw []byte) (map[string]any, error) {
	var c map[string]any
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("invalid revision content: %w", err)
	}
	for _, k := range []string{"status", "pinned", "revision", "review"} {
		delete(c, k)
	}
	if d, ok := c["details"].(map[string]any); ok {
		for _, k := range restrictedDetails {
			delete(d, k)
		}
	}
	return c, nil
}

func validDecision(decision, comment string) error {
	if decision != "reviewed" && decision != "changes_requested" {
		return errors.New("decision must be reviewed or changes_requested")
	}
	if len([]rune(comment)) > 2000 {
		return errors.New("comment is at most 2000 characters")
	}
	if decision == "changes_requested" && strings.TrimSpace(comment) == "" {
		return errors.New("say what needs to change")
	}
	return nil
}

// ─── Profile ─────────────────────────────────────────────────────────────────

type portfolioEntry struct {
	EntryID      string         `json:"entry_id"`
	RevisionID   string         `json:"revision_id"`
	Revision     int            `json:"revision"`
	Content      map[string]any `json:"content"`
	SubmittedAt  time.Time      `json:"submitted_at"`
	Pinned       bool           `json:"pinned"`
	ReviewState  string         `json:"review_state"` // submitted | reviewed | changes_requested
	Review       *review        `json:"review"`
	PendingMine  bool           `json:"awaiting_review"`
	StudentEdits bool           `json:"student_editing"` // a newer private draft exists
}

type review struct {
	Decision  string    `json:"decision"`
	Comment   string    `json:"comment"`
	Reviewer  *string   `json:"reviewer"`
	CreatedAt time.Time `json:"created_at"`
}

type education struct {
	InstitutionName string  `json:"institution_name"`
	Degree          *string `json:"degree"`
	Field           *string `json:"field"`
	StartYear       *int    `json:"start_year"`
	EndYear         *int    `json:"end_year"`
	IsCurrent       bool    `json:"is_current"`
}

type enrollment struct {
	RollNumber    *string    `json:"roll_number"`
	Grade         *string    `json:"grade"`
	Section       *string    `json:"section"`
	AdmissionDate *string    `json:"admission_date"`
	Status        string     `json:"status"`
	JoinedAt      *time.Time `json:"joined_at"`
}

type profile struct {
	Student struct {
		ID                   string     `json:"id"`
		FullName             string     `json:"full_name"`
		DisplayName          string     `json:"display_name"`
		Email                string     `json:"email"`
		Phone                *string    `json:"phone"`
		DateOfBirth          *string    `json:"date_of_birth"`
		Gender               *string    `json:"gender"`
		Address              *string    `json:"address"`
		GuardianName         *string    `json:"guardian_name"`
		GuardianPhone        *string    `json:"guardian_phone"`
		GuardianEmail        *string    `json:"guardian_email"`
		HighestQualification *string    `json:"highest_qualification"`
		Domain               *string    `json:"domain"`
		Interests            []string   `json:"interests"`
		PreferredLanguage    string     `json:"preferred_language"`
		Status               string     `json:"status"`
		MemberSince          time.Time  `json:"member_since"`
		LastActiveAt         *time.Time `json:"last_active_at"`
	} `json:"student"`
	Enrollment  *enrollment      `json:"enrollment"`
	Classes     []string         `json:"classes"`
	Departments []string         `json:"departments"`
	Education   []education      `json:"education"`
	Skills      []string         `json:"skills"`
	Portfolio   []portfolioEntry `json:"portfolio"`
	Summary     map[string]int   `json:"portfolio_summary"`
}

func (h *Handler) TeacherProfile(w http.ResponseWriter, r *http.Request) {
	h.serveProfile(w, r, teacherScopeSQL, middleware.GetUserID(r))
}

func (h *Handler) InstitutionProfile(w http.ResponseWriter, r *http.Request) {
	h.serveProfile(w, r, institutionScopeSQL)
}

// serveProfile checks scope ($1 student, $2 institution, then extra) and
// returns the full profile.
func (h *Handler) serveProfile(w http.ResponseWriter, r *http.Request, scope string, extra ...any) {
	ctx := r.Context()
	studentID, inst := chi.URLParam(r, "userId"), middleware.GetInstitutionID(r)
	var ok bool
	if err := h.db.QueryRow(ctx, `SELECT `+scope, append([]any{studentID, inst}, extra...)...).Scan(&ok); err != nil || !ok {
		middleware.NotFound(w, "student")
		return
	}
	p, err := h.loadProfile(ctx, studentID, inst)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, p)
}

func (h *Handler) loadProfile(ctx context.Context, studentID, inst string) (profile, error) {
	var p profile
	s := &p.Student
	err := h.db.QueryRow(ctx, `SELECT u.id, u.full_name, u.display_name, u.email, NULLIF(u.phone,''), to_char(u.date_of_birth,'YYYY-MM-DD'),
		NULLIF(u.gender,''), NULLIF(u.address,''), NULLIF(u.guardian_name,''), NULLIF(u.guardian_phone,''), NULLIF(u.guardian_email,''),
		NULLIF(u.highest_qualification,''), NULLIF(u.domain,''), u.interest_domains, u.preferred_language, u.status, u.member_since, u.last_active_at,
		COALESCE((SELECT array_agg(g.name ORDER BY g.name) FROM group_students gs JOIN groups g ON g.id=gs.group_id
			WHERE gs.user_id=u.id AND g.institution_id=$2 AND g.archived_at IS NULL),'{}'),
		COALESCE((SELECT array_agg(DISTINCT d.name) FROM group_students gs JOIN groups g ON g.id=gs.group_id JOIN departments d ON d.id=g.department_id
			WHERE gs.user_id=u.id AND g.institution_id=$2 AND g.archived_at IS NULL AND d.archived_at IS NULL),'{}'),
		COALESCE((SELECT array_agg(skill_name ORDER BY skill_name) FROM user_skills WHERE user_id=u.id),'{}')
		FROM users u WHERE u.id=$1`, studentID, inst).Scan(&s.ID, &s.FullName, &s.DisplayName, &s.Email, &s.Phone, &s.DateOfBirth,
		&s.Gender, &s.Address, &s.GuardianName, &s.GuardianPhone, &s.GuardianEmail, &s.HighestQualification, &s.Domain,
		&s.Interests, &s.PreferredLanguage, &s.Status, &s.MemberSince, &s.LastActiveAt, &p.Classes, &p.Departments, &p.Skills)
	if err != nil {
		return p, err
	}

	en := &enrollment{}
	err = h.db.QueryRow(ctx, `SELECT roll_number, grade, section, to_char(admission_date,'YYYY-MM-DD'), status, joined_at
		FROM enrollments WHERE user_id=$1 AND institution_id=$2 AND ended_at IS NULL ORDER BY created_at DESC LIMIT 1`, studentID, inst).
		Scan(&en.RollNumber, &en.Grade, &en.Section, &en.AdmissionDate, &en.Status, &en.JoinedAt)
	if err == nil {
		p.Enrollment = en
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return p, err
	}

	p.Education = []education{}
	rows, err := h.db.Query(ctx, `SELECT institution_name, NULLIF(degree,''), NULLIF(field,''), start_year, end_year, is_current
		FROM user_education WHERE user_id=$1 ORDER BY is_current DESC, end_year DESC NULLS FIRST, start_year DESC NULLS LAST`, studentID)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var e education
		if err := rows.Scan(&e.InstitutionName, &e.Degree, &e.Field, &e.StartYear, &e.EndYear, &e.IsCurrent); err != nil {
			rows.Close()
			return p, err
		}
		p.Education = append(p.Education, e)
	}
	rows.Close()
	if rows.Err() != nil {
		return p, rows.Err()
	}

	p.Portfolio, err = h.portfolio(ctx, studentID, inst)
	if err != nil {
		return p, err
	}
	p.Summary = map[string]int{"submitted": 0, "reviewed": 0, "changes_requested": 0}
	for _, e := range p.Portfolio {
		p.Summary[e.ReviewState]++
	}
	return p, nil
}

// portfolio returns, per entry, the latest revision submitted within this
// institution and its latest review. Never-submitted drafts are absent.
func (h *Handler) portfolio(ctx context.Context, studentID, inst string) ([]portfolioEntry, error) {
	rows, err := h.db.Query(ctx, `SELECT e.id, v.id, v.revision, v.content, v.submitted_at, e.pinned, e.status, e.current_revision,
		x.decision, x.comment, (SELECT COALESCE(NULLIF(ru.display_name,''),ru.full_name) FROM users ru WHERE ru.id=x.reviewer_id), x.created_at
		FROM user_profile_entries e
		JOIN LATERAL (SELECT * FROM user_profile_entry_revisions v WHERE v.entry_id=e.id AND v.institution_id=$2 ORDER BY v.revision DESC LIMIT 1) v ON true
		LEFT JOIN LATERAL (SELECT * FROM user_profile_entry_reviews x WHERE x.revision_id=v.id ORDER BY x.created_at DESC LIMIT 1) x ON true
		WHERE e.user_id=$1
		ORDER BY e.pinned DESC, v.submitted_at DESC`, studentID, inst)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []portfolioEntry{}
	for rows.Next() {
		var p portfolioEntry
		var raw []byte
		var status string
		var current int
		var decision, comment *string
		var reviewer *string
		var reviewedAt *time.Time
		if err := rows.Scan(&p.EntryID, &p.RevisionID, &p.Revision, &raw, &p.SubmittedAt, &p.Pinned, &status, &current,
			&decision, &comment, &reviewer, &reviewedAt); err != nil {
			return nil, err
		}
		if p.Content, err = staffContent(raw); err != nil {
			return nil, err
		}
		p.ReviewState = "submitted"
		if decision != nil {
			p.ReviewState = *decision
			p.Review = &review{Decision: *decision, Comment: *comment, Reviewer: reviewer, CreatedAt: *reviewedAt}
		}
		p.PendingMine = status == "submitted" && current == p.Revision && decision == nil
		p.StudentEdits = status == "draft"
		out = append(out, p)
	}
	return out, rows.Err()
}

// ─── Review queue and decisions ──────────────────────────────────────────────

type queueItem struct {
	RevisionID  string    `json:"revision_id"`
	Revision    int       `json:"revision"`
	EntryID     string    `json:"entry_id"`
	StudentID   string    `json:"student_id"`
	StudentName string    `json:"student_name"`
	Classes     []string  `json:"classes"`
	Category    string    `json:"category"`
	Title       string    `json:"title"`
	SubmittedAt time.Time `json:"submitted_at"`
	Resubmitted bool      `json:"resubmitted"`
}

// Queue lists submissions awaiting review for students in the teacher's scope,
// oldest first. ?category= filters by subtype (or legacy kind).
func (h *Handler) Queue(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 100
	}
	teacherID, inst := middleware.GetUserID(r), middleware.GetInstitutionID(r)
	rows, err := h.db.Query(r.Context(), `SELECT v.id, v.revision, e.id, u.id, COALESCE(NULLIF(u.display_name,''),u.full_name,''),
		COALESCE((SELECT array_agg(g.name ORDER BY g.name) FROM group_students gs JOIN groups g ON g.id=gs.group_id
			WHERE gs.user_id=u.id AND g.institution_id=$1 AND g.archived_at IS NULL
			  AND (NOT EXISTS(SELECT 1 FROM group_teachers WHERE user_id=$2) OR EXISTS(SELECT 1 FROM group_teachers gt WHERE gt.group_id=g.id AND gt.user_id=$2))),'{}'),
		COALESCE(e.subtype, e.kind), COALESCE(v.content->>'title',''), v.submitted_at, v.revision>1
		FROM user_profile_entries e
		JOIN user_profile_entry_revisions v ON v.entry_id=e.id AND v.revision=e.current_revision AND v.institution_id=$1
		JOIN users u ON u.id=e.user_id AND u.institution_id=$1 AND u.role='student' AND u.deleted_at IS NULL
		WHERE e.status='submitted'
		  AND (NOT EXISTS(SELECT 1 FROM group_teachers WHERE user_id=$2)
		    OR EXISTS(SELECT 1 FROM group_students gs JOIN group_teachers gt ON gt.group_id=gs.group_id WHERE gs.user_id=u.id AND gt.user_id=$2))
		  AND ($3='' OR COALESCE(e.subtype,e.kind)=$3)
		ORDER BY v.submitted_at, v.id LIMIT $4`, inst, teacherID, r.URL.Query().Get("category"), limit)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	out := []queueItem{}
	for rows.Next() {
		var q queueItem
		if rows.Scan(&q.RevisionID, &q.Revision, &q.EntryID, &q.StudentID, &q.StudentName, &q.Classes, &q.Category, &q.Title, &q.SubmittedAt, &q.Resubmitted) != nil {
			middleware.InternalError(w)
			return
		}
		out = append(out, q)
	}
	if rows.Err() != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}

// Decide records a review of one exact revision. It is refused when the
// revision is no longer the one awaiting review (the student edited or
// resubmitted, or another teacher decided first); an identical retry succeeds.
func (h *Handler) Decide(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Decision string `json:"decision"`
		Comment  string `json:"comment"`
	}
	d := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	in.Comment = strings.TrimSpace(in.Comment)
	if err := validDecision(in.Decision, in.Comment); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	ctx := r.Context()
	teacherID, inst := middleware.GetUserID(r), middleware.GetInstitutionID(r)
	tx, err := h.db.Begin(ctx)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(ctx)

	var revID, entryID, studentID, status, title string
	var revision, current int
	err = tx.QueryRow(ctx, `SELECT v.id, v.revision, e.id, e.user_id, e.status, e.current_revision, COALESCE(v.content->>'title','')
		FROM user_profile_entry_revisions v JOIN user_profile_entries e ON e.id=v.entry_id
		WHERE v.id::text=$1 AND v.institution_id=$2 FOR UPDATE OF e`, chi.URLParam(r, "revisionId"), inst).
		Scan(&revID, &revision, &entryID, &studentID, &status, &current, &title)
	var visible bool
	if err == nil {
		err = tx.QueryRow(ctx, `SELECT `+teacherScopeSQL, studentID, inst, teacherID).Scan(&visible)
	}
	if err != nil || !visible {
		middleware.NotFound(w, "submission")
		return
	}
	if status != "submitted" || current != revision {
		// Identical retry of a decision already recorded on this revision.
		var same bool
		tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_profile_entry_reviews WHERE revision_id=$1 AND reviewer_id=$2 AND decision=$3 AND comment=$4)`,
			revID, teacherID, in.Decision, in.Comment).Scan(&same)
		if same && current == revision && status == in.Decision {
			middleware.JSON(w, http.StatusOK, map[string]any{"revision_id": revID, "status": status})
			return
		}
		middleware.Error(w, http.StatusConflict, "STALE_REVISION", "this submission is no longer awaiting review — reload to see the latest")
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO user_profile_entry_reviews(revision_id, reviewer_id, institution_id, decision, comment) VALUES ($1,$2,$3,$4,$5)`,
		revID, teacherID, inst, in.Decision, in.Comment)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE user_profile_entries SET status=$2, updated_at=now() WHERE id=$1`, entryID, in.Decision)
	}
	if err != nil || tx.Commit(ctx) != nil {
		middleware.InternalError(w)
		return
	}
	if h.notif != nil {
		msg := "A teacher reviewed your portfolio entry."
		if in.Decision == "changes_requested" {
			msg = "A teacher asked for changes to your portfolio entry."
		}
		h.notif.Emit(ctx, studentID, "portfolio_review", "Portfolio review", msg,
			notification.WithIcon("workspace_premium"), notification.WithReference("portfolio:"+revID))
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"revision_id": revID, "status": in.Decision})
}
