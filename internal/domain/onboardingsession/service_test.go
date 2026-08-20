package onboardingsession

import (
	"context"
	"testing"
)

func TestSessionLifecycle(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool, nil)

	id, err := svc.Create(ctx, "hi", []string{"verbal", "logical"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, id)
	})

	lang, topics, err := svc.Prefs(ctx, id)
	if err != nil || lang != "hi" || len(topics) != 2 {
		t.Fatalf("Prefs = %q, %v, %v; want hi, 2 topics, nil", lang, topics, err)
	}

	if err := svc.UpdatePrefs(ctx, id, "en", []string{"general"}); err != nil {
		t.Fatalf("UpdatePrefs: %v", err)
	}
	lang, topics, _ = svc.Prefs(ctx, id)
	if lang != "en" || len(topics) != 1 || topics[0] != "general" {
		t.Fatalf("after update: %q %v; want en [general]", lang, topics)
	}
}

func TestExpiredSessionIsInvisible(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool, nil)

	id, err := svc.Create(ctx, "en", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, id)
	})

	if _, err := pool.Exec(ctx,
		`UPDATE onboarding_sessions SET expires_at = now() - interval '1 minute' WHERE id=$1`, id); err != nil {
		t.Fatalf("age the session: %v", err)
	}

	if _, _, err := svc.Prefs(ctx, id); err != ErrSession {
		t.Fatalf("Prefs on expired session = %v; want ErrSession", err)
	}
	if err := svc.UpdatePrefs(ctx, id, "en", nil); err != ErrSession {
		t.Fatalf("UpdatePrefs on expired session = %v; want ErrSession", err)
	}
}
