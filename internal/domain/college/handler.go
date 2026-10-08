// Package college holds the first-release college structure: programmes,
// admission cohorts, academic terms and course offerings
// (plans/institute-college-structure-gaps.md). Tenant ownership is enforced
// by composite foreign keys; handlers add lifecycle checks on top.
package college

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct{ db *pgxpool.Pool }

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

// InstitutionRoutes mounts under the institution_admin route.
func (h *Handler) InstitutionRoutes(r chi.Router) {
	r.Get("/programmes", h.ListProgrammes)
	r.Post("/programmes", h.CreateProgramme)
	r.Patch("/programmes/{id}", h.UpdateProgramme)
	r.Delete("/programmes/{id}", h.ArchiveProgramme)
	r.Get("/cohorts", h.ListCohorts)
	r.Post("/cohorts", h.CreateCohort)
	r.Delete("/cohorts/{id}", h.ArchiveCohort)
	r.Get("/academic-terms", h.ListTerms)
	r.Post("/academic-terms", h.CreateTerm)
	r.Patch("/academic-terms/{id}", h.UpdateTerm)
	r.Delete("/academic-terms/{id}", h.DeleteTerm)
	r.Get("/course-offerings", h.ListOfferings)
	r.Post("/course-offerings", h.CreateOffering)
	r.Delete("/course-offerings/{id}", h.DeleteOffering)
	r.Put("/course-offerings/{id}/groups", h.SetOfferingGroups)
	r.Put("/groups/{groupId}/academic-context", h.SetGroupContext)
}

// ─── helpers ────────────────────────────────────────────────────────────────

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	d := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return false
	}
	return true
}

func validID(s string) bool { id, err := uuid.Parse(s); return err == nil && id != uuid.Nil }

func pathID(w http.ResponseWriter, r *http.Request, key string) (string, bool) {
	id := chi.URLParam(r, key)
	if !validID(id) {
		middleware.BadRequest(w, key+" must be a UUID")
		return "", false
	}
	return id, true
}

// optID validates an optional UUID; "" and nil both mean none.
func optID(s *string) (*string, bool) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, true
	}
	v := strings.TrimSpace(*s)
	return &v, validID(v)
}

func text(s *string, max int) bool {
	*s = strings.TrimSpace(*s)
	n := len([]rune(*s))
	return n >= 1 && n <= max
}

func optText(s **string, max int) bool {
	if *s == nil {
		return true
	}
	v := strings.TrimSpace(**s)
	if v == "" {
		*s = nil
		return true
	}
	*s = &v
	return len([]rune(v)) <= max
}

func pgCode(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

// writeErr maps constraint failures to client errors; anything else is a 500.
func writeErr(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		middleware.NotFound(w, what)
	case pgCode(err) == "23505":
		middleware.Error(w, http.StatusConflict, "CONFLICT", "a "+what+" with this code or name already exists")
	case pgCode(err) == "23503":
		// Composite FKs: a reference from another institution looks missing.
		middleware.Error(w, http.StatusConflict, "IN_USE_OR_UNKNOWN", what+" references a missing record, or is still in use")
	case pgCode(err) == "23514":
		middleware.BadRequest(w, "invalid "+what+" values")
	default:
		middleware.InternalError(w)
	}
}

func audit(ctx context.Context, tx pgx.Tx, r *http.Request, action, targetType, targetID string, after any) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_log (admin_id, admin_name, admin_role, action_type, target_type, target_id, institution_id, new_value)
		SELECT u.id, COALESCE(NULLIF(u.display_name,''),u.full_name,''), u.role, $2, $3, $4, $5, $6 FROM users u WHERE u.id=$1`,
		middleware.GetUserID(r), action, targetType, targetID, middleware.GetInstitutionID(r), after)
	return err
}

// mutate runs fn in a transaction and audits it. fn returns the target id.
func (h *Handler) mutate(w http.ResponseWriter, r *http.Request, action, what string, status int, after any,
	fn func(ctx context.Context, tx pgx.Tx, inst string) (string, error)) {
	ctx := r.Context()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(ctx)
	inst := middleware.GetInstitutionID(r)
	// ponytail: one lock per institution serializes every structure write (and
	// department archive, which takes the same lock), so lifecycle checks never
	// race. Admin-only, low volume; use per-row locks if that ever contends.
	if _, err := tx.Exec(ctx, `SELECT id FROM institutions WHERE id=$1 FOR UPDATE`, inst); err != nil {
		middleware.InternalError(w)
		return
	}
	id, err := fn(ctx, tx, inst)
	if err != nil {
		var ce clientErr
		if errors.As(err, &ce) {
			middleware.Error(w, ce.status, ce.code, ce.msg)
			return
		}
		writeErr(w, err, what)
		return
	}
	if audit(ctx, tx, r, action, what, id, after) != nil || tx.Commit(ctx) != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, status, map[string]string{"id": id})
}

type clientErr struct {
	status    int
	code, msg string
}

func (e clientErr) Error() string { return e.msg }

func conflict(msg string) error { return clientErr{http.StatusConflict, "CONFLICT", msg} }
func invalid(msg string) error  { return clientErr{http.StatusBadRequest, "BAD_REQUEST", msg} }

// list scans rows into T via pgx.RowToStructByName and always returns a slice.
func list[T any](w http.ResponseWriter, r *http.Request, db *pgxpool.Pool, sql string, args ...any) {
	rows, err := db.Query(r.Context(), sql, args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[T])
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if out == nil {
		out = []T{}
	}
	middleware.JSON(w, http.StatusOK, out)
}

// queryID reads an optional UUID filter; ok=false after a 400 was written.
func queryID(w http.ResponseWriter, r *http.Request, key string) (*string, bool) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return nil, true
	}
	if !validID(v) {
		middleware.BadRequest(w, key+" must be a UUID")
		return nil, false
	}
	return &v, true
}

// ─── programmes ─────────────────────────────────────────────────────────────

type programme struct {
	ID             string  `json:"id" db:"id"`
	DepartmentID   string  `json:"department_id" db:"department_id"`
	DepartmentName string  `json:"department_name" db:"department_name"`
	Code           string  `json:"code" db:"code"`
	Name           string  `json:"name" db:"name"`
	Award          *string `json:"award" db:"award"`
	DurationTerms  *int16  `json:"duration_terms" db:"duration_terms"`
	CohortCount    int     `json:"cohort_count" db:"cohort_count"`
}

func (h *Handler) ListProgrammes(w http.ResponseWriter, r *http.Request) {
	dept, ok := queryID(w, r, "department_id")
	if !ok {
		return
	}
	list[programme](w, r, h.db, `SELECT p.id::text, p.department_id::text, d.name AS department_name, p.code, p.name, p.award, p.duration_terms,
		(SELECT count(*)::int FROM cohorts c WHERE c.programme_id=p.id AND c.archived_at IS NULL) AS cohort_count
		FROM programmes p JOIN departments d ON d.id=p.department_id
		WHERE p.institution_id=$1 AND p.archived_at IS NULL AND ($2::uuid IS NULL OR p.department_id=$2)
		ORDER BY d.name, p.code`, middleware.GetInstitutionID(r), dept)
}

type programmeInput struct {
	DepartmentID  string  `json:"department_id"`
	Code          string  `json:"code"`
	Name          string  `json:"name"`
	Award         *string `json:"award"`
	DurationTerms *int16  `json:"duration_terms"`
}

func (in *programmeInput) valid() bool {
	return validID(in.DepartmentID) && text(&in.Code, 20) && text(&in.Name, 160) && optText(&in.Award, 60) &&
		(in.DurationTerms == nil || (*in.DurationTerms >= 1 && *in.DurationTerms <= 20))
}

const programmeRule = "department_id, code (1–20), and name (1–160) are required; award is at most 60; duration_terms is 1–20"

func (h *Handler) CreateProgramme(w http.ResponseWriter, r *http.Request) {
	var in programmeInput
	if !decode(w, r, &in) {
		return
	}
	if !in.valid() {
		middleware.BadRequest(w, programmeRule)
		return
	}
	h.mutate(w, r, "create_programme", "programme", http.StatusCreated, in, func(ctx context.Context, tx pgx.Tx, inst string) (string, error) {
		var id string
		// Selecting from departments refuses archived or foreign departments.
		err := tx.QueryRow(ctx, `INSERT INTO programmes (institution_id, department_id, code, name, award, duration_terms)
			SELECT $1, d.id, $3, $4, $5, $6 FROM departments d WHERE d.id=$2 AND d.institution_id=$1 AND d.archived_at IS NULL
			RETURNING id`, inst, in.DepartmentID, in.Code, in.Name, in.Award, in.DurationTerms).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", invalid("department not found or archived")
		}
		return id, err
	})
}

func (h *Handler) UpdateProgramme(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in programmeInput
	if !decode(w, r, &in) {
		return
	}
	if !in.valid() {
		middleware.BadRequest(w, programmeRule)
		return
	}
	h.mutate(w, r, "update_programme", "programme", http.StatusOK, in, func(ctx context.Context, tx pgx.Tx, inst string) (string, error) {
		var dept bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM departments WHERE id=$1 AND institution_id=$2 AND archived_at IS NULL)`,
			in.DepartmentID, inst).Scan(&dept); err != nil {
			return "", err
		}
		if !dept {
			return "", invalid("department not found or archived")
		}
		return id, tx.QueryRow(ctx, `UPDATE programmes SET department_id=$3, code=$4, name=$5, award=$6, duration_terms=$7
			WHERE id=$1 AND institution_id=$2 AND archived_at IS NULL RETURNING id`,
			id, inst, in.DepartmentID, in.Code, in.Name, in.Award, in.DurationTerms).Scan(&id)
	})
}

func (h *Handler) ArchiveProgramme(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	h.mutate(w, r, "archive_programme", "programme", http.StatusOK, nil, func(ctx context.Context, tx pgx.Tx, inst string) (string, error) {
		var cohorts int
		if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM cohorts c WHERE c.programme_id=p.id AND c.archived_at IS NULL)
			FROM programmes p WHERE p.id=$1 AND p.institution_id=$2 AND p.archived_at IS NULL FOR UPDATE OF p`, id, inst).Scan(&cohorts); err != nil {
			return "", err
		}
		if cohorts > 0 {
			return "", conflict("archive this programme's cohorts first")
		}
		_, err := tx.Exec(ctx, `UPDATE programmes SET archived_at=now() WHERE id=$1`, id)
		return id, err
	})
}

// ─── cohorts ────────────────────────────────────────────────────────────────

type cohort struct {
	ID             string `json:"id" db:"id"`
	ProgrammeID    string `json:"programme_id" db:"programme_id"`
	ProgrammeCode  string `json:"programme_code" db:"programme_code"`
	ProgrammeName  string `json:"programme_name" db:"programme_name"`
	AdmissionYear  int16  `json:"admission_year" db:"admission_year"`
	CompletionYear *int16 `json:"completion_year" db:"completion_year"`
	DivisionCount  int    `json:"division_count" db:"division_count"`
}

func (h *Handler) ListCohorts(w http.ResponseWriter, r *http.Request) {
	prog, ok := queryID(w, r, "programme_id")
	if !ok {
		return
	}
	list[cohort](w, r, h.db, `SELECT c.id::text, c.programme_id::text, p.code AS programme_code, p.name AS programme_name,
		c.admission_year, c.completion_year,
		(SELECT count(*)::int FROM groups g WHERE g.cohort_id=c.id AND g.archived_at IS NULL) AS division_count
		FROM cohorts c JOIN programmes p ON p.id=c.programme_id
		WHERE c.institution_id=$1 AND c.archived_at IS NULL AND ($2::uuid IS NULL OR c.programme_id=$2)
		ORDER BY p.code, c.admission_year DESC`, middleware.GetInstitutionID(r), prog)
}

func (h *Handler) CreateCohort(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProgrammeID    string `json:"programme_id"`
		AdmissionYear  int16  `json:"admission_year"`
		CompletionYear *int16 `json:"completion_year"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !validID(in.ProgrammeID) || in.AdmissionYear < 1900 || in.AdmissionYear > 2200 ||
		(in.CompletionYear != nil && (*in.CompletionYear < in.AdmissionYear || *in.CompletionYear > 2200)) {
		middleware.BadRequest(w, "programme_id and admission_year (1900–2200) are required; completion_year cannot precede admission")
		return
	}
	h.mutate(w, r, "create_cohort", "cohort", http.StatusCreated, in, func(ctx context.Context, tx pgx.Tx, inst string) (string, error) {
		var id string
		err := tx.QueryRow(ctx, `INSERT INTO cohorts (institution_id, programme_id, admission_year, completion_year)
			SELECT $1, p.id, $3, $4 FROM programmes p WHERE p.id=$2 AND p.institution_id=$1 AND p.archived_at IS NULL
			RETURNING id`, inst, in.ProgrammeID, in.AdmissionYear, in.CompletionYear).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", invalid("programme not found or archived")
		}
		return id, err
	})
}

func (h *Handler) ArchiveCohort(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	h.mutate(w, r, "archive_cohort", "cohort", http.StatusOK, nil, func(ctx context.Context, tx pgx.Tx, inst string) (string, error) {
		var groups int
		if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM groups g WHERE g.cohort_id=c.id AND g.archived_at IS NULL)
			FROM cohorts c WHERE c.id=$1 AND c.institution_id=$2 AND c.archived_at IS NULL FOR UPDATE OF c`, id, inst).Scan(&groups); err != nil {
			return "", err
		}
		if groups > 0 {
			return "", conflict("end or unlink this cohort's active classes first")
		}
		_, err := tx.Exec(ctx, `UPDATE cohorts SET archived_at=now() WHERE id=$1`, id)
		return id, err
	})
}

// ─── terms ──────────────────────────────────────────────────────────────────

type term struct {
	ID             string `json:"id" db:"id"`
	AcademicYearID string `json:"academic_year_id" db:"academic_year_id"`
	YearName       string `json:"academic_year_name" db:"academic_year_name"`
	Name           string `json:"name" db:"name"`
	Sequence       int16  `json:"sequence" db:"sequence"`
	StartsOn       string `json:"starts_on" db:"starts_on"`
	EndsOn         string `json:"ends_on" db:"ends_on"`
	Today          string `json:"today" db:"today"`
	ClassCount     int    `json:"class_count" db:"class_count"`
	OfferingCount  int    `json:"offering_count" db:"offering_count"`
}

func (h *Handler) ListTerms(w http.ResponseWriter, r *http.Request) {
	year, ok := queryID(w, r, "academic_year_id")
	if !ok {
		return
	}
	list[term](w, r, h.db, `SELECT t.id::text, t.academic_year_id::text, y.name AS academic_year_name, t.name, t.sequence,
		t.starts_on::text, t.ends_on::text, (now() AT TIME ZONE i.timezone)::date::text AS today,
		(SELECT count(*)::int FROM groups g WHERE g.term_id=t.id AND g.archived_at IS NULL) AS class_count,
		(SELECT count(*)::int FROM course_offerings o WHERE o.term_id=t.id) AS offering_count
		FROM academic_terms t JOIN academic_years y ON y.id=t.academic_year_id JOIN institutions i ON i.id=t.institution_id
		WHERE t.institution_id=$1 AND ($2::uuid IS NULL OR t.academic_year_id=$2)
		ORDER BY y.starts_on DESC, t.sequence`, middleware.GetInstitutionID(r), year)
}

type termInput struct {
	AcademicYearID string `json:"academic_year_id"`
	Name           string `json:"name"`
	Sequence       int16  `json:"sequence"`
	StartsOn       string `json:"starts_on"`
	EndsOn         string `json:"ends_on"`
}

func (in *termInput) valid() bool {
	s, err1 := time.Parse(time.DateOnly, in.StartsOn)
	e, err2 := time.Parse(time.DateOnly, in.EndsOn)
	return validID(in.AcademicYearID) && text(&in.Name, 80) && in.Sequence >= 1 && in.Sequence <= 12 &&
		err1 == nil && err2 == nil && !e.Before(s)
}

const termRule = "academic_year_id, name (1–80), sequence (1–12), and starts_on ≤ ends_on (YYYY-MM-DD) are required"

// checkTerm locks the year and refuses terms outside it or overlapping a
// sibling, so the year's terms never give two answers for "current term".
func checkTerm(ctx context.Context, tx pgx.Tx, inst, self string, in termInput) error {
	var inside bool
	err := tx.QueryRow(ctx, `SELECT $3::date >= starts_on AND $4::date <= ends_on FROM academic_years
		WHERE id=$1 AND institution_id=$2 FOR UPDATE`, in.AcademicYearID, inst, in.StartsOn, in.EndsOn).Scan(&inside)
	if errors.Is(err, pgx.ErrNoRows) {
		return invalid("academic year not found")
	}
	if err != nil {
		return err
	}
	if !inside {
		return invalid("term dates must fall inside the academic year")
	}
	var clash *string
	err = tx.QueryRow(ctx, `SELECT name FROM academic_terms WHERE academic_year_id=$1 AND ($2='' OR id::text<>$2)
		AND daterange(starts_on, ends_on, '[]') && daterange($3::date, $4::date, '[]') LIMIT 1`,
		in.AcademicYearID, self, in.StartsOn, in.EndsOn).Scan(&clash)
	if err == nil {
		return conflict("term dates overlap " + *clash)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return nil
}

func (h *Handler) CreateTerm(w http.ResponseWriter, r *http.Request) {
	var in termInput
	if !decode(w, r, &in) {
		return
	}
	if !in.valid() {
		middleware.BadRequest(w, termRule)
		return
	}
	h.mutate(w, r, "create_term", "term", http.StatusCreated, in, func(ctx context.Context, tx pgx.Tx, inst string) (string, error) {
		if err := checkTerm(ctx, tx, inst, "", in); err != nil {
			return "", err
		}
		var id string
		return id, tx.QueryRow(ctx, `INSERT INTO academic_terms (institution_id, academic_year_id, name, sequence, starts_on, ends_on)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, inst, in.AcademicYearID, in.Name, in.Sequence, in.StartsOn, in.EndsOn).Scan(&id)
	})
}

func (h *Handler) UpdateTerm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in termInput
	if !decode(w, r, &in) {
		return
	}
	if !in.valid() {
		middleware.BadRequest(w, termRule)
		return
	}
	h.mutate(w, r, "update_term", "term", http.StatusOK, in, func(ctx context.Context, tx pgx.Tx, inst string) (string, error) {
		// A term stays in its year; moving it would silently re-date its classes.
		var year string
		if err := tx.QueryRow(ctx, `SELECT academic_year_id::text FROM academic_terms WHERE id=$1 AND institution_id=$2 FOR UPDATE`,
			id, inst).Scan(&year); err != nil {
			return "", err
		}
		if year != in.AcademicYearID {
			return "", invalid("a term cannot move to another academic year")
		}
		if err := checkTerm(ctx, tx, inst, id, in); err != nil {
			return "", err
		}
		_, err := tx.Exec(ctx, `UPDATE academic_terms SET name=$2, sequence=$3, starts_on=$4, ends_on=$5 WHERE id=$1`,
			id, in.Name, in.Sequence, in.StartsOn, in.EndsOn)
		return id, err
	})
}

// DeleteTerm only removes unused terms; the FKs from groups and offerings refuse otherwise.
func (h *Handler) DeleteTerm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	h.mutate(w, r, "delete_term", "term", http.StatusOK, nil, func(ctx context.Context, tx pgx.Tx, inst string) (string, error) {
		return id, tx.QueryRow(ctx, `DELETE FROM academic_terms WHERE id=$1 AND institution_id=$2 RETURNING id`, id, inst).Scan(&id)
	})
}

// ─── course offerings ───────────────────────────────────────────────────────

type offeringGroup struct {
	GroupID   string `json:"group_id"`
	Name      string `json:"name"`
	Component string `json:"component"`
}

type offering struct {
	ID             string          `json:"id" db:"id"`
	TermID         string          `json:"term_id" db:"term_id"`
	TermName       string          `json:"term_name" db:"term_name"`
	DepartmentID   *string         `json:"department_id" db:"department_id"`
	DepartmentName *string         `json:"department_name" db:"department_name"`
	Code           string          `json:"code" db:"code"`
	Title          string          `json:"title" db:"title"`
	Groups         []offeringGroup `json:"groups" db:"groups"`
}

func (h *Handler) ListOfferings(w http.ResponseWriter, r *http.Request) {
	termID, ok := queryID(w, r, "term_id")
	if !ok {
		return
	}
	dept, ok := queryID(w, r, "department_id")
	if !ok {
		return
	}
	list[offering](w, r, h.db, `SELECT o.id::text, o.term_id::text, t.name AS term_name, o.department_id::text, d.name AS department_name, o.code, o.title,
		COALESCE((SELECT json_agg(json_build_object('group_id', g.id, 'name', g.name, 'component', og.component) ORDER BY og.component, g.name)
			FROM offering_groups og JOIN groups g ON g.id=og.group_id WHERE og.offering_id=o.id), '[]'::json) AS groups
		FROM course_offerings o JOIN academic_terms t ON t.id=o.term_id LEFT JOIN departments d ON d.id=o.department_id
		WHERE o.institution_id=$1 AND ($2::uuid IS NULL OR o.term_id=$2) AND ($3::uuid IS NULL OR o.department_id=$3)
		ORDER BY t.starts_on DESC, o.code`, middleware.GetInstitutionID(r), termID, dept)
}

func (h *Handler) CreateOffering(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TermID       string  `json:"term_id"`
		DepartmentID *string `json:"department_id"`
		Code         string  `json:"code"`
		Title        string  `json:"title"`
	}
	if !decode(w, r, &in) {
		return
	}
	dept, ok := optID(in.DepartmentID)
	if !ok || !validID(in.TermID) || !text(&in.Code, 30) || !text(&in.Title, 160) {
		middleware.BadRequest(w, "term_id, code (1–30) and title (1–160) are required; department_id must be a UUID")
		return
	}
	h.mutate(w, r, "create_offering", "course offering", http.StatusCreated, in, func(ctx context.Context, tx pgx.Tx, inst string) (string, error) {
		var id string
		return id, tx.QueryRow(ctx, `INSERT INTO course_offerings (institution_id, term_id, department_id, code, title)
			VALUES ($1,$2,$3,$4,$5) RETURNING id`, inst, in.TermID, dept, in.Code, in.Title).Scan(&id)
	})
}

func (h *Handler) DeleteOffering(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	h.mutate(w, r, "delete_offering", "course offering", http.StatusOK, nil, func(ctx context.Context, tx pgx.Tx, inst string) (string, error) {
		return id, tx.QueryRow(ctx, `DELETE FROM course_offerings WHERE id=$1 AND institution_id=$2 RETURNING id`, id, inst).Scan(&id)
	})
}

var components = map[string]bool{"theory": true, "lab": true, "tutorial": true, "other": true}

// SetOfferingGroups replaces the offering's teaching groups.
func (h *Handler) SetOfferingGroups(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Groups []struct {
			GroupID   string `json:"group_id"`
			Component string `json:"component"`
		} `json:"groups"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Groups) > 50 {
		middleware.BadRequest(w, "at most 50 groups per offering")
		return
	}
	seen := map[string]bool{}
	for _, g := range in.Groups {
		if !validID(g.GroupID) || !components[g.Component] || seen[g.GroupID] {
			middleware.BadRequest(w, "each group needs a unique group_id and a component of theory, lab, tutorial or other")
			return
		}
		seen[g.GroupID] = true
	}
	h.mutate(w, r, "set_offering_groups", "course offering", http.StatusOK, in, func(ctx context.Context, tx pgx.Tx, inst string) (string, error) {
		if err := tx.QueryRow(ctx, `SELECT id FROM course_offerings WHERE id=$1 AND institution_id=$2 FOR UPDATE`, id, inst).Scan(&id); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM offering_groups WHERE offering_id=$1`, id); err != nil {
			return "", err
		}
		for _, g := range in.Groups {
			// Only active groups of this institution; the composite FK backs this up.
			tag, err := tx.Exec(ctx, `INSERT INTO offering_groups (offering_id, group_id, institution_id, component)
				SELECT $1, g.id, $3, $4 FROM groups g WHERE g.id=$2 AND g.institution_id=$3 AND g.archived_at IS NULL`,
				id, g.GroupID, inst, g.Component)
			if err != nil {
				return "", err
			}
			if tag.RowsAffected() == 0 {
				return "", invalid("class " + g.GroupID + " not found or ended")
			}
		}
		return id, nil
	})
}

// ─── group academic context ─────────────────────────────────────────────────

// SetGroupContext places a class in a cohort (as a division) and/or a term.
// Both fields are always written; null clears.
func (h *Handler) SetGroupContext(w http.ResponseWriter, r *http.Request) {
	groupID, ok := pathID(w, r, "groupId")
	if !ok {
		return
	}
	var in struct {
		CohortID *string `json:"cohort_id"`
		TermID   *string `json:"term_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	cohortID, ok1 := optID(in.CohortID)
	termID, ok2 := optID(in.TermID)
	if !ok1 || !ok2 {
		middleware.BadRequest(w, "cohort_id and term_id must be UUIDs or null")
		return
	}
	h.mutate(w, r, "set_group_academic_context", "class", http.StatusOK, in, func(ctx context.Context, tx pgx.Tx, inst string) (string, error) {
		if cohortID != nil {
			var active bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cohorts WHERE id=$1 AND institution_id=$2 AND archived_at IS NULL)`,
				*cohortID, inst).Scan(&active); err != nil {
				return "", err
			}
			if !active {
				return "", invalid("cohort not found or archived")
			}
		}
		return groupID, tx.QueryRow(ctx, `UPDATE groups SET cohort_id=$3, term_id=$4
			WHERE id=$1 AND institution_id=$2 AND archived_at IS NULL RETURNING id`, groupID, inst, cohortID, termID).Scan(&groupID)
	})
}
