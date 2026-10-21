package quiz

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/middleware"
)

// leaderboardSize bounds the ranked list; the caller's own row rides along
// even when it falls outside it.
const leaderboardSize = 50

var ErrNotPlayAndWin = errors.New("leaderboards are only available for play & win quizzes")

type LeaderboardEntry struct {
	Rank            int     `json:"rank"`
	UserID          string  `json:"user_id"`
	DisplayName     string  `json:"display_name"`
	InstitutionName *string `json:"institution_name,omitempty"`
	ScorePct        float64 `json:"score_pct"`
	TotalCorrect    int     `json:"total_correct"`
	TotalQuestions  int     `json:"total_questions"`
	DurationSeconds int     `json:"duration_seconds"`
	AvatarPersona   *string `json:"avatar_persona"`
	AvatarTier      int     `json:"avatar_tier"`
}

type QuizLeaderboard struct {
	Entries []LeaderboardEntry `json:"entries"`
	Me      *LeaderboardEntry  `json:"me"`
	Total   int                `json:"total"`
}

// Leaderboard ranks one play-and-win quiz: higher score first, then the faster
// finish; learners tied on both share a rank. Eligibility matches the global
// board (active, non-deleted students). Play-and-win allows one completed
// attempt, but that is an application check, so each learner's best attempt counts.
func (s *Service) Leaderboard(ctx context.Context, quizID, viewerID string) (*QuizLeaderboard, error) {
	var quizType string
	if err := s.db.QueryRow(ctx, `SELECT type FROM quizzes WHERE id=$1 AND deleted_at IS NULL`, quizID).Scan(&quizType); err != nil {
		return nil, err
	}
	if quizType != "play_and_win" {
		return nil, ErrNotPlayAndWin
	}
	rows, err := s.db.Query(ctx, `
		WITH best AS (
		  SELECT DISTINCT ON (qa.user_id) qa.user_id, COALESCE(qa.score_pct, 0) AS score_pct,
		         COALESCE(qa.total_correct, 0) AS total_correct, qa.total_questions, qa.completed_at,
		         GREATEST(EXTRACT(EPOCH FROM qa.completed_at - qa.started_at), 0)::int AS secs
		    FROM quiz_attempts qa JOIN users u ON u.id = qa.user_id
		   WHERE qa.quiz_id = $1 AND qa.status = 'completed' AND qa.completed_at IS NOT NULL
		     AND u.status = 'active' AND u.role = 'student' AND u.deleted_at IS NULL
		   ORDER BY qa.user_id, score_pct DESC, secs, qa.completed_at
		), ranked AS (
		  SELECT b.*, RANK() OVER (ORDER BY score_pct DESC, secs) AS rank,
		         ROW_NUMBER() OVER (ORDER BY score_pct DESC, secs, completed_at, user_id) AS n,
		         COUNT(*) OVER () AS total
		    FROM best b
		)
		SELECT r.rank, r.user_id, u.display_name, i.name, r.score_pct, r.total_correct, r.total_questions,
		       r.secs, u.avatar_persona, u.avatar_tier, r.n, r.total
		  FROM ranked r JOIN users u ON u.id = r.user_id LEFT JOIN institutions i ON i.id = u.institution_id
		 WHERE r.n <= $2 OR r.user_id = $3::uuid
		 ORDER BY r.n`, quizID, leaderboardSize, nullableUUID(viewerID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	board := &QuizLeaderboard{Entries: []LeaderboardEntry{}}
	for rows.Next() {
		var e LeaderboardEntry
		var n int
		if err := rows.Scan(&e.Rank, &e.UserID, &e.DisplayName, &e.InstitutionName, &e.ScorePct, &e.TotalCorrect,
			&e.TotalQuestions, &e.DurationSeconds, &e.AvatarPersona, &e.AvatarTier, &n, &board.Total); err != nil {
			return nil, err
		}
		if e.UserID == viewerID {
			me := e
			board.Me = &me
		}
		if n <= leaderboardSize {
			board.Entries = append(board.Entries, e)
		}
	}
	return board, rows.Err()
}

// GET /api/v1/quizzes/:quizId/leaderboard
func (h *Handler) Leaderboard(w http.ResponseWriter, r *http.Request) {
	quizID := chi.URLParam(r, "quizId")
	if !h.svc.CanView(r.Context(), quizID, middleware.GetUserID(r), middleware.GetInstitutionID(r), middleware.GetRole(r)) {
		middleware.NotFound(w, "quiz")
		return
	}
	board, err := h.svc.Leaderboard(r.Context(), quizID, middleware.GetUserID(r))
	if errors.Is(err, ErrNotPlayAndWin) {
		middleware.BadRequest(w, err.Error())
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, board)
}
