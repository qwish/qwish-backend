package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

const maxFeaturedQuizzes = 12

var ErrInvalidFeaturedQuizzes = errors.New("featured quizzes must be unique valid quiz ids (maximum 12)")

func (s *Service) GetFeaturedQuizzes(ctx context.Context) ([]RecommendedQuiz, error) {
	rows, err := s.db.Query(ctx, `SELECT q.id, q.title, q.description, q.question_count, q.type,
		q.domain, q.subdomain, q.published_at
		FROM featured_quizzes f JOIN quizzes q ON q.id=f.quiz_id
		WHERE q.visibility='public' AND q.status='published' AND q.deleted_at IS NULL
		AND (q.starts_at IS NULL OR q.starts_at <= now()) AND (q.ends_at IS NULL OR q.ends_at > now())
		ORDER BY f.position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []RecommendedQuiz{}
	for rows.Next() {
		var q RecommendedQuiz
		if err := rows.Scan(&q.ID, &q.Title, &q.Description, &q.QuestionCount, &q.Type, &q.Domain, &q.Subdomain, &q.PublishedAt); err != nil {
			return nil, err
		}
		q.RecommendationReason = "Featured by Qwish"
		list = append(list, q)
	}
	return list, rows.Err()
}

func (s *Service) SetFeaturedQuizzes(ctx context.Context, ids []string) error {
	if len(ids) > maxFeaturedQuizzes {
		return ErrInvalidFeaturedQuizzes
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			return ErrInvalidFeaturedQuizzes
		}
		if _, ok := seen[id]; ok {
			return ErrInvalidFeaturedQuizzes
		}
		seen[id] = struct{}{}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var valid int
	if len(ids) > 0 {
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM quizzes WHERE id = ANY($1::uuid[]) AND visibility='public' AND status='published' AND deleted_at IS NULL`, ids).Scan(&valid); err != nil {
			return err
		}
		if valid != len(ids) {
			return fmt.Errorf("%w: only published public quizzes can be featured", ErrInvalidFeaturedQuizzes)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM featured_quizzes`); err != nil {
		return err
	}
	for position, id := range ids {
		if _, err := tx.Exec(ctx, `INSERT INTO featured_quizzes (quiz_id, position) VALUES ($1, $2)`, id, position); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
