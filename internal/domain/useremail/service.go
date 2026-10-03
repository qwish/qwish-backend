// Package useremail owns a student's secondary emails. The login email stays on
// users.email; these prove institute affiliation (domain admission, invites).
package useremail

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/auth"
)

var (
	ErrInvalidEmail    = errors.New("invalid email")
	ErrEmailTaken      = errors.New("email verified on another account")
	ErrIsLoginEmail    = errors.New("email is the login email")
	ErrBadCode         = errors.New("wrong or expired code")
	ErrTooManyAttempts = errors.New("too many attempts")
	ErrNotFound        = errors.New("email not found")
)

const (
	codeTTL     = 10 * time.Minute
	maxAttempts = 5
)

type Email struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	Verified   bool       `json:"verified"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
}

type Service struct {
	db   *pgxpool.Pool
	send func(ctx context.Context, to, code string) error
}

func NewService(db *pgxpool.Pool, send func(ctx context.Context, to, code string) error) *Service {
	return &Service{db: db, send: send}
}

func normalize(raw string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if a, err := mail.ParseAddress(e); err != nil || a.Address != e || auth.IsDisposableEmail(e) {
		return "", ErrInvalidEmail
	}
	return e, nil
}

func hash(code string) string { s := sha256.Sum256([]byte(code)); return hex.EncodeToString(s[:]) }

func newCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func (s *Service) List(ctx context.Context, userID string) ([]Email, error) {
	rows, err := s.db.Query(ctx, `SELECT id, email, verified_at FROM user_emails WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Email{}
	for rows.Next() {
		var e Email
		if err := rows.Scan(&e.ID, &e.Email, &e.VerifiedAt); err != nil {
			return nil, err
		}
		e.Verified = e.VerifiedAt != nil
		out = append(out, e)
	}
	return out, rows.Err()
}

// Add stores the address unverified (or refreshes its code) and sends a code.
func (s *Service) Add(ctx context.Context, userID, raw string) (Email, error) {
	addr, err := normalize(raw)
	if err != nil {
		return Email{}, err
	}
	var login string
	if err := s.db.QueryRow(ctx, `SELECT lower(btrim(email)) FROM users WHERE id=$1`, userID).Scan(&login); err != nil {
		return Email{}, err
	}
	if login == addr {
		return Email{}, ErrIsLoginEmail
	}
	var taken bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_emails WHERE email=$1 AND verified_at IS NOT NULL AND user_id<>$2)
		OR EXISTS(SELECT 1 FROM users WHERE lower(btrim(email))=$1 AND id<>$2 AND deleted_at IS NULL)`, addr, userID).Scan(&taken); err != nil {
		return Email{}, err
	}
	if taken {
		return Email{}, ErrEmailTaken
	}
	return s.issue(ctx, userID, addr)
}

// Resend issues a fresh code for an unverified address the user already added.
func (s *Service) Resend(ctx context.Context, userID, emailID string) (Email, error) {
	var addr string
	err := s.db.QueryRow(ctx, `SELECT email FROM user_emails WHERE id=$1 AND user_id=$2 AND verified_at IS NULL`, emailID, userID).Scan(&addr)
	if errors.Is(err, pgx.ErrNoRows) {
		return Email{}, ErrNotFound
	}
	if err != nil {
		return Email{}, err
	}
	return s.issue(ctx, userID, addr)
}

func (s *Service) issue(ctx context.Context, userID, addr string) (Email, error) {
	code, err := newCode()
	if err != nil {
		return Email{}, err
	}
	var e Email
	err = s.db.QueryRow(ctx, `INSERT INTO user_emails (user_id, email, code_hash, code_expires_at, attempts)
		VALUES ($1,$2,$3,now()+$4::interval,0)
		ON CONFLICT (user_id, email) DO UPDATE SET code_hash=EXCLUDED.code_hash, code_expires_at=EXCLUDED.code_expires_at, attempts=0
		WHERE user_emails.verified_at IS NULL
		RETURNING id, email, verified_at`, userID, addr, hash(code), codeTTL.String()).Scan(&e.ID, &e.Email, &e.VerifiedAt)
	if errors.Is(err, pgx.ErrNoRows) { // already verified on this account
		err = s.db.QueryRow(ctx, `SELECT id, email, verified_at FROM user_emails WHERE user_id=$1 AND email=$2`, userID, addr).Scan(&e.ID, &e.Email, &e.VerifiedAt)
		e.Verified = true
		return e, err
	}
	if err != nil {
		return Email{}, err
	}
	return e, s.send(ctx, addr, code)
}

func (s *Service) Verify(ctx context.Context, userID, emailID, code string) (Email, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Email{}, err
	}
	defer tx.Rollback(ctx)
	var e Email
	var codeHash *string
	var expires *time.Time
	var attempts int
	err = tx.QueryRow(ctx, `SELECT id, email, verified_at, code_hash, code_expires_at, attempts
		FROM user_emails WHERE id=$1 AND user_id=$2 FOR UPDATE`, emailID, userID).
		Scan(&e.ID, &e.Email, &e.VerifiedAt, &codeHash, &expires, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return Email{}, ErrNotFound
	}
	if err != nil {
		return Email{}, err
	}
	if e.VerifiedAt != nil {
		e.Verified = true
		return e, nil
	}
	if attempts >= maxAttempts {
		return Email{}, ErrTooManyAttempts
	}
	if codeHash == nil || expires == nil || time.Now().After(*expires) || hash(strings.TrimSpace(code)) != *codeHash {
		if _, err := tx.Exec(ctx, `UPDATE user_emails SET attempts=attempts+1 WHERE id=$1`, emailID); err != nil {
			return Email{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Email{}, err
		}
		return Email{}, ErrBadCode
	}
	err = tx.QueryRow(ctx, `UPDATE user_emails SET verified_at=now(), code_hash=NULL, code_expires_at=NULL
		WHERE id=$1 RETURNING verified_at`, emailID).Scan(&e.VerifiedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Email{}, ErrEmailTaken
	}
	if err != nil {
		return Email{}, err
	}
	e.Verified = true
	return e, tx.Commit(ctx)
}

// Remove deletes the address. Memberships it earned are kept; it only stops
// counting for future domain admission and invites.
func (s *Service) Remove(ctx context.Context, userID, emailID string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM user_emails WHERE id=$1 AND user_id=$2`, emailID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
