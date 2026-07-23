package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/golang-jwt/jwt/v5"
	"github.com/qwish/backend/internal/middleware"
)

var errChallengeExpired = errors.New("challenge expired")

// challengeTTL bounds how long a begun ceremony stays valid before finish.
const challengeTTL = 5 * time.Minute

// passkeyAccessTTL / passkeyRefreshTTL govern the self-minted session tokens
// issued after a successful assertion. The access token mirrors a Supabase
// access token (short-lived, HS256, SUPABASE_JWT_SECRET); the refresh token is
// our own JWT recognised by the /auth/refresh handler. The refresh token is
// long-lived (30 days) so passkey users stay signed in, but it is re-validated
// against the account + a live credential on every refresh (see
// TryPasskeyRefresh), so deleting the passkey or suspending the account revokes
// the session immediately despite the long TTL.
const (
	passkeyAccessTTL  = time.Hour
	passkeyRefreshTTL = 30 * 24 * time.Hour
)

// passkeyUser adapts an admin account to the go-webauthn User interface.
type passkeyUser struct {
	id      []byte
	name    string
	display string
	creds   []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte                         { return u.id }
func (u *passkeyUser) WebAuthnName() string                       { return u.name }
func (u *passkeyUser) WebAuthnDisplayName() string                { return u.display }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// ─── Store: admin lookup ──────────────────────────────────────────────────────

func (s *Service) getAdminByID(ctx context.Context, id string) (*AdminAccount, error) {
	var a AdminAccount
	err := s.db.QueryRow(ctx,
		`SELECT id, supabase_uid, name, email, role, status FROM admin_accounts
		 WHERE id = $1 AND deleted_at IS NULL`, id,
	).Scan(&a.ID, &a.SupabaseUID, &a.Name, &a.Email, &a.Role, &a.Status)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Service) getActiveAdminByEmail(ctx context.Context, email string) (*AdminAccount, error) {
	var a AdminAccount
	err := s.db.QueryRow(ctx,
		`SELECT id, supabase_uid, name, email, role, status FROM admin_accounts
		 WHERE email = $1 AND deleted_at IS NULL`, email,
	).Scan(&a.ID, &a.SupabaseUID, &a.Name, &a.Email, &a.Role, &a.Status)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Service) getAdminBySupabaseUID(ctx context.Context, uid string) (*AdminAccount, error) {
	var a AdminAccount
	err := s.db.QueryRow(ctx,
		`SELECT id, supabase_uid, name, email, role, status FROM admin_accounts
		 WHERE supabase_uid = $1 AND deleted_at IS NULL`, uid,
	).Scan(&a.ID, &a.SupabaseUID, &a.Name, &a.Email, &a.Role, &a.Status)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// countCredentials reports how many passkeys an admin still has. A passkey
// session is only refreshable while at least one credential survives, so a user
// who removes their last passkey (e.g. from a lost device) revokes every
// long-lived refresh token they hold.
func (s *Service) countCredentials(ctx context.Context, adminID string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM webauthn_credentials WHERE admin_id = $1`, adminID,
	).Scan(&n)
	return n, err
}

// ─── Store: credentials ───────────────────────────────────────────────────────

type storedCredential struct {
	ID         string
	Name       string
	Cred       webauthn.Credential
	Primary    bool
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

func (s *Service) listCredentials(ctx context.Context, adminID string) ([]storedCredential, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, name, credential, is_primary, created_at, last_used_at
		 FROM webauthn_credentials WHERE admin_id = $1
		 ORDER BY is_primary DESC, created_at`, adminID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []storedCredential
	for rows.Next() {
		var sc storedCredential
		var raw []byte
		if err := rows.Scan(&sc.ID, &sc.Name, &raw, &sc.Primary, &sc.CreatedAt, &sc.LastUsedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &sc.Cred); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// renameCredential updates a passkey's display name, scoped to its owner.
func (s *Service) renameCredential(ctx context.Context, adminID, id, name string) (bool, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE webauthn_credentials SET name = $1 WHERE id = $2 AND admin_id = $3`,
		name, id, adminID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// setPrimaryCredential marks one passkey primary and clears the rest, in a
// single transaction so the partial unique index never sees two primaries.
func (s *Service) setPrimaryCredential(ctx context.Context, adminID, id string) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE webauthn_credentials SET is_primary = false
		 WHERE admin_id = $1 AND is_primary`, adminID); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx,
		`UPDATE webauthn_credentials SET is_primary = true
		 WHERE id = $1 AND admin_id = $2`, id, adminID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	return true, tx.Commit(ctx)
}

func (s *Service) buildPasskeyUser(admin *AdminAccount, creds []storedCredential) *passkeyUser {
	list := make([]webauthn.Credential, len(creds))
	for i, c := range creds {
		list[i] = c.Cred
	}
	return &passkeyUser{
		id:      []byte(admin.ID),
		name:    admin.Email,
		display: admin.Name,
		creds:   list,
	}
}

func (s *Service) saveCredential(ctx context.Context, adminID, name string, cred *webauthn.Credential) error {
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		`INSERT INTO webauthn_credentials (admin_id, credential_id, name, credential, sign_count)
		 VALUES ($1, $2, $3, $4, $5)`,
		adminID, cred.ID, name, raw, int64(cred.Authenticator.SignCount))
	return err
}

func (s *Service) touchCredential(ctx context.Context, cred *webauthn.Credential) {
	raw, err := json.Marshal(cred)
	if err != nil {
		return
	}
	s.db.Exec(ctx,
		`UPDATE webauthn_credentials SET credential = $1, sign_count = $2, last_used_at = now()
		 WHERE credential_id = $3`,
		raw, int64(cred.Authenticator.SignCount), cred.ID)
}

func (s *Service) deleteCredential(ctx context.Context, adminID, id string) (bool, error) {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM webauthn_credentials WHERE id = $1 AND admin_id = $2`, id, adminID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ─── Store: challenges ────────────────────────────────────────────────────────

func (s *Service) saveChallenge(ctx context.Context, subject, purpose string, sd *webauthn.SessionData) error {
	raw, err := json.Marshal(sd)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		`INSERT INTO webauthn_challenges (subject, purpose, session_data, expires_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (subject, purpose)
		 DO UPDATE SET session_data = EXCLUDED.session_data, expires_at = EXCLUDED.expires_at, created_at = now()`,
		subject, purpose, raw, time.Now().Add(challengeTTL))
	return err
}

// consumeChallenge loads and deletes a stored ceremony, rejecting expired ones.
func (s *Service) consumeChallenge(ctx context.Context, subject, purpose string) (*webauthn.SessionData, error) {
	var raw []byte
	var expires time.Time
	err := s.db.QueryRow(ctx,
		`DELETE FROM webauthn_challenges WHERE subject = $1 AND purpose = $2
		 RETURNING session_data, expires_at`, subject, purpose,
	).Scan(&raw, &expires)
	if err != nil {
		return nil, err
	}
	if time.Now().After(expires) {
		return nil, errChallengeExpired
	}
	var sd webauthn.SessionData
	if err := json.Unmarshal(raw, &sd); err != nil {
		return nil, err
	}
	return &sd, nil
}

// ─── Token minting ────────────────────────────────────────────────────────────

// mintAccessToken issues a Supabase-compatible HS256 access token. The auth
// middleware verifies the signature against SUPABASE_JWT_SECRET and resolves the
// admin via the `sub` (supabase_uid) claim, so a passkey session is accepted by
// every protected route exactly like an OTP session.
func (s *Service) mintAccessToken(supabaseUID, email string) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":   supabaseUID,
		"email": email,
		"role":  "authenticated",
		"aud":   "authenticated",
		"iat":   now.Unix(),
		"exp":   now.Add(passkeyAccessTTL).Unix(),
		"amr":   []map[string]any{{"method": "webauthn"}},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.cfg.SupabaseJWTSecret))
}

// mintRefreshToken issues our own refresh JWT (distinct `typ`) so /auth/refresh
// can renew a passkey session without involving Supabase.
func (s *Service) mintRefreshToken(supabaseUID, email string) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":   supabaseUID,
		"email": email,
		"typ":   "passkey_refresh",
		"iat":   now.Unix(),
		"exp":   now.Add(passkeyRefreshTTL).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.cfg.SupabaseJWTSecret))
}

func (s *Service) mintSession(supabaseUID, email string) (access, refresh string, err error) {
	if access, err = s.mintAccessToken(supabaseUID, email); err != nil {
		return "", "", err
	}
	if refresh, err = s.mintRefreshToken(supabaseUID, email); err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

// PasskeyRefreshSubject validates a refresh token and, if it is one of our
// passkey refresh JWTs, returns its subject (supabase_uid). Lets the refresh
// handler try the admin and user passkey paths without each re-parsing.
func (s *Service) PasskeyRefreshSubject(refreshToken string) (sub string, ok bool) {
	tok, err := jwt.Parse(refreshToken, func(t *jwt.Token) (interface{}, error) {
		return []byte(s.cfg.SupabaseJWTSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !tok.Valid {
		return "", false
	}
	claims, _ := tok.Claims.(jwt.MapClaims)
	if claims == nil || claims["typ"] != "passkey_refresh" {
		return "", false
	}
	sub, _ = claims["sub"].(string)
	return sub, sub != ""
}

// TryPasskeyRefresh renews a passkey session from its refresh token. It returns
// ok=false (without error) when the token isn't one of ours, letting the caller
// fall back to the Supabase refresh path. Opaque Supabase refresh tokens are not
// JWTs, so they never parse here.
func (s *Service) TryPasskeyRefresh(ctx context.Context, refreshToken string) (access, refresh string, ok bool) {
	tok, err := jwt.Parse(refreshToken, func(t *jwt.Token) (interface{}, error) {
		return []byte(s.cfg.SupabaseJWTSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !tok.Valid {
		return "", "", false
	}
	claims, _ := tok.Claims.(jwt.MapClaims)
	if claims == nil || claims["typ"] != "passkey_refresh" {
		return "", "", false
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", "", false
	}

	// Re-validate against live state so a 30-day token can't outlive the
	// account or the passkey that authorised it: the admin must still exist,
	// be non-suspended, and retain at least one credential.
	admin, err := s.getAdminBySupabaseUID(ctx, sub)
	if err != nil || admin.Status == "suspended" {
		return "", "", false
	}
	if n, err := s.countCredentials(ctx, admin.ID); err != nil || n == 0 {
		return "", "", false
	}

	a, r, err := s.mintSession(admin.SupabaseUID, admin.Email)
	if err != nil {
		return "", "", false
	}
	return a, r, true
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

// PasskeyRegisterBegin starts enrolling a new passkey for the authenticated
// admin. Requires an admin session (OTP or existing passkey).
// POST /api/v1/auth/passkey/register/begin
func (h *Handler) PasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	if h.svc.wa == nil {
		middleware.Error(w, http.StatusServiceUnavailable, "PASSKEY_DISABLED", "passkeys are not configured")
		return
	}
	adminID := middleware.GetAdminID(r)
	if adminID == "" {
		middleware.Forbidden(w)
		return
	}
	admin, err := h.svc.getAdminByID(r.Context(), adminID)
	if err != nil {
		middleware.Forbidden(w)
		return
	}
	creds, err := h.svc.listCredentials(r.Context(), adminID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	user := h.svc.buildPasskeyUser(admin, creds)

	options, session, err := h.svc.wa.BeginRegistration(user)
	if err != nil {
		middleware.Error(w, http.StatusBadRequest, "PASSKEY_BEGIN_FAILED", err.Error())
		return
	}
	if err := h.svc.saveChallenge(r.Context(), adminID, "register", session); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, options)
}

// PasskeyRegisterFinish verifies the attestation and stores the credential.
// POST /api/v1/auth/passkey/register/finish  Body: { name?, response }
func (h *Handler) PasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	if h.svc.wa == nil {
		middleware.Error(w, http.StatusServiceUnavailable, "PASSKEY_DISABLED", "passkeys are not configured")
		return
	}
	adminID := middleware.GetAdminID(r)
	if adminID == "" {
		middleware.Forbidden(w)
		return
	}
	var body struct {
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Response) == 0 {
		middleware.BadRequest(w, "response is required")
		return
	}

	admin, err := h.svc.getAdminByID(r.Context(), adminID)
	if err != nil {
		middleware.Forbidden(w)
		return
	}
	creds, err := h.svc.listCredentials(r.Context(), adminID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	user := h.svc.buildPasskeyUser(admin, creds)

	session, err := h.svc.consumeChallenge(r.Context(), adminID, "register")
	if err != nil {
		middleware.Error(w, http.StatusBadRequest, "PASSKEY_NO_CHALLENGE", "no active registration challenge")
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(body.Response))
	if err != nil {
		middleware.Error(w, http.StatusBadRequest, "PASSKEY_INVALID_RESPONSE", "could not parse attestation")
		return
	}
	cred, err := h.svc.wa.CreateCredential(user, *session, parsed)
	if err != nil {
		middleware.Error(w, http.StatusBadRequest, "PASSKEY_VERIFY_FAILED", err.Error())
		return
	}

	name := body.Name
	if name == "" {
		name = "Passkey"
	}
	if err := h.svc.saveCredential(r.Context(), adminID, name, cred); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"message": "passkey registered", "name": name})
}

// PasskeyLoginBegin starts a passwordless assertion for the given admin email.
// POST /api/v1/auth/passkey/login/begin  Body: { email }
func (h *Handler) PasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if h.svc.wa == nil {
		middleware.Error(w, http.StatusServiceUnavailable, "PASSKEY_DISABLED", "passkeys are not configured")
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		middleware.BadRequest(w, "email is required")
		return
	}

	admin, err := h.svc.getActiveAdminByEmail(r.Context(), req.Email)
	if err != nil {
		middleware.Error(w, http.StatusNotFound, "PASSKEY_NOT_FOUND", "no passkey is registered for this email")
		return
	}
	creds, err := h.svc.listCredentials(r.Context(), admin.ID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if len(creds) == 0 {
		middleware.Error(w, http.StatusNotFound, "PASSKEY_NOT_FOUND", "no passkey is registered for this email")
		return
	}
	user := h.svc.buildPasskeyUser(admin, creds)

	options, session, err := h.svc.wa.BeginLogin(user)
	if err != nil {
		middleware.Error(w, http.StatusBadRequest, "PASSKEY_BEGIN_FAILED", err.Error())
		return
	}
	if err := h.svc.saveChallenge(r.Context(), req.Email, "login", session); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, options)
}

// PasskeyLoginFinish verifies the assertion and mints a session.
// POST /api/v1/auth/passkey/login/finish  Body: { email, response }
func (h *Handler) PasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	if h.svc.wa == nil {
		middleware.Error(w, http.StatusServiceUnavailable, "PASSKEY_DISABLED", "passkeys are not configured")
		return
	}
	var body struct {
		Email    string          `json:"email"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" || len(body.Response) == 0 {
		middleware.BadRequest(w, "email and response are required")
		return
	}

	admin, err := h.svc.getActiveAdminByEmail(r.Context(), body.Email)
	if err != nil {
		middleware.Error(w, http.StatusUnauthorized, "PASSKEY_VERIFY_FAILED", "passkey verification failed")
		return
	}
	if admin.Status == "suspended" {
		middleware.Error(w, http.StatusForbidden, "ACCOUNT_SUSPENDED", "account is suspended")
		return
	}
	creds, err := h.svc.listCredentials(r.Context(), admin.ID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	user := h.svc.buildPasskeyUser(admin, creds)

	session, err := h.svc.consumeChallenge(r.Context(), body.Email, "login")
	if err != nil {
		middleware.Error(w, http.StatusBadRequest, "PASSKEY_NO_CHALLENGE", "no active login challenge")
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(body.Response))
	if err != nil {
		middleware.Error(w, http.StatusBadRequest, "PASSKEY_INVALID_RESPONSE", "could not parse assertion")
		return
	}
	cred, err := h.svc.wa.ValidateLogin(user, *session, parsed)
	if err != nil {
		middleware.Error(w, http.StatusUnauthorized, "PASSKEY_VERIFY_FAILED", "passkey verification failed")
		return
	}

	// First successful passkey assertion accepts a pending invite, mirroring OTP.
	if admin.Status == "pending" || admin.Status == "invite_failed" {
		h.svc.ActivateAdmin(r.Context(), admin.ID)
	}
	h.svc.touchCredential(r.Context(), cred)
	h.writePasskeySession(w, admin)
}

// writePasskeySession mints a session for the verified admin and writes the
// standard auth payload shared by the email and discoverable login finishers.
func (h *Handler) writePasskeySession(w http.ResponseWriter, admin *AdminAccount) {
	access, refresh, err := h.svc.mintSession(admin.SupabaseUID, admin.Email)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"user": map[string]interface{}{
			"id":           admin.ID,
			"full_name":    admin.Name,
			"display_name": admin.Name,
			"email":        admin.Email,
			"role":         admin.Role,
		},
		"access_token":  access,
		"refresh_token": refresh,
		"is_new_user":   false,
	})
}

// PasskeyList returns the authenticated admin's registered passkeys.
// GET /api/v1/auth/passkey/credentials
func (h *Handler) PasskeyList(w http.ResponseWriter, r *http.Request) {
	adminID := middleware.GetAdminID(r)
	if adminID == "" {
		middleware.Forbidden(w)
		return
	}
	creds, err := h.svc.listCredentials(r.Context(), adminID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	out := make([]map[string]interface{}, 0, len(creds))
	for _, c := range creds {
		out = append(out, map[string]interface{}{
			"id":           c.ID,
			"name":         c.Name,
			"is_primary":   c.Primary,
			"created_at":   c.CreatedAt,
			"last_used_at": c.LastUsedAt,
		})
	}
	middleware.JSON(w, http.StatusOK, out)
}

// PasskeyDelete removes one of the authenticated admin's passkeys.
// DELETE /api/v1/auth/passkey/credentials/{id}
func (h *Handler) PasskeyDelete(w http.ResponseWriter, r *http.Request) {
	adminID := middleware.GetAdminID(r)
	if adminID == "" {
		middleware.Forbidden(w)
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		middleware.BadRequest(w, "id is required")
		return
	}
	ok, err := h.svc.deleteCredential(r.Context(), adminID, id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if !ok {
		middleware.NotFound(w, "passkey")
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "passkey removed"})
}

// PasskeyRename changes the display name of one of the admin's passkeys.
// PATCH /api/v1/auth/passkey/credentials/{id}  Body: { name }
func (h *Handler) PasskeyRename(w http.ResponseWriter, r *http.Request) {
	adminID := middleware.GetAdminID(r)
	if adminID == "" {
		middleware.Forbidden(w)
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || id == "" {
		middleware.BadRequest(w, "id and name are required")
		return
	}
	name := body.Name
	if name == "" {
		name = "Passkey"
	}
	ok, err := h.svc.renameCredential(r.Context(), adminID, id, name)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if !ok {
		middleware.NotFound(w, "passkey")
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "passkey renamed", "name": name})
}

// PasskeySetPrimary marks one of the admin's passkeys as their primary/default.
// POST /api/v1/auth/passkey/credentials/{id}/primary
func (h *Handler) PasskeySetPrimary(w http.ResponseWriter, r *http.Request) {
	adminID := middleware.GetAdminID(r)
	if adminID == "" {
		middleware.Forbidden(w)
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		middleware.BadRequest(w, "id is required")
		return
	}
	ok, err := h.svc.setPrimaryCredential(r.Context(), adminID, id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if !ok {
		middleware.NotFound(w, "passkey")
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "primary passkey updated"})
}

// PasskeyLoginBeginDiscoverable starts a usernameless (conditional-UI / autofill)
// assertion: no email is supplied, so the browser offers any passkey bound to
// this site. The ceremony is keyed by its own challenge for the finish step.
// POST /api/v1/auth/passkey/login/begin-discoverable
func (h *Handler) PasskeyLoginBeginDiscoverable(w http.ResponseWriter, r *http.Request) {
	if h.svc.wa == nil {
		middleware.Error(w, http.StatusServiceUnavailable, "PASSKEY_DISABLED", "passkeys are not configured")
		return
	}
	options, session, err := h.svc.wa.BeginDiscoverableLogin()
	if err != nil {
		middleware.Error(w, http.StatusBadRequest, "PASSKEY_BEGIN_FAILED", err.Error())
		return
	}
	if err := h.svc.saveChallenge(r.Context(), session.Challenge, "login_discoverable", session); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, options)
}

// PasskeyLoginFinishDiscoverable verifies a usernameless assertion. The admin is
// resolved from the credential's user handle (the admin id we set at
// registration), so no email is required.
// POST /api/v1/auth/passkey/login/finish-discoverable  Body: { response }
func (h *Handler) PasskeyLoginFinishDiscoverable(w http.ResponseWriter, r *http.Request) {
	if h.svc.wa == nil {
		middleware.Error(w, http.StatusServiceUnavailable, "PASSKEY_DISABLED", "passkeys are not configured")
		return
	}
	var body struct {
		Response json.RawMessage `json:"response"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Response) == 0 {
		middleware.BadRequest(w, "response is required")
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(body.Response))
	if err != nil {
		middleware.Error(w, http.StatusBadRequest, "PASSKEY_INVALID_RESPONSE", "could not parse assertion")
		return
	}

	// The ceremony was stored under its challenge, which the authenticator
	// echoes back in the client data — use it to recover the SessionData.
	session, err := h.svc.consumeChallenge(r.Context(), parsed.Response.CollectedClientData.Challenge, "login_discoverable")
	if err != nil {
		middleware.Error(w, http.StatusBadRequest, "PASSKEY_NO_CHALLENGE", "no active login challenge")
		return
	}

	// Resolve the admin from the credential's user handle during validation.
	var resolved *AdminAccount
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		admin, err := h.svc.getAdminByID(r.Context(), string(userHandle))
		if err != nil {
			return nil, err
		}
		if admin.Status == "suspended" {
			return nil, errors.New("account is suspended")
		}
		creds, err := h.svc.listCredentials(r.Context(), admin.ID)
		if err != nil {
			return nil, err
		}
		resolved = admin
		return h.svc.buildPasskeyUser(admin, creds), nil
	}

	cred, err := h.svc.wa.ValidateDiscoverableLogin(handler, *session, parsed)
	if err != nil || resolved == nil {
		middleware.Error(w, http.StatusUnauthorized, "PASSKEY_VERIFY_FAILED", "passkey verification failed")
		return
	}

	if resolved.Status == "pending" || resolved.Status == "invite_failed" {
		h.svc.ActivateAdmin(r.Context(), resolved.ID)
	}
	h.svc.touchCredential(r.Context(), cred)
	h.writePasskeySession(w, resolved)
}
