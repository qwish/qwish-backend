package user

import (
	"context"
	"net/http"

	"github.com/qwish/backend/internal/middleware"
)

// StoryStats feeds the app's shareable "performance story": all-time totals
// over completed attempts plus the three most-played domains.
type StoryStats struct {
	Count      int             `json:"count"`
	Questions  int             `json:"questions"`
	Correct    int             `json:"correct"`
	Seconds    float64         `json:"seconds"` // active answering time
	Perfect    int             `json:"perfect"`
	Days       int             `json:"days"`
	BestStreak int             `json:"best_streak"`
	FirstDate  *string         `json:"first_date"` // YYYY-MM-DD, null before any attempt
	LastDate   *string         `json:"last_date"`
	Categories []StoryCategory `json:"categories"`
}

type StoryCategory struct {
	Name      string `json:"name"`
	Plays     int    `json:"plays"`
	Questions int    `json:"questions"`
	Correct   int    `json:"correct"`
}

// One row per completed attempt. Active time is the sum of per-question
// answer times, not completed_at - started_at, which counts idle tabs.
const storyAttemptsCTE = `
WITH a AS (
  SELECT qa.completed_at,
         COALESCE(qa.total_questions, 0) AS q,
         COALESCE(qa.total_correct, 0)   AS c,
         COALESCE(d.label, 'General')    AS cat,
         COALESCE((SELECT SUM(r.time_taken_ms) FROM question_responses r
                   WHERE r.attempt_id = qa.id), 0) / 1000.0 AS secs
    FROM quiz_attempts qa
    JOIN quizzes z ON z.id = qa.quiz_id
    LEFT JOIN domains d ON d.slug = z.domain
   WHERE qa.user_id = $1 AND qa.status = 'completed' AND qa.completed_at IS NOT NULL
)`

func (s *Service) GetStoryStats(ctx context.Context, userID string) (*StoryStats, error) {
	st := &StoryStats{Categories: []StoryCategory{}}
	// ponytail: days bucket by UTC date; use the institution timezone if
	// learners near midnight notice.
	err := s.db.QueryRow(ctx, storyAttemptsCTE+`
		SELECT COUNT(*), COALESCE(SUM(q), 0), COALESCE(SUM(c), 0), COALESCE(SUM(secs), 0),
		       COUNT(*) FILTER (WHERE q > 0 AND c = q),
		       COUNT(DISTINCT (completed_at AT TIME ZONE 'UTC')::date),
		       to_char(MIN(completed_at AT TIME ZONE 'UTC'), 'YYYY-MM-DD'),
		       to_char(MAX(completed_at AT TIME ZONE 'UTC'), 'YYYY-MM-DD'),
		       (SELECT COALESCE(longest_streak, 0) FROM users WHERE id = $1)
		  FROM a`, userID,
	).Scan(&st.Count, &st.Questions, &st.Correct, &st.Seconds, &st.Perfect,
		&st.Days, &st.FirstDate, &st.LastDate, &st.BestStreak)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx, storyAttemptsCTE+`
		SELECT cat, COUNT(*), SUM(q), SUM(c) FROM a
		 GROUP BY cat
		 ORDER BY COUNT(*) DESC, SUM(c)::float / NULLIF(SUM(q), 0) DESC NULLS LAST, cat
		 LIMIT 3`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c StoryCategory
		if err := rows.Scan(&c.Name, &c.Plays, &c.Questions, &c.Correct); err != nil {
			return nil, err
		}
		st.Categories = append(st.Categories, c)
	}
	return st, rows.Err()
}

// GET /api/v1/users/me/story-stats
func (h *Handler) GetMyStoryStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.svc.GetStoryStats(r.Context(), middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, stats)
}
