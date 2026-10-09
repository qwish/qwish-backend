# Backend reliability and performance changes

## Implemented architecture

Completion now locks only the attempt and its learner. The response snapshot, points ledger, streak milestone credit, badges, notification, and learning/calibration jobs commit together. Repeating completion returns the stored response without awarding again. Historical completions are reconstructed once with `legacy: true`; historical bonus and badge attribution cannot be recovered reliably.

PostgreSQL is the durable queue and coordination layer. Three worker lanes separate learning/calibration/read models, notification delivery, and question generation. Claiming uses `FOR UPDATE SKIP LOCKED`. Jobs have a three-minute lease, a two-minute execution budget, eight attempts, and exponential retry delays capped at five minutes. Database effects and job acknowledgement share a transaction. Provider calls support at least once delivery: email uses a stable provider idempotency header; clients should deduplicate pushes using `notification_id`. A partial multi-device push failure can repeat delivery to a device that succeeded earlier.

Quiz feeds and recommendations read `quiz_read_stats` instead of aggregating all attempts on each request. The read model is rebuilt asynchronously, coalescing pending jobs for a quiz. Popularity, average score, duration, and question difficulty can lag until workers catch up. Learner scores, balances, and milestone credits remain synchronous. Daily reporting counts, average scores, and active-user counts use transactionally maintained per-learner/per-quiz aggregates in Asia/Kolkata. Hourly and unsupported metrics retain their raw source.

Announcements claim scheduled rows and persist recipient jobs in one transaction. Existing `status: sent` means dispatch has been committed. `delivery_state` distinguishes `idle`, `dispatching`, `completed`, and `failed`; the dispatch cron also reconciles delivery state. Teacher in-app notifications and email jobs now commit together.

## Caching

Every cache has a capacity and TTL, coalesces concurrent misses, and prevents an in-flight load from repopulating a cache invalidated during that load.

| Reads | TTL |
| --- | --- |
| Quiz feeds, student stats/milestones, teacher and institution attention/action views | 10 seconds |
| Overviews and learning summaries/matrices | 15 seconds |
| Education, skills, learning preferences, insights, featured quizzes, reports and scoped metrics | 30 seconds |
| Taxonomy, avatar options and metric catalogs | 300 seconds |
| Recommendation results | 15 seconds |

The final handler wrappers cover 51 routes. Authentication and role middleware execute before cache access. Keys include user, admin, role, institution, path, sorted query parameters, and relevant representation headers. Recommendation impressions still execute on cache hits. The HTTP response cache holds at most 128 entries, each with at most 256 KiB of cached body data (32 MiB maximum cached bodies, excluding headers/key overhead). Recommendations and leaderboards are separately bounded to 256 entries.

Mutation requests invalidate mutable local responses; committed database statement triggers broadcast invalidations to all replicas. Independent metadata survives ordinary learner writes. Listener reconnection clears caches because notifications can be missed while disconnected. TTLs bound staleness during listener outages. HTTP clients receive `Cache-Control: private, no-store`; this is server-side caching. Sending `Cache-Control: no-cache` bypasses the response cache. Side-effecting badge reads, balances, notifications, attempt results, signed assets, and public/shared profiles are excluded from this response cache.

## Multiple replicas and connection capacity

Distributed GCRA rate limits use atomic PostgreSQL state, namespaced by route, limiter kind, window, and budget. Database failures return 503 instead of bypassing the limit. This adds a database round trip to limited requests; monitor pool wait time and database load before increasing traffic. Redis can become a later coordination layer if this state becomes a measured bottleneck.

Notification insert triggers broadcast durable IDs through PostgreSQL LISTEN/NOTIFY. Each replica fetches the row and publishes to its own SSE subscribers. Notification IDs are SSE event IDs. `Last-Event-ID` supports bounded replay; `resync` events tell clients to refresh the persisted notification list and unread count, including after a missed notification or overflow. This stream is not an unlimited event archive.

Configuration defaults per API replica:

```dotenv
DB_MIN_CONNS=2
DB_MAX_CONNS=10
WORKER_DB_MAX_CONNS=8
# Optional: the SAME database through a direct or session connection URL.
WORKER_DATABASE_URL=
```

The worker URL must support session-scoped LISTEN; a transaction pooler is unsuitable. When omitted, DATABASE_URL must itself support this. Two listener connections are reserved from the worker pool. The worker maximum must be at least six; the API maximum must be at least two for migrations. At defaults each replica can occupy up to 18 connections, plus deployment migration processes and other clients. Budget total connections across all replicas against the database or pooler capacity. Raising worker concurrency is not exposed as an environment setting yet; three lanes start at one worker each per replica.

## Rollout

1. Configure worker session connectivity and reserve the combined connection budget. Keep both URLs on the same database.
2. Apply migrations **090–093** before serving the new binary. They add durable jobs, rate state, completion snapshots, read models, announcement delivery state, daily aggregates, and indexes. Aggregate backfills and index creation touch existing histories; allow a deployment window appropriate to database size.
3. `MIGRATE_ONLY=true` runs migrations and exits before initializing cloud clients. `deploy/deploy.sh` now runs this one-off task before starting the updated service. API startup still checks migrations under the existing migration lock.
4. Rating backfill no longer runs on every replica startup. Run `BACKFILL_RATINGS=true` explicitly as a one-off when needed; errors exit unsuccessfully.
5. Keep announcement/points/streak cron schedules enabled. Workers run inside the API process and stop with it. Use a coordinated rollout for completion/reward traffic when replacing older binaries that lack response snapshots and atomic milestones.
6. Check worker backlog, listener reconnect logs, failed jobs, cache hit rate, pool waits, and endpoint latency after rollout. No production migration or deployment was performed during implementation.

These migrations are additive, but old binaries retain the earlier completion/delivery behavior. Do not remove schema objects on rollback while jobs or new responses exist. Process remaining jobs with a compatible worker binary.

## Operations

`GET /api/v1/internal/profile/operational-metrics` is enabled only with CRON_SECRET and uses the same secret authorization as internal cron routes. It reports normalized route counts/errors/latency histograms, approximate bucket-based p50/p95/p99, API and worker pool acquisition statistics, cache hit/miss counts, job backlog/oldest pending age, and database lock waiters. Request logs include X-Request-Id for failed or slow requests. Route/cache counters are process-local; collect each replica. Existing internal quiz-list profiling supplies query plans. Enable and inspect pg_stat_statements separately when supported by your database; this change does not enable database extensions automatically.

Investigate failed jobs before retrying. A bounded example for a reviewed job:

```sql
SELECT id, kind, attempts, last_error
FROM background_jobs WHERE state = 'failed' ORDER BY updated_at DESC LIMIT 50;

UPDATE background_jobs
SET state='pending', attempts=0, available_at=now(),
    lease_until=NULL, lease_token=NULL, last_error=NULL, updated_at=now()
WHERE id='<reviewed-job-id>'::uuid AND state='failed';
```

Completed jobs are removed in batches after 30 days; failed jobs remain for inspection. Completed generation results and their idempotency keys therefore have the same retention. Business references and completion snapshots independently prevent completion rewards from being replayed after job cleanup.

## Validation

All migrations were applied to an isolated local PostgreSQL 18 database. The full Go suite passed against that database. The race detector passed for cache, middleware, durable jobs, attempts, quiz feeds, streaks, notifications, and fresh-schema migration/learning checks. Regression coverage includes cache isolation/invalidation/coalescing, distributed rate-limit concurrency, outbox rollback/retry, atomic milestone credit, completion while the shared quiz row is locked, duplicate completion responses, and feed cursor traversal including NULL publication dates.

No production load benchmark was performed. Establish a baseline and compare endpoint p95/p99, lock waiters, pool acquisition time, and queue lag under representative traffic before claiming a specific speedup. Provider delivery and proxy streaming behavior still require environment-specific integration checks.

## Leaderboard rank snapshots

Leaderboard requests reuse a bounded per-scope/domain snapshot for 30 seconds.
It contains the top 100 campus or top 10,000 national entries plus an 82-bucket
score histogram and eligible total. Below the exact-rank cutoff the API returns
`my_rank: 0` and a conservative integer `my_top_percent`. Ties retain SQL RANK
semantics. Every student request checks eligibility and membership live with one
primary-key lookup; personal placement uses the cached score distribution.
Snapshot misses read the histogram and top list in one repeatable-read transaction.
Concurrent refreshes coalesce; at most 16 snapshots are retained per replica.
Score/user changes age out through the TTL, while institution/enrollment changes
and listener reconnects clear snapshots. List pagination stops at the cutoff;
`meta.total` still reports the full eligible population. No production latency
benchmark has been performed.
