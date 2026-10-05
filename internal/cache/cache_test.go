package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoalescesMisses(t *testing.T) {
	c := New[int](2)
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := c.Load(context.Background(), "same", time.Minute, func() (int, error) {
				if calls.Add(1) == 1 {
					close(entered)
				}
				<-release
				return 42, nil
			})
			if err != nil || v != 42 {
				t.Errorf("got %v %v", v, err)
			}
		}()
	}
	<-entered
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("duplicate loads: %d", calls.Load())
	}
}
func TestCancellationDoesNotBlockAnotherWaiter(t *testing.T) {
	c := New[int](2)
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := c.Load(ctx, "key", time.Minute, func() (int, error) { close(entered); <-release; return 7, nil })
		done <- err
	}()
	<-entered
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatal(err)
	}
	close(release)
	v, err := c.Load(context.Background(), "key", time.Minute, func() (int, error) { return 7, nil })
	if err != nil || v != 7 {
		t.Fatalf("%d %v", v, err)
	}
}

func TestInvalidationDoesNotRestoreInflightValue(t *testing.T) {
	c := New[int](2)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.Load(context.Background(), "same", time.Minute, func() (int, error) { close(entered); <-release; return 42, nil })
	}()
	<-entered
	c.Clear()
	close(release)
	<-done
	value, err := c.Load(context.Background(), "same", time.Minute, func() (int, error) { return 99, nil })
	if err != nil || value != 99 {
		t.Fatalf("invalidated load was cached: %d %v", value, err)
	}
}
