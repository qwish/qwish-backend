package user

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

// Education and skills already have their own tables from 003_profile_features.
// Everything else on a student's CV shares one shape, so it shares one table.
var profileEntryKinds = map[string]bool{
	"experience": true, "certification": true, "achievement": true, "course": true,
}

func validKind(kind string) bool { return profileEntryKinds[kind] }

// Portfolio subtypes ride on a legacy kind so clients that only know the four
// kinds still list them in a sensible section.
var subtypeKind = map[string]string{
	"project":       "experience",
	"internship":    "experience",
	"hackathon":     "achievement",
	"certification": "certification",
	"achievement":   "achievement",
}

// The detail keys each subtype accepts. A nil set means free text; a non-nil
// set is the closed list of allowed values. Anything else is rejected, so
// details never become an arbitrary metadata bag.
var subtypeDetails = map[string]map[string]map[string]bool{
	"project": {
		"problem": nil, "approach": nil, "contribution": nil, "outcome": nil, "mentor": nil,
		"team_type": {"individual": true, "team": true},
	},
	"internship": {
		"responsibilities": nil, "learnings": nil, "supervisor": nil,
		"work_mode": {"onsite": true, "remote": true, "hybrid": true},
	},
	"hackathon": {
		"problem": nil, "team": nil, "contribution": nil,
		"level":  {"college": true, "state": true, "national": true, "international": true},
		"result": {"participant": true, "finalist": true, "winner": true},
	},
	"certification": {"credential_id": nil, "expiry_date": nil},
	"achievement":   {"category": nil, "role": nil},
}

const (
	maxTitleLen  = 200
	maxDetailLen = 2000
	maxSkills    = 20
	maxSkillLen  = 50
	maxLinks     = 5
	maxLinkLen   = 500
	maxPinned    = 3
)

var academicYearRe = regexp.MustCompile(`^\d{4}-\d{2}$`)

type ProfileEntryHandler struct{ db *pgxpool.Pool }

func NewProfileEntryHandler(db *pgxpool.Pool) *ProfileEntryHandler {
	return &ProfileEntryHandler{db: db}
}

type entryReview struct {
	Revision  int       `json:"revision"`
	Decision  string    `json:"decision"`
	Comment   string    `json:"comment"`
	Reviewer  *string   `json:"reviewer,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type profileEntry struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	Subtype      *string           `json:"subtype,omitempty"`
	Title        string            `json:"title"`
	Org          *string           `json:"org,omitempty"`
	StartDate    *time.Time        `json:"start_date,omitempty"`
	EndDate      *time.Time        `json:"end_date,omitempty"`
	Description  *string           `json:"description,omitempty"`
	Details      map[string]string `json:"details"`
	Skills       []string          `json:"skills"`
	Links        []string          `json:"links"`
	Ongoing      bool              `json:"ongoing"`
	AcademicYear *string           `json:"academic_year,omitempty"`
	Semester     *int16            `json:"semester,omitempty"`
	Status       string            `json:"status"`
	Pinned       bool              `json:"pinned"`
	Revision     int               `json:"revision"`
	Review       *entryReview      `json:"review,omitempty"`
}

const entryColumns = `e.id, e.kind, e.subtype, e.title, e.org, e.start_date, e.end_date, e.description,
	e.details, e.skills, e.links, e.ongoing, e.academic_year, e.semester, e.status, e.pinned, e.current_revision`

func scanEntry(row pgx.Row, e *profileEntry, extra ...any) error {
	return row.Scan(append([]any{&e.ID, &e.Kind, &e.Subtype, &e.Title, &e.Org, &e.StartDate, &e.EndDate,
		&e.Description, &e.Details, &e.Skills, &e.Links, &e.Ongoing, &e.AcademicYear, &e.Semester,
		&e.Status, &e.Pinned, &e.Revision}, extra...)...)
}

// GET /api/v1/users/me/profile-entries?kind=experience
func (h *ProfileEntryHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	kind := r.URL.Query().Get("kind")
	if kind != "" && !validKind(kind) {
		middleware.BadRequest(w, "unknown kind")
		return
	}

	// The review shown is the latest decision on the latest submitted revision;
	// an older revision's decision never describes the current content.
	rows, err := h.db.Query(r.Context(),
		`SELECT `+entryColumns+`, rv.decision, rv.comment, rv.created_at
		   FROM user_profile_entries e
		   LEFT JOIN LATERAL (
		     SELECT x.decision, x.comment, x.created_at
		       FROM user_profile_entry_reviews x
		       JOIN user_profile_entry_revisions v ON v.id = x.revision_id
		      WHERE v.entry_id = e.id AND v.revision = e.current_revision
		      ORDER BY x.created_at DESC LIMIT 1) rv ON true
		  WHERE e.user_id=$1 AND ($2='' OR e.kind=$2)
		  ORDER BY COALESCE(e.start_date, '1900-01-01') DESC, e.created_at DESC`, userID, kind)
	if err != nil {
		log.Printf("profile entry list: %v", err)
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	entries := []profileEntry{}
	for rows.Next() {
		var e profileEntry
		var decision, comment *string
		var reviewedAt *time.Time
		if err := scanEntry(rows, &e, &decision, &comment, &reviewedAt); err != nil {
			log.Printf("profile entry scan: %v", err)
			middleware.InternalError(w)
			return
		}
		if decision != nil {
			e.Review = &entryReview{Revision: e.Revision, Decision: *decision, Comment: *comment, CreatedAt: *reviewedAt}
		}
		entries = append(entries, e)
	}
	middleware.JSON(w, http.StatusOK, entries)
}

// Pointer fields added for the portfolio are optional: nil leaves the stored
// value alone, which is what keeps older clients' PATCHes from wiping them.
type profileEntryInput struct {
	Kind         string             `json:"kind"`
	Subtype      *string            `json:"subtype"`
	Title        string             `json:"title"`
	Org          *string            `json:"org"`
	StartDate    *string            `json:"start_date"`
	EndDate      *string            `json:"end_date"`
	Description  *string            `json:"description"`
	Details      *map[string]string `json:"details"`
	Skills       *[]string          `json:"skills"`
	Links        *[]string          `json:"links"`
	Ongoing      *bool              `json:"ongoing"`
	AcademicYear *string            `json:"academic_year"`
	Semester     *int16             `json:"semester"`
}

func parseDate(s *string) (*time.Time, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", *s)
	if err != nil {
		return nil, errors.New("dates must be YYYY-MM-DD")
	}
	return &t, nil
}

func blankToNil(s *string) *string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	t := strings.TrimSpace(*s)
	return &t
}

// apply merges the input onto e and validates the result as a whole, so
// cross-field rules (date order, details vs subtype) see the final state.
func (in *profileEntryInput) apply(e *profileEntry, creating bool) error {
	if creating {
		e.Details, e.Skills, e.Links = map[string]string{}, []string{}, []string{}
		if in.Subtype == nil && !validKind(in.Kind) {
			return errors.New("kind must be one of experience, certification, achievement, course")
		}
		e.Kind = in.Kind
	}
	if in.Subtype != nil {
		kind, ok := subtypeKind[*in.Subtype]
		if !ok {
			return errors.New("subtype must be one of project, internship, hackathon, certification, achievement")
		}
		e.Subtype, e.Kind = in.Subtype, kind
	}

	e.Title = strings.TrimSpace(in.Title)
	if e.Title == "" {
		return errors.New("title is required")
	}
	if len([]rune(e.Title)) > maxTitleLen {
		return fmt.Errorf("title must be at most %d characters", maxTitleLen)
	}
	e.Org, e.Description = blankToNil(in.Org), blankToNil(in.Description)

	var err error
	if e.StartDate, err = parseDate(in.StartDate); err != nil {
		return err
	}
	if e.EndDate, err = parseDate(in.EndDate); err != nil {
		return err
	}
	if in.Ongoing != nil {
		e.Ongoing = *in.Ongoing
	} else if e.EndDate != nil {
		// An older client that knows nothing of ongoing just set an end date.
		e.Ongoing = false
	}
	if e.Ongoing && e.EndDate != nil {
		return errors.New("an ongoing entry has no end date")
	}
	if e.StartDate != nil && e.EndDate != nil && e.EndDate.Before(*e.StartDate) {
		return errors.New("end_date must not be before start_date")
	}

	if in.Details != nil {
		e.Details = map[string]string{}
		for k, v := range *in.Details {
			if v = strings.TrimSpace(v); v != "" {
				e.Details[k] = v
			}
		}
	}
	if err := validateDetails(e.Subtype, e.Details); err != nil {
		return err
	}
	if in.Skills != nil {
		if e.Skills, err = cleanSkills(*in.Skills); err != nil {
			return err
		}
	}
	if in.Links != nil {
		if e.Links, err = cleanLinks(*in.Links); err != nil {
			return err
		}
	}
	if in.AcademicYear != nil {
		e.AcademicYear = blankToNil(in.AcademicYear)
		if e.AcademicYear != nil && !academicYearRe.MatchString(*e.AcademicYear) {
			return errors.New("academic_year must look like 2025-26")
		}
	}
	if in.Semester != nil {
		if *in.Semester == 0 {
			e.Semester = nil
		} else if *in.Semester < 1 || *in.Semester > 12 {
			return errors.New("semester must be between 1 and 12")
		} else {
			e.Semester = in.Semester
		}
	}
	return nil
}

func validateDetails(subtype *string, details map[string]string) error {
	if subtype == nil {
		if len(details) > 0 {
			return errors.New("details need a subtype")
		}
		return nil
	}
	allowed := subtypeDetails[*subtype]
	for k, v := range details {
		values, ok := allowed[k]
		if !ok {
			return fmt.Errorf("details.%s is not a %s field", k, *subtype)
		}
		if values != nil && !values[v] {
			return fmt.Errorf("details.%s has an unsupported value", k)
		}
		if len([]rune(v)) > maxDetailLen {
			return fmt.Errorf("details.%s must be at most %d characters", k, maxDetailLen)
		}
	}
	if d, ok := details["expiry_date"]; ok {
		if _, err := time.Parse("2006-01-02", d); err != nil {
			return errors.New("details.expiry_date must be YYYY-MM-DD")
		}
	}
	return nil
}

func cleanSkills(raw []string) ([]string, error) {
	out, seen := []string{}, map[string]bool{}
	for _, s := range raw {
		s = strings.TrimSpace(s)
		key := strings.ToLower(s)
		if s == "" || seen[key] {
			continue
		}
		if len([]rune(s)) > maxSkillLen {
			return nil, fmt.Errorf("skills must be at most %d characters each", maxSkillLen)
		}
		seen[key] = true
		out = append(out, s)
	}
	if len(out) > maxSkills {
		return nil, fmt.Errorf("at most %d skills", maxSkills)
	}
	return out, nil
}

// Links are stored, never fetched server-side.
func cleanLinks(raw []string) ([]string, error) {
	out := []string{}
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(s) > maxLinkLen {
			return nil, errors.New("links must be http(s) URLs")
		}
		out = append(out, s)
	}
	if len(out) > maxLinks {
		return nil, fmt.Errorf("at most %d links", maxLinks)
	}
	return out, nil
}

// missingForSubmit lists what a draft still needs before review. Drafts may be
// as thin as a title; the category rules only bite at submission.
func missingForSubmit(e *profileEntry) []string {
	var missing []string
	need := func(ok bool, field string) {
		if !ok {
			missing = append(missing, field)
		}
	}
	has := func(k string) bool { return e.Details[k] != "" }
	if e.Subtype == nil {
		return nil
	}
	switch *e.Subtype {
	case "project":
		need(e.Description != nil || has("problem"), "description")
		need(has("contribution"), "details.contribution")
	case "internship":
		need(e.Org != nil, "org")
		need(e.StartDate != nil, "start_date")
		need(e.EndDate != nil || e.Ongoing, "end_date")
	case "hackathon":
		need(e.Org != nil, "org")
		need(e.StartDate != nil, "start_date")
		need(has("result"), "details.result")
	case "certification":
		need(e.Org != nil, "org")
		need(e.StartDate != nil, "start_date")
	case "achievement":
		need(e.StartDate != nil, "start_date")
	}
	return missing
}

func dateArg(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format("2006-01-02")
}

// POST /api/v1/users/me/profile-entries
func (h *ProfileEntryHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in profileEntryInput
	if err := jsonx.NewDecoder(r.Body).Decode(&in); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	var e profileEntry
	if err := in.apply(&e, true); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}

	var id string
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO user_profile_entries (user_id, kind, subtype, title, org, start_date, end_date, description,
		        details, skills, links, ongoing, academic_year, semester)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id`,
		middleware.GetUserID(r), e.Kind, e.Subtype, e.Title, e.Org, dateArg(e.StartDate), dateArg(e.EndDate),
		e.Description, e.Details, e.Skills, e.Links, e.Ongoing, e.AcademicYear, e.Semester).Scan(&id)
	if err != nil {
		log.Printf("profile entry create: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": id})
}

// PATCH /api/v1/users/me/profile-entries/{entryId}
//
// Any edit returns the entry to draft. The submitted revision stays as it was
// for reviewers, and a review badge never transfers to the changed content —
// whichever client, old or new, made the edit.
func (h *ProfileEntryHandler) Update(w http.ResponseWriter, r *http.Request) {
	var in profileEntryInput
	if err := jsonx.NewDecoder(r.Body).Decode(&in); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	ctx := r.Context()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(ctx)

	var e profileEntry
	// The user_id predicate is the authorization check: a student can only
	// touch their own entries.
	err = scanEntry(tx.QueryRow(ctx,
		`SELECT `+entryColumns+` FROM user_profile_entries e WHERE e.id=$1 AND e.user_id=$2 FOR UPDATE`,
		chi.URLParam(r, "entryId"), middleware.GetUserID(r)), &e)
	if errors.Is(err, pgx.ErrNoRows) {
		middleware.NotFound(w, "profile entry")
		return
	}
	if err != nil {
		log.Printf("profile entry load: %v", err)
		middleware.InternalError(w)
		return
	}
	if err := in.apply(&e, false); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	_, err = tx.Exec(ctx,
		`UPDATE user_profile_entries
		    SET kind=$1, subtype=$2, title=$3, org=$4, start_date=$5, end_date=$6, description=$7,
		        details=$8, skills=$9, links=$10, ongoing=$11, academic_year=$12, semester=$13,
		        status='draft', updated_at=now()
		  WHERE id=$14`,
		e.Kind, e.Subtype, e.Title, e.Org, dateArg(e.StartDate), dateArg(e.EndDate), e.Description,
		e.Details, e.Skills, e.Links, e.Ongoing, e.AcademicYear, e.Semester, e.ID)
	if err != nil || tx.Commit(ctx) != nil {
		log.Printf("profile entry update: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// POST /api/v1/users/me/profile-entries/{entryId}/submit
//
// Snapshots the entry into an immutable revision for review. Retry-safe: an
// entry already submitted or reviewed with no edits since is left as is.
func (h *ProfileEntryHandler) Submit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := middleware.GetUserID(r)
	tx, err := h.db.Begin(ctx)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(ctx)

	var e profileEntry
	err = scanEntry(tx.QueryRow(ctx,
		`SELECT `+entryColumns+` FROM user_profile_entries e WHERE e.id=$1 AND e.user_id=$2 FOR UPDATE`,
		chi.URLParam(r, "entryId"), userID), &e)
	if errors.Is(err, pgx.ErrNoRows) {
		middleware.NotFound(w, "profile entry")
		return
	}
	if err != nil {
		log.Printf("profile entry submit load: %v", err)
		middleware.InternalError(w)
		return
	}

	switch e.Status {
	case "submitted", "reviewed":
		middleware.JSON(w, http.StatusOK, map[string]any{"status": e.Status, "revision": e.Revision})
		return
	case "changes_requested":
		middleware.Error(w, http.StatusConflict, "NO_CHANGES", "edit the entry before resubmitting")
		return
	}
	if missing := missingForSubmit(&e); len(missing) > 0 {
		middleware.Error(w, http.StatusBadRequest, "INCOMPLETE_ENTRY", "missing: "+strings.Join(missing, ", "))
		return
	}

	next := e.Revision + 1
	snapshot := e
	snapshot.Status, snapshot.Pinned, snapshot.Revision, snapshot.Review = "", false, next, nil
	// Institution context is captured now so the review keeps its meaning after
	// a transfer or graduation.
	_, err = tx.Exec(ctx,
		`INSERT INTO user_profile_entry_revisions (entry_id, revision, content, institution_id)
		 VALUES ($1, $2, $3, (SELECT institution_id FROM users WHERE id=$4))`,
		e.ID, next, snapshot, userID)
	if err == nil {
		_, err = tx.Exec(ctx,
			`UPDATE user_profile_entries SET status='submitted', current_revision=$1, updated_at=now() WHERE id=$2`,
			next, e.ID)
	}
	if err != nil || tx.Commit(ctx) != nil {
		log.Printf("profile entry submit: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"status": "submitted", "revision": next})
}

// GET /api/v1/users/me/profile-entries/{entryId}/reviews
//
// Student-visible review history, newest first. Private faculty notes, when
// they exist, live elsewhere and never appear here.
func (h *ProfileEntryHandler) Reviews(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(),
		`SELECT v.revision, x.decision, x.comment, u.full_name, x.created_at
		   FROM user_profile_entry_reviews x
		   JOIN user_profile_entry_revisions v ON v.id = x.revision_id
		   JOIN user_profile_entries e ON e.id = v.entry_id
		   LEFT JOIN users u ON u.id = x.reviewer_id
		  WHERE e.id=$1 AND e.user_id=$2
		  ORDER BY x.created_at DESC`,
		chi.URLParam(r, "entryId"), middleware.GetUserID(r))
	if err != nil {
		log.Printf("profile entry reviews: %v", err)
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	out := []entryReview{}
	for rows.Next() {
		var rv entryReview
		if err := rows.Scan(&rv.Revision, &rv.Decision, &rv.Comment, &rv.Reviewer, &rv.CreatedAt); err != nil {
			middleware.InternalError(w)
			return
		}
		out = append(out, rv)
	}
	middleware.JSON(w, http.StatusOK, out)
}

// PUT /api/v1/users/me/profile-entries/{entryId}/pin  {"pinned": true}
//
// Pinning is presentation, not content, so it leaves status alone.
func (h *ProfileEntryHandler) Pin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pinned bool `json:"pinned"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&in); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	// ponytail: the count check races two simultaneous pins; worst case is a
	// fourth highlight. Lock the user's rows if that ever matters.
	tag, err := h.db.Exec(r.Context(),
		`UPDATE user_profile_entries e SET pinned=$1
		  WHERE e.id=$2 AND e.user_id=$3
		    AND (NOT $1 OR e.pinned OR (SELECT count(*) FROM user_profile_entries p
		                                 WHERE p.user_id=$3 AND p.pinned) < $4)`,
		in.Pinned, chi.URLParam(r, "entryId"), middleware.GetUserID(r), maxPinned)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		h.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM user_profile_entries WHERE id=$1 AND user_id=$2)`,
			chi.URLParam(r, "entryId"), middleware.GetUserID(r)).Scan(&exists)
		if !exists {
			middleware.NotFound(w, "profile entry")
			return
		}
		middleware.Error(w, http.StatusConflict, "PIN_LIMIT", fmt.Sprintf("at most %d highlights", maxPinned))
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]bool{"pinned": in.Pinned})
}

// DELETE /api/v1/users/me/profile-entries/{entryId}
func (h *ProfileEntryHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tag, err := h.db.Exec(r.Context(),
		`DELETE FROM user_profile_entries WHERE id=$1 AND user_id=$2`,
		chi.URLParam(r, "entryId"), middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "profile entry")
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
