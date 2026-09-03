package quiz

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	db *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

type Quiz struct {
	ID              string  `json:"id"`
	InstitutionID   *string `json:"institution_id,omitempty"`
	CreatedBy       string  `json:"created_by"`
	TeacherName     string  `json:"teacher_name,omitempty"`
	InstitutionName string  `json:"institution_name,omitempty"`
	Title           string  `json:"title"`
	Description     *string `json:"description,omitempty"`
	Type            string  `json:"type"`
	Visibility      string  `json:"visibility"`
	Status          string  `json:"status"`
	// QuestionCount is the number delivered to a learner. Learner-facing
	// queries must never expose the larger authoring-bank size.
	QuestionCount   int        `json:"question_count"`
	TakerCount      int        `json:"taker_count"`
	EndsAt          *time.Time `json:"ends_at,omitempty"`
	PublishedAt     *time.Time `json:"published_at,omitempty"`
	RejectionReason *string    `json:"rejection_reason,omitempty"`
	GroupID         *string    `json:"group_id,omitempty"`
	Domain          *string    `json:"domain,omitempty"`
	Subdomain       *string    `json:"subdomain,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	QuestionTypes   []string   `json:"question_types,omitempty"`
	// Peer stats over completed attempts; nil when nobody has finished the quiz.
	AvgScorePct *float64 `json:"avg_score_pct,omitempty"`
	AvgSeconds  *float64 `json:"avg_seconds,omitempty"`
	// HasPlayed is derived from the authenticated learner's completed attempts.
	// It is never accepted from a client write.
	HasPlayed bool `json:"has_played"`
}

type Question struct {
	ID               string          `json:"id"`
	QuizID           string          `json:"quiz_id"`
	Position         int             `json:"position"`
	Type             string          `json:"type"`
	Prompt           string          `json:"prompt"`
	MediaURL         *string         `json:"media_url,omitempty"`
	Options          json.RawMessage `json:"options"`
	CorrectAnswer    json.RawMessage `json:"correct_answer"`
	TimeLimitSeconds int             `json:"time_limit_seconds"`
	Clues            json.RawMessage `json:"clues,omitempty"`
}

type CreateQuizReq struct {
	Title       string     `json:"title"`
	Description *string    `json:"description"`
	Type        string     `json:"type"`
	Visibility  string     `json:"visibility"`
	GroupID     *string    `json:"group_id"`
	EndsAt      *time.Time `json:"ends_at"`
	Domain      *string    `json:"domain"`
	Subdomain   *string    `json:"subdomain"`
}

type AddQuestionReq struct {
	Position         int             `json:"position"`
	Type             string          `json:"type"`
	Prompt           string          `json:"prompt"`
	MediaURL         *string         `json:"media_url"`
	Options          json.RawMessage `json:"options"`
	CorrectAnswer    json.RawMessage `json:"correct_answer"`
	TimeLimitSeconds int             `json:"time_limit_seconds"`
	Clues            json.RawMessage `json:"clues,omitempty"`
}

// SystemUserID is the fixed user seeded for platform-authored content. It
// keeps quizzes.created_by valid while presenting "Qwish" as the author,
// rather than attributing a platform quiz to the individual administrator.
const SystemUserID = "00000000-0000-0000-0000-000000000001"

// AdminCreateQuizReq is intentionally separate from the teacher request:
// platform quizzes are public, published content with an optional delivery
// window and optional random subset of their authoring question bank.
type AdminCreateQuizReq struct {
	Title            string           `json:"title"`
	Description      *string          `json:"description"`
	Type             string           `json:"type"`
	QuestionLimit    *int             `json:"question_limit"`
	ShuffleQuestions bool             `json:"shuffle_questions"`
	StartsAt         *time.Time       `json:"starts_at"`
	EndsAt           *time.Time       `json:"ends_at"`
	Domain           *string          `json:"domain"`
	Subdomain        *string          `json:"subdomain"`
	Questions        []AddQuestionReq `json:"questions"`
}

// AdminAuthoringQuiz is the privileged authoring representation. Correct
// answers are exposed only by the super-admin route, never by learner APIs.
type AdminAuthoringQuiz struct {
	ID               string     `json:"id"`
	Title            string     `json:"title"`
	Description      *string    `json:"description"`
	Type             string     `json:"type"`
	Status           string     `json:"status"`
	QuestionLimit    *int       `json:"question_limit"`
	ShuffleQuestions bool       `json:"shuffle_questions"`
	StartsAt         *time.Time `json:"starts_at"`
	EndsAt           *time.Time `json:"ends_at"`
	Domain           *string    `json:"domain"`
	Subdomain        *string    `json:"subdomain"`
	PublishedAt      *time.Time `json:"published_at"`
	Questions        []Question `json:"questions"`
}

// attemptStatsSelect aggregates a quiz's completed attempts: how many distinct
// people took it, and the peer averages shown on the quiz detail screen.
// Correlated on q.id — only valid inside a LATERAL join over `quizzes q`.
const attemptStatsSelect = `SELECT COUNT(DISTINCT qa.user_id) AS taker_count,
		               AVG(qa.score_pct)::float8 AS avg_score_pct,
		               AVG(EXTRACT(EPOCH FROM (qa.completed_at - qa.started_at)))::float8 AS avg_seconds
		        FROM quiz_attempts qa
		        WHERE qa.quiz_id = q.id AND qa.status = 'completed'`

// studentListSelect is the SELECT/FROM prefix of the student quiz list query.
// Shared with the profiling endpoint so profiled plans match production SQL.
func studentListSelect(userArg int) string {
	return fmt.Sprintf(`SELECT q.id, q.institution_id, q.created_by, u.display_name, COALESCE(i.name, '') AS institution_name,
		        q.title, q.description, q.type, q.visibility, q.status,
		        LEAST(q.question_count, COALESCE(q.question_limit, q.question_count)) AS question_count,
		        st.taker_count, q.ends_at, q.published_at, q.group_id, q.domain, q.subdomain, q.created_at,
		        st.avg_score_pct, st.avg_seconds,
		        EXISTS (SELECT 1 FROM quiz_attempts mine
		                WHERE mine.quiz_id = q.id AND mine.user_id = $%d
		                  AND mine.status = 'completed') AS has_played
		 FROM quizzes q
		 JOIN users u ON u.id = q.created_by
		 LEFT JOIN institutions i ON i.id = u.institution_id
		 LEFT JOIN LATERAL (`+attemptStatsSelect+`) st ON TRUE
		 WHERE `, userArg)
}

// studentListWhere builds the WHERE clause and its args for the student quiz
// list. Extracted so ListForStudent and the profiling endpoint always run the
// same SQL — a profiler that drifts from the real query is worse than none.
// Invariant: the next free placeholder is always $(len(args)+1).
func studentListWhere(institutionID, quizType, saved, search, userID string) (string, []interface{}) {
	var baseWhere string
	var args []interface{}
	if institutionID != "" {
		baseWhere = `(q.institution_id = $1 OR q.visibility = 'public') AND q.status = 'published' AND q.deleted_at IS NULL AND (q.starts_at IS NULL OR q.starts_at <= now()) AND (q.ends_at IS NULL OR q.ends_at > now())`
		args = []interface{}{institutionID}
	} else {
		baseWhere = `q.visibility = 'public' AND q.status = 'published' AND q.deleted_at IS NULL AND (q.starts_at IS NULL OR q.starts_at <= now()) AND (q.ends_at IS NULL OR q.ends_at > now())`
		args = []interface{}{}
	}
	argN := len(args) + 1

	if quizType != "" {
		baseWhere += fmt.Sprintf(` AND q.type = $%d`, argN)
		args = append(args, quizType)
		argN++
	}
	if saved == "true" {
		baseWhere += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM saved_quizzes sq WHERE sq.quiz_id = q.id AND sq.user_id = $%d)`, argN)
		args = append(args, userID)
		argN++
	}
	if search != "" {
		baseWhere += fmt.Sprintf(` AND (q.title ILIKE $%d OR q.description ILIKE $%d)`, argN, argN)
		args = append(args, "%"+search+"%")
	}
	return baseWhere, args
}

func (s *Service) ListForStudent(ctx context.Context, institutionID, quizType, saved, search, userID string, page, limit int) ([]Quiz, int, error) {
	return s.ListForStudentFiltered(ctx, institutionID, quizType, saved, search, "", "", nil, nil, userID, page, limit)
}

func (s *Service) ListForStudentFiltered(ctx context.Context, institutionID, quizType, saved, search, domain, subdomain string, publishedAfter, publishedBefore *time.Time, userID string, page, limit int) ([]Quiz, int, error) {
	offset := (page - 1) * limit
	var total int

	baseWhere, args := studentListWhere(institutionID, quizType, saved, search, userID)
	if domain != "" {
		baseWhere += fmt.Sprintf(` AND q.domain = $%d`, len(args)+1)
		args = append(args, domain)
	}
	if subdomain != "" {
		baseWhere += fmt.Sprintf(` AND q.subdomain = $%d`, len(args)+1)
		args = append(args, subdomain)
	}
	if publishedAfter != nil {
		baseWhere += fmt.Sprintf(` AND q.published_at >= $%d`, len(args)+1)
		args = append(args, *publishedAfter)
	}
	if publishedBefore != nil {
		baseWhere += fmt.Sprintf(` AND q.published_at < $%d`, len(args)+1)
		args = append(args, *publishedBefore)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM quizzes q WHERE `+baseWhere, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	userArgN := len(args) + 1
	args = append(args, userID)
	argN := len(args) + 1
	args = append(args, limit, offset)
	rows, err := s.db.Query(ctx,
		studentListSelect(userArgN)+baseWhere+
			fmt.Sprintf(` ORDER BY q.published_at DESC LIMIT $%d OFFSET $%d`, argN, argN+1),
		args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	return s.scanQuizRows(rows), total, nil
}

func (s *Service) GetByID(ctx context.Context, quizID string) (*Quiz, error) {
	q := &Quiz{}
	err := s.db.QueryRow(ctx,
		`SELECT q.id, q.institution_id, q.created_by, u.display_name, COALESCE(i.name, '') AS institution_name,
		        q.title, q.description, q.type, q.visibility, q.status,
		        LEAST(q.question_count, COALESCE(q.question_limit, q.question_count)) AS question_count,
		        st.taker_count, q.ends_at, q.published_at, q.rejection_reason, q.group_id, q.domain, q.subdomain, q.created_at,
		        st.avg_score_pct, st.avg_seconds
		 FROM quizzes q
		 JOIN users u ON u.id = q.created_by
		 LEFT JOIN institutions i ON i.id = u.institution_id
		 LEFT JOIN LATERAL (`+attemptStatsSelect+`) st ON TRUE
		 WHERE q.id = $1 AND q.deleted_at IS NULL`, quizID,
	).Scan(&q.ID, &q.InstitutionID, &q.CreatedBy, &q.TeacherName, &q.InstitutionName, &q.Title, &q.Description,
		&q.Type, &q.Visibility, &q.Status, &q.QuestionCount, &q.TakerCount, &q.EndsAt, &q.PublishedAt,
		&q.RejectionReason, &q.GroupID, &q.Domain, &q.Subdomain, &q.CreatedAt,
		&q.AvgScorePct, &q.AvgSeconds)
	if err != nil {
		return nil, err
	}
	// Get distinct question types
	rows, _ := s.db.Query(ctx, `SELECT DISTINCT type FROM questions WHERE quiz_id = $1 ORDER BY type`, quizID)
	defer rows.Close()
	for rows.Next() {
		var t string
		rows.Scan(&t)
		q.QuestionTypes = append(q.QuestionTypes, t)
	}
	return q, nil
}

// ErrInvalidTaxonomy is returned when a domain/subdomain pair is unknown or
// the subdomain does not belong to the given domain.
var ErrInvalidTaxonomy = fmt.Errorf("invalid domain/subdomain")

// validateTaxonomy checks that domain (if set) exists and that subdomain (if
// set) exists and belongs to domain. Both nil is allowed (untagged quiz).
func (s *Service) validateTaxonomy(ctx context.Context, domain, subdomain *string) error {
	if domain == nil && subdomain == nil {
		return nil
	}
	if subdomain != nil {
		// A subdomain requires a matching domain.
		if domain == nil {
			return ErrInvalidTaxonomy
		}
		var ok bool
		s.db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM subdomains WHERE slug=$1 AND domain_slug=$2)`,
			*subdomain, *domain).Scan(&ok)
		if !ok {
			return ErrInvalidTaxonomy
		}
		return nil
	}
	var ok bool
	s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM domains WHERE slug=$1)`, *domain).Scan(&ok)
	if !ok {
		return ErrInvalidTaxonomy
	}
	return nil
}

func (s *Service) Create(ctx context.Context, req CreateQuizReq, userID, institutionID string) (*Quiz, error) {
	if err := s.validateTaxonomy(ctx, req.Domain, req.Subdomain); err != nil {
		return nil, err
	}
	q := &Quiz{}
	err := s.db.QueryRow(ctx,
		`INSERT INTO quizzes (institution_id, created_by, title, description, type, visibility, group_id, ends_at, domain, subdomain)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING id, institution_id, created_by, title, description, type, visibility, status, question_count, ends_at, group_id, domain, subdomain, created_at`,
		institutionID, userID, req.Title, req.Description, req.Type, req.Visibility, req.GroupID, req.EndsAt, req.Domain, req.Subdomain,
	).Scan(&q.ID, &q.InstitutionID, &q.CreatedBy, &q.Title, &q.Description, &q.Type,
		&q.Visibility, &q.Status, &q.QuestionCount, &q.EndsAt, &q.GroupID, &q.Domain, &q.Subdomain, &q.CreatedAt)
	return q, err
}

// CreateForAdmin creates and publishes a Qwish-authored quiz in a single
// transaction. The author and visibility are server-owned so a request cannot
// impersonate a person or create unexpected private platform content.
func (s *Service) CreateForAdmin(ctx context.Context, req AdminCreateQuizReq, adminID string) (*Quiz, error) {
	if err := s.validateAdminQuiz(ctx, req); err != nil {
		return nil, err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	q := &Quiz{}
	err = tx.QueryRow(ctx,
		`INSERT INTO quizzes (created_by, title, description, type, visibility, status, question_count, starts_at, ends_at, question_limit, shuffle_questions, published_at, domain, subdomain)
		 VALUES ($1, $2, $3, $4, 'public', 'published', $5, $6, $7, $8, $9, now(), $10, $11)
		 RETURNING id, created_by, title, description, type, visibility, status, question_count, ends_at, published_at, domain, subdomain, created_at`,
		SystemUserID, req.Title, req.Description, req.Type, len(req.Questions), req.StartsAt, req.EndsAt, req.QuestionLimit, req.ShuffleQuestions, req.Domain, req.Subdomain,
	).Scan(&q.ID, &q.CreatedBy, &q.Title, &q.Description, &q.Type, &q.Visibility, &q.Status,
		&q.QuestionCount, &q.EndsAt, &q.PublishedAt, &q.Domain, &q.Subdomain, &q.CreatedAt)
	if err != nil {
		return nil, err
	}

	if err := insertAdminQuestions(ctx, tx, q.ID, req.Questions); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	// Creation is privileged; retain an audit trail when the authenticated
	// principal is an admin_accounts identity. A user-table super-admin has no
	// compatible audit FK, so it is deliberately not coerced into one.
	s.auditAdminQuiz(ctx, adminID, "create_qwish_quiz", q.ID, q.Title, len(req.Questions))
	q.TeacherName = "Qwish"
	return q, nil
}

func (s *Service) validateAdminQuiz(ctx context.Context, req AdminCreateQuizReq) error {
	if len(req.Title) == 0 || len(req.Title) > 160 {
		return fmt.Errorf("title must be between 1 and 160 characters")
	}
	if len(req.Questions) == 0 || len(req.Questions) > 200 {
		return fmt.Errorf("a quiz must contain between 1 and 200 questions")
	}
	if req.Type != "knowledge_check" && req.Type != "play_and_win" {
		return fmt.Errorf("invalid quiz type")
	}
	if req.StartsAt != nil && req.EndsAt != nil && !req.EndsAt.After(*req.StartsAt) {
		return fmt.Errorf("end date must be after start date")
	}
	if req.QuestionLimit != nil && (*req.QuestionLimit < 1 || *req.QuestionLimit > len(req.Questions)) {
		return fmt.Errorf("question limit must be between 1 and the number of questions")
	}
	if err := s.validateTaxonomy(ctx, req.Domain, req.Subdomain); err != nil {
		return err
	}
	for i, question := range req.Questions {
		if len(question.Prompt) == 0 || len(question.Prompt) > 4000 {
			return fmt.Errorf("question %d needs a prompt", i+1)
		}
		if err := validateAdminQuestion(question); err != nil {
			return fmt.Errorf("question %d: %w", i+1, err)
		}
		if question.TimeLimitSeconds < 0 || question.TimeLimitSeconds > 600 {
			return fmt.Errorf("question %d has an invalid time limit", i+1)
		}
	}
	return nil
}

func validateAdminQuestion(question AddQuestionReq) error {
	choice := map[string]bool{
		"multiple_choice": true, "confidence_based": true,
		"eliminate_wrong": true, "puzzle": true, "speed_chain": true,
	}
	if choice[question.Type] {
		var options []string
		var answer string
		if json.Unmarshal(question.Options, &options) != nil || len(options) < 2 || len(options) > 8 {
			return fmt.Errorf("needs between 2 and 8 options")
		}
		if json.Unmarshal(question.CorrectAnswer, &answer) != nil || answer == "" {
			return fmt.Errorf("needs a string correct answer")
		}
		for _, option := range options {
			if option == answer {
				return nil
			}
		}
		return fmt.Errorf("correct answer must match an option")
	}
	if question.Type == "arrange_order" {
		var options, answer []string
		if json.Unmarshal(question.Options, &options) != nil || json.Unmarshal(question.CorrectAnswer, &answer) != nil || len(answer) < 2 || len(answer) > 20 || len(options) != len(answer) {
			return fmt.Errorf("needs 2 to 20 ordered items")
		}
		return nil
	}
	if question.Type == "clue_reveal" {
		var answer string
		var clues []string
		if json.Unmarshal(question.CorrectAnswer, &answer) != nil || answer == "" {
			return fmt.Errorf("needs a string answer")
		}
		if json.Unmarshal(question.Clues, &clues) != nil || len(clues) < 1 || len(clues) > 10 {
			return fmt.Errorf("needs between 1 and 10 clues")
		}
		return nil
	}
	return fmt.Errorf("has an unsupported type")
}

func insertAdminQuestions(ctx context.Context, tx pgx.Tx, quizID string, questions []AddQuestionReq) error {
	for i, question := range questions {
		options := question.Options
		if len(options) == 0 {
			options = json.RawMessage("[]")
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO questions (quiz_id, position, type, prompt, media_url, options, correct_answer, time_limit_seconds, clues)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			quizID, i+1, question.Type, question.Prompt, question.MediaURL, options, question.CorrectAnswer, question.TimeLimitSeconds, question.Clues); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) auditAdminQuiz(ctx context.Context, adminID, action, quizID, title string, count int) {
	if adminID != "" {
		var name, role string
		if err := s.db.QueryRow(ctx, `SELECT name, role FROM admin_accounts WHERE id=$1`, adminID).Scan(&name, &role); err == nil {
			s.db.Exec(ctx,
				`INSERT INTO audit_log (admin_id, admin_name, admin_role, action_type, target_type, target_id, new_value)
				 VALUES ($1,$2,$3,$4,'quiz',$5,jsonb_build_object('title',$6,'question_count',$7))`,
				adminID, name, role, action, quizID, title, count)
		}
	}
}

// GetForAdmin exposes the complete Qwish-authored quiz to the privileged
// authoring UI. The fixed creator check prevents this endpoint from leaking a
// teacher's answer key or allowing cross-owner edits.
func (s *Service) GetForAdmin(ctx context.Context, quizID string) (*AdminAuthoringQuiz, error) {
	q := &AdminAuthoringQuiz{}
	err := s.db.QueryRow(ctx, `
		SELECT id, title, description, type, status, question_limit,
		       shuffle_questions, starts_at, ends_at, domain, subdomain, published_at
		FROM quizzes
		WHERE id=$1 AND created_by=$2 AND deleted_at IS NULL`, quizID, SystemUserID).Scan(
		&q.ID, &q.Title, &q.Description, &q.Type, &q.Status, &q.QuestionLimit,
		&q.ShuffleQuestions, &q.StartsAt, &q.EndsAt, &q.Domain, &q.Subdomain, &q.PublishedAt)
	if err != nil {
		return nil, err
	}
	questions, err := s.GetQuestions(ctx, quizID)
	if err != nil {
		return nil, err
	}
	if questions == nil {
		questions = []Question{}
	}
	q.Questions = questions
	return q, nil
}

// UpdateForAdmin atomically replaces a platform quiz and its question bank.
// Existing attempts remain attached to the quiz id; historical attempt answer
// snapshots are not rewritten.
func (s *Service) UpdateForAdmin(ctx context.Context, quizID string, req AdminCreateQuizReq, adminID string) (*AdminAuthoringQuiz, error) {
	if err := s.validateAdminQuiz(ctx, req); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE quizzes
		SET title=$1, description=$2, type=$3, question_count=$4, starts_at=$5,
		    ends_at=$6, question_limit=$7, shuffle_questions=$8, domain=$9,
		    subdomain=$10, updated_at=now()
		WHERE id=$11 AND created_by=$12 AND status='published' AND deleted_at IS NULL`,
		req.Title, req.Description, req.Type, len(req.Questions), req.StartsAt, req.EndsAt,
		req.QuestionLimit, req.ShuffleQuestions, req.Domain, req.Subdomain, quizID, SystemUserID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, fmt.Errorf("published Qwish quiz not found")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM questions WHERE quiz_id=$1`, quizID); err != nil {
		return nil, err
	}
	if err := insertAdminQuestions(ctx, tx, quizID, req.Questions); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.auditAdminQuiz(ctx, adminID, "update_qwish_quiz", quizID, req.Title, len(req.Questions))
	return s.GetForAdmin(ctx, quizID)
}

func (s *Service) Update(ctx context.Context, quizID, ownerID string, req CreateQuizReq) error {
	if err := s.validateTaxonomy(ctx, req.Domain, req.Subdomain); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx,
		`UPDATE quizzes SET title=$1, description=$2, group_id=$3, ends_at=$4, domain=$5, subdomain=$6, updated_at=now()
		 WHERE id=$7 AND created_by=$8 AND status='draft' AND deleted_at IS NULL`,
		req.Title, req.Description, req.GroupID, req.EndsAt, req.Domain, req.Subdomain, quizID, ownerID)
	return err
}

// ── Taxonomy ────────────────────────────────────────────────────────────────

type SubdomainOption struct {
	Slug  string `json:"slug"`
	Label string `json:"label"`
}

type DomainOption struct {
	Slug       string            `json:"slug"`
	Label      string            `json:"label"`
	Subdomains []SubdomainOption `json:"subdomains"`
}

// GetTaxonomy returns the domain → subdomain tree for authoring dropdowns.
func (s *Service) GetTaxonomy(ctx context.Context) ([]DomainOption, error) {
	rows, err := s.db.Query(ctx, `
		SELECT d.slug, d.label, sd.slug, sd.label
		FROM domains d
		LEFT JOIN subdomains sd ON sd.domain_slug = d.slug
		ORDER BY d.sort, sd.sort`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var order []string
	byDomain := map[string]*DomainOption{}
	for rows.Next() {
		var dSlug, dLabel string
		var sdSlug, sdLabel *string
		if err := rows.Scan(&dSlug, &dLabel, &sdSlug, &sdLabel); err != nil {
			return nil, err
		}
		d, ok := byDomain[dSlug]
		if !ok {
			d = &DomainOption{Slug: dSlug, Label: dLabel}
			byDomain[dSlug] = d
			order = append(order, dSlug)
		}
		if sdSlug != nil {
			d.Subdomains = append(d.Subdomains, SubdomainOption{Slug: *sdSlug, Label: *sdLabel})
		}
	}
	out := make([]DomainOption, 0, len(order))
	for _, slug := range order {
		out = append(out, *byDomain[slug])
	}
	return out, nil
}

func (s *Service) AddQuestion(ctx context.Context, quizID, ownerID string, req AddQuestionReq) (*Question, error) {
	if req.TimeLimitSeconds < 0 || req.TimeLimitSeconds > 600 {
		return nil, fmt.Errorf("time limit must be from 0 to 600 seconds")
	}
	// Verify ownership
	var check int
	s.db.QueryRow(ctx, `SELECT 1 FROM quizzes WHERE id=$1 AND created_by=$2 AND deleted_at IS NULL`, quizID, ownerID).Scan(&check)
	if check == 0 {
		return nil, fmt.Errorf("not found or forbidden")
	}
	if duplicate, err := findNearDuplicate(ctx, s.db, req.Prompt, ""); err != nil {
		return nil, err
	} else if duplicate {
		return nil, ErrDuplicateQuestion
	}

	if req.Options == nil {
		req.Options = json.RawMessage("[]")
	}

	q := &Question{}
	err := s.db.QueryRow(ctx,
		`INSERT INTO questions (quiz_id, position, type, prompt, media_url, options, correct_answer, time_limit_seconds, clues)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 RETURNING id, quiz_id, position, type, prompt, media_url, options, correct_answer, time_limit_seconds, clues`,
		quizID, req.Position, req.Type, req.Prompt, req.MediaURL, req.Options, req.CorrectAnswer, req.TimeLimitSeconds, req.Clues,
	).Scan(&q.ID, &q.QuizID, &q.Position, &q.Type, &q.Prompt, &q.MediaURL,
		&q.Options, &q.CorrectAnswer, &q.TimeLimitSeconds, &q.Clues)
	if err != nil {
		return nil, err
	}
	storeMinhash(ctx, s.db, q.ID, q.Prompt)
	// Update question count
	s.db.Exec(ctx,
		`UPDATE quizzes SET question_count = (SELECT COUNT(*) FROM questions WHERE quiz_id=$1), updated_at=now() WHERE id=$1`, quizID)
	return q, nil
}

func (s *Service) UpdateQuestion(ctx context.Context, quizID, questionID, ownerID string, req AddQuestionReq) error {
	if req.TimeLimitSeconds < 0 || req.TimeLimitSeconds > 600 {
		return fmt.Errorf("time limit must be from 0 to 600 seconds")
	}
	var check int
	s.db.QueryRow(ctx, `SELECT 1 FROM quizzes WHERE id=$1 AND created_by=$2`, quizID, ownerID).Scan(&check)
	if check == 0 {
		return fmt.Errorf("forbidden")
	}
	if duplicate, err := findNearDuplicate(ctx, s.db, req.Prompt, questionID); err != nil {
		return err
	} else if duplicate {
		return ErrDuplicateQuestion
	}
	_, err := s.db.Exec(ctx,
		`UPDATE questions SET position=$1, type=$2, prompt=$3, media_url=$4, options=$5, correct_answer=$6, time_limit_seconds=$7, clues=$8
		 WHERE id=$9 AND quiz_id=$10`,
		req.Position, req.Type, req.Prompt, req.MediaURL, req.Options, req.CorrectAnswer, req.TimeLimitSeconds, req.Clues, questionID, quizID)
	if err == nil {
		storeMinhash(ctx, s.db, questionID, req.Prompt)
	}
	return err
}

func (s *Service) DeleteQuestion(ctx context.Context, quizID, questionID, ownerID string) error {
	var check int
	s.db.QueryRow(ctx, `SELECT 1 FROM quizzes WHERE id=$1 AND created_by=$2`, quizID, ownerID).Scan(&check)
	if check == 0 {
		return fmt.Errorf("forbidden")
	}
	_, err := s.db.Exec(ctx, `DELETE FROM questions WHERE id=$1 AND quiz_id=$2`, questionID, quizID)
	if err == nil {
		s.db.Exec(ctx,
			`UPDATE quizzes SET question_count = (SELECT COUNT(*) FROM questions WHERE quiz_id=$1), updated_at=now() WHERE id=$1`, quizID)
	}
	return err
}

func (s *Service) Publish(ctx context.Context, quizID, ownerID string) (string, error) {
	// Must have at least 1 question
	var qCount int
	s.db.QueryRow(ctx, `SELECT question_count FROM quizzes WHERE id=$1 AND created_by=$2`, quizID, ownerID).Scan(&qCount)
	if qCount == 0 {
		return "", fmt.Errorf("quiz must have at least one question")
	}

	var visibility string
	s.db.QueryRow(ctx, `SELECT visibility FROM quizzes WHERE id=$1`, quizID).Scan(&visibility)

	var newStatus string
	if visibility == "public" {
		newStatus = "pending_approval"
	} else {
		newStatus = "published"
	}

	_, err := s.db.Exec(ctx,
		`UPDATE quizzes SET status=$1, published_at=CASE WHEN $1='published' THEN now() ELSE NULL END, updated_at=now()
		 WHERE id=$2 AND created_by=$3 AND status IN ('draft','rejected')`,
		newStatus, quizID, ownerID)
	return newStatus, err
}

// Delete soft-deletes a quiz the teacher owns. Only drafts and rejected quizzes
// can be deleted by the teacher; once published, only super-admin can unpublish.
func (s *Service) Delete(ctx context.Context, quizID, ownerID string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE quizzes SET deleted_at=now(), updated_at=now()
		 WHERE id=$1 AND created_by=$2 AND status IN ('draft','rejected') AND deleted_at IS NULL`,
		quizID, ownerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("not found, already deleted, or cannot delete a published quiz")
	}
	return nil
}

// Unpublish reverts a published quiz the teacher owns back to draft so it can be
// edited. Quizzes in pending_approval cannot be unpublished by the teacher.
func (s *Service) Unpublish(ctx context.Context, quizID, ownerID string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE quizzes SET status='draft', published_at=NULL, updated_at=now()
		 WHERE id=$1 AND created_by=$2 AND status='published' AND deleted_at IS NULL`,
		quizID, ownerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("quiz is not published or not owned by you")
	}
	return nil
}

// ReorderQuestions sets the position of each question in the given order. The
// caller must own the quiz; question IDs not belonging to the quiz are ignored.
func (s *Service) ReorderQuestions(ctx context.Context, quizID, ownerID string, order []string) error {
	// Ownership check and the whole reorder in one statement. This used to be a
	// SELECT plus a transaction with one UPDATE per question — a round trip per
	// question in the quiz. The ownership CTE returns no rows when the caller
	// does not own the quiz, so the UPDATE's join matches nothing and the
	// RowsAffected check below turns that into the same error as before.
	if len(order) == 0 {
		return nil
	}
	ct, err := s.db.Exec(ctx,
		`WITH owned AS (
		   SELECT id FROM quizzes
		    WHERE id=$1 AND created_by=$2 AND deleted_at IS NULL
		 )
		 UPDATE questions q
		    SET position = o.ord
		   FROM unnest($3::uuid[]) WITH ORDINALITY AS o(qid, ord)
		  WHERE q.id = o.qid
		    AND q.quiz_id = (SELECT id FROM owned)`,
		quizID, ownerID, order)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("not found or forbidden")
	}
	return nil
}

func (s *Service) SaveQuiz(ctx context.Context, userID, quizID string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO saved_quizzes (user_id, quiz_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, userID, quizID)
	return err
}

func (s *Service) UnsaveQuiz(ctx context.Context, userID, quizID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM saved_quizzes WHERE user_id=$1 AND quiz_id=$2`, userID, quizID)
	return err
}

func (s *Service) GetQuestions(ctx context.Context, quizID string) ([]Question, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, quiz_id, position, type, prompt, media_url, options, correct_answer, time_limit_seconds, clues
		 FROM questions WHERE quiz_id=$1 ORDER BY position`, quizID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var questions []Question
	for rows.Next() {
		var q Question
		rows.Scan(&q.ID, &q.QuizID, &q.Position, &q.Type, &q.Prompt, &q.MediaURL,
			&q.Options, &q.CorrectAnswer, &q.TimeLimitSeconds, &q.Clues)
		questions = append(questions, q)
	}
	if questions == nil {
		questions = []Question{}
	}
	return questions, nil
}

func (s *Service) GetQuestionsForStudent(ctx context.Context, quizID string) ([]QuestionForStudent, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, quiz_id, position, type, prompt, media_url, options, time_limit_seconds,
		        jsonb_array_length(COALESCE(clues, '[]'::jsonb))
		 FROM questions WHERE quiz_id=$1 ORDER BY position`, quizID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var questions []QuestionForStudent
	for rows.Next() {
		var q QuestionForStudent
		rows.Scan(&q.ID, &q.QuizID, &q.Position, &q.Type, &q.Prompt, &q.MediaURL,
			&q.Options, &q.TimeLimitSeconds, &q.ClueCount)
		questions = append(questions, q)
	}
	if questions == nil {
		questions = []QuestionForStudent{}
	}
	return questions, nil
}

// QuestionForStudent strips correct_answer before sending to client.
// Clue text is withheld too — only the count ships, so the UI knows how many
// hints exist; each one must be fetched from the clue-reveal endpoint, which
// is what makes the clue penalty enforceable.
type QuestionForStudent struct {
	ID               string          `json:"id"`
	QuizID           string          `json:"quiz_id"`
	Position         int             `json:"position"`
	Type             string          `json:"type"`
	Prompt           string          `json:"prompt"`
	MediaURL         *string         `json:"media_url,omitempty"`
	Options          json.RawMessage `json:"options"`
	TimeLimitSeconds int             `json:"time_limit_seconds"`
	ClueCount        int             `json:"clue_count"`
}

func (s *Service) ListForTeacher(ctx context.Context, teacherID, statusFilter string, page, limit int) ([]Quiz, int, error) {
	offset := (page - 1) * limit
	var total int
	args := []interface{}{teacherID}
	where := `created_by = $1 AND deleted_at IS NULL`
	if statusFilter != "" {
		where += ` AND status = $2`
		args = append(args, statusFilter)
	}
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM quizzes WHERE `+where, args...).Scan(&total)
	args = append(args, limit, offset)
	n := len(args)
	rows, err := s.db.Query(ctx,
		`SELECT q.id, q.institution_id, q.created_by, '' as teacher, '' as institution_name,
		        q.title, q.description, q.type, q.visibility, q.status, q.question_count,
		        0 AS taker_count, q.ends_at, q.published_at, q.group_id, q.domain, q.subdomain, q.created_at,
		        NULL::float8, NULL::float8
		 FROM quizzes q WHERE `+where+fmt.Sprintf(` ORDER BY q.created_at DESC LIMIT $%d OFFSET $%d`, n-1, n),
		args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	return s.scanQuizRows(rows), total, nil
}

// InstitutionQuiz is a Quiz as an institution admin sees it: the admin list is
// the only view that reports an average score, so the field lives here rather
// than on Quiz where every other caller would carry a nil.
type InstitutionQuiz struct {
	Quiz
	AverageScore *float64 `json:"average_score,omitempty"`
}

// institutionListWhere builds the WHERE clause and args for the admin quiz
// roster. Same invariant as studentListWhere: the next free placeholder is
// always $(len(args)+1), because LIMIT/OFFSET are appended after it.
func institutionListWhere(institutionID, statusFilter, quizType, search string) (string, []interface{}) {
	where := `q.institution_id = $1 AND q.deleted_at IS NULL`
	args := []interface{}{institutionID}
	if statusFilter != "" {
		where += fmt.Sprintf(` AND q.status = $%d`, len(args)+1)
		args = append(args, statusFilter)
	}
	if quizType != "" {
		where += fmt.Sprintf(` AND q.type = $%d`, len(args)+1)
		args = append(args, quizType)
	}
	if search != "" {
		where += fmt.Sprintf(` AND q.title ILIKE $%d`, len(args)+1)
		args = append(args, "%"+search+"%")
	}
	return where, args
}

// ListForInstitution lists every quiz owned by the institution — including
// draft, pending and closed ones. Deliberately not ListForStudent: that query
// pins status='published' and lets public quizzes from other institutions in,
// neither of which an admin roster should do.
func (s *Service) ListForInstitution(ctx context.Context, institutionID, statusFilter, quizType, search string, page, limit int) ([]InstitutionQuiz, int, error) {
	offset := (page - 1) * limit
	where, args := institutionListWhere(institutionID, statusFilter, quizType, search)

	var total int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM quizzes q WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	n := len(args)
	rows, err := s.db.Query(ctx,
		`SELECT q.id, q.institution_id, q.created_by, u.display_name, '' AS institution_name,
		        q.title, q.description, q.type, q.visibility, q.status, q.question_count,
		        COUNT(qa.id) FILTER (WHERE qa.status = 'completed') AS taker_count,
		        AVG(qa.score_pct) FILTER (WHERE qa.status = 'completed') AS average_score,
		        q.ends_at, q.published_at, q.group_id, q.created_at
		 FROM quizzes q
		 JOIN users u ON u.id = q.created_by
		 LEFT JOIN quiz_attempts qa ON qa.quiz_id = q.id
		 WHERE `+where+
			` GROUP BY q.id, u.display_name`+
			fmt.Sprintf(` ORDER BY q.created_at DESC LIMIT $%d OFFSET $%d`, n-1, n),
		args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	quizzes := []InstitutionQuiz{}
	for rows.Next() {
		var q InstitutionQuiz
		if err := rows.Scan(&q.ID, &q.InstitutionID, &q.CreatedBy, &q.TeacherName, &q.InstitutionName,
			&q.Title, &q.Description, &q.Type, &q.Visibility, &q.Status, &q.QuestionCount,
			&q.TakerCount, &q.AverageScore, &q.EndsAt, &q.PublishedAt, &q.GroupID, &q.CreatedAt); err != nil {
			return nil, 0, err
		}
		quizzes = append(quizzes, q)
	}
	return quizzes, total, rows.Err()
}

func (s *Service) SubmitReport(ctx context.Context, reporterID, quizID string, questionID *string, reason, description string) error {
	// Auto-escalate if 3+ reports on same quiz
	var existingCount int
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM reports WHERE quiz_id=$1 AND status='open'`, quizID).Scan(&existingCount)
	priority := "normal"
	if existingCount >= 2 {
		priority = "high"
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO reports (reporter_id, quiz_id, question_id, reason, description, priority)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		reporterID, quizID, questionID, reason, description, priority)
	return err
}

func (s *Service) GetTeacherResults(ctx context.Context, quizID, teacherID string) (map[string]interface{}, error) {
	var check int
	s.db.QueryRow(ctx, `SELECT 1 FROM quizzes WHERE id=$1 AND created_by=$2`, quizID, teacherID).Scan(&check)
	if check == 0 {
		return nil, fmt.Errorf("not found or forbidden")
	}
	return s.getResults(ctx, quizID)
}

func (s *Service) getResults(ctx context.Context, quizID string) (map[string]interface{}, error) {
	// One pass over quiz_attempts for the completed count, the average score and
	// the started count — they were three separate round trips scanning the same
	// rows twice. question_count comes along as a scalar subquery.
	var totalAttempts, started, questionCount int
	var avgScore float64
	s.db.QueryRow(ctx,
		`SELECT COUNT(*) FILTER (WHERE status='completed'),
		        COALESCE(AVG(score_pct) FILTER (WHERE status='completed'), 0),
		        COUNT(*),
		        COALESCE((SELECT question_count FROM quizzes WHERE id=$1), 0)
		 FROM quiz_attempts WHERE quiz_id=$1`, quizID,
	).Scan(&totalAttempts, &avgScore, &started, &questionCount)

	completionRate := 0.0
	if started > 0 {
		completionRate = float64(totalAttempts) / float64(started) * 100
	}

	// Per-question accuracy
	rows, _ := s.db.Query(ctx,
		`SELECT qr.question_id, q.position, q.prompt,
		        COUNT(*) as total,
		        SUM(CASE WHEN qr.is_correct THEN 1 ELSE 0 END) as correct
		 FROM question_responses qr
		 JOIN questions q ON q.id = qr.question_id
		 JOIN quiz_attempts qa ON qa.id = qr.attempt_id
		 WHERE qa.quiz_id = $1 AND qa.status = 'completed'
		 GROUP BY qr.question_id, q.position, q.prompt
		 ORDER BY q.position`, quizID)
	defer rows.Close()

	var perQuestion []map[string]interface{}
	for rows.Next() {
		var qid, prompt string
		var pos, total, correct int
		rows.Scan(&qid, &pos, &prompt, &total, &correct)
		acc := 0.0
		if total > 0 {
			acc = float64(correct) / float64(total) * 100
		}
		perQuestion = append(perQuestion, map[string]interface{}{
			"question_id": qid, "position": pos, "prompt": prompt,
			"total_responses": total, "correct_count": correct, "accuracy_pct": acc,
		})
	}

	return map[string]interface{}{
		"total_attempts":  totalAttempts,
		"completion_rate": completionRate,
		"average_score":   avgScore,
		"question_count":  questionCount,
		"per_question":    perQuestion,
	}, nil
}

func (s *Service) scanQuizRows(rows interface {
	Next() bool
	Scan(...interface{}) error
	Close()
}) []Quiz {
	var quizzes []Quiz
	for rows.Next() {
		var q Quiz
		rows.Scan(&q.ID, &q.InstitutionID, &q.CreatedBy, &q.TeacherName, &q.InstitutionName, &q.Title, &q.Description,
			&q.Type, &q.Visibility, &q.Status, &q.QuestionCount, &q.TakerCount, &q.EndsAt, &q.PublishedAt, &q.GroupID, &q.Domain, &q.Subdomain, &q.CreatedAt,
			&q.AvgScorePct, &q.AvgSeconds, &q.HasPlayed)
		quizzes = append(quizzes, q)
	}
	if quizzes == nil {
		quizzes = []Quiz{}
	}
	return quizzes
}
