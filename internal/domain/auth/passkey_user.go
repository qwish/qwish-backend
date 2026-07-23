package auth

// User (teacher) passkeys. Parallel to passkey.go (which serves admin_accounts)
// but bound to the `users` table and the webauthn_user_credentials table, so the
// production admin passkey path is left untouched. Challenge storage and token
// minting are shared with the admin path; user ceremonies namespace their
// challenge subject with a "u:" prefix to avoid collisions.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/qwish/backend/internal/middleware"
)

// userAccount is the `users`-table analogue of AdminAccount for passkey flows.
type userAccount struct {
	ID          string
	SupabaseUID string
	Name        string
	Email       string
	Status      string
	Role        string
}

const userSubject = "u:" // challenge subject namespace for user ceremonies

// ─── Store: user lookup ────────────────────────────────────────────────────────

func (s *Service) getUserByID(ctx context.Context, id string) (*userAccount, error) {
	var u userAccount
	err := s.db.QueryRow(ctx,
		`SELECT id, supabase_uid, display_name, email, status, role FROM users
		 WHERE id = $1 AND deleted_at IS NULL`, id,
	).Scan(&u.ID, &u.SupabaseUID, &u.Name, &u.Email, &u.Status, &u.Role)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Service) getUserByEmail(ctx context.Context, email string) (*userAccount, error) {
	var u userAccount
	err := s.db.QueryRow(ctx,
		`SELECT id, supabase_uid, display_name, email, status, role FROM users
		 WHERE email = $1 AND deleted_at IS NULL`, email,
	).Scan(&u.ID, &u.SupabaseUID, &u.Name, &u.Email, &u.Status, &u.Role)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Service) getUserBySupabaseUID(ctx context.Context, uid string) (*userAccount, error) {
	var u userAccount
	err := s.db.QueryRow(ctx,
		`SELECT id, supabase_uid, display_name, email, status, role FROM users
		 WHERE supabase_uid = $1 AND deleted_at IS NULL`, uid,
	).Scan(&u.ID, &u.SupabaseUID, &u.Name, &u.Email, &u.Status, &u.Role)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Service) buildPasskeyUserForUser(u *userAccount, creds []storedCredential) *passkeyUser {
	list := make([]webauthn.Credential, len(creds))
	for i, c := range creds {
		list[i] = c.Cred
	}
	return &passkeyUser{
		id:      []byte(u.ID),
		name:    u.Email,
		display: u.Name,
		creds:   list,
	}
}

// ─── Store: user credentials ───────────────────────────────────────────────────

func (s *Service) countUserCredentials(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM webauthn_user_credentials WHERE user_id = $1`, userID,
	).Scan(&n)
	return n, err
}

func (s *Service) listUserCredentials(ctx context.Context, userID string) ([]storedCredential, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, name, credential, is_primary, created_at, last_used_at
		 FROM webauthn_user_credentials WHERE user_id = $1
		 ORDER BY is_primary DESC, created_at`, userID)
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

func (s *Service) saveUserCredential(ctx context.Context, userID, name string, cred *webauthn.Credential) error {
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		`INSERT INTO webauthn_user_credentials (user_id, credential_id, name, credential, sign_count)
		 VALUES ($1, $2, $3, $4, $5)`,
		userID, cred.ID, name, raw, int64(cred.Authenticator.SignCount))
	return err
}

func (s *Service) touchUserCredential(ctx context.Context, cred *webauthn.Credential) {
	raw, err := json.Marshal(cred)
	if err != nil {
		return
	}
	s.db.Exec(ctx,
		`UPDATE webauthn_user_credentials SET credential = $1, sign_count = $2, last_used_at = now()
		 WHERE credential_id = $3`,
		raw, int64(cred.Authenticator.SignCount), cred.ID)
}

func (s *Service) renameUserCredential(ctx context.Context, userID, id, name string) (bool, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE webauthn_user_credentials SET name = $1 WHERE id = $2 AND user_id = $3`,
		name, id, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Service) setPrimaryUserCredential(ctx context.Context, userID, id string) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE webauthn_user_credentials SET is_primary = false
		 WHERE user_id = $1 AND is_primary`, userID); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx,
		`UPDATE webauthn_user_credentials SET is_primary = true
		 WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	return true, tx.Commit(ctx)
}

func (s *Service) deleteUserCredential(ctx context.Context, userID, id string) (bool, error) {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM webauthn_user_credentials WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// TryUserPasskeyRefresh mirrors TryPasskeyRefresh for user (teacher) passkey
// sessions. The refresh handler tries the admin path first, then this.
func (s *Service) TryUserPasskeyRefresh(ctx context.Context, sub string) (access, refresh string, ok bool) {
	u, err := s.getUserBySupabaseUID(ctx, sub)
	if err != nil || u.Status == "suspended" {
		return "", "", false
	}
	if n, err := s.countUserCredentials(ctx, u.ID); err != nil || n == 0 {
		return "", "", false
	}
	a, r, err := s.mintSession(u.SupabaseUID, u.Email)
	if err != nil {
		return "", "", false
	}
	return a, r, true
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

// UserPasskeyRegisterBegin starts enrolling a passkey for the signed-in user.
// POST /api/v1/auth/passkey/user/register/begin
func (h *Handler) UserPasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	if h.svc.wa == nil {
		middleware.Error(w, http.StatusServiceUnavailable, "PASSKEY_DISABLED", "passkeys are not configured")
		return
	}
	userID := middleware.GetUserID(r)
	if userID == "" {
		middleware.Forbidden(w)
		return
	}
	u, err := h.svc.getUserByID(r.Context(), userID)
	if err != nil {
		middleware.Forbidden(w)
		return
	}
	creds, err := h.svc.listUserCredentials(r.Context(), userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	user := h.svc.buildPasskeyUserForUser(u, creds)

	options, session, err := h.svc.wa.BeginRegistration(user)
	if err != nil {
		middleware.Error(w, http.StatusBadRequest, "PASSKEY_BEGIN_FAILED", err.Error())
		return
	}
	if err := h.svc.saveChallenge(r.Context(), userSubject+userID, "register", session); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, options)
}

// UserPasskeyRegisterFinish verifies the attestation and stores the credential.
// POST /api/v1/auth/passkey/user/register/finish  Body: { name?, response }
func (h *Handler) UserPasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	if h.svc.wa == nil {
		middleware.Error(w, http.StatusServiceUnavailable, "PASSKEY_DISABLED", "passkeys are not configured")
		return
	}
	userID := middleware.GetUserID(r)
	if userID == "" {
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

	u, err := h.svc.getUserByID(r.Context(), userID)
	if err != nil {
		middleware.Forbidden(w)
		return
	}
	creds, err := h.svc.listUserCredentials(r.Context(), userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	user := h.svc.buildPasskeyUserForUser(u, creds)

	session, err := h.svc.consumeChallenge(r.Context(), userSubject+userID, "register")
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
	if err := h.svc.saveUserCredential(r.Context(), userID, name, cred); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"message": "passkey registered", "name": name})
}

// UserPasskeyLoginBegin starts a passwordless assertion for the given user email.
// POST /api/v1/auth/passkey/user/login/begin  Body: { email }
func (h *Handler) UserPasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
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

	u, err := h.svc.getUserByEmail(r.Context(), req.Email)
	if err != nil {
		middleware.Error(w, http.StatusNotFound, "PASSKEY_NOT_FOUND", "no passkey is registered for this email")
		return
	}
	creds, err := h.svc.listUserCredentials(r.Context(), u.ID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if len(creds) == 0 {
		middleware.Error(w, http.StatusNotFound, "PASSKEY_NOT_FOUND", "no passkey is registered for this email")
		return
	}
	user := h.svc.buildPasskeyUserForUser(u, creds)

	options, session, err := h.svc.wa.BeginLogin(user)
	if err != nil {
		middleware.Error(w, http.StatusBadRequest, "PASSKEY_BEGIN_FAILED", err.Error())
		return
	}
	if err := h.svc.saveChallenge(r.Context(), userSubject+req.Email, "login", session); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, options)
}

// UserPasskeyLoginFinish verifies the assertion and mints a session.
// POST /api/v1/auth/passkey/user/login/finish  Body: { email, response }
func (h *Handler) UserPasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
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

	u, err := h.svc.getUserByEmail(r.Context(), body.Email)
	if err != nil {
		middleware.Error(w, http.StatusUnauthorized, "PASSKEY_VERIFY_FAILED", "passkey verification failed")
		return
	}
	if u.Status == "suspended" {
		middleware.Error(w, http.StatusForbidden, "ACCOUNT_SUSPENDED", "account is suspended")
		return
	}
	creds, err := h.svc.listUserCredentials(r.Context(), u.ID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	user := h.svc.buildPasskeyUserForUser(u, creds)

	session, err := h.svc.consumeChallenge(r.Context(), userSubject+body.Email, "login")
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

	h.svc.touchUserCredential(r.Context(), cred)
	h.writeUserPasskeySession(w, u)
}

// UserPasskeyLoginBeginDiscoverable starts a usernameless assertion.
// POST /api/v1/auth/passkey/user/login/begin-discoverable
func (h *Handler) UserPasskeyLoginBeginDiscoverable(w http.ResponseWriter, r *http.Request) {
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

// UserPasskeyLoginFinishDiscoverable verifies a usernameless assertion, resolving
// the user from the credential's user handle (the user id set at registration).
// POST /api/v1/auth/passkey/user/login/finish-discoverable  Body: { response }
func (h *Handler) UserPasskeyLoginFinishDiscoverable(w http.ResponseWriter, r *http.Request) {
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

	session, err := h.svc.consumeChallenge(r.Context(), parsed.Response.CollectedClientData.Challenge, "login_discoverable")
	if err != nil {
		middleware.Error(w, http.StatusBadRequest, "PASSKEY_NO_CHALLENGE", "no active login challenge")
		return
	}

	var resolved *userAccount
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		u, err := h.svc.getUserByID(r.Context(), string(userHandle))
		if err != nil {
			return nil, err
		}
		if u.Status == "suspended" {
			return nil, errors.New("account is suspended")
		}
		creds, err := h.svc.listUserCredentials(r.Context(), u.ID)
		if err != nil {
			return nil, err
		}
		resolved = u
		return h.svc.buildPasskeyUserForUser(u, creds), nil
	}

	cred, err := h.svc.wa.ValidateDiscoverableLogin(handler, *session, parsed)
	if err != nil || resolved == nil {
		middleware.Error(w, http.StatusUnauthorized, "PASSKEY_VERIFY_FAILED", "passkey verification failed")
		return
	}

	h.svc.touchUserCredential(r.Context(), cred)
	h.writeUserPasskeySession(w, resolved)
}

// writeUserPasskeySession mints a session for the verified user.
func (h *Handler) writeUserPasskeySession(w http.ResponseWriter, u *userAccount) {
	access, refresh, err := h.svc.mintSession(u.SupabaseUID, u.Email)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"user": map[string]interface{}{
			"id":           u.ID,
			"full_name":    u.Name,
			"display_name": u.Name,
			"email":        u.Email,
			"role":         u.Role,
		},
		"access_token":  access,
		"refresh_token": refresh,
		"is_new_user":   false,
	})
}

// UserPasskeyList returns the signed-in user's registered passkeys.
// GET /api/v1/auth/passkey/user/credentials
func (h *Handler) UserPasskeyList(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		middleware.Forbidden(w)
		return
	}
	creds, err := h.svc.listUserCredentials(r.Context(), userID)
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

// UserPasskeyDelete removes one of the signed-in user's passkeys.
// DELETE /api/v1/auth/passkey/user/credentials/{id}
func (h *Handler) UserPasskeyDelete(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		middleware.Forbidden(w)
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		middleware.BadRequest(w, "id is required")
		return
	}
	ok, err := h.svc.deleteUserCredential(r.Context(), userID, id)
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

// UserPasskeyRename changes the display name of one of the user's passkeys.
// PATCH /api/v1/auth/passkey/user/credentials/{id}  Body: { name }
func (h *Handler) UserPasskeyRename(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
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
	ok, err := h.svc.renameUserCredential(r.Context(), userID, id, name)
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

// UserPasskeySetPrimary marks one of the user's passkeys as primary/default.
// POST /api/v1/auth/passkey/user/credentials/{id}/primary
func (h *Handler) UserPasskeySetPrimary(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		middleware.Forbidden(w)
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		middleware.BadRequest(w, "id is required")
		return
	}
	ok, err := h.svc.setPrimaryUserCredential(r.Context(), userID, id)
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
