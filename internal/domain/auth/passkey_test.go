package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/qwish/backend/internal/config"
)

// The passkey refresh token must be long-lived (≈30 days) so users stay signed
// in, carry the passkey_refresh type so /auth/refresh routes it locally, and
// verify against SUPABASE_JWT_SECRET. This guards the TTL + claim contract that
// TryPasskeyRefresh relies on, without needing a database.
func TestMintRefreshTokenTTLAndClaims(t *testing.T) {
	const secret = "test-secret"
	s := &Service{cfg: &config.Config{SupabaseJWTSecret: secret}}

	raw, err := s.mintRefreshToken("uid-123", "admin@example.com")
	if err != nil {
		t.Fatalf("mintRefreshToken: %v", err)
	}

	claims := jwt.MapClaims{}
	tok, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !tok.Valid {
		t.Fatalf("refresh token did not verify with the secret: %v", err)
	}

	if claims["typ"] != "passkey_refresh" {
		t.Errorf("typ = %v, want passkey_refresh", claims["typ"])
	}
	if claims["sub"] != "uid-123" {
		t.Errorf("sub = %v, want uid-123", claims["sub"])
	}

	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil {
		t.Fatalf("missing exp claim: %v", err)
	}
	got := time.Until(exp.Time)
	if got < 29*24*time.Hour || got > 31*24*time.Hour {
		t.Errorf("refresh TTL = %v, want ~30 days", got)
	}

	// A token signed with a different secret must not verify (no forged sessions).
	if _, err := jwt.Parse(raw, func(*jwt.Token) (interface{}, error) {
		return []byte("wrong-secret"), nil
	}, jwt.WithValidMethods([]string{"HS256"})); err == nil {
		t.Error("refresh token verified under the wrong secret")
	}
}

// The access token stays short-lived even though the refresh token is long,
// so a leaked access token has a small blast radius.
func TestMintAccessTokenShortTTL(t *testing.T) {
	s := &Service{cfg: &config.Config{SupabaseJWTSecret: "test-secret"}}
	raw, err := s.mintAccessToken("uid-123", "admin@example.com")
	if err != nil {
		t.Fatalf("mintAccessToken: %v", err)
	}
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (interface{}, error) {
		return []byte("test-secret"), nil
	}); err != nil {
		t.Fatalf("access token did not verify: %v", err)
	}
	exp, _ := claims.GetExpirationTime()
	if exp == nil || time.Until(exp.Time) > 2*time.Hour {
		t.Errorf("access TTL too long: %v", exp)
	}
}
