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
}

func New[V any](capacity int) *Cache[V] {
	return &Cache[V]{items: map[string]entry[V]{}, capacity: capacity}
}
func (c *Cache[V]) Clear() { c.mu.Lock(); c.epoch++; c.items = map[string]entry[V]{}; c.mu.Unlock() }
func (c *Cache[V]) Invalidate(matches func(string) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	for key := range c.items {
		if matches(key) {
			delete(c.items, key)
		}
	}
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
		c.mu.Unlock()
		v, err := safeLoad(load)
		if err != nil {
			return v, err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.epoch != epoch {
			return v, nil
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
