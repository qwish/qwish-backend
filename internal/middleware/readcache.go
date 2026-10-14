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

func NewResponseCache() *ResponseCache { return &ResponseCache{cache.New[cachedResponse](1024)} }
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

type actorKey struct{}

// actor is filled in by authentication so ClearOnWrite, which runs outside it,
// knows whose request just wrote.
type actor struct{ user, admin string }

func noteActor(ctx context.Context, user, admin string) {
	if a, ok := ctx.Value(actorKey{}).(*actor); ok {
		a.user, a.admin = user, admin
	}
}

// ClearOnWrite drops the writer's own cached reads after a mutation, so people
// always see their own changes. Other users' entries are left to their TTL or
// to table-level invalidation: one learner answering a question must not
// clear every dashboard.
func (c *ResponseCache) ClearOnWrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
			next.ServeHTTP(w, r)
			return
		}
		who := &actor{}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, who)))
		if who.user != "" || who.admin != "" {
			c.InvalidateUser(who.user, who.admin)
		}
	})
}

// InvalidateUser removes every cached read keyed to this identity.
func (c *ResponseCache) InvalidateUser(user, admin string) {
	head, _ := json.Marshal([]string{user, admin})
	prefix := strings.TrimSuffix(string(head), "]") + ","
	c.store.Invalidate(func(key string) bool { return strings.HasPrefix(key, prefix) })
}

// activityTables change constantly as learners play: attempts, answers, scores,
// streaks, points, sessions and last-seen stamps. Cached reads built on them
// expire with their TTL (10–30s) instead of being cleared on every write; the
// writer's own entries are cleared by ClearOnWrite. Every table not listed
// here still clears the cache, so a new table is safe by default.
var activityTables = map[string]bool{
	"quiz_attempts": true, "quiz_attempt_questions": true, "question_responses": true,
	"clue_reveals": true, "learning_evidence": true, "learning_evidence_misconceptions": true,
	"leaderboard_scores": true, "learner_ratings": true, "learner_topic_mastery": true,
	"quiz_read_stats": true, "attempt_daily_metrics": true, "points_ledger": true,
	"streaks": true, "badges": true, "saved_quizzes": true, "quiz_ratings": true,
	"learning_assignment_recipients": true, "practice_sessions": true,
	// questions churns from difficulty calibration after every completion; an
	// author's edit is visible to the author at once and to others within TTL.
	"questions": true,
	// users is written on every attempt (last_active_at, points, streak). Role,
	// status and institution are checked live by authentication and are part
	// of every cache key, so a permission change never serves stale scope.
	"users": true, "admin_sessions": true,
	"user_notifications": true, "notification_preferences": true, "device_tokens": true,
	"recommendation_bandit_stats": true, "content_delivery_events": true, "profile_views": true,
}

// IsActivityTable reports whether writes to table are left to cache TTLs.
func IsActivityTable(table string) bool { return activityTables[table] }

// Invalidate reacts to a committed write on table. Learner activity is left to
// TTLs; content, membership and configuration changes clear cached reads.
// Taxonomy/metadata entries survive everything except taxonomy changes.
func (c *ResponseCache) Invalidate(table string) {
	if activityTables[table] {
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
