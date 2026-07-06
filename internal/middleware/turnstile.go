package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// turnstileVerifyURL is Cloudflare's siteverify endpoint.
const turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

var turnstileClient = &http.Client{Timeout: 10 * time.Second}

// VerifyTurnstile validates a Cloudflare Turnstile token against siteverify.
//
// It fails open when secret is empty (feature disabled / unconfigured) so local
// and pre-key deploys keep working. When a secret is set, a missing or invalid
// token returns an error and the caller should reject the request.
// ponytail: no remoteip sent — optional in Turnstile, and behind proxies the
// client IP needs extra parsing. Add it if abuse slips through.
func VerifyTurnstile(ctx context.Context, secret, token string) error {
	if secret == "" {
		return nil // verification disabled
	}
	if strings.TrimSpace(token) == "" {
		return errTurnstile
	}

	form := url.Values{"secret": {secret}, "response": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, turnstileVerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return errTurnstile
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := turnstileClient.Do(req)
	if err != nil {
		return errTurnstile
	}
	defer resp.Body.Close()

	var out struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || !out.Success {
		return errTurnstile
	}
	return nil
}

type turnstileError struct{}

func (turnstileError) Error() string { return "turnstile verification failed" }

var errTurnstile = turnstileError{}
