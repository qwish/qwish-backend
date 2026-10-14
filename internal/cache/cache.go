// Package cache provides bounded TTL caches and coalesces simultaneous misses.
package cache

import (
	"context"
	"fmt"
	"golang.org/x/sync/singleflight"
	"sync"
	"sync/atomic"
	"time"
)

type entry[V any] struct {
	value   V
	expires time.Time
	order   uint64
}
type Cache[V any] struct {
	mu           sync.Mutex
	items        map[string]entry[V]
	epoch        uint64
	sequence     uint64
	capacity     int
	flight       singleflight.Group
	Hits, Misses atomic.Uint64

	// Targeted invalidations must not discard unrelated loads, so each one is
	// numbered and remembered while any load that began before it is running;
	// a finishing load skips storing only if a later invalidation matches it.
	invalidations uint64
	recent        []invalidation
	active        map[uint64]int // loads in flight, by the invalidation count at their start
}

type invalidation struct {
	seq     uint64
	matches func(string) bool
}

func New[V any](capacity int) *Cache[V] {
	return &Cache[V]{items: map[string]entry[V]{}, capacity: capacity, active: map[uint64]int{}}
}
func (c *Cache[V]) Clear() { c.mu.Lock(); c.epoch++; c.items = map[string]entry[V]{}; c.mu.Unlock() }

// Invalidate removes matching entries. Loads already in flight are discarded
// only when their key matches, so frequent narrow invalidations (one user's
// entries) do not stop everyone else's results from being cached.
func (c *Cache[V]) Invalidate(matches func(string) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.invalidations++
	if len(c.active) > 0 {
		c.recent = append(c.recent, invalidation{c.invalidations, matches})
	}
	for key := range c.items {
		if matches(key) {
			delete(c.items, key)
		}
	}
}

// finishLoad drops a load from the in-flight set and forgets invalidations
// that no remaining load could have overlapped. Caller holds c.mu.
func (c *Cache[V]) finishLoad(start uint64) {
	if c.active[start]--; c.active[start] <= 0 {
		delete(c.active, start)
	}
	if len(c.active) == 0 {
		c.recent = nil
		return
	}
	oldest := ^uint64(0)
	for s := range c.active {
		oldest = min(oldest, s)
	}
	kept := c.recent[:0]
	for _, inv := range c.recent {
		if inv.seq > oldest {
			kept = append(kept, inv)
		}
	}
	c.recent = kept
}
func (c *Cache[V]) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.items) }
func (c *Cache[V]) Load(ctx context.Context, key string, ttl time.Duration, load func() (V, error)) (V, error) {
	c.mu.Lock()
	epoch := c.epoch
	if e, ok := c.items[key]; ok && time.Now().Before(e.expires) {
		c.mu.Unlock()
		c.Hits.Add(1)
		return e.value, nil
	}
	c.mu.Unlock()
	c.Misses.Add(1)
	ch := c.flight.DoChan(fmt.Sprintf("%d:%s", epoch, key), func() (any, error) {
		c.mu.Lock()
		if e, ok := c.items[key]; ok && time.Now().Before(e.expires) {
			c.mu.Unlock()
			return e.value, nil
		}
		start := c.invalidations
		c.active[start]++
		c.mu.Unlock()
		v, err := safeLoad(load)
		c.mu.Lock()
		defer c.mu.Unlock()
		defer c.finishLoad(start)
		if err != nil {
			return v, err
		}
		if c.epoch != epoch {
			return v, nil
		}
		for _, inv := range c.recent {
			if inv.seq > start && inv.matches(key) {
				return v, nil
			}
		}
		now := time.Now()
		for k, e := range c.items {
			if !now.Before(e.expires) {
				delete(c.items, k)
			}
		}
		if c.capacity > 0 {
			if len(c.items) >= c.capacity {
				var oldest string
				var seq uint64 = ^uint64(0)
				for k, e := range c.items {
					if e.order < seq {
						oldest, seq = k, e.order
					}
				}
				delete(c.items, oldest)
			}
			c.sequence++
			c.items[key] = entry[V]{v, now.Add(ttl), c.sequence}
		}
		return v, nil
	})
	select {
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			var zero V
			return zero, result.Err
		}
		return result.Val.(V), nil
	}
}

func safeLoad[V any](load func() (V, error)) (v V, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("cache loader panicked: %v", p)
		}
	}()
	return load()
}
