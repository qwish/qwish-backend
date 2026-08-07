package auth

import (
	"encoding/base64"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/golang-jwt/jwt/v5"
	"github.com/qwish/backend/internal/config"
)

func testService() *Service {
	return &Service{cfg: &config.Config{
		SupabaseJWTSecret: "test-secret",
		WebAuthnRPID:      "qwish.in",
	}}
}

// The `gen` claim is what makes "sign out everywhere" possible: bumping
// token_generation must invalidate tokens already in the wild.
func TestMintedTokensCarryGeneration(t *testing.T) {
	s := testService()

	for _, gen := range []int{0, 1, 42} {
		access, err := s.mintAccessToken("uid-1", "a@example.com", gen)
		if err != nil {
			t.Fatalf("mintAccessToken: %v", err)
		}
		refresh, err := s.mintRefreshToken("uid-1", "a@example.com", gen)
		if err != nil {
			t.Fatalf("mintRefreshToken: %v", err)
		}

		for name, raw := range map[string]string{"access": access, "refresh": refresh} {
			claims := jwt.MapClaims{}
			if _, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (interface{}, error) {
				return []byte("test-secret"), nil
			}); err != nil {
				t.Fatalf("%s token did not verify: %v", name, err)
			}
			if got := tokenGeneration(claims); got != gen {
				t.Errorf("%s token gen = %d, want %d", name, got, gen)
			}
		}
	}
}

// A token minted before migration 040 has no `gen` claim. It must read as
// generation 0 — the column default — so deploying revocation does not sign
// every existing user out.
func TestTokenGenerationDefaultsToZeroWhenClaimAbsent(t *testing.T) {
	if got := tokenGeneration(jwt.MapClaims{}); got != 0 {
		t.Errorf("absent gen = %d, want 0", got)
	}
	// JSON numbers decode as float64; anything else is malformed and must not panic.
	if got := tokenGeneration(jwt.MapClaims{"gen": float64(7)}); got != 7 {
		t.Errorf("float64 gen = %d, want 7", got)
	}
	if got := tokenGeneration(jwt.MapClaims{"gen": "3"}); got != 0 {
		t.Errorf("string gen = %d, want 0 (malformed reads as base generation)", got)
	}
	if got := tokenGeneration(jwt.MapClaims{"gen": nil}); got != 0 {
		t.Errorf("nil gen = %d, want 0", got)
	}
}

func TestPasskeyRefreshSubjectReturnsGeneration(t *testing.T) {
	s := testService()
	raw, err := s.mintRefreshToken("uid-9", "b@example.com", 5)
	if err != nil {
		t.Fatalf("mintRefreshToken: %v", err)
	}

	sub, gen, ok := s.PasskeyRefreshSubject(raw)
	if !ok {
		t.Fatal("PasskeyRefreshSubject rejected a token it minted")
	}
	if sub != "uid-9" {
		t.Errorf("sub = %q, want uid-9", sub)
	}
	if gen != 5 {
		t.Errorf("gen = %d, want 5", gen)
	}

	// An access token is not a refresh token, and must not be accepted as one.
	access, _ := s.mintAccessToken("uid-9", "b@example.com", 5)
	if _, _, ok := s.PasskeyRefreshSubject(access); ok {
		t.Error("an access token was accepted as a passkey refresh token")
	}

	// Forged signature.
	if _, _, ok := s.PasskeyRefreshSubject(raw + "x"); ok {
		t.Error("a tampered refresh token was accepted")
	}
}

// The decoy is the enumeration mitigation. If it is distinguishable from a real
// challenge — by shape, length, or a missing field — the oracle is still open.
func TestDecoyAssertionLooksLikeARealChallenge(t *testing.T) {
	s := testService()

	first, err := s.buildDecoyAssertion()
	if err != nil {
		t.Fatalf("buildDecoyAssertion: %v", err)
	}

	if got := len(first.Response.Challenge); got != decoyChallengeBytes {
		t.Errorf("challenge length = %d, want %d (must match go-webauthn's)", got, decoyChallengeBytes)
	}
	if first.Response.RelyingPartyID != "qwish.in" {
		t.Errorf("rpId = %q, want qwish.in", first.Response.RelyingPartyID)
	}
	if first.Response.UserVerification != protocol.VerificationPreferred {
		t.Errorf("userVerification = %q, want preferred", first.Response.UserVerification)
	}
	// A real assertion for a known account always lists at least one credential;
	// an empty list would give the game away immediately.
	if len(first.Response.AllowedCredentials) != 1 {
		t.Fatalf("allowCredentials = %d, want 1", len(first.Response.AllowedCredentials))
	}
	cd := first.Response.AllowedCredentials[0]
	if len(cd.CredentialID) != decoyCredentialIDBytes {
		t.Errorf("credential id length = %d, want %d", len(cd.CredentialID), decoyCredentialIDBytes)
	}
	if cd.Type != protocol.PublicKeyCredentialType {
		t.Errorf("credential type = %q, want public-key", cd.Type)
	}
	if first.Response.Timeout <= 0 {
		t.Error("timeout must be set, or the browser applies its own and the shape differs")
	}

	// Unpredictable: two decoys must never repeat, or a probe could recognise one.
	second, err := s.buildDecoyAssertion()
	if err != nil {
		t.Fatalf("buildDecoyAssertion (second): %v", err)
	}
	if base64.RawURLEncoding.EncodeToString(first.Response.Challenge) ==
		base64.RawURLEncoding.EncodeToString(second.Response.Challenge) {
		t.Error("two decoy challenges were identical")
	}
	if base64.RawURLEncoding.EncodeToString(first.Response.AllowedCredentials[0].CredentialID) ==
		base64.RawURLEncoding.EncodeToString(second.Response.AllowedCredentials[0].CredentialID) {
		t.Error("two decoy credential ids were identical")
	}
}

// The challenge subject must be built from the normalized email, because the
// user lookup normalizes too. When these disagreed, a capitalised address at
// begin and a lowercase one at finish produced PASSKEY_NO_CHALLENGE.
func TestChallengeSubjectUsesNormalizedEmail(t *testing.T) {
	for _, raw := range []string{"Ann@Example.COM", "  ann@example.com  ", "ann@example.com"} {
		if got, want := userSubject+NormalizeEmail(raw), "u:ann@example.com"; got != want {
			t.Errorf("subject for %q = %q, want %q", raw, got, want)
		}
	}
}
