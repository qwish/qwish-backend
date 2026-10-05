package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/cache"
	"net/http"
	"strings"
	"time"
)

type cachedResponse struct {
	Status int
	Header http.Header
	Body   []byte
}
type uncacheable struct{ response cachedResponse }

func (e *uncacheable) Error() string { return "response is not cacheable" }

type ResponseCache struct{ store *cache.Cache[cachedResponse] }

func NewResponseCache() *ResponseCache { return &ResponseCache{cache.New[cachedResponse](128)} }
func (c *ResponseCache) Clear()        { c.store.Clear() }
func (c *ResponseCache) Stats() map[string]any {
	return map[string]any{"hits": c.store.Hits.Load(), "misses": c.store.Misses.Load(), "entries": c.store.Len()}
}

type capture struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	overflow bool
}

func (w *capture) Header() http.Header { return w.header }
func (w *capture) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *capture) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.body.Len()+len(b) > 256<<10 {
		w.overflow = true
	}
	return w.body.Write(b)
}

// Wrap is installed at the final handler, after authentication/role middleware.
// Only explicitly selected read-only JSON handlers are eligible.
func (c *ResponseCache) Wrap(ttl time.Duration, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Cache-Control") == "no-cache" {
			h(w, r)
			return
		}
		// A canceled leader may return while its loader is finishing. chi pools
		// route contexts, so the loader owns a copy of the matched parameters.
		if rc := chi.RouteContext(r.Context()); rc != nil {
			copy := *rc
			copy.URLParams.Keys = append([]string(nil), rc.URLParams.Keys...)
			copy.URLParams.Values = append([]string(nil), rc.URLParams.Values...)
			copy.RoutePatterns = append([]string(nil), rc.RoutePatterns...)
			r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, &copy))
		}
		identity := []string{GetUserID(r), GetAdminID(r), GetRole(r), GetInstitutionID(r), r.URL.Path, r.URL.Query().Encode(), r.Header.Get("Accept"), r.Header.Get("Accept-Language"), r.Header.Get("X-Qwish-Client")}
		keyRaw, _ := json.Marshal(identity)
		key := string(keyRaw)
		result, err := c.store.Load(r.Context(), key, ttl, func() (cachedResponse, error) {
			original := &capture{header: make(http.Header)}
			h(original, r)
			if original.status == 0 {
				original.status = 200
			}
			res := cachedResponse{original.status, original.header.Clone(), append([]byte(nil), original.body.Bytes()...)}
			if original.overflow || res.Status != 200 || len(res.Header.Values("Set-Cookie")) > 0 || strings.Contains(res.Header.Get("Cache-Control"), "no-store") || !strings.Contains(res.Header.Get("Content-Type"), "application/json") {
				return res, &uncacheable{res}
			}
			return res, nil
		})
		if err != nil {
			var uncached *uncacheable
			if errors.As(err, &uncached) {
				result = uncached.response
			} else {
				if r.Context().Err() == nil {
					InternalError(w)
				}
				return
			}
		}

		for name, values := range result.Header {
			w.Header()[name] = append([]string(nil), values...)
		}
		// Browser caches must not retain scoped data beyond the server's invalidation.
		w.Header().Set("Cache-Control", "private, no-store")
		w.WriteHeader(result.Status)
		_, _ = w.Write(result.Body)
	}
}

// ClearOnWrite removes local cached reads immediately after successful mutation.
func (c *ResponseCache) ClearOnWrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			c.Invalidate("mutation")
		}
	})
}

// Independent taxonomy/metadata survives ordinary learner traffic. Other
// caches are invalidated conservatively when their dependencies change.
func (c *ResponseCache) Invalidate(table string) {
	switch table {
	case "user_notifications", "notification_preferences", "device_tokens", "recommendation_bandit_stats", "content_delivery_events", "profile_views":
		return
	}
	c.store.Invalidate(func(key string) bool {
		taxonomy := strings.Contains(key, "taxonomy") || strings.Contains(key, "avatars/options") || strings.Contains(key, "metrics/catalog")
		if taxonomy {
			return table == "domains" || table == "subdomains" || table == "curriculum_versions" || table == "curriculum_concepts" || table == "curriculum_chapters"
		}
		return true
	})
}
