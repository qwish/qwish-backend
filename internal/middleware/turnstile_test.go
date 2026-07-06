package middleware

import (
	"context"
	"testing"
)

func TestVerifyTurnstile_Guards(t *testing.T) {
	// No secret => feature disabled, fails open.
	if err := VerifyTurnstile(context.Background(), "", ""); err != nil {
		t.Fatalf("empty secret should skip verification, got %v", err)
	}
	// Secret set but no token => reject without a network call.
	if err := VerifyTurnstile(context.Background(), "s3cret", ""); err == nil {
		t.Fatal("missing token with secret set should fail")
	}
}
