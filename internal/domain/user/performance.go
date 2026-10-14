package user

import (
	"context"
	"fmt"
	"time"
)

// Performance is the diagnostic half of Insights: how the learner answers,
// not just how often they are right. Every figure is computed from completed
// attempts' question_responses, the same rows the topic breakdown reads.
type Performance struct {
	Pace       *Pace             `json:"pace"`
	Domains    []DomainPace      `json:"domains"`
	Campus     *CampusComparison `json:"campus"`
	Difficulty []DifficultyTier  `json:"difficulty"`
	Week       WeekActivity      `json:"week"`
	Peak       *PeakWindow       `json:"peak"`
}

// Pace is average answer time against accuracy.
type Pace struct {
	SecondsPerQuestion float64 `json:"seconds_per_question"`
	Accuracy           float64 `json:"accuracy"` // 0–100
	// fast_accurate | slow_steady | rushing | struggling
	Label string `json:"label"`
}

// DomainPace is per-subject speed and, when the campus has enough learners,
// the campus accuracy to compare against.
type DomainPace struct {
	Slug               string   `json:"slug"`
	SecondsPerQuestion float64  `json:"seconds_per_question"`
	CampusAvgScore     *float64 `json:"campus_avg_score,omitempty"`
}

// CampusComparison is the learner's overall accuracy against their campus.
type CampusComparison struct {
	Accuracy       float64 `json:"accuracy"`
	CampusAccuracy float64 `json:"campus_accuracy"`
	Learners       int     `json:"learners"`
}

type DifficultyTier struct {
	Tier      string `json:"tier"` // easy | medium | hard
	Questions int    `json:"questions"`
	Correct   int    `json:"correct"`
}

// WeekActivity is answered questions per day for the last seven days, oldest
// first, and the week-on-week totals.
type WeekActivity struct {
	Days     []DayCount `json:"days"`
	ThisWeek int        `json:"this_week"`
	LastWeek int        `json:"last_week"`
}

type DayCount struct {
	Date      string `json:"date"` // YYYY-MM-DD, IST
	Questions int    `json:"questions"`
}

// PeakWindow is the three-hour stretch of the day with the best accuracy.
type PeakWindow struct {
	StartHour       int     `json:"start_hour"` // IST, 0–21
	EndHour         int     `json:"end_hour"`   // exclusive, 3–24
	Accuracy        float64 `json:"accuracy"`
	OverallAccuracy float64 `json:"overall_accuracy"`
}

const (
	// accurateAt splits the pacing quadrant: at or above is "accurate".
	accurateAt = 70.0
	// fastAt is the share of a question's time limit under which an answer
	// counts as fast — the same third the Qwish Score speed factor uses.
	fastAt = 1.0 / 3
	// minCampusLearners keeps a campus average from describing one person.
	minCampusLearners = 3
	// peakMinAnswers and peakMinLift keep the peak window from being noise.
	peakMinAnswers = 20
	peakMinLift    = 5.0
	// The app's learners are in India; days and hours are IST.
	insightsZone = "Asia/Kolkata"
)

// paceLabel places accuracy and relative speed in the four quadrants.
// timeShare is average time over average time limit.
func paceLabel(accuracy, timeShare float64) string {
	accurate := accuracy >= accurateAt
	fast := timeShare < fastAt
	switch {
	case accurate && fast:
		return "fast_accurate"
	case accurate:
		return "slow_steady"
	case fast:
		return "rushing"
	default:
		return "struggling"
	}
}

// difficultyTier buckets the derived 0.40–1.00 coefficient. Questions without
// enough responses carry the 0.60 default, so they read as medium.
func difficultyTier(d float64) string {
	switch {
	case d < 0.55:
		return "easy"
	case d < 0.75:
		return "medium"
	default:
		return "hard"
	}
}

type hourBucket struct {
	answers, correct int
}

// peakWindow picks the best three-hour window with enough answers, if it beats
// the overall accuracy by peakMinLift points. Null otherwise.
func peakWindow(byHour [24]hourBucket) *PeakWindow {
	var total, correct int
	for _, b := range byHour {
		total += b.answers
		correct += b.correct
	}
	if total == 0 {
		return nil
	}
	overall := float64(correct) / float64(total) * 100
	var best *PeakWindow
	for start := 0; start < 24; start += 3 {
		var n, c int
		for h := start; h < start+3; h++ {
			n += byHour[h].answers
			c += byHour[h].correct
		}
		if n < peakMinAnswers {
			continue
		}
		acc := float64(c) / float64(n) * 100
		if acc-overall < peakMinLift {
			continue
		}
		if best == nil || acc > best.Accuracy {
			best = &PeakWindow{StartHour: start, EndHour: start + 3, Accuracy: round1(acc), OverallAccuracy: round1(overall)}
		}
	}
	return best
}

// GetPerformance builds the diagnostics. instID is the learner's current
// campus; without one, campus comparisons are left out.
func (s *Service) GetPerformance(ctx context.Context, userID, instID string) (*Performance, error) {
	p := &Performance{Domains: []DomainPace{}, Difficulty: []DifficultyTier{}}

	// Pace and difficulty tiers, one pass over the learner's answers.
	rows, err := s.db.Query(ctx, `
		SELECT q.difficulty::float8,
		       COUNT(*)::int,
		       (COUNT(*) FILTER (WHERE qr.is_correct))::int,
		       COALESCE(SUM(qr.time_taken_ms) FILTER (WHERE qr.time_taken_ms > 0), 0)::float8,
		       (COUNT(*) FILTER (WHERE qr.time_taken_ms > 0))::int,
		       COALESCE(SUM(q.time_limit_seconds*1000) FILTER (WHERE qr.time_taken_ms > 0 AND q.time_limit_seconds > 0), 0)::float8
		  FROM question_responses qr
		  JOIN quiz_attempts a ON a.id = qr.attempt_id
		  JOIN questions q ON q.id = qr.question_id
		 WHERE a.user_id = $1 AND a.status = 'completed'
		 GROUP BY q.difficulty`, userID)
	if err != nil {
		return nil, err
	}
	tiers := map[string]*DifficultyTier{}
	var answered, correct, timedAnswers int
	var timeMs, limitMs float64
	for rows.Next() {
		var diff, t, l float64
		var n, c, timed int
		if err := rows.Scan(&diff, &n, &c, &t, &timed, &l); err != nil {
			rows.Close()
			return nil, err
		}
		name := difficultyTier(diff)
		tier := tiers[name]
		if tier == nil {
			tier = &DifficultyTier{Tier: name}
			tiers[name] = tier
		}
		tier.Questions += n
		tier.Correct += c
		answered += n
		correct += c
		timeMs += t
		timedAnswers += timed
		limitMs += l
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, name := range []string{"easy", "medium", "hard"} {
		if t := tiers[name]; t != nil {
			p.Difficulty = append(p.Difficulty, *t)
		}
	}
	if answered > 0 && timedAnswers > 0 {
		acc := float64(correct) / float64(answered) * 100
		share := 1.0
		if limitMs > 0 {
			share = timeMs / limitMs
		}
		p.Pace = &Pace{
			SecondsPerQuestion: round1(timeMs / float64(timedAnswers) / 1000),
			Accuracy:           round1(acc),
			Label:              paceLabel(acc, share),
		}
	}

	// Seconds per question by subject.
	rows, err = s.db.Query(ctx, `
		SELECT COALESCE(qz.domain, 'general'),
		       AVG(qr.time_taken_ms)::float8 / 1000
		  FROM question_responses qr
		  JOIN quiz_attempts a ON a.id = qr.attempt_id
		  JOIN quizzes qz ON qz.id = a.quiz_id
		 WHERE a.user_id = $1 AND a.status = 'completed' AND qr.time_taken_ms > 0
		 GROUP BY 1`, userID)
	if err != nil {
		return nil, err
	}
	byDomain := map[string]*DomainPace{}
	var order []string
	for rows.Next() {
		var slug string
		var secs float64
		if err := rows.Scan(&slug, &secs); err != nil {
			rows.Close()
			return nil, err
		}
		byDomain[slug] = &DomainPace{Slug: slug, SecondsPerQuestion: round1(secs)}
		order = append(order, slug)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Campus accuracy by subject, from the other active members of the
	// learner's campus, shown only where enough of them have answered.
	if instID != "" {
		rows, err = s.db.Query(ctx, `
			WITH peers AS (
			  SELECT DISTINCT e.user_id FROM enrollments e
			   WHERE e.institution_id = $1::uuid AND e.status = 'active'
			     AND e.user_id IS NOT NULL AND e.user_id <> $2::uuid
			)
			SELECT COALESCE(qz.domain, 'general'),
			       COUNT(*)::int,
			       (COUNT(*) FILTER (WHERE qr.is_correct))::int,
			       COUNT(DISTINCT a.user_id)::int
			  FROM peers
			  JOIN quiz_attempts a ON a.user_id = peers.user_id AND a.status = 'completed'
			  JOIN question_responses qr ON qr.attempt_id = a.id
			  JOIN quizzes qz ON qz.id = a.quiz_id
			 GROUP BY 1`, instID, userID)
		if err != nil {
			return nil, err
		}
		var campusAnswers, campusCorrect, campusLearners int
		for rows.Next() {
			var slug string
			var n, c, learners int
			if err := rows.Scan(&slug, &n, &c, &learners); err != nil {
				rows.Close()
				return nil, err
			}
			campusAnswers += n
			campusCorrect += c
			if learners < minCampusLearners || n == 0 {
				continue
			}
			avg := round1(float64(c) / float64(n) * 100)
			if dp := byDomain[slug]; dp != nil {
				dp.CampusAvgScore = &avg
			} else {
				byDomain[slug] = &DomainPace{Slug: slug, CampusAvgScore: &avg}
				order = append(order, slug)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if err := s.db.QueryRow(ctx, `
			SELECT COUNT(DISTINCT a.user_id)::int
			  FROM enrollments e
			  JOIN quiz_attempts a ON a.user_id = e.user_id AND a.status = 'completed'
			 WHERE e.institution_id = $1::uuid AND e.status = 'active' AND e.user_id <> $2::uuid`,
			instID, userID).Scan(&campusLearners); err != nil {
			return nil, err
		}
		if campusLearners >= minCampusLearners && campusAnswers > 0 && answered > 0 {
			p.Campus = &CampusComparison{
				Accuracy:       round1(float64(correct) / float64(answered) * 100),
				CampusAccuracy: round1(float64(campusCorrect) / float64(campusAnswers) * 100),
				Learners:       campusLearners,
			}
		}
	}
	for _, slug := range order {
		p.Domains = append(p.Domains, *byDomain[slug])
	}

	// Answers per IST day for the last 14 days, and by IST hour for all time.
	loc, err := time.LoadLocation(insightsZone)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", insightsZone, err)
	}
	today := time.Now().In(loc)
	start := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -13)
	rows, err = s.db.Query(ctx, `
		SELECT to_char(qr.submitted_at AT TIME ZONE $3, 'YYYY-MM-DD'), COUNT(*)::int
		  FROM question_responses qr
		  JOIN quiz_attempts a ON a.id = qr.attempt_id
		 WHERE a.user_id = $1 AND qr.submitted_at >= $2
		 GROUP BY 1`, userID, start, insightsZone)
	if err != nil {
		return nil, err
	}
	perDay := map[string]int{}
	for rows.Next() {
		var day string
		var n int
		if err := rows.Scan(&day, &n); err != nil {
			rows.Close()
			return nil, err
		}
		perDay[day] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	p.Week.Days = make([]DayCount, 0, 7)
	for i := 0; i < 14; i++ {
		day := start.AddDate(0, 0, i).Format("2006-01-02")
		n := perDay[day]
		if i < 7 {
			p.Week.LastWeek += n
			continue
		}
		p.Week.ThisWeek += n
		p.Week.Days = append(p.Week.Days, DayCount{Date: day, Questions: n})
	}

	rows, err = s.db.Query(ctx, `
		SELECT EXTRACT(HOUR FROM qr.submitted_at AT TIME ZONE $2)::int,
		       COUNT(*)::int, (COUNT(*) FILTER (WHERE qr.is_correct))::int
		  FROM question_responses qr
		  JOIN quiz_attempts a ON a.id = qr.attempt_id
		 WHERE a.user_id = $1 AND a.status = 'completed'
		 GROUP BY 1`, userID, insightsZone)
	if err != nil {
		return nil, err
	}
	var byHour [24]hourBucket
	for rows.Next() {
		var h, n, c int
		if err := rows.Scan(&h, &n, &c); err != nil {
			rows.Close()
			return nil, err
		}
		if h >= 0 && h < 24 {
			byHour[h] = hourBucket{answers: n, correct: c}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	p.Peak = peakWindow(byHour)
	return p, nil
}
