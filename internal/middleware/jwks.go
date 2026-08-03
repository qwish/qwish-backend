package middleware

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/qwish/backend/internal/httpx"
)

type jwksKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type jwksResponse struct {
	Keys []jwksKey `json:"keys"`
}

type jwksCache struct {
	mu sync.RWMutex
	// keys/fetchedAt describe the last successful fetch; lastAttempt also covers
	// failed ones so a dead JWKS endpoint isn't retried per request.
	keys        map[string]*ecdsa.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
	url         string
}

var globalJWKS = &jwksCache{
	keys: make(map[string]*ecdsa.PublicKey),
}

// jwksMinRefreshInterval throttles refetches. Without it an unauthenticated
// caller can send tokens with a bogus `kid` and make every request miss the
// cache, serializing all ES256 auth behind the write lock and hammering the
// JWKS endpoint.
const jwksMinRefreshInterval = time.Minute

func (c *jwksCache) getKey(kid string) (*ecdsa.PublicKey, error) {
	c.mu.RLock()
	key, ok := c.keys[kid]
	fresh := time.Since(c.fetchedAt) < time.Hour
	c.mu.RUnlock()

	if ok && fresh {
		return key, nil
	}
	return c.refresh(kid)
}

func (c *jwksCache) refresh(kid string) (*ecdsa.PublicKey, error) {
	// Claim the right to fetch under the lock, then release it: the HTTP call
	// must not happen while holding the mutex, or one slow JWKS response blocks
	// every concurrent ES256 verification for its whole duration.
	c.mu.Lock()
	// Another goroutine may have refreshed while we waited for the lock, and a
	// recent refresh that didn't produce this kid won't produce it now either.
	if key, ok := c.keys[kid]; ok && time.Since(c.fetchedAt) < time.Hour {
		c.mu.Unlock()
		return key, nil
	}
	if time.Since(c.lastAttempt) < jwksMinRefreshInterval {
		c.mu.Unlock()
		return nil, fmt.Errorf("kid %q not found in jwks", kid)
	}
	c.lastAttempt = time.Now()
	url := c.url
	c.mu.Unlock()

	resp, err := httpx.Client.Get(url) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("parse jwks: %w", err)
	}

	newKeys := make(map[string]*ecdsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kty != "EC" || k.Crv != "P-256" {
			continue
		}
		xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			continue
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			continue
		}
		newKeys[k.Kid] = &ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(xBytes),
			Y:     new(big.Int).SetBytes(yBytes),
		}
	}
	c.mu.Lock()
	c.keys = newKeys
	c.fetchedAt = time.Now()
	key, ok := newKeys[kid]
	c.mu.Unlock()

	if ok {
		return key, nil
	}
	return nil, fmt.Errorf("kid %q not found in jwks", kid)
}

// SupabaseIssuer is the `iss` claim Supabase (and our own passkey token minting)
// puts on access tokens.
func SupabaseIssuer(supabaseURL string) string {
	return strings.TrimRight(supabaseURL, "/") + "/auth/v1"
}

// parseOpts are the validation rules applied to every access token: signature
// algorithm, and the issuer so a token minted for another project can't be
// replayed here.
func parseOpts(supabaseURL string) []jwt.ParserOption {
	return []jwt.ParserOption{
		jwt.WithValidMethods([]string{"HS256", "ES256"}),
		jwt.WithIssuer(SupabaseIssuer(supabaseURL)),
	}
}

// makeKeyFunc returns a jwt.Keyfunc that handles both HS256 and ES256 Supabase tokens.
func makeKeyFunc(jwtSecret, supabaseURL string) jwt.Keyfunc {
	globalJWKS.mu.Lock()
	if globalJWKS.url == "" {
		globalJWKS.url = supabaseURL + "/auth/v1/.well-known/jwks.json"
	}
	globalJWKS.mu.Unlock()

	return func(t *jwt.Token) (interface{}, error) {
		switch t.Method.Alg() {
		case "HS256":
			return []byte(jwtSecret), nil
		case "ES256":
			kid, _ := t.Header["kid"].(string)
			return globalJWKS.getKey(kid)
		default:
			return nil, fmt.Errorf("unexpected signing method: %s", t.Method.Alg())
		}
	}
}
