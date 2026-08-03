// Package httpx holds the HTTP client used for every outbound call to a third
// party (Supabase Auth, Resend, FCM, Cloudflare).
package httpx

import (
	"net/http"
	"time"
)

// Client replaces http.DefaultClient, which has no timeout at all: a hung
// upstream would otherwise pin the request goroutine — and its database pool
// connection — until the client gave up. Ten seconds is well past the p99 of
// every provider we call and well short of the 30s router timeout, so a slow
// upstream surfaces as a provider error rather than a request timeout.
var Client = &http.Client{Timeout: 10 * time.Second}
