package middleware

import (
	"context"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"sync"
	"time"
)

var observations = struct {
	sync.Mutex
	routes map[string]*routeStats
}{routes: map[string]*routeStats{}}
var latencyBounds = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 120, 900}

type routeStats struct {
	Count        uint64   `json:"count"`
	Errors       uint64   `json:"server_errors"`
	TotalSeconds float64  `json:"total_seconds"`
	Buckets      []uint64 `json:"latency_buckets"`
}

func ObserveRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		route := ""
		if rc := chi.RouteContext(r.Context()); rc != nil {
			route = rc.RoutePattern()
		}
		if route == "" {
			route = "unmatched"
		}
		key := r.Method + " " + route
		seconds := time.Since(start).Seconds()
		observations.Lock()
		defer observations.Unlock()
		stat := observations.routes[key]
		if stat == nil {
			stat = &routeStats{Buckets: make([]uint64, len(latencyBounds)+1)}
			observations.routes[key] = stat
		}
		stat.Count++
		stat.TotalSeconds += seconds
		if ww.Status() >= 500 {
			stat.Errors++
		}
		i := 0
		for i < len(latencyBounds) && seconds > latencyBounds[i] {
			i++
		}
		stat.Buckets[i]++
	})
}
func poolSnapshot(p *pgxpool.Pool) map[string]any {
	s := p.Stat()
	return map[string]any{"max": s.MaxConns(), "total": s.TotalConns(), "acquired": s.AcquiredConns(), "idle": s.IdleConns(), "acquire_count": s.AcquireCount(), "acquire_seconds": s.AcquireDuration().Seconds(), "empty_acquire_count": s.EmptyAcquireCount(), "canceled_acquire_count": s.CanceledAcquireCount()}
}
func OperationalMetrics(api, worker *pgxpool.Pool, c *ResponseCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		routes := map[string]routeStats{}
		percentiles := map[string]map[string]float64{}
		observations.Lock()
		for k, v := range observations.routes {
			copy := *v
			copy.Buckets = append([]uint64(nil), v.Buckets...)
			routes[k] = copy
			percentiles[k] = map[string]float64{"p50": quantile(copy, .5), "p95": quantile(copy, .95), "p99": quantile(copy, .99)}
		}
		observations.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		rows, err := worker.Query(ctx, `SELECT kind,state,count(*),CASE WHEN state='pending' THEN EXTRACT(epoch FROM now()-min(created_at)) ELSE 0 END FROM background_jobs GROUP BY kind,state`)
		backlog := []map[string]any{}
		if err == nil {
			for rows.Next() {
				var kind, state string
				var count int64
				var age float64
				if e := rows.Scan(&kind, &state, &count, &age); e != nil {
					err = e
					break
				}
				backlog = append(backlog, map[string]any{"kind": kind, "state": state, "count": count, "oldest_pending_seconds": age})
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
		}
		var lockWaiters int
		_ = worker.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&lockWaiters)
		data := map[string]any{"routes": routes, "latency_percentile_upper_seconds": percentiles, "latency_bucket_upper_seconds": latencyBounds, "api_pool": poolSnapshot(api), "worker_pool": poolSnapshot(worker), "cache": c.Stats(), "jobs": backlog, "database_lock_waiters": lockWaiters}
		if err != nil {
			data["jobs_error"] = "job statistics unavailable"
		}
		JSON(w, 200, data)
	}
}

func quantile(stat routeStats, q float64) float64 {
	if stat.Count == 0 {
		return 0
	}
	target := uint64(float64(stat.Count) * q)
	if float64(target) < float64(stat.Count)*q {
		target++
	}
	if target == 0 {
		target = 1
	}
	var seen uint64
	for i, n := range stat.Buckets {
		seen += n
		if seen >= target {
			if i < len(latencyBounds) {
				return latencyBounds[i]
			}
			return 900
		}
	}
	return 0
}
