package auth

import (
	_ "embed"
	"strings"
)

// disposable_domains.txt is the community list from
// github.com/disposable-email-domains/disposable-email-domains.
// ponytail: static snapshot, re-download the file to pick up new domains.
//
//go:embed disposable_domains.txt
var disposableList string

var disposableDomains = func() map[string]struct{} {
	m := make(map[string]struct{})
	for _, d := range strings.Fields(disposableList) {
		m[strings.ToLower(d)] = struct{}{}
	}
	return m
}()

// IsDisposableEmail reports whether email belongs to a temporary-inbox
// provider, including subdomains of a listed domain (x.mailinator.com).
func IsDisposableEmail(email string) bool {
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return false
	}
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(email[at+1:])), ".")
	for domain != "" {
		if _, ok := disposableDomains[domain]; ok {
			return true
		}
		dot := strings.IndexByte(domain, '.')
		if dot < 0 {
			return false
		}
		domain = domain[dot+1:]
	}
	return false
}
