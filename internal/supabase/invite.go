// Package supabase wraps Supabase GoTrue admin operations shared across domains.
package supabase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/config"
)

// InviteClient is the single entry point for provisioning Supabase auth users
// via invite. Both internal-admin and institution-admin flows route through it
// so invite behaviour (UID resolution, branded email link, duplicate handling)
// stays consistent.
type InviteClient struct {
	db  *pgxpool.Pool
	cfg *config.Config
}

func NewInviteClient(db *pgxpool.Pool, cfg *config.Config) *InviteClient {
	return &InviteClient{db: db, cfg: cfg}
}

// InviteResult describes the outcome of resolving a Supabase user for an email.
type InviteResult struct {
	UID            string // Supabase auth user id (always set when err == nil)
	ActionLink     string // one-time invite/magic link; empty when the user already existed
	AlreadyExisted bool   // true when the email already had a Supabase account
}

// Invite creates (or resolves) a Supabase auth user for email using the admin
// generate_link API (type=invite). metadata is attached as user_metadata.
// redirectTo, when non-empty, is where the action link lands the user after
// verification (must be in the Supabase project's redirect allow-list); when
// empty Supabase falls back to the project Site URL.
//
// Returns the user's UID plus a one-time ActionLink the caller should email via
// Resend. If the user already exists, ActionLink is empty and AlreadyExisted is
// true (caller should send a welcome email instead). An error is returned only
// when no UID can be resolved at all (Supabase unreachable/rejected AND no
// existing auth.users row) — callers must not create a local account in that
// case, as it would be an unauthenticatable orphan.
func (c *InviteClient) Invite(ctx context.Context, email, redirectTo string, metadata map[string]string) (*InviteResult, error) {
	res := &InviteResult{}

	payload := map[string]interface{}{
		"type":  "invite",
		"email": email,
		"data":  metadata,
	}
	if redirectTo != "" {
		payload["redirect_to"] = redirectTo
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.SupabaseURL+"/auth/v1/admin/generate_link", bytes.NewReader(body))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("apikey", c.cfg.SupabaseServiceKey)
		req.Header.Set("Authorization", "Bearer "+c.cfg.SupabaseServiceKey)

		resp, derr := http.DefaultClient.Do(req)
		if derr == nil {
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			if resp.StatusCode < 400 {
				var parsed struct {
					ActionLink string `json:"action_link"`
					ID         string `json:"id"`
					User       struct {
						ID string `json:"id"`
					} `json:"user"`
				}
				json.Unmarshal(raw, &parsed)
				res.ActionLink = parsed.ActionLink
				if parsed.ID != "" {
					res.UID = parsed.ID
				} else if parsed.User.ID != "" {
					res.UID = parsed.User.ID
				}
			} else {
				fmt.Printf("[supabase] generate_link failed (status %d): %s\n", resp.StatusCode, string(raw))
				if resp.StatusCode == 422 || bytes.Contains(raw, []byte("already")) {
					res.AlreadyExisted = true
				}
			}
		} else {
			fmt.Printf("[supabase] generate_link request failed: %v\n", derr)
		}
	} else {
		fmt.Printf("[supabase] failed to build generate_link request: %v\n", err)
	}

	// Fallback: link call failed or returned no id — resolve an existing user.
	if res.UID == "" {
		if qerr := c.db.QueryRow(ctx, `SELECT id FROM auth.users WHERE email = $1`, email).Scan(&res.UID); qerr == nil {
			res.AlreadyExisted = true
			res.ActionLink = "" // no fresh link for an already-existing user
		}
	}

	if res.UID == "" {
		return nil, fmt.Errorf("could not resolve Supabase user for %s", email)
	}
	return res, nil
}

// SetPassword sets (resets) the password for an existing Supabase auth user via
// the admin API. Used to (re)issue institution-admin login credentials.
func (c *InviteClient) SetPassword(ctx context.Context, uid, password string) error {
	body, _ := json.Marshal(map[string]interface{}{"password": password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		c.cfg.SupabaseURL+"/auth/v1/admin/users/"+uid, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", c.cfg.SupabaseServiceKey)
	req.Header.Set("Authorization", "Bearer "+c.cfg.SupabaseServiceKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase set-password failed (status %d): %s", resp.StatusCode, string(raw))
	}
	return nil
}
