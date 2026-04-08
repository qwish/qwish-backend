package scheduler

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/scoring"
	"github.com/qwish/backend/internal/domain/streak"
)

type Scheduler struct {
	db        *pgxpool.Pool
	streakSvc *streak.Service
}

func New(db *pgxpool.Pool, streakSvc *streak.Service) *Scheduler {
	return &Scheduler{db: db, streakSvc: streakSvc}
}

// ExpirePoints runs nightly. Marks points older than institution/config expiry.
func (s *Scheduler) ExpirePoints(ctx context.Context) error {
	log.Println("[cron] running expire-points")

	cfg, err := scoring.LoadConfig(ctx, s.db)
	if err != nil {
		return err
	}

	// Find unexpired positive ledger entries that are past expiry
	rows, err := s.db.Query(ctx,
		`SELECT pl.id, pl.user_id, pl.amount FROM points_ledger pl
		 WHERE pl.expires_at IS NOT NULL AND pl.expires_at <= now() AND pl.amount > 0
		 AND NOT EXISTS (
		   SELECT 1 FROM points_ledger e WHERE e.reference_id=pl.id AND e.reason='expiry'
		 )`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var ledgerID, userID string
		var amount int64
		rows.Scan(&ledgerID, &userID, &amount)

		// Get current balance
		var balance int64
		s.db.QueryRow(ctx, `SELECT total_points FROM users WHERE id=$1`, userID).Scan(&balance)
		deduction := amount
		if deduction > balance {
			deduction = balance
		}
		newBalance := balance - deduction

		s.db.Exec(ctx, `UPDATE users SET total_points=$1, updated_at=now() WHERE id=$2`, newBalance, userID)
		expiry := time.Now().AddDate(0, int(cfg.PointsExpiryMonths), 0)
		s.db.Exec(ctx,
			`INSERT INTO points_ledger (user_id, amount, reason, reference_id, balance_after, expires_at)
			 VALUES ($1,$2,'expiry',$3,$4,$5)`,
			userID, -deduction, ledgerID, newBalance, expiry)
	}
	log.Println("[cron] expire-points done")
	return nil
}

// ResetStreaks runs daily at 00:05 UTC.
func (s *Scheduler) ResetStreaks(ctx context.Context) error {
	log.Println("[cron] running reset-streaks")
	err := s.streakSvc.DailyReset(ctx)
	log.Println("[cron] reset-streaks done")
	return err
}

// SnapshotLeaderboard runs every Monday at 00:01 UTC.
func (s *Scheduler) SnapshotLeaderboard(ctx context.Context) error {
	log.Println("[cron] running snapshot-leaderboard")

	weekStart := time.Now().Truncate(7 * 24 * time.Hour)

	// Global snapshot
	rows, err := s.db.Query(ctx,
		`SELECT id, display_name, total_points, current_streak,
		        RANK() OVER (ORDER BY total_points DESC) as rank
		 FROM users WHERE status='active' AND role IN ('student','teacher')
		 ORDER BY total_points DESC LIMIT 100`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type rankEntry struct {
		Rank          int    `json:"rank"`
		UserID        string `json:"user_id"`
		DisplayName   string `json:"display_name"`
		TotalPoints   int64  `json:"total_points"`
		CurrentStreak int    `json:"current_streak"`
	}
	var globalRankings []rankEntry
	for rows.Next() {
		var e rankEntry
		rows.Scan(&e.UserID, &e.DisplayName, &e.TotalPoints, &e.CurrentStreak, &e.Rank)
		globalRankings = append(globalRankings, e)
	}

	globalJSON, _ := json.Marshal(globalRankings)
	s.db.Exec(ctx,
		`INSERT INTO leaderboard_snapshots (scope, week_start, rankings) VALUES ('global',$1,$2)`,
		weekStart.Format("2006-01-02"), globalJSON)

	// Per-institution snapshots
	instRows, _ := s.db.Query(ctx, `SELECT id FROM institutions WHERE status='verified'`)
	defer instRows.Close()
	for instRows.Next() {
		var instID string
		instRows.Scan(&instID)

		iRows, _ := s.db.Query(ctx,
			`SELECT id, display_name, total_points, current_streak,
			        RANK() OVER (ORDER BY total_points DESC) as rank
			 FROM users WHERE institution_id=$1 AND status='active' AND role IN ('student','teacher')
			 ORDER BY total_points DESC LIMIT 100`, instID)

		var instRankings []rankEntry
		for iRows.Next() {
			var e rankEntry
			iRows.Scan(&e.UserID, &e.DisplayName, &e.TotalPoints, &e.CurrentStreak, &e.Rank)
			instRankings = append(instRankings, e)
		}
		iRows.Close()

		instJSON, _ := json.Marshal(instRankings)
		s.db.Exec(ctx,
			`INSERT INTO leaderboard_snapshots (scope, institution_id, week_start, rankings)
			 VALUES ('institution',$1,$2,$3)`,
			instID, weekStart.Format("2006-01-02"), instJSON)
	}

	log.Println("[cron] snapshot-leaderboard done")
	return nil
}

// CloseExpiredQuizzes closes P&W quizzes past their ends_at.
func (s *Scheduler) CloseExpiredQuizzes(ctx context.Context) error {
	log.Println("[cron] running close-expired-quizzes")
	_, err := s.db.Exec(ctx,
		`UPDATE quizzes SET status='closed', updated_at=now()
		 WHERE type='play_and_win' AND status='published' AND ends_at IS NOT NULL AND ends_at <= now()`)
	log.Println("[cron] close-expired-quizzes done")
	return err
}
