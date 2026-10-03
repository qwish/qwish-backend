package enrollment

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/qwish/backend/internal/domain/auth"
)

var (
	ErrDomainInvalid  = errors.New("invalid domain")
	ErrDomainFreeMail = errors.New("free or disposable mail domain")
	ErrDomainTaken    = errors.New("domain verified for another institute")
)

// ponytail: static list of big free-mail providers; extend when support sees a new one.
var freeMail = map[string]bool{
	"gmail.com": true, "googlemail.com": true, "yahoo.com": true, "yahoo.co.in": true,
	"outlook.com": true, "hotmail.com": true, "live.com": true, "icloud.com": true,
	"me.com": true, "aol.com": true, "proton.me": true, "protonmail.com": true,
	"rediffmail.com": true, "zoho.com": true, "gmx.com": true, "yandex.com": true,
}

type Domain struct {
	Domain     string     `json:"domain"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
}

func NormalizeDomain(raw string) (string, error) {
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if d == "" || strings.ContainsAny(d, "@ /") || !strings.Contains(d, ".") || strings.HasPrefix(d, ".") {
		return "", ErrDomainInvalid
	}
	if freeMail[d] || auth.IsDisposableEmail("x@"+d) {
		return "", ErrDomainFreeMail
	}
	return d, nil
}

func (s *Service) ListDomains(ctx context.Context, instID string) ([]Domain, error) {
	rows, err := s.db.Query(ctx, `SELECT domain, verified_at FROM institution_domains WHERE institution_id=$1 ORDER BY domain`, instID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		var d Domain
		if err := rows.Scan(&d.Domain, &d.VerifiedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AddDomain records a domain as verified. Only super-admins reach this, and
// they verify ownership out of band (or by DNS TXT) before adding it.
func (s *Service) AddDomain(ctx context.Context, instID, raw, adminID string) (Domain, error) {
	d, err := NormalizeDomain(raw)
	if err != nil {
		return Domain{}, err
	}
	var out Domain
	err = s.db.QueryRow(ctx, `INSERT INTO institution_domains (institution_id, domain, verified_at, verified_by)
		VALUES ($1,$2,now(),NULLIF($3,'')::uuid)
		ON CONFLICT (institution_id, domain) DO UPDATE SET verified_at=COALESCE(institution_domains.verified_at, now())
		RETURNING domain, verified_at`, instID, d, adminID).Scan(&out.Domain, &out.VerifiedAt)
	if isUniqueViolation(err, "institution_domains_verified_unique") {
		return Domain{}, ErrDomainTaken
	}
	return out, err
}

func (s *Service) RemoveDomain(ctx context.Context, instID, domain string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM institution_domains WHERE institution_id=$1 AND domain=lower($2)`, instID, domain)
	return err
}
