package middleware

import (
	"crypto/ecdsa"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret"
const testSupabaseURL = "https://proj.supabase.co"

func signHS256(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return raw
}

// A token signed with our secret but issued by a different Supabase project
// must not be accepted here.
func TestParseOptsRejectsForeignIssuer(t *testing.T) {
	keyFunc := makeKeyFunc(testSecret, testSupabaseURL)
	opts := parseOpts(testSupabaseURL)
	exp := time.Now().Add(time.Hour).Unix()

	ours := signHS256(t, jwt.MapClaims{"sub": "u1", "iss": SupabaseIssuer(testSupabaseURL), "exp": exp})
	if _, err := jwt.Parse(ours, keyFunc, opts...); err != nil {
		t.Fatalf("own issuer rejected: %v", err)
	}

	for _, iss := range []string{"https://other.supabase.co/auth/v1", ""} {
		claims := jwt.MapClaims{"sub": "u1", "exp": exp}
		if iss != "" {
			claims["iss"] = iss
		}
		if _, err := jwt.Parse(signHS256(t, claims), keyFunc, opts...); err == nil {
			t.Errorf("iss=%q accepted, want rejected", iss)
		}
	}
}

// An unknown kid must not trigger a JWKS fetch on every request.
func TestJWKSRefreshThrottled(t *testing.T) {
	c := &jwksCache{keys: map[string]*ecdsa.PublicKey{}, url: "http://127.0.0.1:0/jwks"}

	for i := 0; i < 3; i++ {
		if _, err := c.getKey("bogus"); err == nil {
			t.Fatal("unknown kid resolved")
		}
	}
	if c.lastAttempt.IsZero() {
		t.Fatal("lastAttempt not stamped; failed fetches will be retried per request")
	}
	// Second and third calls must have been short-circuited by the throttle,
	// i.e. lastAttempt stayed at the first attempt's timestamp.
	first := c.lastAttempt
	if _, err := c.getKey("bogus"); err == nil {
		t.Fatal("unknown kid resolved")
	}
	if !c.lastAttempt.Equal(first) {
		t.Error("refetched within throttle window")
	}
}
