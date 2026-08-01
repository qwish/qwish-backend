package user

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

// Education and skills already have their own tables from 003_profile_features.
// Everything else on a student's CV shares one shape, so it shares one table.
var profileEntryKinds = map[string]bool{
	"experience": true, "certification": true, "achievement": true, "course": true,
}

func validKind(kind string) bool { return profileEntryKinds[kind] }

type ProfileEntryHandler struct{ db *pgxpool.Pool }

func NewProfileEntryHandler(db *pgxpool.Pool) *ProfileEntryHandler {
	return &ProfileEntryHandler{db: db}
}

type profileEntry struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Title       string     `json:"title"`
	Org         *string    `json:"org,omitempty"`
	StartDate   *time.Time `json:"start_date,omitempty"`
	EndDate     *time.Time `json:"end_date,omitempty"`
	Description *string    `json:"description,omitempty"`
}

// GET /api/v1/users/me/profile-entries?kind=experience
func (h *ProfileEntryHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	kind := r.URL.Query().Get("kind")
	if kind != "" && !validKind(kind) {
		middleware.BadRequest(w, "unknown kind")
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, kind, title, org, start_date, end_date, description
		   FROM user_profile_entries
		  WHERE user_id=$1 AND ($2='' OR kind=$2)
		  ORDER BY COALESCE(start_date, '1900-01-01') DESC, created_at DESC`, userID, kind)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	entries := []profileEntry{}
	for rows.Next() {
		var e profileEntry
		rows.Scan(&e.ID, &e.Kind, &e.Title, &e.Org, &e.StartDate, &e.EndDate, &e.Description)
		entries = append(entries, e)
	}
	middleware.JSON(w, http.StatusOK, entries)
}

type profileEntryInput struct {
	Kind        string  `json:"kind"`
	Title       string  `json:"title"`
	Org         *string `json:"org"`
	StartDate   *string `json:"start_date"`
	EndDate     *string `json:"end_date"`
	Description *string `json:"description"`
}

// POST /api/v1/users/me/profile-entries
func (h *ProfileEntryHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in profileEntryInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if !validKind(in.Kind) {
		middleware.BadRequest(w, "kind must be one of experience, certification, achievement, course")
		return
	}
	if in.Title == "" {
		middleware.BadRequest(w, "title is required")
		return
	}

	var id string
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO user_profile_entries (user_id, kind, title, org, start_date, end_date, description)
		 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		middleware.GetUserID(r), in.Kind, in.Title, in.Org, in.StartDate, in.EndDate, in.Description).Scan(&id)
	if err != nil {
		log.Printf("profile entry create: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": id})
}

// PATCH /api/v1/users/me/profile-entries/{entryId}
func (h *ProfileEntryHandler) Update(w http.ResponseWriter, r *http.Request) {
	var in profileEntryInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if in.Title == "" {
		middleware.BadRequest(w, "title is required")
		return
	}

	// The user_id predicate is the authorization check: a student can only
	// touch their own entries.
	tag, err := h.db.Exec(r.Context(),
		`UPDATE user_profile_entries
		    SET title=$1, org=$2, start_date=$3, end_date=$4, description=$5
		  WHERE id=$6 AND user_id=$7`,
		in.Title, in.Org, in.StartDate, in.EndDate, in.Description,
		chi.URLParam(r, "entryId"), middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "profile entry")
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "updated"})
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
