package recruiter

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct{ db *pgxpool.Pool }

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

type membership struct {
	OrganisationID string `json:"organisation_id"`
	Organisation   string `json:"organisation_name"`
	Role           string `json:"role"`
}

func (h *Handler) member(r *http.Request) (membership, error) {
	var m membership
	err := h.db.QueryRow(r.Context(), `
		SELECT o.id, o.name, m.role
		  FROM recruiter_memberships m
		  JOIN recruiter_organisations o ON o.id=m.organisation_id AND o.status='active'
		 WHERE m.user_id=$1 AND m.status='active'
		 ORDER BY m.created_at LIMIT 1`, middleware.GetUserID(r)).Scan(&m.OrganisationID, &m.Organisation, &m.Role)
	return m, err
}

func (h *Handler) requireMember(w http.ResponseWriter, r *http.Request) (membership, bool) {
	m, err := h.member(r)
	if errors.Is(err, pgx.ErrNoRows) {
		middleware.Error(w, http.StatusForbidden, "RECRUITER_MEMBERSHIP_REQUIRED", "an active recruiter organisation membership is required")
		return m, false
	}
	if err != nil {
		log.Printf("recruiter membership lookup failed: %v", err)
		middleware.InternalError(w)
		return m, false
	}
	return m, true
}

func (h *Handler) Context(w http.ResponseWriter, r *http.Request) {
	m, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	middleware.JSON(w, http.StatusOK, m)
}

type candidate struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Institution     string         `json:"institution"`
	Education       string         `json:"education"`
	QwishScore      float64        `json:"qwish_score"`
	Percentile      int            `json:"percentile"`
	AssessmentCount int            `json:"assessment_count"`
	LastAssessedAt  *time.Time     `json:"last_assessed_at"`
	Shortlisted     bool           `json:"shortlisted"`
	Domains         []domainSignal `json:"domains"`
}

type domainSignal struct {
	Slug     string  `json:"slug"`
	Label    string  `json:"label"`
	Score    float64 `json:"score"`
	Attempts int     `json:"attempts"`
}

const scoredCandidates = `WITH attempt_stats AS (
	SELECT user_id, COALESCE(SUM(total_correct),0)::float8 total_correct,
	       COALESCE(SUM(total_questions),0)::float8 total_questions,
	       COUNT(*)::float8 completed, MAX(completed_at) last_assessed_at
	  FROM quiz_attempts WHERE status='completed' GROUP BY user_id
), response_stats AS (
	SELECT a.user_id, COALESCE(SUM(q.difficulty),0)::float8 total_difficulty,
	       COALESCE(SUM(q.difficulty) FILTER (WHERE qr.is_correct),0)::float8 correct_difficulty,
	       COALESCE(AVG(CASE WHEN qr.time_taken_ms < 1000 THEN .1
	         WHEN qr.time_taken_ms <= (q.time_limit_seconds*1000)/3.0 THEN 1.0
	         ELSE GREATEST((q.time_limit_seconds*1000.0-qr.time_taken_ms)/
	           NULLIF(q.time_limit_seconds*1000.0-q.time_limit_seconds*1000.0/3.0,0),.1)
	       END) FILTER (WHERE qr.is_correct AND qr.time_taken_ms IS NOT NULL),0)::float8 speed
	  FROM question_responses qr JOIN questions q ON q.id=qr.question_id
	  JOIN quiz_attempts a ON a.id=qr.attempt_id AND a.status='completed' GROUP BY a.user_id
), scored AS (
	SELECT u.id, u.display_name, COALESCE(i.name,'') AS institution,
	       COALESCE((SELECT concat_ws(', ', NULLIF(e.degree,''), NULLIF(e.field,''))
	                   FROM user_education e WHERE e.user_id=u.id
	                  ORDER BY e.is_current DESC, e.end_year DESC NULLS FIRST LIMIT 1),'') AS education,
	       LEAST(900, GREATEST(100, 100 + 8 * (
	         CASE WHEN COALESCE(a.total_questions,0)>0 THEN ((a.total_correct+5)/(a.total_questions+10))*50 ELSE 0 END +
	         CASE WHEN COALESCE(rs.total_difficulty,0)>0 THEN rs.correct_difficulty/rs.total_difficulty*20 ELSE 0 END +
	         (1-EXP(-COALESCE(u.current_streak,0)::float8/14))*15 + COALESCE(rs.speed,0)*10 +
	         (1-EXP(-COALESCE(a.completed,0)/20))*5)))::float8 qwish_score,
	       COALESCE(a.completed,0)::int AS completed, a.last_assessed_at
	  FROM users u LEFT JOIN institutions i ON i.id=u.institution_id
	  LEFT JOIN attempt_stats a ON a.user_id=u.id LEFT JOIN response_stats rs ON rs.user_id=u.id
	 WHERE u.status='active' AND u.role='student' AND u.recruiter_visible=true
), ranked AS (
	SELECT *, CEIL(PERCENT_RANK() OVER (ORDER BY qwish_score)*99+1)::int percentile FROM scored
) `

func parseLimit(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 25
	}
	if n > 100 {
		return 100
	}
	return n
}

func (h *Handler) Candidates(w http.ResponseWriter, r *http.Request) {
	m, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"))
	minScore, err := strconv.Atoi(r.URL.Query().Get("min_score"))
	if err != nil {
		minScore = 100
	}
	if minScore < 100 || minScore > 900 {
		middleware.BadRequest(w, "min_score must be between 100 and 900")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) > 120 {
		middleware.BadRequest(w, "q must be at most 120 characters")
		return
	}
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	if len(domain) > 80 {
		middleware.BadRequest(w, "domain must be at most 80 characters")
		return
	}

	rows, err := h.db.Query(r.Context(), scoredCandidates+`
		SELECT r.id, r.display_name, r.institution, r.education, r.qwish_score, r.percentile,
		       r.completed, r.last_assessed_at,
		       EXISTS (SELECT 1 FROM recruiter_candidate_states s WHERE s.organisation_id=$1 AND s.candidate_id=r.id AND s.stage='shortlisted')
		  FROM ranked r
		 WHERE r.qwish_score >= $2
		   AND ($3='' OR r.display_name ILIKE '%'||$3||'%' OR r.institution ILIKE '%'||$3||'%' OR r.education ILIKE '%'||$3||'%'
		        OR EXISTS (SELECT 1 FROM user_skills us WHERE us.user_id=r.id AND us.skill_name ILIKE '%'||$3||'%'))
		   AND ($4='' OR EXISTS (SELECT 1 FROM quiz_attempts qa JOIN quizzes qz ON qz.id=qa.quiz_id
		                           WHERE qa.user_id=r.id AND qa.status='completed' AND qz.domain=$4))
		 ORDER BY r.qwish_score DESC, r.id LIMIT $5`, m.OrganisationID, minScore, query, domain, limit)
	if err != nil {
		log.Printf("recruiter candidates: list query failed: %v", err)
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	out := make([]candidate, 0)
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.ID, &c.Name, &c.Institution, &c.Education, &c.QwishScore, &c.Percentile, &c.AssessmentCount, &c.LastAssessedAt, &c.Shortlisted); err != nil {
			log.Printf("recruiter candidates: list scan failed: %v", err)
			middleware.InternalError(w)
			return
		}
		c.Domains, err = h.domains(r, c.ID)
		if err != nil {
			log.Printf("recruiter candidates: domain query failed: %v", err)
			middleware.InternalError(w)
			return
		}
		out = append(out, c)
	}
	if rows.Err() != nil {
		log.Printf("recruiter candidates: row iteration failed: %v", rows.Err())
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"candidates": out, "count": len(out), "source": "completed_verified_assessments"})
}

func (h *Handler) domains(r *http.Request, userID string) ([]domainSignal, error) {
	rows, err := h.db.Query(r.Context(), `SELECT COALESCE(q.domain,'general'), COALESCE(d.label,'General'),
		ROUND(COALESCE(AVG(a.score_pct),0),1)::float8, COUNT(*)
		FROM quiz_attempts a JOIN quizzes q ON q.id=a.quiz_id LEFT JOIN domains d ON d.slug=q.domain
		WHERE a.user_id=$1 AND a.status='completed' GROUP BY q.domain,d.label,d.sort ORDER BY d.sort NULLS LAST`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domainSignal, 0)
	for rows.Next() {
		var d domainSignal
		if err := rows.Scan(&d.Slug, &d.Label, &d.Score, &d.Attempts); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (h *Handler) Candidate(w http.ResponseWriter, r *http.Request) {
	m, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "candidateId")
	var c candidate
	err := h.db.QueryRow(r.Context(), scoredCandidates+`SELECT r.id,r.display_name,r.institution,r.education,r.qwish_score,r.percentile,r.completed,r.last_assessed_at,
		EXISTS(SELECT 1 FROM recruiter_candidate_states s WHERE s.organisation_id=$1 AND s.candidate_id=r.id AND s.stage='shortlisted') FROM ranked r WHERE r.id=$2`, m.OrganisationID, id).
		Scan(&c.ID, &c.Name, &c.Institution, &c.Education, &c.QwishScore, &c.Percentile, &c.AssessmentCount, &c.LastAssessedAt, &c.Shortlisted)
	if errors.Is(err, pgx.ErrNoRows) {
		middleware.NotFound(w, "candidate")
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	c.Domains, err = h.domains(r, c.ID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	_, _ = h.db.Exec(r.Context(), `INSERT INTO recruiter_audit_events(organisation_id,actor_id,action,target_type,target_id) VALUES($1,$2,'candidate.view','candidate',$3)`, m.OrganisationID, middleware.GetUserID(r), c.ID)
	middleware.JSON(w, http.StatusOK, c)
}

func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	m, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	type overview struct {
		Organisation string `json:"organisation"`
		ActiveRoles  int    `json:"active_roles"`
		Discoverable int    `json:"discoverable_candidates"`
		Shortlisted  int    `json:"shortlisted"`
		Interviewing int    `json:"interviewing"`
		Hired        int    `json:"hired"`
	}
	var o overview
	o.Organisation = m.Organisation
	err := h.db.QueryRow(r.Context(), `SELECT
		(SELECT COUNT(*) FROM recruiter_roles WHERE organisation_id=$1 AND status='open'),
		(SELECT COUNT(*) FROM users WHERE status='active' AND role='student' AND recruiter_visible=true),
		(SELECT COUNT(*) FROM recruiter_candidate_states WHERE organisation_id=$1 AND stage='shortlisted'),
		(SELECT COUNT(*) FROM recruiter_candidate_states WHERE organisation_id=$1 AND stage='interview'),
		(SELECT COUNT(*) FROM recruiter_candidate_states WHERE organisation_id=$1 AND stage='hired')`, m.OrganisationID).Scan(&o.ActiveRoles, &o.Discoverable, &o.Shortlisted, &o.Interviewing, &o.Hired)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, o)
}

func canWrite(role string) bool {
	return role == "owner" || role == "admin" || role == "recruiter" || role == "hiring_manager"
}

func (h *Handler) Shortlist(w http.ResponseWriter, r *http.Request) {
	m, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	if !canWrite(m.Role) {
		middleware.Forbidden(w)
		return
	}
	id := chi.URLParam(r, "candidateId")
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(r.Context())
	var visible bool
	if err = tx.QueryRow(r.Context(), `SELECT recruiter_visible FROM users WHERE id=$1 AND status='active'`, id).Scan(&visible); errors.Is(err, pgx.ErrNoRows) {
		middleware.NotFound(w, "candidate")
		return
	} else if err != nil {
		middleware.InternalError(w)
		return
	}
	if !visible {
		middleware.Error(w, http.StatusForbidden, "CANDIDATE_NOT_DISCOVERABLE", "candidate has not consented to recruiter discovery")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO recruiter_candidate_states(organisation_id,candidate_id,role_id,stage,updated_by) VALUES($1,$2,NULL,'shortlisted',$3)
		ON CONFLICT (organisation_id,candidate_id) WHERE role_id IS NULL DO UPDATE SET stage='shortlisted',updated_by=EXCLUDED.updated_by,updated_at=now()`, m.OrganisationID, id, middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO recruiter_audit_events(organisation_id,actor_id,action,target_type,target_id) VALUES($1,$2,'candidate.shortlist','candidate',$3)`, m.OrganisationID, middleware.GetUserID(r), id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]bool{"shortlisted": true})
}

func (h *Handler) RemoveShortlist(w http.ResponseWriter, r *http.Request) {
	m, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	if !canWrite(m.Role) {
		middleware.Forbidden(w)
		return
	}
	id := chi.URLParam(r, "candidateId")
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `DELETE FROM recruiter_candidate_states WHERE organisation_id=$1 AND candidate_id=$2 AND role_id IS NULL AND stage='shortlisted'`, m.OrganisationID, id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	metadata, _ := json.Marshal(map[string]string{"previous_stage": "shortlisted"})
	_, err = tx.Exec(r.Context(), `INSERT INTO recruiter_audit_events(organisation_id,actor_id,action,target_type,target_id,metadata) VALUES($1,$2,'candidate.unshortlist','candidate',$3,$4)`, m.OrganisationID, middleware.GetUserID(r), id, metadata)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]bool{"shortlisted": false})
}
