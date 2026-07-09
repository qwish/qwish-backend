package quiz

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	db *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

type Quiz struct {
	ID              string     `json:"id"`
	InstitutionID   *string    `json:"institution_id,omitempty"`
	CreatedBy       string     `json:"created_by"`
	TeacherName     string     `json:"teacher_name,omitempty"`
	Title           string     `json:"title"`
	Description     *string    `json:"description,omitempty"`
	Type            string     `json:"type"`
	Visibility      string     `json:"visibility"`
	Status          string     `json:"status"`
	QuestionCount   int        `json:"question_count"`
	EndsAt          *time.Time `json:"ends_at,omitempty"`
	PublishedAt     *time.Time `json:"published_at,omitempty"`
	RejectionReason *string    `json:"rejection_reason,omitempty"`
	GroupID         *string    `json:"group_id,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	QuestionTypes   []string   `json:"question_types,omitempty"`
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
	Title       string  `json:"title"`
	Description *string `json:"description"`
	Type        string  `json:"type"`
	Visibility  string  `json:"visibility"`
	GroupID     *string `json:"group_id"`
	EndsAt      *time.Time `json:"ends_at"`
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

func (s *Service) ListForStudent(ctx context.Context, institutionID, quizType, saved, search, userID string, page, limit int) ([]Quiz, int, error) {
	offset := (page - 1) * limit
	var total int

	var baseWhere string
	var args []interface{}
	if institutionID != "" {
		baseWhere = `(q.institution_id = $1 OR q.visibility = 'public') AND q.status = 'published' AND q.deleted_at IS NULL`
		args = []interface{}{institutionID}
	} else {
		baseWhere = `q.visibility = 'public' AND q.status = 'published' AND q.deleted_at IS NULL`
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
		argN++
	}

	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM quizzes q WHERE `+baseWhere, args...).Scan(&total)

	args = append(args, limit, offset)
	rows, err := s.db.Query(ctx,
		`SELECT q.id, q.institution_id, q.created_by, u.display_name, q.title, q.description,
		        q.type, q.visibility, q.status, q.question_count, q.ends_at, q.published_at, q.group_id, q.created_at
		 FROM quizzes q
		 JOIN users u ON u.id = q.created_by
		 WHERE `+baseWhere+
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
		`SELECT q.id, q.institution_id, q.created_by, u.display_name, q.title, q.description,
		        q.type, q.visibility, q.status, q.question_count, q.ends_at, q.published_at,
		        q.rejection_reason, q.group_id, q.created_at
		 FROM quizzes q
		 JOIN users u ON u.id = q.created_by
		 WHERE q.id = $1 AND q.deleted_at IS NULL`, quizID,
	).Scan(&q.ID, &q.InstitutionID, &q.CreatedBy, &q.TeacherName, &q.Title, &q.Description,
		&q.Type, &q.Visibility, &q.Status, &q.QuestionCount, &q.EndsAt, &q.PublishedAt,
		&q.RejectionReason, &q.GroupID, &q.CreatedAt)
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

func (s *Service) Create(ctx context.Context, req CreateQuizReq, userID, institutionID string) (*Quiz, error) {
	q := &Quiz{}
	err := s.db.QueryRow(ctx,
		`INSERT INTO quizzes (institution_id, created_by, title, description, type, visibility, group_id, ends_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id, institution_id, created_by, title, description, type, visibility, status, question_count, ends_at, group_id, created_at`,
		institutionID, userID, req.Title, req.Description, req.Type, req.Visibility, req.GroupID, req.EndsAt,
	).Scan(&q.ID, &q.InstitutionID, &q.CreatedBy, &q.Title, &q.Description, &q.Type,
		&q.Visibility, &q.Status, &q.QuestionCount, &q.EndsAt, &q.GroupID, &q.CreatedAt)
	return q, err
}

func (s *Service) Update(ctx context.Context, quizID, ownerID string, req CreateQuizReq) error {
	_, err := s.db.Exec(ctx,
		`UPDATE quizzes SET title=$1, description=$2, group_id=$3, ends_at=$4, updated_at=now()
		 WHERE id=$5 AND created_by=$6 AND status='draft' AND deleted_at IS NULL`,
		req.Title, req.Description, req.GroupID, req.EndsAt, quizID, ownerID)
	return err
}

func (s *Service) AddQuestion(ctx context.Context, quizID, ownerID string, req AddQuestionReq) (*Question, error) {
	// Verify ownership
	var check int
	s.db.QueryRow(ctx, `SELECT 1 FROM quizzes WHERE id=$1 AND created_by=$2 AND deleted_at IS NULL`, quizID, ownerID).Scan(&check)
	if check == 0 {
		return nil, fmt.Errorf("not found or forbidden")
	}

	if req.TimeLimitSeconds == 0 {
		req.TimeLimitSeconds = 15
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
	// Update question count
	s.db.Exec(ctx,
		`UPDATE quizzes SET question_count = (SELECT COUNT(*) FROM questions WHERE quiz_id=$1), updated_at=now() WHERE id=$1`, quizID)
	return q, nil
}

func (s *Service) UpdateQuestion(ctx context.Context, quizID, questionID, ownerID string, req AddQuestionReq) error {
	var check int
	s.db.QueryRow(ctx, `SELECT 1 FROM quizzes WHERE id=$1 AND created_by=$2`, quizID, ownerID).Scan(&check)
	if check == 0 {
		return fmt.Errorf("forbidden")
	}
	_, err := s.db.Exec(ctx,
		`UPDATE questions SET position=$1, type=$2, prompt=$3, media_url=$4, options=$5, correct_answer=$6, time_limit_seconds=$7, clues=$8
		 WHERE id=$9 AND quiz_id=$10`,
		req.Position, req.Type, req.Prompt, req.MediaURL, req.Options, req.CorrectAnswer, req.TimeLimitSeconds, req.Clues, questionID, quizID)
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
	var check int
	s.db.QueryRow(ctx, `SELECT 1 FROM quizzes WHERE id=$1 AND created_by=$2 AND deleted_at IS NULL`,
		quizID, ownerID).Scan(&check)
	if check == 0 {
		return fmt.Errorf("not found or forbidden")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for i, qid := range order {
		if _, err := tx.Exec(ctx,
			`UPDATE questions SET position=$1 WHERE id=$2 AND quiz_id=$3`, i+1, qid, quizID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
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
		`SELECT id, quiz_id, position, type, prompt, media_url, options, time_limit_seconds, clues
		 FROM questions WHERE quiz_id=$1 ORDER BY position`, quizID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var questions []QuestionForStudent
	for rows.Next() {
		var q QuestionForStudent
		rows.Scan(&q.ID, &q.QuizID, &q.Position, &q.Type, &q.Prompt, &q.MediaURL,
			&q.Options, &q.TimeLimitSeconds, &q.Clues)
		questions = append(questions, q)
	}
	if questions == nil {
		questions = []QuestionForStudent{}
	}
	return questions, nil
}

// QuestionForStudent strips correct_answer before sending to client
type QuestionForStudent struct {
	ID               string          `json:"id"`
	QuizID           string          `json:"quiz_id"`
	Position         int             `json:"position"`
	Type             string          `json:"type"`
	Prompt           string          `json:"prompt"`
	MediaURL         *string         `json:"media_url,omitempty"`
	Options          json.RawMessage `json:"options"`
	TimeLimitSeconds int             `json:"time_limit_seconds"`
	Clues            json.RawMessage `json:"clues,omitempty"`
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
		`SELECT id, institution_id, created_by, '' as teacher, title, description, type, visibility, status, question_count, ends_at, published_at, group_id, created_at
		 FROM quizzes WHERE `+where+fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, n-1, n),
		args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	return s.scanQuizRows(rows), total, nil
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
	var totalAttempts int
	var avgScore float64
	s.db.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(AVG(score_pct),0) FROM quiz_attempts WHERE quiz_id=$1 AND status='completed'`, quizID,
	).Scan(&totalAttempts, &avgScore)

	var questionCount int
	s.db.QueryRow(ctx, `SELECT question_count FROM quizzes WHERE id=$1`, quizID).Scan(&questionCount)

	completionRate := 0.0
	// simplified: all who started vs completed
	var started int
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM quiz_attempts WHERE quiz_id=$1`, quizID).Scan(&started)
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

func (s *Service) scanQuizRows(rows interface{ Next() bool; Scan(...interface{}) error; Close() }) []Quiz {
	var quizzes []Quiz
	for rows.Next() {
		var q Quiz
		rows.Scan(&q.ID, &q.InstitutionID, &q.CreatedBy, &q.TeacherName, &q.Title, &q.Description,
			&q.Type, &q.Visibility, &q.Status, &q.QuestionCount, &q.EndsAt, &q.PublishedAt, &q.GroupID, &q.CreatedAt)
		quizzes = append(quizzes, q)
	}
	if quizzes == nil {
		quizzes = []Quiz{}
	}
	return quizzes
}
