package middleware

import (
	"testing"
	"time"
)

func TestGCRAAllowsBurstThenRejects(t *testing.T) {
	rl := &rateLimiter{clients: make(map[string]*gcraState), max: 3, window: time.Minute}
	for i := 0; i < 3; i++ {
		if ok, _ := rl.allow("user"); !ok {
			t.Fatalf("request %d in configured burst was rejected", i+1)
		}
	}
	if ok, retry := rl.allow("user"); ok || retry <= 0 {
		t.Fatalf("expected rejection with retry duration, got ok=%v retry=%v", ok, retry)
	}
}

func TestGCRAKeysAreIndependent(t *testing.T) {
	rl := &rateLimiter{clients: make(map[string]*gcraState), max: 1, window: time.Minute}
	if ok, _ := rl.allow("a"); !ok {
		t.Fatal("first key rejected")
	}
	if ok, _ := rl.allow("b"); !ok {
		t.Fatal("second key shared the first key's budget")
	}
}
