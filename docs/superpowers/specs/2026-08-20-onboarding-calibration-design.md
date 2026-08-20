# Onboarding Calibration Flow — Design

Date: 2026-08-20
Projects: `qwish-backend` (Go), `numpie` (Flutter)

## Problem

A first-run user today sees a six-slide carousel, is offered a random demo
quiz, and is then dropped at the login screen. Nothing the user does before
signup is carried forward: the demo is graded statelessly and discarded, and
the new account starts empty — no preferences, no score, no history.

The new flow personalises before the pitch is spent, gives the user a reason
to finish the quiz, and makes signup the moment that saves work already done
rather than a toll gate in front of it.

## Flow

```
/onboarding          3 slides (was 6)
   -> "Get started"
/personalise         language + topic multi-select -> POST session
   -> "Start calibrating your Qwish score"
/calibrate           recommended quizzes, filtered by picked topics
   -> tap a quiz
/calibrate/:id       play
   -> submit
/calibrate/result    correct/total + per-question review; score card LOCKED
   -> "Create account to unlock"
/login -> /otp -> /create-profile    (carries session_id)
   -> claim
/home                real Qwish Score present
```

## Decisions

| Question | Decision |
|---|---|
| Is "Qwish Score" new? | No. It already exists: `scoring.CalculateQwishScore`, written to `quiz_attempts.score_pct`, aggregated and rendered in Insights. Calibration seeds the first data point; no new metric, no new score column. |
| What do personalisation answers drive? | Preferred language and topics only. Profession is dropped from the original sketch. Topics filter the recommended quiz list. |
| What does language do? | App UI language. l10n scaffolding lands with this work; English is the only locale shipped. |
| Where do pre-signup quizzes come from? | Public, published quizzes filtered by `domain`. Not the `is_demo` set. |
| How does pre-auth state survive? | Server-side pending session, claimed by id at `create-profile`. |
| What does the user see before signup? | Correct/total and per-question review. The Qwish Score itself renders locked. |

## Taxonomy — reuse, do not invent

Topics are the existing `domains` table (migration 020): `aptitude`,
`quantitative`, `logical`, `verbal`, `computer_science`, `general`. Quizzes
already carry `quizzes.domain` and `quizzes.subdomain`. No new topic table.

## Backend

New package `internal/domain/onboardingsession`. The existing
`internal/domain/onboarding/handler.go` serves institution registration and is
not touched.

### Endpoints

All public, no auth. Rate limited to 30 requests per 10 minutes, matching
`/demo/quizzes/{quizId}/score`.

| Method | Path | Body / Params | Returns |
|---|---|---|---|
| POST | `/api/v1/onboarding/session` | `{language, topics[]}` | `{session_id}` |
| PATCH | `/api/v1/onboarding/session/{id}` | `{language?, topics[]?}` | `{ok}` |
| GET | `/api/v1/onboarding/session/{id}/recommendations` | — | `{data: [quiz summaries]}` |
| GET | `/api/v1/onboarding/session/{id}/quizzes/{quizId}` | — | `{data: [questions]}` |
| POST | `/api/v1/onboarding/session/{id}/submit` | `{quiz_id, answers}` | `{total_correct, total_questions, review[]}` |

Recommendations reuse the public-quiz predicate already in
`internal/domain/quiz/service.go:113`
(`visibility = 'public' AND status = 'published' AND deleted_at IS NULL`) plus
`domain = ANY($topics)`. Empty topics means no domain filter.

The questions endpoint strips correct answers, mirroring the response shape of
`/demo/quizzes/{quizId}` so the Flutter model is shared.

`submit` grades server-side and stores the raw answers on the session row. It
deliberately does **not** return `score_pct` — the score is the signup unlock.

### Validation

- `language` must be one of `en`, `hi`, `mr`; anything else is rejected. Only
  `en` renders as a locale today; the other two are stored preferences that
  take effect when their ARB files land.
- `topics` entries must exist in `domains.slug`; unknown slugs are rejected.
- `quiz_id` in `submit` must be public, published, and not deleted.
- A session may be submitted once. A second submit is rejected.

## Data model

`migrations/041_onboarding_calibration.sql`:

```sql
CREATE TABLE IF NOT EXISTS onboarding_sessions (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  language     TEXT NOT NULL DEFAULT 'en',
  topics       TEXT[] NOT NULL DEFAULT '{}',
  quiz_id      UUID REFERENCES quizzes(id),
  responses    JSONB,
  submitted_at TIMESTAMPTZ,
  claimed_by   UUID REFERENCES users(id),
  claimed_at   TIMESTAMPTZ,
  expires_at   TIMESTAMPTZ NOT NULL DEFAULT now() + interval '24 hours',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_onboarding_sessions_expires
  ON onboarding_sessions(expires_at) WHERE claimed_by IS NULL;

ALTER TABLE users ADD COLUMN IF NOT EXISTS preferred_language TEXT NOT NULL DEFAULT 'en';
ALTER TABLE users ADD COLUMN IF NOT EXISTS interest_domains   TEXT[] NOT NULL DEFAULT '{}';
```

RLS: the table is reached only through the service-role connection, and the
session id is the bearer of authority. Follow the pattern of the other tables
covered in migration 033 — enable RLS with no permissive policy for the anon
role.

## Claim at signup

`POST /api/v1/auth/create-profile` accepts an optional `onboarding_session`
field. Inside the transaction that already creates the user
(`internal/domain/auth/handler.go:159`):

1. Load the session `FOR UPDATE`. Missing, expired, or already claimed means
   skip to step 4 — never an error surfaced to the user, who has just created
   an account and must land on `/home` regardless.
2. Copy `language` -> `users.preferred_language`, `topics` ->
   `users.interest_domains`.
3. If `responses` is present, replay them through the normal attempt path
   (start -> record responses -> finish) so `score_pct`, points, streak, and
   the ledger row are all produced by `internal/domain/attempt` unchanged.
4. Mark the session `claimed_by` / `claimed_at`.

Replaying through the existing engine rather than writing a `quiz_attempts`
row directly is the point of this design: the scoring formula, the point
economy, and the streak rules stay in one place.

### Accepted consequences

- The calibration quiz counts as the user's first real attempt at that quiz.
  A later replay of the same quiz earns no points, via the existing
  `isRepeatAttempt` rule in `attempt/service.go`.
- Anti-cheat fields on the replayed attempt row are empty. The attempt did not
  happen under an authenticated session and there is nothing truthful to put
  there.

## Cleanup

The scheduler (`internal/scheduler`) gains a daily job that deletes unclaimed
sessions past `expires_at`, running alongside the streak rollover.

## Flutter

New feature slice `lib/features/onboarding/` following the existing structure
(`data/`, `presentation/`, `bloc/`).

- `carousel_data.dart` cuts to three slides: the hook, quiz types, and
  streaks. The leaderboard and play-and-win slides are removed; the demo slide
  is replaced by the personalisation call to action.
- Routes added to `route_names.dart` and `app_router.dart`: `/personalise`,
  `/calibrate`, `/calibrate/:id`, `/calibrate/result`. The router's global
  redirect must treat all of them as public, the way `/demo` is treated today.
- The existing `/demo` routes and `DemoLauncherScreen` stay as they are.
- The play screen reuses the structure of `DemoPlayScreen`; question models
  are shared with the demo repository since the response shape matches.
- Session id is persisted to local storage so an app kill mid-flow does not
  lose the run, and is cleared once claimed.
- Skip on personalisation applies defaults: `en`, all six domains. Skip on the
  result screen goes to `/login` with no session, which is today's behaviour.

### Localisation

Add `flutter_localizations` and `intl`, one `app_en.arb`, and a `LocalePrefs`
store holding the chosen `Locale`. The picker lists English, Hindi, and
Marathi; picking Hindi or Marathi persists the choice and the UI continues to
render English until those ARB files exist, so the option must be labelled as
coming soon rather than appearing broken. String extraction across existing
screens is explicitly **not** part of this work.

## Testing

Go:
- Session lifecycle: create, submit, claim.
- Double claim is rejected; the second caller still gets a working account.
- Expired session: prefs skipped, account still created, no error.
- Recommendations return only public, published, non-deleted quizzes, and
  respect the domain filter.
- The questions endpoint response contains no correct answers.
- Claim produces a `quiz_attempts` row with a `score_pct` matching a direct
  call to the scoring path with the same answers.

Dart:
- Personalisation skip yields the documented defaults.
- Session id survives an app restart.
- Result screen renders the locked score state, not a number.

## Out of scope

- Translating existing UI strings.
- Profession as a personalisation field.
- Filtering quiz *content* by language; nothing in the schema carries a
  content language, and adding one is separate work.
- Changing the Qwish Score formula or its 100-980 display scale.
