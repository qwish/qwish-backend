# Verification Checklist — Domain Performance + Derived Difficulty

Covers migration `020`, subdomain-aware scoring, the insights breakdown endpoint,
teacher tagging, and the numpie insights UI. Run against a real Postgres +
running backend. Env: set `CRON_SECRET` so the manual cron endpoint is callable;
internal cron routes are only mounted when `AppEnv != "production"`.

## 1. Migration `020`
- [ ] Boot backend; log shows migration `020_quiz_domains` applied (runs on start).
- [ ] `SELECT count(*) FROM domains;` → 6 · `SELECT count(*) FROM subdomains;` → 28.
- [ ] `\d quizzes` shows `domain`, `subdomain`; `\d questions` shows `difficulty` (default 0.60).
- [ ] Existing quizzes backfilled: `SELECT count(*) FROM quizzes WHERE domain IS NULL;` → 0.
- [ ] FK holds: inserting a quiz with `subdomain` whose `domain_slug` differs is rejected at the app layer (see step 4).

## 2. Scoring (real points — change-with-care)
- [ ] Complete a quiz whose questions are still at default difficulty 0.60 →
      content multiplier ≈ 1.0× (no points shift vs. pre-change baseline).
- [ ] Manually set a quiz's questions to `difficulty = 1.0`, complete it →
      points higher than the 0.60 run (multiplier clamps at 1.6×).
- [ ] `points_ledger` row for the attempt reflects the multiplied `points_delta`.
- [ ] Qwish Score difficulty component moves with `questions.difficulty`
      (harder correct answers raise it).
- [ ] `go test ./internal/scheduler/` passes (`deriveDifficulty` model).

## 3. Nightly difficulty job
- [ ] Seed responses: have ≥1 question answered by several users (mix correct/wrong, varied time).
- [ ] `POST /internal/cron/recompute-question-difficulty` (header `X-Cron-Secret: $CRON_SECRET`) → `{"message":"done"}`.
- [ ] `questions.difficulty` for a mostly-wrong/slow question rises toward 1.0;
      a mostly-correct/fast one falls toward 0.4.
- [ ] A brand-new question (0 responses) stays at its prior (subdomain difficulty, else type coeff).

## 4. Teacher tagging
- [ ] `GET /teacher/quizzes/taxonomy` → domain→subdomain tree.
- [ ] `POST /teacher/quizzes` with valid `domain`+`subdomain` → `201`, row persisted.
- [ ] `POST` with `subdomain` not belonging to `domain` → `400 invalid domain or subdomain`.
- [ ] `POST` with `subdomain` but no `domain` → `400`.
- [ ] `PATCH` a draft quiz's domain/subdomain → persists; `GET` returns them.
- [ ] Teacher panel: New Quiz modal shows Domain dropdown → dependent Subdomain
      dropdown (resets on domain change, disabled until a domain is picked).

## 5. Insights breakdown endpoint
- [ ] `GET /users/me/insights/breakdown` → `qwish_score`, `components{}`, `domains[]`.
- [ ] `qwish_score` ≈ `accuracy*50 + difficulty*20 + consistency*15 + speed*10 + activity*5`.
- [ ] Each domain `avg_score` is question-weighted (a 30-Q quiz outweighs a 3-Q one).
- [ ] Subdomain roll-up sums to its domain; `low_sample=true` when `< 10` answered questions.
- [ ] Legacy/untagged attempts appear under `general` / `Mixed`.
- [ ] `GET /users/me/insights/trend?range=4w|12w|all` → bucketed points; `4w`→4, `12w`→12, `all`→12 monthly. Empty buckets carry the previous value. Switching the chart's 4w/12w/all buttons refetches and reshapes the line.

## 6. numpie insights UI
- [ ] Top card shows **only** the Qwish Score gauge (no points); gauge value equals `breakdown.qwish_score`.
- [ ] **Score Breakdown** section: 5 animated bars matching `components`.
- [ ] **Domain Performance** section: per-domain bars + subdomain rows (`↳ Label  84% · 40Q`), `low data` flag on sparse domains.
- [ ] Points still visible below (Quick Stats + Score Progress), not in the top card.
- [ ] Home screen: points pill opens Rewards ("Coming soon").

## Regression
- [ ] `go build ./... && go test ./...` (backend) green.
- [ ] `flutter analyze` (numpie) clean.
- [ ] `bunx tsc --noEmit` (teacher panel) exits 0. (`bun run lint` has 4 pre-existing
      errors in `useApi.ts`, unrelated to this work.)
