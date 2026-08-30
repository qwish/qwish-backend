// Package algorithms contains bounded-memory primitives used by backend hot
// paths where exact global aggregation would be unnecessarily expensive.
package algorithms

import (
	"hash/fnv"
	"math"
	"sort"
	"strconv"
)

func hash64(seed uint64, value string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strconv.FormatUint(seed, 10)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(value))
	return h.Sum64()
}

// CountMinSketch estimates frequencies without under-counting.
type CountMinSketch struct {
	width uint64
	rows  [][]uint64
}

func NewCountMinSketch(width, depth uint64) *CountMinSketch {
	if width < 1 {
		width = 1
	}
	if depth < 1 {
		depth = 1
	}
	r := make([][]uint64, depth)
	for i := range r {
		r[i] = make([]uint64, width)
	}
	return &CountMinSketch{width: width, rows: r}
}
func (s *CountMinSketch) Add(key string, n uint64) {
	for i := range s.rows {
		s.rows[i][hash64(uint64(i+1), key)%s.width] += n
	}
}
func (s *CountMinSketch) Estimate(key string) uint64 {
	var min uint64 = math.MaxUint64
	for i := range s.rows {
		if v := s.rows[i][hash64(uint64(i+1), key)%s.width]; v < min {
			min = v
		}
	}
	return min
}

type FrequentItem struct {
	Key   string `json:"key"`
	Count uint64 `json:"estimated_count"`
}

// SpaceSaving tracks approximate heavy hitters in bounded space.
type SpaceSaving struct {
	capacity int
	counts   map[string]uint64
}

func NewSpaceSaving(capacity int) *SpaceSaving {
	if capacity < 1 {
		capacity = 1
	}
	return &SpaceSaving{capacity: capacity, counts: map[string]uint64{}}
}
func (s *SpaceSaving) Add(key string) {
	if _, ok := s.counts[key]; ok {
		s.counts[key]++
		return
	}
	if len(s.counts) < s.capacity {
		s.counts[key] = 1
		return
	}
	var victim string
	min := uint64(math.MaxUint64)
	for k, v := range s.counts {
		if v < min {
			victim, min = k, v
		}
	}
	delete(s.counts, victim)
	s.counts[key] = min + 1
}
func (s *SpaceSaving) Top() []FrequentItem {
	out := make([]FrequentItem, 0, len(s.counts))
	for k, v := range s.counts {
		out = append(out, FrequentItem{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

// HyperLogLog estimates distinct counts using 2^precision registers.
type HyperLogLog struct {
	precision uint8
	registers []uint8
}

func NewHyperLogLog(precision uint8) *HyperLogLog {
	if precision < 4 {
		precision = 4
	}
	if precision > 16 {
		precision = 16
	}
	return &HyperLogLog{precision: precision, registers: make([]uint8, 1<<precision)}
}
func (h *HyperLogLog) Add(value string) {
	x := hash64(99, value)
	idx := x >> (64 - h.precision)
	w := (x << h.precision) | (1 << (h.precision - 1))
	rank := uint8(1)
	for w&(1<<63) == 0 && rank < 64-h.precision+1 {
		rank++
		w <<= 1
	}
	if rank > h.registers[idx] {
		h.registers[idx] = rank
	}
}
func (h *HyperLogLog) Count() uint64 {
	m := float64(len(h.registers))
	sum := 0.0
	zeros := 0
	for _, r := range h.registers {
		sum += math.Pow(2, -float64(r))
		if r == 0 {
			zeros++
		}
	}
	alpha := 0.7213 / (1 + 1.079/m)
	estimate := alpha * m * m / sum
	if estimate <= 2.5*m && zeros > 0 {
		estimate = m * math.Log(m/float64(zeros))
	}
	return uint64(math.Round(estimate))
}

// HashRing maps keys to cache nodes with limited remapping when nodes change.
type HashRing struct {
	points []uint64
	owners map[uint64]string
}

func NewHashRing(nodes []string, replicas int) *HashRing {
	if replicas < 1 {
		replicas = 64
	}
	r := &HashRing{owners: map[uint64]string{}}
	for _, n := range nodes {
		for i := 0; i < replicas; i++ {
			p := hash64(uint64(i+1), n)
			r.points = append(r.points, p)
			r.owners[p] = n
		}
	}
	sort.Slice(r.points, func(i, j int) bool { return r.points[i] < r.points[j] })
	return r
}
func (r *HashRing) Node(key string) string {
	if len(r.points) == 0 {
		return ""
	}
	p := hash64(0, key)
	i := sort.Search(len(r.points), func(i int) bool { return r.points[i] >= p })
	if i == len(r.points) {
		i = 0
	}
	return r.owners[r.points[i]]
}
