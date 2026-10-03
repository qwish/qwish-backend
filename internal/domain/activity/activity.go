// Package activity implements teacher-authored forms and identified quick polls
// (plans/teacher-forms-events-and-polls.md, Phase 1).
//
// Organisers: the author, plus institution admins for anything in their
// institution. Teachers publish only to classes they teach; institution-wide
// publication is for institution admins. Students see an activity while they
// are in its audience, and always keep access to their own response.
package activity

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	qdb "github.com/qwish/backend/internal/db"
	"github.com/qwish/backend/internal/domain/notification"
	"github.com/qwish/backend/internal/middleware"
)

// IdentityDisclosure is shown before every submission in the first release.
const IdentityDisclosure = "Your name and class are shared with the organisers along with your answers."

type Handler struct {
	db    *pgxpool.Pool
	notif *notification.Service
}

func NewHandler(db *pgxpool.Pool, notif *notification.Service) *Handler {
	return &Handler{db: db, notif: notif}
}

// OrganiserRoutes mounts the authoring and response-management API. Mount it
// under a role-guarded route (teacher or institution_admin).
func (h *Handler) OrganiserRoutes(r chi.Router) {
	r.Get("/activity-templates", h.Templates)
	r.Get("/activities", h.List)
	r.Post("/activities", h.Create)
	r.Get("/activities/{id}", h.Get)
	r.Put("/activities/{id}", h.Update)
	r.Get("/activities/{id}/audience-estimate", h.AudienceEstimate)
	r.Post("/activities/{id}/publish", h.Publish)
	r.Post("/activities/{id}/close", h.Close)
	r.Post("/activities/{id}/archive", h.Archive)
	r.Post("/activities/{id}/duplicate", h.Duplicate)
	r.Post("/activities/{id}/remind", h.Remind)
	r.Get("/activities/{id}/responses", h.Responses)
	r.Get("/activities/{id}/responses.csv", h.ExportCSV)
}

// StudentRoutes mounts the student API. Mount it behind RequireRole("student").
func (h *Handler) StudentRoutes(r chi.Router) {
	r.Get("/activities", h.Feed)
	r.Get("/activities/{id}", h.StudentGet)
	r.Put("/activities/{id}/response/draft", h.SaveDraft)
	r.Post("/activities/{id}/response", h.Submit)
	r.Delete("/activities/{id}/response", h.Withdraw)
}

// stateSQL is the effective state of a non-draft activity, always on server time.
const stateSQL = `CASE WHEN a.status='draft' THEN 'draft' WHEN a.status='archived' THEN 'archived'
	WHEN a.status='closed' OR (a.closes_at IS NOT NULL AND a.closes_at<=now()) THEN 'closed'
	WHEN a.opens_at IS NOT NULL AND a.opens_at>now() THEN 'scheduled' ELSE 'open' END`

// eligibleSQL: student $2 is in the current audience of activity a in institution $3.
const eligibleSQL = `(a.institution_id=$3 AND (a.institution_wide OR EXISTS(
	SELECT 1 FROM activity_audience_groups ag
	JOIN groups g ON g.id=ag.group_id AND g.archived_at IS NULL
	JOIN group_students gs ON gs.group_id=ag.group_id
	WHERE ag.activity_id=a.id AND gs.user_id=$2)))`

// audienceCountSQL counts students currently in activity $1's audience; $2 is the institution.
var audienceCountSQL = `SELECT count(*) FROM users u, activities a
	WHERE a.id=$1 AND ` + qdb.LiveMemberSQL("u.id", "$2") + ` AND u.role='student' AND u.deleted_at IS NULL
	AND (a.institution_wide OR EXISTS(SELECT 1 FROM activity_audience_groups ag
		JOIN groups g ON g.id=ag.group_id AND g.archived_at IS NULL
		JOIN group_students gs ON gs.group_id=ag.group_id
		WHERE ag.activity_id=a.id AND gs.user_id=u.id))`

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// audit records an organiser action. Pass the transaction when there is one so
// the record commits or rolls back with the change it describes.
func audit(ctx context.Context, q execer, activityID, actorID, action string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	_, err := q.Exec(ctx, `INSERT INTO activity_audit_events(activity_id,actor_id,action,detail) VALUES ($1,$2,$3,$4)`,
		activityID, actorID, action, detail)
	return err
}

// QuestionCounts are aggregate option counts for one choice question.
type QuestionCounts struct {
	QuestionID string         `json:"question_id"`
	Counts     map[string]int `json:"counts"`
}

// Results are aggregate counts over submitted responses. For multiple choice,
// percentages computed from them may sum above 100%.
type Results struct {
	Respondents int              `json:"respondents"`
	Questions   []QuestionCounts `json:"questions"`
}

func (h *Handler) results(ctx context.Context, activityID string, qs []Question) (Results, error) {
	out := Results{Questions: []QuestionCounts{}}
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM activity_responses WHERE activity_id=$1 AND status='submitted'`,
		activityID).Scan(&out.Respondents); err != nil {
		return out, err
	}
	for _, q := range qs {
		if !isChoice(q.Type) {
			continue
		}
		qc := QuestionCounts{QuestionID: q.ID, Counts: map[string]int{}}
		for _, o := range q.Options {
			qc.Counts[o.ID] = 0
		}
		rows, err := h.db.Query(ctx, `SELECT opt, count(*) FROM activity_responses r,
			LATERAL jsonb_array_elements_text(CASE jsonb_typeof(r.answers->$2) WHEN 'array' THEN r.answers->$2
				WHEN 'string' THEN jsonb_build_array(r.answers->$2) ELSE '[]'::jsonb END) opt
			WHERE r.activity_id=$1 AND r.status='submitted' GROUP BY opt`, activityID, q.ID)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var opt string
			var n int
			if err := rows.Scan(&opt, &n); err != nil {
				rows.Close()
				return out, err
			}
			if _, ok := qc.Counts[opt]; ok {
				qc.Counts[opt] = n
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return out, err
		}
		out.Questions = append(out.Questions, qc)
	}
	return out, nil
}

func parseQuestions(raw []byte) ([]Question, error) {
	qs := []Question{}
	if len(raw) == 0 {
		return qs, nil
	}
	err := json.Unmarshal(raw, &qs)
	return qs, err
}

func isNoRows(err error) bool { return err == pgx.ErrNoRows }

func notFound(w http.ResponseWriter) { middleware.NotFound(w, "activity") }
