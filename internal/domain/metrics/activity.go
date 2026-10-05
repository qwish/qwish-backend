package metrics

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/qwish/backend/internal/middleware"
)

type ActivityDay struct {
	Date             string `json:"date"`
	QuizzesCreated   int    `json:"quizzes_created"`
	QuizzesCompleted int    `json:"quizzes_completed"`
}

type ActivityHeatmap struct {
	InstitutionID string        `json:"institution_id"`
	Timezone      string        `json:"timezone"`
	From          string        `json:"from"`
	To            string        `json:"to"`
	Days          []ActivityDay `json:"days"`
}

// InstitutionActivity is deliberately institution-wide on both panels. It
// returns only aggregate counts, never another teacher's student records.
func (h *Handler) InstitutionActivity(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	if instID == "" {
		middleware.BadRequest(w, "an institution is required")
		return
	}
	result, err := h.svc.Activity(r.Context(), instID, time.Now())
	if err != nil {
		failed(w, "institution activity", err)
		return
	}
	middleware.JSON(w, http.StatusOK, result)
}

func activityWindow(now time.Time, loc *time.Location) (time.Time, time.Time) {
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	// 52 Sunday-aligned columns, ending in the current partial week.
	return today.AddDate(0, 0, -int(today.Weekday())-51*7), today
}

func (s *MetricsService) Activity(ctx context.Context, instID string, now time.Time) (ActivityHeatmap, error) {
	if instID == "" {
		return ActivityHeatmap{}, errors.New("institution is required")
	}
	var timezone string
	if err := s.db.QueryRow(ctx, `SELECT timezone FROM institutions WHERE id=$1`, instID).Scan(&timezone); err != nil {
		return ActivityHeatmap{}, err
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return ActivityHeatmap{}, err
	}
	from, to := activityWindow(now, loc)
	result := ActivityHeatmap{InstitutionID: instID, Timezone: timezone, From: from.Format(DateLayout), To: to.Format(DateLayout), Days: []ActivityDay{}}
	rows, err := s.db.Query(ctx, `
 WITH created AS (
  SELECT (q.created_at AT TIME ZONE $2)::date AS day, COUNT(*) AS count
  FROM quizzes q JOIN users author ON author.id=q.created_by
  WHERE q.institution_id=$1 AND author.role='teacher' AND q.deleted_at IS NULL
   AND q.created_at >= $3 AND q.created_at < $4
  GROUP BY day
 ), completed AS (
  SELECT (qa.completed_at AT TIME ZONE $2)::date AS day, COUNT(*) AS count
  FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id JOIN users u ON u.id=qa.user_id
  WHERE q.institution_id=$1 AND qa.status='completed' AND u.role='student'
   AND EXISTS (
    SELECT 1 FROM enrollments membership
    WHERE membership.user_id=u.id AND membership.institution_id=$1
     AND membership.status <> 'pending_claim'
     AND COALESCE(membership.joined_at, membership.created_at) <= qa.completed_at
     AND (membership.ended_at IS NULL OR qa.completed_at < membership.ended_at)
   )
   AND qa.completed_at >= $3 AND qa.completed_at < $4
  GROUP BY day
 )
 SELECT TO_CHAR(d.day, 'YYYY-MM-DD'), COALESCE(c.count,0), COALESCE(a.count,0)
 FROM generate_series($5::date::timestamp, $6::date::timestamp, INTERVAL '1 day') AS d(day)
 LEFT JOIN created c ON c.day=d.day::date
 LEFT JOIN completed a ON a.day=d.day::date
 ORDER BY d.day`, instID, timezone, from, to.AddDate(0, 0, 1), result.From, result.To)
	if err != nil {
		return ActivityHeatmap{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var day ActivityDay
		if err := rows.Scan(&day.Date, &day.QuizzesCreated, &day.QuizzesCompleted); err != nil {
			return ActivityHeatmap{}, err
		}
		result.Days = append(result.Days, day)
	}
	if err := rows.Err(); err != nil {
		return ActivityHeatmap{}, err
	}
	return result, nil
}
