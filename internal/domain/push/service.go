// Package push delivers mobile push notifications via Firebase Cloud
// Messaging HTTP v1 API.
//
// Configuration: set FCM_PROJECT_ID and FCM_SERVICE_ACCOUNT_JSON env vars.
// FCM_SERVICE_ACCOUNT_JSON must contain the full JSON service-account key
// (the file Firebase generates under Settings → Service accounts →
// "Generate new private key"). When either env var is empty the service
// becomes a no-op so local development continues to function.
package push

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/qwish/backend/internal/httpx"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	tokenAudience    = "https://oauth2.googleapis.com/token"
	scopeMessaging   = "https://www.googleapis.com/auth/firebase.messaging"
	tokenExchangeURL = "https://oauth2.googleapis.com/token"
)

// Service emits push notifications via FCM and prunes invalid tokens.
type Service struct {
	db          *pgxpool.Pool
	projectID   string
	clientEmail string
	privateKey  *rsa.PrivateKey

	mu      sync.Mutex
	cached  string
	expires time.Time
}

type serviceAccount struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	ProjectID   string `json:"project_id"`
}

// NewService constructs the push service. credentialsJSON is the raw service
// account JSON. If either argument is empty, returns a disabled service that
// silently no-ops on send.
func NewService(db *pgxpool.Pool, projectID, credentialsJSON string) *Service {
	s := &Service{db: db, projectID: projectID}
	if projectID == "" || credentialsJSON == "" {
		log.Println("[push] FCM disabled — set FCM_PROJECT_ID and FCM_SERVICE_ACCOUNT_JSON to enable")
		return s
	}
	var sa serviceAccount
	if err := json.Unmarshal([]byte(credentialsJSON), &sa); err != nil {
		log.Printf("[push] failed to parse FCM_SERVICE_ACCOUNT_JSON: %v", err)
		return s
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		log.Println("[push] FCM service account private_key is not PEM")
		return s
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		log.Printf("[push] FCM service account key parse error: %v", err)
		return s
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		log.Println("[push] FCM service account key is not RSA")
		return s
	}
	s.clientEmail = sa.ClientEmail
	s.privateKey = rsaKey
	if s.projectID == "" {
		s.projectID = sa.ProjectID
	}
	return s
}

// Enabled reports whether FCM is configured and operational.
func (s *Service) Enabled() bool { return s.privateKey != nil && s.projectID != "" }

// accessToken returns a cached or freshly-minted OAuth access token.
func (s *Service) accessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	if s.cached != "" && time.Now().Before(s.expires.Add(-30*time.Second)) {
		t := s.cached
		s.mu.Unlock()
		return t, nil
	}
	s.mu.Unlock()

	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   s.clientEmail,
		"scope": scopeMessaging,
		"aud":   tokenAudience,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(s.privateKey)
	if err != nil {
		return "", fmt.Errorf("sign assertion: %w", err)
	}

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", signed)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenExchangeURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpx.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("token exchange %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.cached = out.AccessToken
	s.expires = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	s.mu.Unlock()
	return out.AccessToken, nil
}

// Payload is the cross-platform delivery payload. Title/body show on the lock
// screen; Data is delivered to the app as a string map so it can route the
// user when they tap.
type Payload struct {
	Title string
	Body  string
	Data  map[string]string
}

// SendToUser delivers payload to every registered device token for userID.
// Invalid (404/UNREGISTERED) tokens are pruned. Errors are logged, never
// surfaced — push is best-effort.
func (s *Service) SendToUser(ctx context.Context, userID string, p Payload) {
	if !s.Enabled() || s.db == nil || userID == "" {
		return
	}
	rows, err := s.db.Query(ctx, `SELECT token FROM device_tokens WHERE user_id=$1`, userID)
	if err != nil {
		log.Printf("[push] load tokens for user %s: %v", userID, err)
		return
	}
	defer rows.Close()
	var tokens []string
	for rows.Next() {
		var t string
		rows.Scan(&t)
		tokens = append(tokens, t)
	}
	// Collect dead tokens and prune them in one DELETE rather than one per
	// token — a user with several stale devices paid a round trip each.
	var dead []string
	for _, t := range tokens {
		if err := s.sendOne(ctx, t, p); err != nil {
			if errors.Is(err, errInvalidToken) {
				dead = append(dead, t)
				continue
			}
			log.Printf("[push] send to token: %v", err)
		}
	}
	if len(dead) > 0 {
		s.db.Exec(ctx, `DELETE FROM device_tokens WHERE token = ANY($1)`, dead)
	}
}

var errInvalidToken = errors.New("invalid registration token")

func (s *Service) sendOne(ctx context.Context, deviceToken string, p Payload) error {
	at, err := s.accessToken(ctx)
	if err != nil {
		return err
	}

	msg := map[string]any{
		"message": map[string]any{
			"token": deviceToken,
			"notification": map[string]any{
				"title": p.Title,
				"body":  p.Body,
			},
			"data": p.Data,
			"android": map[string]any{
				"priority": "HIGH",
				"notification": map[string]any{
					"sound":         "default",
					"default_sound": true,
					"channel_id":    "qwish_default",
				},
			},
			"apns": map[string]any{
				"headers": map[string]any{
					"apns-priority": "10",
				},
				"payload": map[string]any{
					"aps": map[string]any{
						"sound":             "default",
						"content-available": 1,
					},
				},
			},
		},
	}
	body, _ := json.Marshal(msg)

	endpoint := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", s.projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+at)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpx.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 404 || resp.StatusCode == 410 {
		return errInvalidToken
	}
	if resp.StatusCode == 400 && bytes.Contains(respBody, []byte("UNREGISTERED")) {
		return errInvalidToken
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("fcm %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
