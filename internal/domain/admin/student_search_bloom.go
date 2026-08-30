package admin

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	studentSearchBloomKey  = "admin_students"
	bloomFalsePositiveRate = 0.01
)

// bloomFilter is a compact probabilistic set. It may report false positives,
// but never false negatives for values added to the current snapshot.
type bloomFilter struct {
	bits []uint64
	m    uint64
	k    uint64
}

func newBloomFilter(expectedItems int, falsePositiveRate float64) *bloomFilter {
	if expectedItems < 1 {
		expectedItems = 1
	}
	if falsePositiveRate <= 0 || falsePositiveRate >= 1 {
		falsePositiveRate = bloomFalsePositiveRate
	}
	m := uint64(math.Ceil(-float64(expectedItems) * math.Log(falsePositiveRate) / (math.Ln2 * math.Ln2)))
	if m < 64 {
		m = 64
	}
	k := uint64(math.Round(float64(m) / float64(expectedItems) * math.Ln2))
	if k < 1 {
		k = 1
	}
	return &bloomFilter{bits: make([]uint64, (m+63)/64), m: m, k: k}
}

func (b *bloomFilter) add(value string) {
	h1, h2 := bloomHashes(value)
	for i := uint64(0); i < b.k; i++ {
		pos := (h1 + i*h2) % b.m
		b.bits[pos/64] |= uint64(1) << (pos % 64)
	}
}

func (b *bloomFilter) mightContain(value string) bool {
	h1, h2 := bloomHashes(value)
	for i := uint64(0); i < b.k; i++ {
		pos := (h1 + i*h2) % b.m
		if b.bits[pos/64]&(uint64(1)<<(pos%64)) == 0 {
			return false
		}
	}
	return true
}

func bloomHashes(value string) (uint64, uint64) {
	a := fnv.New64a()
	_, _ = a.Write([]byte(value))
	b := fnv.New64()
	_, _ = b.Write([]byte(value))
	h2 := b.Sum64()
	if h2 == 0 {
		h2 = 0x9e3779b97f4a7c15
	}
	return a.Sum64(), h2
}

func normalizedTrigrams(value string) []string {
	runes := []rune(strings.ToLower(strings.TrimSpace(value)))
	if len(runes) < 3 {
		return nil
	}
	out := make([]string, 0, len(runes)-2)
	for i := 0; i+3 <= len(runes); i++ {
		out = append(out, string(runes[i:i+3]))
	}
	return out
}

type studentSearchBloom struct {
	mu      sync.RWMutex
	version int64
	loaded  bool
	filter  *bloomFilter
}

func newStudentSearchBloom() *studentSearchBloom { return &studentSearchBloom{} }

// refresh rebuilds only when a database trigger reports that searchable
// student fields changed. It returns false when no trustworthy snapshot is
// available, which makes the caller fall back to the normal SQL query.
func (s *studentSearchBloom) refresh(ctx context.Context, db *pgxpool.Pool) (bool, error) {
	var version int64
	if err := db.QueryRow(ctx,
		`SELECT version FROM internal_search_versions WHERE key=$1`, studentSearchBloomKey,
	).Scan(&version); err != nil {
		return false, fmt.Errorf("read version: %w", err)
	}

	s.mu.RLock()
	current := s.loaded && s.version == version
	s.mu.RUnlock()
	if current {
		return true, nil
	}

	rows, err := db.Query(ctx, `
		SELECT u.display_name, u.email, COALESCE(e.roll_number, '')
		  FROM users u
		  LEFT JOIN enrollments e ON e.user_id=u.id AND e.status IN ('active','suspended')
		 WHERE u.role='student'`)
	if err != nil {
		return false, fmt.Errorf("load values: %w", err)
	}
	defer rows.Close()

	grams := make([]string, 0, 1024)
	for rows.Next() {
		var name, email, roll string
		if err := rows.Scan(&name, &email, &roll); err != nil {
			return false, fmt.Errorf("scan values: %w", err)
		}
		grams = append(grams, normalizedTrigrams(name)...)
		grams = append(grams, normalizedTrigrams(email)...)
		grams = append(grams, normalizedTrigrams(roll)...)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate values: %w", err)
	}

	// A write during the scan invalidates this snapshot; use SQL for this
	// request and rebuild on the next one.
	var after int64
	if err := db.QueryRow(ctx,
		`SELECT version FROM internal_search_versions WHERE key=$1`, studentSearchBloomKey,
	).Scan(&after); err != nil {
		return false, fmt.Errorf("recheck version: %w", err)
	}
	if after != version {
		return false, nil
	}

	filter := newBloomFilter(len(grams), bloomFalsePositiveRate)
	for _, gram := range grams {
		filter.add(gram)
	}
	s.mu.Lock()
	s.filter, s.version, s.loaded = filter, version, true
	s.mu.Unlock()
	return true, nil
}

func (s *studentSearchBloom) mightContain(query string) bool {
	grams := normalizedTrigrams(query)
	if len(grams) == 0 {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.loaded || s.filter == nil {
		return true
	}
	for _, gram := range grams {
		if !s.filter.mightContain(gram) {
			return false
		}
	}
	return true
}
