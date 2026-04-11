package middleware

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
	mu        sync.RWMutex
	keys      map[string]*ecdsa.PublicKey
	fetchedAt time.Time
	url       string
}

var globalJWKS = &jwksCache{
	keys: make(map[string]*ecdsa.PublicKey),
}

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
	c.mu.Lock()
	defer c.mu.Unlock()

	resp, err := http.Get(c.url) //nolint:gosec
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
	c.keys = newKeys
	c.fetchedAt = time.Now()

	if key, ok := c.keys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("kid %q not found in jwks", kid)
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
