package leaderboard

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	qdb "github.com/qwish/backend/internal/db"
)

const maxSnapshots = 16

func rankCutoff(scope string) int {
	if scope == "institution" {
		return 100
	}
	return 10000
}

func nullableInstitution(id string) any {
	if id == "" {
		return nil
	}
	return id
}

type rankSnapshot struct {
	entries   []Entry
	buckets   map[int]int
	total     int
	updatedAt time.Time
	expires   time.Time
}

func (s *rankSnapshot) page(limit, offset int) []Entry {
	if offset < 0 || offset >= len(s.entries) || limit <= 0 {
		return []Entry{}
	}
	end := offset + limit
	if end > len(s.entries) {
		end = len(s.entries)
	}
	return s.entries[offset:end]
}

// Match PostgreSQL width_bucket(score,100,900,80), including outliers.
func scoreBucket(score float64) int {
	if score < 100 {
		return 0
	}
	if score >= 900 {
		return 81
	}
	return int(math.Floor((score-100)/10)) + 1
}

func (s *rankSnapshot) position(score float64, cutoff int) (int, *int) {
	if s.total == 0 {
		return 0, nil
	}
	// Binary search counts strictly higher snapshot scores and handles ties at
	// the cutoff, even when the caller's live score changed after refresh.
	higher := sort.Search(len(s.entries), func(i int) bool { return s.entries[i].QwishScore <= score })
	if (higher < len(s.entries) || s.total <= len(s.entries)) && higher+1 <= cutoff {
		return higher + 1, nil
	}
	// Use the bottom of the caller's ten-point bucket: a conservative estimate
	// rather than claiming precision the cached histogram cannot provide.
	count := 0
	for bucket, n := range s.buckets {
		if bucket >= scoreBucket(score) {
			count += n
		}
	}
	if count < cutoff+1 {
		count = cutoff + 1
	}
	percent := int(math.Ceil(float64(count) * 100 / float64(s.total)))
	if percent < 1 {
		percent = 1
	}
	if percent > 100 {
		percent = 100
	}
	return 0, &percent
}

func (h *Handler) snapshot(ctx context.Context, scope, instID, domain string) (*rankSnapshot, error) {
	if scope == "global" {
		instID = ""
	}
	// Structured keys avoid collisions between arbitrary domain inputs.
	keyBytes, _ := json.Marshal([]string{scope, instID, domain})
	key := string(keyBytes)
	h.mu.Lock()
	if s := h.snapshots[key]; s != nil && time.Now().Before(s.expires) {
		h.mu.Unlock()
		return s, nil
	}
	epoch := h.epoch
	h.mu.Unlock()
	ch := h.flight.DoChan(fmt.Sprintf("%d:%s", epoch, key), func() (any, error) {
		// A disconnected caller must not cancel a shared refresh for other callers.
		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		s, err := h.loadSnapshot(refreshCtx, scope, instID, domain)
		if err != nil {
			return nil, err
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.epoch == epoch {
			for k, v := range h.snapshots {
				if time.Now().After(v.expires) {
					delete(h.snapshots, k)
				}
			}
			if len(h.snapshots) >= maxSnapshots {
				var oldestKey string
				var oldest time.Time
				for k, v := range h.snapshots {
					if oldest.IsZero() || v.updatedAt.Before(oldest) {
						oldestKey, oldest = k, v.updatedAt
					}
				}
				delete(h.snapshots, oldestKey)
			}
			h.snapshots[key] = s
		}
		return s, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			return nil, result.Err
		}
		return result.Val.(*rankSnapshot), nil
	}
}

func (h *Handler) loadSnapshot(ctx context.Context, scope, instID, domain string) (*rankSnapshot, error) {
	// Both queries observe one snapshot, so page ranks, histogram and total agree.
	tx, err := h.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	filter := ` FROM leaderboard_scores ls JOIN users u ON u.id=ls.user_id
 WHERE ls.completed_quizzes>=5 AND u.status='active' AND u.role='student' AND u.deleted_at IS NULL`
	args := []any{}
	if domain != "" {
		args = append(args, domain)
		filter += fmt.Sprintf(" AND lower(u.domain)=$%d", len(args))
	}
	if scope == "institution" {
		args = append(args, instID)
		filter += " AND " + qdb.LiveMemberSQL("u.id", fmt.Sprintf("$%d", len(args)))
	}
	s := &rankSnapshot{entries: []Entry{}, buckets: map[int]int{}}
	rows, err := tx.Query(ctx, `SELECT width_bucket(ls.qwish_score,100,900,80),count(*)`+filter+` GROUP BY 1`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var bucket, n int
		if err = rows.Scan(&bucket, &n); err != nil {
			rows.Close()
			return nil, err
		}
		s.buckets[bucket] = n
		s.total += n
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Rank only needs scores and IDs; fetch display information for the bounded
	// top list afterwards. Tied scores keep SQL RANK semantics (1,1,3).
	args = append(args, rankCutoff(scope))
	query := `WITH leaders AS (SELECT ls.user_id,ls.qwish_score,
 RANK() OVER (ORDER BY ls.qwish_score DESC) AS rank` + filter +
		fmt.Sprintf(` ORDER BY ls.qwish_score DESC,ls.user_id LIMIT $%d)
 SELECT l.user_id,u.display_name,i.name,l.qwish_score,u.total_points,u.current_streak,l.rank
 FROM leaders l JOIN users u ON u.id=l.user_id LEFT JOIN institutions i ON i.id=u.institution_id
 ORDER BY l.qwish_score DESC,l.user_id`, len(args))
	rows, err = tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e Entry
		if err = rows.Scan(&e.UserID, &e.DisplayName, &e.InstitutionName, &e.QwishScore, &e.TotalPoints, &e.CurrentStreak, &e.Rank); err != nil {
			rows.Close()
			return nil, err
		}
		s.entries = append(s.entries, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.updatedAt = time.Now().UTC()
	s.expires = s.updatedAt.Add(pageTTL)
	return s, nil
}
