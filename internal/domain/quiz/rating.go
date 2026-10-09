package quiz

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

// Ratings are optional quiz feedback, independent of learner ability scores.
// A completed attempt establishes ownership; quiz/user uniqueness makes retries
// and repeat attempts update the same rating.
func (h *Handler) GetRating(w http.ResponseWriter, r *http.Request) {
	quizID := chi.URLParam(r, "quizId")
	if _, err := uuid.Parse(quizID); err != nil {
		middleware.BadRequest(w, "invalid quiz id")
		return
	}
	var stars *int
	err := h.svc.db.QueryRow(r.Context(), `
  SELECT qr.stars FROM quizzes q
  LEFT JOIN quiz_ratings qr ON qr.quiz_id=q.id AND qr.user_id=$2
  WHERE q.id=$1 AND q.deleted_at IS NULL
    AND EXISTS (SELECT 1 FROM quiz_attempts a
      WHERE a.quiz_id=q.id AND a.user_id=$2 AND a.status='completed')`,
		quizID, middleware.GetUserID(r)).Scan(&stars)
	if errors.Is(err, pgx.ErrNoRows) {
		middleware.NotFound(w, "completed quiz")
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"stars": stars})
}

func (h *Handler) PutRating(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Stars int `json:"stars"`
	}
	decoder := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Stars < 1 || input.Stars > 5 {
		middleware.BadRequest(w, "stars must be an integer from 1 to 5")
		return
	}
	quizID := chi.URLParam(r, "quizId")
	if _, err := uuid.Parse(quizID); err != nil {
		middleware.BadRequest(w, "invalid quiz id")
		return
	}
	var stars int
	err := h.svc.db.QueryRow(r.Context(), `
  INSERT INTO quiz_ratings (quiz_id,user_id,stars)
  SELECT q.id,$2,$3 FROM quizzes q
  WHERE q.id=$1 AND q.deleted_at IS NULL
    AND EXISTS (SELECT 1 FROM quiz_attempts a
      WHERE a.quiz_id=q.id AND a.user_id=$2 AND a.status='completed')
  ON CONFLICT (quiz_id,user_id) DO UPDATE SET stars=EXCLUDED.stars,updated_at=now()
  RETURNING stars`, quizID, middleware.GetUserID(r), input.Stars).Scan(&stars)
	if errors.Is(err, pgx.ErrNoRows) {
		middleware.NotFound(w, "completed quiz")
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]int{"stars": stars})
}

// AdminRatings exposes aggregate feedback only through a super-admin route.
// It is deliberately separate from shared quiz, teacher and institution APIs.
func (h *Handler) AdminRatings(w http.ResponseWriter, r *http.Request) {
	quizID := chi.URLParam(r, "quizId")
	if _, err := uuid.Parse(quizID); err != nil {
		middleware.BadRequest(w, "invalid quiz id")
		return
	}
	var total int
	var average *float64
	var one, two, three, four, five int
	err := h.svc.db.QueryRow(r.Context(), `
  SELECT COUNT(qr.stars), AVG(qr.stars)::float8,
    COUNT(*) FILTER (WHERE qr.stars=1), COUNT(*) FILTER (WHERE qr.stars=2),
    COUNT(*) FILTER (WHERE qr.stars=3), COUNT(*) FILTER (WHERE qr.stars=4),
    COUNT(*) FILTER (WHERE qr.stars=5)
  FROM quizzes q LEFT JOIN quiz_ratings qr ON qr.quiz_id=q.id
  WHERE q.id=$1 AND q.deleted_at IS NULL GROUP BY q.id`, quizID).
		Scan(&total, &average, &one, &two, &three, &four, &five)
	if errors.Is(err, pgx.ErrNoRows) {
		middleware.NotFound(w, "quiz")
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]any{
		"total": total, "average": average,
		"distribution": map[int]int{1: one, 2: two, 3: three, 4: four, 5: five},
	})
}
