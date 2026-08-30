package quiz

import (
	"context"
	"errors"
	"hash/fnv"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrDuplicateQuestion = errors.New("a near-duplicate question already exists")

var wordPattern = regexp.MustCompile(`[\pL\pN]+`)

func promptTokens(prompt string) []string {
	seen := map[string]struct{}{}
	for _, token := range wordPattern.FindAllString(strings.ToLower(prompt), -1) {
		seen[token] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for token := range seen {
		out = append(out, token)
	}
	sort.Strings(out)
	return out
}

func minhashBands(prompt string) [4]int64 {
	tokens := promptTokens(prompt)
	var mins [16]uint64
	for i := range mins {
		mins[i] = ^uint64(0)
	}
	for _, token := range tokens {
		for seed := range mins {
			h := fnv.New64a()
			_, _ = h.Write([]byte{byte(seed), byte(seed >> 8)})
			_, _ = h.Write([]byte(token))
			if sum := h.Sum64(); sum < mins[seed] {
				mins[seed] = sum
			}
		}
	}
	var bands [4]int64
	for band := range bands {
		h := fnv.New64a()
		for i := 0; i < 4; i++ {
			v := mins[band*4+i]
			for shift := 0; shift < 64; shift += 8 {
				_, _ = h.Write([]byte{byte(v >> shift)})
			}
		}
		bands[band] = int64(h.Sum64())
	}
	return bands
}

func jaccardPrompt(a, b string) float64 {
	at, bt := promptTokens(a), promptTokens(b)
	set := make(map[string]struct{}, len(at))
	for _, token := range at {
		set[token] = struct{}{}
	}
	intersection := 0
	for _, token := range bt {
		if _, ok := set[token]; ok {
			intersection++
		}
	}
	union := len(at) + len(bt) - intersection
	if union == 0 {
		return 1
	}
	return float64(intersection) / float64(union)
}

func findNearDuplicate(ctx context.Context, db *pgxpool.Pool, prompt, excludeID string) (bool, error) {
	b := minhashBands(prompt)
	rows, err := db.Query(ctx, `
		WITH candidates AS (
		  SELECT q.id,q.prompt FROM question_lsh_buckets l JOIN questions q ON q.id=l.question_id
		   WHERE (l.band,l.bucket) IN ((0,$1),(1,$2),(2,$3),(3,$4))
		  UNION
		  SELECT q.id,q.prompt FROM questions q
		   WHERE similarity(q.prompt,$6)>=0.45
		)
		SELECT DISTINCT id,prompt FROM candidates
		WHERE ($5='' OR id<>$5::uuid) LIMIT 100`, b[0], b[1], b[2], b[3], excludeID, prompt)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, candidate string
		if err := rows.Scan(&id, &candidate); err != nil {
			return false, err
		}
		if jaccardPrompt(prompt, candidate) >= 0.60 {
			return true, nil
		}
	}
	return false, rows.Err()
}

func storeMinhash(ctx context.Context, db *pgxpool.Pool, questionID, prompt string) {
	b := minhashBands(prompt)
	_, _ = db.Exec(ctx, `
		INSERT INTO question_lsh_buckets(question_id,band,bucket)
		VALUES ($1,0,$2),($1,1,$3),($1,2,$4),($1,3,$5)
		ON CONFLICT(question_id,band) DO UPDATE SET bucket=EXCLUDED.bucket`, questionID, b[0], b[1], b[2], b[3])
}
