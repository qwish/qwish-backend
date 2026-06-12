# Implementation Plan — qwish-backend API Type Fixes

> Audit context: backend is 100% REST (HTTP + JSON via chi). Most routes are correctly typed. This plan addresses the gaps: real-time delivery, upload bandwidth, cron duplication, and optional live-quiz support.

---

## Phase 1: Cron Duplication Fix (P0 — 1 hr)

**Problem:** `runInProcessCron` (`cmd/api/main.go:432`) AND `/internal/cron/*` HTTP endpoints both active in prod. Double-execution risk.

**Steps:**
1. Decide source of truth. Recommend: **keep in-process** (already has pg advisory lock at `lockKey=7654321`, prevents multi-instance dup).
2. Gate HTTP cron routes behind env flag:
   ```go
   if cfg.AppEnv != "production" {
       r.Route("/internal/cron", ...) // dev/manual trigger only
   }
   ```
3. Or inverse: delete in-process, rely on Render Cron Jobs hitting HTTP endpoints (simpler, but needs `render.yaml` cron entries).
4. Update `render.yaml` accordingly.

**Files:** `cmd/api/main.go`, `render.yaml`

---

## Phase 2: SSE for Notifications (P1 — 4-6 hrs)

**Problem:** Frontends poll `/users/me/notifications/unread-count`. Wasteful + delayed.

**Steps:**
1. New endpoint `GET /api/v1/users/me/notifications/stream` in `internal/domain/notification/handler.go`.
2. SSE handler skeleton:
   ```go
   func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
       w.Header().Set("Content-Type", "text/event-stream")
       w.Header().Set("Cache-Control", "no-cache")
       w.Header().Set("Connection", "keep-alive")
       flusher := w.(http.Flusher)
       userID := mw.UserID(r.Context())
       ch := h.svc.Subscribe(userID)
       defer h.svc.Unsubscribe(userID, ch)
       for {
           select {
           case <-r.Context().Done(): return
           case n := <-ch:
               fmt.Fprintf(w, "data: %s\n\n", n.JSON())
               flusher.Flush()
           }
       }
   }
   ```
3. Add in-memory pub/sub in `notification.Service`:
   - `map[userID][]chan Notification` + `sync.RWMutex`
   - `Subscribe`, `Unsubscribe`, `Publish` methods
   - Call `Publish` inside existing `notifSvc.SetPusher` callback (`cmd/api/main.go:54`).
4. Remove `chimw.Timeout(30s)` for this route (long-lived) — wrap route group without it.
5. Heartbeat: send `: ping\n\n` every 25s to defeat proxy timeouts.
6. Scale note: in-memory pub/sub = single-instance only. If horizontal scale → add Postgres `LISTEN/NOTIFY` or Redis pub/sub.

**Files:** `internal/domain/notification/{service.go,handler.go}`, `cmd/api/main.go` (route + timeout exclusion)

**Client change:** Frontend swaps polling for `EventSource('/api/v1/users/me/notifications/stream')`. Keep `unread-count` for initial load.

---

## Phase 3: Presigned R2 Uploads (P1 — 3-4 hrs)

**Problem:** `/upload/image` (handler at `internal/domain/upload`) proxies bytes through Go → R2. Wastes Render bandwidth, slower for client.

**Steps:**
1. Add `POST /api/v1/upload/presign` returning:
   ```json
   { "uploadUrl": "https://...r2...?X-Amz-...", "publicUrl": "...", "key": "uploads/2026/05/uuid.jpg", "expiresIn": 300 }
   ```
2. Handler logic:
   - Validate content-type (whitelist `image/jpeg|png|webp`) + size header.
   - Generate UUID key with prefix.
   - Use `s3.NewPresignClient(r2Client).PresignPutObject` with 5-min expiry.
   - Return URL.
3. Keep existing `/upload/image` for backwards compat 1 release cycle, then delete.
4. Client uploads directly: `PUT` to `uploadUrl` with file body.
5. Optional: `POST /upload/confirm` to register the key in DB if tracking needed.

**Files:** `internal/storage/r2.go` (add presign helper), `internal/domain/upload/handler.go`

---

## Phase 4: WebSocket for Live Quiz (P2 — only if roadmap confirms)

**Trigger:** Decide first — is Kahoot-style live multiplayer on roadmap? If no, skip.

**Steps:**
1. Add dep: `github.com/coder/websocket` (modern, std lib API).
2. New package `internal/domain/livequiz/`:
   - `Hub` — `map[roomID]*Room`, manages lifecycle
   - `Room` — `map[clientID]*Client`, broadcast channel, current question state
   - `Client` — websocket conn, send chan, read/write goroutines
3. Endpoint `GET /api/v1/ws/quiz/{quizId}` — upgrade to WS, JWT in query param or first message (browsers can't set auth header on WS).
4. Message protocol (JSON):
   - Client→server: `{type:"join"|"answer", payload:...}`
   - Server→client: `{type:"question"|"score"|"leaderboard"|"end", payload:...}`
5. Persist results via existing `attempt.Service` when round ends — reuse, don't fork.
6. Exclude WS route from `chimw.Timeout`.
7. Scale: single-instance OK for MVP. Multi-instance → Redis pub/sub between hubs.

**Files:** new `internal/domain/livequiz/{hub.go,room.go,client.go,handler.go}`, `cmd/api/main.go`, `go.mod`

---

## Phase 5: Live Leaderboard (P2 — bundle with Phase 4)

If live quiz exists, push leaderboard updates over same WS connection (`type:"leaderboard"` message). Skip separate SSE endpoint. Static leaderboard REST stays as-is for browse/profile views.

---

## Rollout Order

1. **Week 1:** Phase 1 (cron) — pure cleanup, no client impact.
2. **Week 1-2:** Phase 3 (presign) — backend + frontend coordination, parallel deploy.
3. **Week 2-3:** Phase 2 (SSE) — backend ship, frontend opt-in, then remove poll.
4. **Later/conditional:** Phase 4-5 (WebSocket) — product decision gate.

## Testing

- Phase 1: integration test `pg_try_advisory_lock` blocks second process.
- Phase 2: `curl -N` SSE endpoint, trigger notification via existing flow, assert delivery <100ms.
- Phase 3: presign → PUT → fetch public URL → assert 200.
- Phase 4: load test 1k concurrent WS conns per room with `vegeta`/custom client.

## Risks

- **SSE behind CDN/proxy:** Cloudflare/Render proxies may buffer. Test in prod-like env. Heartbeat mitigates.
- **Presign CORS:** R2 bucket CORS policy must allow frontend origins for direct PUT.
- **WS auth:** JWT in query string logs to access logs — prefer first-message auth handshake.
- **In-memory pub/sub:** Breaks on horizontal scale. Document constraint or use Postgres `LISTEN/NOTIFY` upfront.
