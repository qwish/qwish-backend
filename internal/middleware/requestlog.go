package middleware

import (
	"log"
	"net/http"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// slowRequest is the latency above which a successful request is still worth a
// log line.
const slowRequest = time.Second

// RequestLog logs failures and slow requests, one line each. chi's Logger
// writes two lines for every request including health checks and fast 200s,
// which is pure noise on a busy instance and costs a synchronous write to
// stdout on the request path. Errors and slow requests are the only ones
// anybody reads.
func RequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)

		took := time.Since(start)
		if ww.Status() < 400 && took < slowRequest {
			return
		}
		log.Printf("%d %s %s %s (%d bytes)",
			ww.Status(), r.Method, r.URL.Path, took.Round(time.Millisecond), ww.BytesWritten())
	})
}
