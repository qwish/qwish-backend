# Qwish Backend — API Documentation

> **Base URL:** `https://<your-domain>/api/v1`
> **Content-Type:** `application/json` (unless noted otherwise)
> **Request Timeout:** 30 seconds

---

## Authentication

All protected endpoints require a **Supabase JWT** in the `Authorization` header:

```
Authorization: Bearer <access_token>
```

Tokens are obtained via `/auth/verify-otp`.

---

## Avatars

Deterministic, procedurally-generated SVG avatars. **Public — no auth.** Same
seed always returns the exact same image, so use a stable per-user seed (the
user `id`). Zero stored assets; every avatar is generated from the seed.

### GET `/avatars/{seed}`

Returns an avatar as `image/svg+xml`. Use `<img>` / `SvgPicture.network` directly.

Optional query params override the seed's random baseline. Unknown or invalid
values are **ignored** (the random choice is kept), so the endpoint never errors
on bad input.

| Param        | Values                                                                    |
|--------------|---------------------------------------------------------------------------|
| `skin`       | `cream`, `peach`, `tan`, `brown`, or a `#RRGGBB` hex                       |
| `hairStyle`  | `cap`, `helmet`, `swept`, `afro`                                           |
| `hairColor`  | palette name (see below), or a `#RRGGBB` hex                              |
| `background` | palette name, or a `#RRGGBB` hex                                          |
| `expression` | `happy`, `neutral`, `sad`                                                  |
| `accessory`  | `none`, `circle`, `triangle`, `bar`                                        |

Palette names: `cobalt`, `vermilion`, `yellow`, `teal`, `terracotta`, `violet`,
`deepteal`, `sand`.

Response is cacheable (`Cache-Control: public, max-age=31536000, immutable`);
params are part of the URL, so each customization caches separately.

```
GET /avatars/8f2a...            # random avatar for this user
GET /avatars/8f2a...?hairStyle=afro&skin=brown&expression=happy&background=cobalt
```

### GET `/avatars/options`

Returns the valid values for every customization param as JSON, for building
pickers without hardcoding:

```json
{
  "skin": ["cream", "peach", "tan", "brown"],
  "hairStyle": ["cap", "helmet", "swept", "afro"],
  "hairColor": ["cobalt", "vermilion", "yellow", "teal", "terracotta", "violet", "deepteal", "sand"],
  "background": ["cobalt", "vermilion", "yellow", "teal", "terracotta", "violet", "deepteal", "sand"],
  "expression": ["happy", "neutral", "sad"],
  "accessory": ["none", "circle", "triangle", "bar"],
  "note": "hairColor/background/skin also accept a #RRGGBB hex"
}
```

---

## Standard Response Shapes

### Success (single object)
```json
{
  "success": true,
  "data": { ...fields... },
  "error": null
}
```

### Success (paginated list)
```json
{
  "success": true,
  "data": [ ...items... ],
  "error": null,
  "meta": { "page": 1, "limit": 20, "total": 123 }
}
```

### Error
```json
{
  "success": false,
  "data": null,
  "error": {
    "code": "ERROR_CODE",
    "message": "Human-readable description"
  }
}
```

---

## Roles

| Role | Description |
|------|-------------|
| `student` | Learner linked via student referral code |
| `teacher` | Educator linked via teacher referral code |
| `parent` | Parent monitoring a child student |
| `institution_admin` | Manages a specific institution |
| `super_admin` | Full platform administration |
| `moderator` | Content and quiz moderation |
| `support_agent` | Read-only admin access |

---

## Pagination

| Query Param | Default | Max |
|-------------|---------|-----|
| `page` | 1 | — |
| `limit` | 20 | 50 (100 for leaderboard) |

---

## Question Types

| Type | Description | Answer Format |
|------|-------------|---------------|
| `multiple_choice` | Select one from options | `"option text"` |
| `confidence_based` | Answer + confidence level | `"answer text"` |
| `eliminate_wrong` | Select the correct option | `"option text"` |
| `puzzle` | Solve the puzzle | `"correct option"` |
| `speed_chain` | Speed-based consecutive answers | `"correct option"` |
| `arrange_order` | Put items in correct order | `["item1","item2","item3"]` |
| `clue_reveal` | Answer with optional clues | `"answer text"` |

---

## Badge Types

| Badge | Awarded When |
|-------|-------------|
| `first_quiz` | First quiz completed |
| `on_a_roll` | 7-day streak reached |
| `unstoppable` | 30-day streak reached |
| `top_10` | Ranked top 10 in institution |
| `perfect_score` | 100% correct answers on a quiz |
| `speed_demon` | `speed_chain` question with combo ≥ 3 |
| `sharp_mind` | 100% on `confidence_based` questions, all answered as `very_confident` |
| `explorer` | Answered at least one question of each of the 7 question types (across all attempts) |

---

## Scoring System

Points are calculated per-question at answer submission time, then a final score is computed on completion.

### Per-Question Scoring

| Type | Correct | Wrong |
|------|---------|-------|
| `multiple_choice`, `eliminate_wrong`, `puzzle` | `base_points` | 0 |
| `speed_chain` | `base_points × (1 + combo_step × combo_level)` | 0 |
| `clue_reveal` | `base_points × (2 − deduction × clues_used)`, min `base × 0.5` | 0 |
| `confidence_based` | `base_points × confidence_multiplier` | May be negative if `very_confident` and wrong |
| `arrange_order` | `base_points` | 0 |

### Confidence Multipliers (defaults)

| Confidence | Correct | Wrong |
|------------|---------|-------|
| `very_confident` | ×1.5 | −0.5× |
| `pretty_sure` | ×1.0 | 0 |
| `not_sure` | ×0.5 | 0 |

### Final Score Calculation

After all answers are submitted:

- **Score ≥ 75%** → add performance bonus (default: +20% of base points for correct answers)
- **Score 50–74%** → no adjustment
- **Score < 50%** → deduction (default: −50% of base points for correct answers)
- Final points are multiplied by the institution's `point_multiplier`
- Points cannot drop below 0 (floored at current balance)

### Default Point Economy Config

| Key | Default |
|-----|---------|
| `base_points_per_question` | 10 |
| `performance_bonus_pct_75` | 20 |
| `deduction_pct_below_50` | 50 |
| `streak_bonus_7_day` | 50 |
| `streak_bonus_15_day` | 100 |
| `streak_bonus_30_day` | 250 |
| `combo_multiplier_step` | 0.5 |
| `clue_reveal_deduction_per_clue` | 0.25 |
| `points_expiry_months` | 6 |

> The point economy config is **snapshotted** at attempt start so mid-quiz config changes don't affect in-progress attempts.

---

## Streaks

- A streak increments when a quiz is completed on a new calendar day (in the institution's timezone).
- Completing multiple quizzes in one day counts only once.
- **Grace Window:** If a user misses a day, a 12-hour grace window is activated. Completing a quiz within the grace period extends the streak instead of resetting it.
- **Milestones:** 7 days → +50 pts bonus, 15 days → +100 pts bonus, 30 days → +250 pts bonus (each milestone claimed once per streak cycle).
- Streaks are reset nightly at 00:05 UTC by the scheduler.

---

# 1. Auth

## Auth Flow

```
send-otp → verify-otp → (if is_new_user) create-profile → home
```

---

## POST `/auth/send-otp`
**Auth required:** No

Sends a 6-digit OTP to the normalized email address. The response is identical
whether or not an account exists; account state is returned only after OTP
verification. Limited per source IP and normalized email.

### Request Body
```json
{ "email": "alice@example.com" }
```

### Response `200`
```json
{ "message": "OTP sent" }
```

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Missing `email` |
| 429 | `RATE_LIMITED` | IP or normalized-email limit exceeded |

---

## POST `/auth/verify-otp`
**Auth required:** No

Verifies the OTP and returns session tokens. Limited per source IP and
normalized email to prevent brute force.

- `is_new_user: false` → profile exists, go to home
- `is_new_user: true` → call `POST /auth/create-profile` next

### Request Body
```json
{
  "email": "alice@example.com",
  "otp":   "123456"
}
```

### Response `200` — Returning user
```json
{
  "user": {
    "id":           "uuid",
    "full_name":    "Alice Smith",
    "display_name": "Alice Smith",
    "email":        "alice@example.com",
    "role":         "student"
  },
  "access_token":  "eyJ...",
  "refresh_token": "eyJ...",
  "is_new_user":   false
}
```

### Response `200` — New user
```json
{
  "access_token":  "eyJ...",
  "refresh_token": "eyJ...",
  "is_new_user":   true
}
```

> No `user` object is returned for new users — profile does not exist yet. Call `POST /auth/create-profile` with the returned `access_token`.

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Missing `email` or `otp` |
| 401 | `INVALID_OTP` | OTP is wrong or expired |
| 429 | `RATE_LIMITED` | IP or normalized-email limit exceeded |

---

## POST `/auth/create-profile`
**Auth required:** Yes (JWT from `verify-otp` — user need not exist in DB yet)

Creates the profile for a newly verified user. Only call this when `verify-otp` returns `is_new_user: true`.

### Request Body
```json
{
  "full_name":     "Alice Smith",
  "referral_code": "SINST-ABC"
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `full_name` | Yes | |
| `referral_code` | No | Determines institution and role (`student` or `teacher`) |

### Response `201`
```json
{
  "user": {
    "id":           "uuid",
    "full_name":    "Alice Smith",
    "display_name": "Alice Smith",
    "email":        "alice@example.com",
    "role":         "student",
    "institution":  { "id": "uuid", "name": "Springfield Academy" }
  }
}
```

> `institution` is `null` if no referral code was provided.

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Missing `full_name` |
| 400 | `BAD_REQUEST` | Invalid or inactive referral code |
| 401 | `UNAUTHORIZED` | Missing or invalid token |

---

## POST `/auth/refresh`
**Auth required:** No

### Request Body
```json
{ "refresh_token": "eyJ..." }
```

### Response `200`
```json
{
  "access_token":  "eyJ...",
  "refresh_token": "eyJ..."
}
```

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Missing `refresh_token` |
| 401 | `INVALID_TOKEN` | Expired or invalid token |

---

## POST `/auth/logout`
**Auth required:** Yes

Invalidates the current session token in Supabase.

### Response `200`
```json
{ "message": "logged out" }
```

---

## PATCH `/auth/referral-code`
**Auth required:** Yes

Links the authenticated user to an institution using a referral code. Updates both `institution_id` and `role`.

### Request Body
```json
{ "referral_code": "SINST-ABC" }
```

### Response `200`
```json
{ "message": "institution updated" }
```

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Missing or invalid referral code |

---

# 2. Users

## GET `/users/me`
**Auth required:** Yes

Returns the authenticated user's full profile.

### Response `200`
```json
{
  "id":             "uuid",
  "full_name":      "Alice Smith",
  "display_name":   "Alice Smith",
  "email":          "alice@example.com",
  "role":           "student",
  "institution_id": "uuid",
  "institution":    { "id": "uuid", "name": "Springfield Academy" },
  "status":         "active",
  "total_points":   1250,
  "current_streak": 5,
  "longest_streak": 12,
  "member_since":   "2024-01-15T00:00:00Z"
}
```

---

## PATCH `/users/me`
**Auth required:** Yes

Updates the authenticated user's profile. Currently supports updating `display_name`.

### Request Body
```json
{ "display_name": "Ali" }
```

### Response `200`
Returns the updated profile (same shape as `GET /users/me`).

---

## DELETE `/users/me`
**Auth required:** Yes

Soft-deletes the authenticated user's account (GDPR-compliant anonymisation).

### Response `200`
```json
{ "message": "account deleted" }
```

---

## GET `/users/me/stats`
**Auth required:** Yes

### Response `200`
```json
{
  "total_points":   1250,
  "quizzes_taken":  34,
  "average_score":  72.5,
  "current_streak": 5,
  "longest_streak": 12
}
```

---

## GET `/users/me/badges`
**Auth required:** Yes

Returns all badge types with earned status.

### Response `200`
```json
[
  { "badge_type": "first_quiz",    "earned": true,  "earned_at": "2024-01-16T10:00:00Z" },
  { "badge_type": "perfect_score", "earned": false }
]
```

---

## GET `/users/me/attempts`
**Auth required:** Yes

### Query Params
`page`, `limit`

### Response `200` (paginated)
```json
[
  {
    "id":           "uuid",
    "quiz_id":      "uuid",
    "quiz_title":   "Biology Chapter 3",
    "score_pct":    85.0,
    "points_delta": 120,
    "status":       "completed",
    "completed_at": "2024-03-01T14:22:00Z"
  }
]
```

---

## GET `/users/me/points`
**Auth required:** Yes

### Response `200`
```json
{
  "total_points": 1250,
  "expiring_soon": {
    "amount":     200,
    "expires_at": "2024-04-01T00:00:00Z"
  }
}
```

> `expiring_soon` is `null` if no points expire within 30 days.

---

## GET `/users/me/points/ledger`
**Auth required:** Yes

### Query Params
`page`, `limit`

### Response `200` (paginated)
```json
[
  {
    "id":            "uuid",
    "amount":        120,
    "reason":        "quiz_attempt",
    "reference_id":  "attempt-uuid",
    "balance_after": 1250,
    "expires_at":    "2024-09-01T00:00:00Z",
    "created_at":    "2024-03-01T14:22:00Z"
  }
]
```

---

## GET `/users/me/streak`
**Auth required:** Yes

### Response `200`
```json
{
  "current_streak":  5,
  "longest_streak":  12,
  "last_activity":   "2024-03-01T00:00:00Z",
  "grace_active":    false,
  "grace_expires_at": null,
  "next_milestone":  7,
  "next_milestone_bonus": 50
}
```

---

## GET `/users/{userId}/profile`
**Auth required:** Yes

Returns a public profile (no email or sensitive data).

### Response `200`
```json
{
  "id":               "uuid",
  "display_name":     "Alice Smith",
  "institution":      "Springfield Academy",
  "total_points":     1250,
  "current_streak":   5,
  "longest_streak":   12,
  "quizzes_completed": 34,
  "badges":           ["first_quiz", "perfect_score"],
  "badge_count":      2,
  "qwish_score":      640,
  "percentile":       67,
  "accuracy":         75.8,
  "questions_answered": 330,
  "active_days_30":   9,
  "member_since":     "2026-01-15T00:00:00Z",
  "strengths": [
    { "label": "Computer Science", "accuracy": 85.0, "questions": 120 }
  ]
}
```

| Field | Notes |
|---|---|
| `qwish_score` | 100–900, from the leaderboard score. 100 for a learner with no attempts; treat `quizzes_completed: 0` as unrated. |
| `percentile` | 1–100: where the score sits among active students (same scale as recruiter search). |
| `accuracy` | Correct ÷ answered across completed attempts, one decimal. `null` until a question is answered. |
| `questions_answered` | Questions in completed attempts. |
| `active_days_30` | Distinct days (UTC) with a completed attempt in the last 30 days. |
| `strengths` | Up to 4 subjects, strongest first. Only subjects with at least 10 answered questions. |
| `badge_count` | Number of earned badges; `badges` still lists them. |

---

## GET `/users/me/rank`
**Auth required:** Yes

Returns the authenticated user's rank and top-percentile across all scopes.

### Response `200`
```json
{
  "global_rank":        23,
  "global_total":       1500,
  "institution_rank":   5,
  "institution_total":  120,
  "domain_rank":        87,
  "domain_total":       340,
  "top_percentile":     12.5,
  "distinct_quizzes_completed": 7,
  "leaderboard_unlocked": true
}
```

| Field | Type | Notes |
|-------|------|-------|
| `top_percentile` | `float` | e.g. `12.5` → "Top 12.5%" of all active users |
| `institution_rank` / `institution_total` | `int` | Omitted if user has no institution |
| `domain_rank` / `domain_total` | `int` | Omitted if user's `domain` is not set |
| `distinct_quizzes_completed` | `int` | Authoritative progress toward leaderboard eligibility |
| `leaderboard_unlocked` | `bool` | Students unlock at five different completed quizzes; non-student accounts are not ranked |

All ranks here order by `qwish_score`, the same value the leaderboard uses.

---

## GET `/users/me/profile-views`
**Auth required:** Yes

Returns how many unique users viewed the authenticated user's public profile.

### Response `200`
```json
{
  "today":     3,
  "this_week": 12,
  "total":     47
}
```

> Views are recorded automatically when any user calls `GET /users/{userId}/profile`. Self-views are excluded.

---

## GET `/users/me/milestones`
**Auth required:** Yes

Returns progress across all defined milestones.

### Response `200`
```json
[
  {
    "id":          "achiever",
    "title":       "Achiever",
    "description": "Earn 500 points",
    "progress":    0.70,
    "current":     350,
    "target":      500,
    "completed":   false
  }
]
```

| Field | Type | Notes |
|-------|------|-------|
| `progress` | `float` | `0.0`–`1.0`; use as fill percentage |
| `completed` | `bool` | `true` when `current >= target` |

### Milestone definitions

| ID | Title | Metric | Target |
|----|-------|--------|--------|
| `first_quiz` | First Steps | Quizzes completed | 1 |
| `quiz_5` | Getting Started | Quizzes completed | 5 |
| `quiz_25` | Quiz Enthusiast | Quizzes completed | 25 |
| `quiz_100` | Century Club | Quizzes completed | 100 |
| `points_100` | Point Scorer | Total points | 100 |
| `points_500` | Achiever | Total points | 500 |
| `points_2000` | Elite | Total points | 2000 |
| `points_5000` | Champion | Total points | 5000 |
| `streak_3` | On Fire | Longest streak | 3 |
| `streak_7` | Streak Master | Longest streak | 7 |
| `streak_30` | Streak Legend | Longest streak | 30 |

---

## GET `/users/me/education`
**Auth required:** Yes

### Response `200`
```json
[
  {
    "id":               "uuid",
    "institution_name": "MIT",
    "degree":           "B.Tech",
    "field":            "Computer Science",
    "start_year":       2020,
    "end_year":         null,
    "is_current":       true
  }
]
```

---

## POST `/users/me/education`
**Auth required:** Yes

### Request Body
```json
{
  "institution_name": "MIT",
  "degree":           "B.Tech",
  "field":            "Computer Science",
  "start_year":       2020,
  "end_year":         null,
  "is_current":       true
}
```

| Field | Required |
|-------|----------|
| `institution_name` | Yes |
| `degree`, `field`, `start_year`, `end_year`, `is_current` | No |

### Response `201`
Returns the created education object (same shape as list item).

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Missing `institution_name` |

---

## DELETE `/users/me/education/{id}`
**Auth required:** Yes

### Response `204`
No content.

---

## GET `/users/me/skills`
**Auth required:** Yes

### Response `200`
```json
["Go", "Flutter", "PostgreSQL"]
```

---

## POST `/users/me/skills`
**Auth required:** Yes

### Request Body
```json
{ "skill": "Go" }
```

### Response `204`
No content. Duplicate skill names are silently ignored.

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Missing `skill` |

---

## DELETE `/users/me/skills/{skill}`
**Auth required:** Yes

### Response `204`
No content.

---

## PATCH `/users/me/domain`
**Auth required:** Yes

Sets the user's study/subject domain. Used for domain-scoped ranking (e.g. "CS Domain #87").

### Request Body
```json
{ "domain": "Computer Science" }
```

### Response `204`
No content.

---

## GET `/users/me/notifications/stream`
**Auth required:** Yes

Streams real-time in-app notifications to the client using Server-Sent Events (SSE).

### Response `200`
`Content-Type: text/event-stream`

Event payload:
```json
{
  "id": "uuid",
  "kind": "streak_milestone",
  "title": "Streak Milestone!",
  "body": "You have reached a 7-day streak!",
  "icon": "fire",
  "color": "#FF5733",
  "reference": "streak_id",
  "read_at": null,
  "created_at": "2026-06-12T10:00:00Z"
}
```

---

## GET `/users/me/notifications`
**Auth required:** Yes

Returns a paginated list of in-app notifications and the unread notification count.

### Query Params
`page`, `limit`

### Response `200` (paginated)
```json
{
  "items": [
    {
      "id": "uuid",
      "kind": "streak_milestone",
      "title": "Streak Milestone!",
      "body": "You have reached a 7-day streak!",
      "icon": "fire",
      "color": "#FF5733",
      "reference": "streak_id",
      "read_at": null,
      "created_at": "2026-06-12T10:00:00Z"
    }
  ],
  "unread": 1
}
```

---

## GET `/users/me/notifications/unread-count`
**Auth required:** Yes

Returns the count of unread notifications.

### Response `200`
```json
{
  "unread": 1
}
```

---

## PATCH `/users/me/notifications/read-all`
**Auth required:** Yes

Marks all of the user's notifications as read.

### Response `204`
No content.

---

## PATCH `/users/me/notifications/{id}/read`
**Auth required:** Yes

Marks a specific notification as read.

### Response `204`
No content.

---

## POST `/users/me/devices`
**Auth required:** Yes

Registers or refreshes a push device token (FCM token) for mobile push notifications.

### Request Body
```json
{
  "token": "fcm_token_string",
  "platform": "ios",
  "app_version": "1.0.0",
  "locale": "en-US"
}
```

`platform` should be one of `ios`, `android`, or `web` (defaults to `unknown` if unrecognized).

### Response `204`
No content.

---

## DELETE `/users/me/devices/{token}`
**Auth required:** Yes

Unregisters a device token (e.g. on logout or app uninstall).

### Response `204`
No content.

---

## GET `/users/me/recommendations`
**Auth required:** Yes

Returns a list of up to 5 personalized quiz recommendations (quizzes in the user's institution or public that the user has not completed).

### Response `200`
```json
[
  {
    "id": "uuid",
    "title": "Introduction to Geometry",
    "description": "Basic concepts of geometry, lines, and angles.",
    "question_count": 10,
    "type": "practice"
  }
]
```

---

## GET `/users/me/content`
**Auth required:** Yes

Returns active, currently scheduled promos and in-app announcements that match
the authenticated user's role and institution. Audience filtering is enforced
by the server. Items dismissed by this user are omitted.

Each item contains `id`, `kind` (`promo` or `announcement`), `placement`,
`title`, and optional `body`, `cta_label`, `cta_url`, `starts_at`, `ends_at`.

---

## POST `/users/me/content/{kind}/{contentId}/events`
**Auth required:** Yes

Records an idempotent unique delivery event for the authenticated user.

```json
{ "event": "impression" }
```

`event` must be `impression`, `click`, or `dismiss`. `kind` must be `promo` or
`announcement`. Returns `204`; clients may safely retry.

---

## GET `/users/me/quiz-pick`
**Auth required:** Yes

Returns one random, currently available assessment that the authenticated
student has never attempted. Quizzes matching the student's selected interests
form the candidate pool. Students must select at least 10 unique topics first.
Pass `exclude_id=<quiz UUID>` when refreshing from the detail screen to
guarantee a different result.

Returns `409 INTERESTS_REQUIRED` when the student has fewer than 10 selected
topics. Returns `404 NO_QUIZ_AVAILABLE` when no matching unplayed assessment remains.

---

## GET `/users/me/report-card`
**Auth required:** Yes

Generates a dated Qwish learning report for the authenticated user. Includes institution-recorded education stages (from class membership), separately labelled self-reported education, first-attempt accuracy and evidence counts, current assignment progress, 30-day trends, domain strengths, and aggregate peer comparisons.

Peer standing uses raw accuracy (not the composite `score_pct` or points) on the same quiz IDs. It excludes the learner, counts strictly lower accuracy, and averages across qualifying quizzes. Requires at least 3 quizzes with 5 other active students each; ties are not lower. Question sampling and revisions may differ, so this is descriptive context, not a calibrated national percentile. Trends require 3 scored first attempts in each 30-day window. Strength labels require 3 assessments and 20 questions; 80%+ is an observed strength and below 60% a review priority.

Stages associate institution assessments with recorded class membership periods. Missing history is explicitly labelled, and self-reported education is not given inferred marks. The report is not a signed credential or academic transcript. All sections use a consistent database snapshot; responses are `private, no-store`.

### Response `200`
`Content-Type: application/pdf` with PDF binary data.

---

# 3. Quizzes

## GET `/quizzes`
**Auth required:** Yes (student / teacher / institution_admin)

### Query Params
| Param | Description |
|-------|-------------|
| `type` | Filter by quiz type (`knowledge_check` practice, `play_and_win` ranked) |
| `saved` | `true` to return only the authenticated user's saved practice quizzes |
| `sort` | `recommended` (interest matches first, then most popular), `popular` (most completions), or `newest` (default) |
| `unplayed` | `true` to drop quizzes the learner has already completed |
| `page`, `limit` | Pagination |

### Response `200` (paginated)
```json
[
  {
    "id":             "uuid",
    "title":          "Biology Chapter 3",
    "description":    "Cell division and genetics",
    "type":           "knowledge_check",
    "status":         "published",
    "question_count": 10,
    "is_saved":       false,
    "created_by":     "uuid",
    "published_at":   "2024-02-01T00:00:00Z",
    "taker_count":    128,
    "avg_score_pct":  67.4,
    "avg_seconds":    252.0
  }
]
```

`taker_count` is the number of **distinct users** with a completed attempt (a
retake does not count twice). `avg_score_pct` and `avg_seconds` are averages over
all completed attempts; both are omitted when no one has completed the quiz.

---

## GET `/quizzes/{quizId}`
**Auth required:** Yes

Returns quiz details including all questions (with options, correct answers hidden for students during attempts).

Also carries the same peer stats as the list: `taker_count` (distinct users with a
completed attempt), `avg_score_pct` and `avg_seconds` (averages over completed
attempts; omitted when there are none). Use this endpoint — not the paginated
list — to render a quiz detail view.

---

## POST `/quizzes/{quizId}/save`
**Auth required:** Yes

Saves an accessible practice quiz (`type=knowledge_check`) to the user's saved
list. Ranked quizzes cannot be saved and return `400`.

### Response `200`
```json
{ "message": "quiz saved" }
```

---

## DELETE `/quizzes/{quizId}/save`
**Auth required:** Yes

Removes a quiz from the user's saved list.

### Response `200`
```json
{ "message": "quiz unsaved" }
```

---

## GET `/quizzes/{quizId}/share`
**Auth required:** Yes

### Response `200`
```json
{ "deep_link": "https://app.qwish.in/quiz/uuid" }
```

---

## POST `/quizzes/{quizId}/reports`
**Auth required:** Yes

Reports a quiz.

### Request Body
```json
{
  "reason":      "inappropriate_content",
  "description": "Optional extra detail"
}
```

`reason` is required.

### Response `200`
```json
{ "message": "thanks — we'll review this" }
```

---

## POST `/quizzes/{quizId}/questions/{questionId}/reports`
**Auth required:** Yes

Reports a specific question.

### Request Body
```json
{
  "reason":      "wrong_answer",
  "description": "Optional extra detail"
}
```

`reason` is required.

### Response `200`
```json
{ "message": "thanks — we'll review this" }
```

---

# 4. Attempts

## POST `/quizzes/{quizId}/attempts`
**Auth required:** Yes

Starts a new quiz attempt. For `play_and_win` quizzes, only one attempt is allowed per user.

### Response `201`
```json
{
  "attempt_id": "uuid",
  "quiz_id":    "uuid",
  "questions": [
    {
      "id":       "uuid",
      "position": 1,
      "type":     "multiple_choice",
      "prompt":   "What is the powerhouse of the cell?",
      "options":  ["Nucleus", "Mitochondria", "Ribosome", "Golgi body"]
    }
  ]
}
```

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Quiz not available or already attempted (play_and_win) |

---

## POST `/attempts/{attemptId}/answers`
**Auth required:** Yes

Submits an answer for one question. Can be called multiple times per question (last answer wins).

### Request Body
```json
{
  "question_id":      "uuid",
  "answer":           "Mitochondria",
  "time_taken_ms":    4200,
  "confidence_level": "very_confident",
  "clues_used":       0,
  "combo_level":      2
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `question_id` | Yes | |
| `answer` | Yes | Format depends on question type |
| `time_taken_ms` | No | Milliseconds taken to answer |
| `confidence_level` | No | `very_confident`, `pretty_sure`, `not_sure` — used for `confidence_based` type |
| `clues_used` | No | Number of clues revealed — used for `clue_reveal` type |
| `combo_level` | No | Current combo — used for `speed_chain` type |

### Response `200`
```json
{
  "is_correct":     true,
  "correct_answer": "Mitochondria",
  "points_earned":  15,
  "combo_level":    3
}
```

---

## POST `/attempts/{attemptId}/complete`
**Auth required:** Yes

Finalises the attempt, calculates score, awards points and badges, updates streak.

### Response `200`
```json
{
  "attempt_id":           "uuid",
  "score_pct":            80.0,
  "performance_badge":    "excellent",
  "points_delta":         144,
  "total_correct":        8,
  "total_questions":      10,
  "streak_bonus_awarded": 0,
  "badges_awarded":       ["first_quiz"],
  "question_breakdown": [
    {
      "position":         1,
      "question_snippet": "What is the powerhouse of the cell?",
      "student_answer":   "Mitochondria",
      "correct_answer":   "Mitochondria",
      "is_correct":       true,
      "points":           15
    }
  ],
  "is_repeat_attempt":    false,
  "qwish_score":          412.6,
  "qwish_score_delta":    9.3
}
```

> `score_pct` is plain accuracy (`total_correct / total_questions × 100`).
> `performance_badge`: `excellent` (≥75%), `good` (50–74%), `needs_work` (<50%)
> `qwish_score` is the learner's skill rating after this attempt (100–900) and
> `qwish_score_delta` the change it caused. Only questions the learner has never
> answered before move it, so a delta of `0` on a retake is expected. See
> **Qwish Score** under Insights.

---

## GET `/attempts/{attemptId}`
**Auth required:** Yes

Returns the result of a completed attempt.

### Response `200`
```json
{
  "attempt_id":      "uuid",
  "quiz_id":         "uuid",
  "status":          "completed",
  "score_pct":       80.0,
  "points_delta":    144,
  "total_correct":   8,
  "total_questions": 10,
  "completed_at":    "2024-03-01T14:22:00Z"
}
```

---

# 5. Leaderboard

## GET `/leaderboard`
**Auth required:** Yes

### Query Params
| Param | Values | Default |
|-------|--------|---------|
| `scope` | `institution`, `global` | `institution` |
| `domain` | Optional domain slug; filters either scope | — |
| `page`, `limit` | — | page=1, limit=50 |

Rankings are live and ordered by the server-calculated `qwish_score`; cumulative
`total_points` is retained as supporting data. Student accounts must complete
five different quizzes first; otherwise the endpoint returns
`403 LEADERBOARD_LOCKED`. Students appear in rankings after the same five-quiz
threshold. Teachers and other non-student accounts never appear in rankings.
Institution names are included for global and institution-scoped entries.

### Response `200` (paginated)
```json
{
  "scope":     "institution",
  "my_rank":   3,
  "my_qwish_score": 712.5,
  "my_institution_name": "Qwish Academy",
  "my_points": 1250,
  "entries": [
    {
      "rank":           1,
      "user_id":        "uuid",
      "display_name":   "Bob Jones",
      "institution_name": "Qwish Academy",
      "qwish_score":    745.2,
      "total_points":   2100,
      "current_streak": 9
    }
  ]
}
```

---

# 6. Topic Requests

## POST `/topic-requests`
**Auth required:** Yes (student)

### Request Body
```json
{
  "topic":       "Photosynthesis",
  "subject":     "Biology",
  "description": "I'd like more questions on the Calvin cycle"
}
```

`topic` is required.

### Response `201`
```json
{
  "id":          "uuid",
  "student_id":  "uuid",
  "topic":       "Photosynthesis",
  "subject":     "Biology",
  "description": "I'd like more questions on the Calvin cycle",
  "status":      "pending",
  "created_at":  "2024-03-01T00:00:00Z"
}
```

---

## GET `/topic-requests/mine`
**Auth required:** Yes (student)

### Response `200`
Array of topic requests (same shape as above).

---

# 7. Parent

## POST `/parent/link-invite`
**Auth required:** Yes (student only)

Generates a short invite code the student shares with their parent.

### Response `200`
```json
{ "invite_code": "a1b2c3d4" }
```

---

## POST `/parent/link`
**Auth required:** Yes (parent)

Submits the invite code. Creates a pending link waiting for student acceptance.

### Request Body
```json
{ "invite_code": "a1b2c3d4" }
```

### Response `200`
```json
{
  "message": "link request sent, waiting for student acceptance",
  "link_id": "uuid"
}
```

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 404 | `NOT_FOUND` | Invite code not found or already used |

---

## POST `/parent/link/{linkId}/accept`
**Auth required:** Yes (student)

Student accepts the parent link request.

### Response `200`
```json
{ "message": "parent link activated" }
```

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Link not found or already processed |

---

## DELETE `/parent/link/{linkId}`
**Auth required:** Yes (student or parent)

Revokes an active parent-student link.

### Response `200`
```json
{ "message": "link revoked" }
```

---

## GET `/parent/children`
**Auth required:** Yes (parent)

### Response `200`
```json
[
  {
    "id":             "uuid",
    "display_name":   "Charlie Smith",
    "total_points":   800,
    "current_streak": 3
  }
]
```

---

## GET `/parent/children/{studentId}/overview`
**Auth required:** Yes (parent — must have an active link to this student)

### Response `200`
```json
{
  "student_id":     "uuid",
  "display_name":   "Charlie Smith",
  "total_points":   800,
  "current_streak": 3,
  "quizzes_taken":  22,
  "average_score":  68.5,
  "recent_attempts": [
    {
      "id":           "uuid",
      "quiz_title":   "Math Basics",
      "score_pct":    90.0,
      "points_delta": 108,
      "completed_at": "2024-03-01T14:00:00Z"
    }
  ],
  "badges": ["first_quiz", "perfect_score"]
}
```

---

# 8. Upload

## POST `/upload/presign`
**Auth required:** Yes (teacher, super_admin, moderator)

Generates a presigned S3 PUT URL for uploading files directly to cloud storage (R2).

### Request Body
```json
{
  "content_type": "image/jpeg",
  "prefix": "quiz-images"
}
```

`prefix` is optional (defaults to `quiz-images`). `content_type` must be one of `image/jpeg`, `image/png`, or `image/webp`.

### Response `200`
```json
{
  "upload_url": "https://<bucket>.r2.cloudflarestorage.com/quiz-images/uuid.jpg?X-Amz-...",
  "public_url": "https://media.yourdomain.com/quiz-images/uuid.jpg",
  "key": "quiz-images/uuid.jpg",
  "expires_in": 300
}
```

---

## POST `/upload/image`
**Auth required:** Yes (teacher, super_admin, moderator)
**Content-Type:** `multipart/form-data`

### Form Fields
| Field | Required | Notes |
|-------|----------|-------|
| `file` | Yes | JPEG, PNG, or WebP — max 5 MB |
| `prefix` | No | Storage path prefix, default `quiz-images` |

### Response `201`
```json
{ "url": "https://media.yourdomain.com/quiz-images/uuid.jpg" }
```

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | File missing, too large, or unsupported format |

---

# 9. Teacher

All routes require role `teacher`.

## GET `/teacher/quizzes`
**Auth required:** Yes (teacher)

### Query Params
| Param | Description |
|-------|-------------|
| `status` | Filter by status (`draft`, `pending_approval`, `published`, `rejected`) |
| `page`, `limit` | Pagination |

### Response `200` (paginated)
Array of quiz objects belonging to the authenticated teacher.

---

## GET `/teacher/quizzes/taxonomy`
**Auth required:** Yes (teacher)

Domain → subdomain tree for the quiz authoring dropdowns.
```json
[
  { "slug": "quantitative", "label": "Quantitative", "subdomains": [
    { "slug": "quant_percentages", "label": "Percentages" },
    { "slug": "quant_geometry", "label": "Geometry" }
  ] }
]
```

---

## POST `/teacher/quizzes`
**Auth required:** Yes (teacher)

### Request Body
```json
{
  "title":       "Biology Chapter 3",
  "description": "Cell division and genetics",
  "type":        "practice",
  "visibility":  "institution",
  "domain":      "quantitative",
  "subdomain":   "quant_percentages",
  "curriculum_unit_ids": ["chapter-uuid-1", "chapter-uuid-2"],
  "curriculum_question_mapping_enabled": true,
  "time_limit":  30,
  "expires_at":  "2024-06-01T00:00:00Z"
}
```

`title` is required. `visibility` defaults to `institution`. `domain`/`subdomain` are optional but validated against `/teacher/quizzes/taxonomy` — a subdomain must belong to its domain, else `400`. `curriculum_unit_ids` contains published curriculum chapter IDs assigned to the selected class. When `curriculum_question_mapping_enabled` is true, the teacher can optionally map individual questions to topics from those units.

### Response `201`
Quiz object.

---

## PATCH `/teacher/quizzes/{quizId}`
**Auth required:** Yes (teacher — own quizzes only)

Same body shape as POST. Only provided fields are updated.

### Response `200`
Updated quiz object.

---

## POST `/teacher/quizzes/{quizId}/questions`
**Auth required:** Yes (teacher — own quizzes only)

### Request Body
```json
{
  "prompt":         "What is the powerhouse of the cell?",
  "type":           "multiple_choice",
  "options":        ["Nucleus", "Mitochondria", "Ribosome", "Golgi body"],
  "correct_answer": "Mitochondria",
  "position":       1,
  "points":         10,
  "time_limit":     20
}
```

`prompt` and `type` are required.

### Response `201`
Question object.

---

## PATCH `/teacher/quizzes/{quizId}/questions/{questionId}`
**Auth required:** Yes (teacher — own quizzes only)

Same body shape as POST.

### Response `200`
```json
{ "message": "question updated" }
```

---

## DELETE `/teacher/quizzes/{quizId}/questions/{questionId}`
**Auth required:** Yes (teacher — own quizzes only)

### Response `200`
```json
{ "message": "question deleted" }
```

---

## POST `/teacher/quizzes/{quizId}/publish`
**Auth required:** Yes (teacher — own quizzes only)

Submits the quiz for admin approval (`pending_approval`). If already published, closes it (`closed`).

### Response `200`
```json
{ "status": "pending_approval" }
```

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Quiz has no questions or other validation failure |

---

## GET `/teacher/quizzes/{quizId}/results`
**Auth required:** Yes (teacher — own quizzes only)

### Response `200`
Aggregated attempt results for the quiz.

---

## GET `/teacher/topic-requests`
**Auth required:** Yes (teacher)

### Query Params
`status`, `page`, `limit`

### Response `200` (paginated)
Array of topic requests from students in the same institution.

---

## PATCH `/teacher/topic-requests/{requestId}`
**Auth required:** Yes (teacher)

### Request Body
```json
{
  "status":      "in_progress",
  "assigned_to": "teacher-uuid"
}
```

### Response `200`
{ "message": "updated" }

---

## GET `/teacher/overview`
**Auth required:** Yes (teacher)

Returns a summary overview for the teacher dashboard.

### Response `200`
```json
{
  "drafts": 3,
  "pending_review": 1,
  "published": 5,
  "total_attempts": 42,
  "average_score": 78.5,
  "open_topic_requests": 2,
  "recent_attempts": [
    {
      "attempt_id": "uuid",
      "quiz_id": "uuid",
      "quiz_title": "Math Quiz",
      "student_id": "uuid",
      "student_name": "John Doe",
      "score_pct": 90.0,
      "completed_at": "2026-06-12T10:00:00Z"
    }
  ]
}
```

---

## DELETE `/teacher/quizzes/{quizId}`
**Auth required:** Yes (teacher - own quizzes only)

Deletes a quiz.

### Response `200`
```json
{ "message": "quiz deleted" }
```

---

## POST `/teacher/quizzes/{quizId}/unpublish`
**Auth required:** Yes (teacher - own quizzes only)

Unpublishes a quiz, changing its status back to `draft`.

### Response `200`
```json
{ "status": "draft" }
```

---

## PATCH `/teacher/quizzes/{quizId}/questions/order`
**Auth required:** Yes (teacher - own quizzes only)

Reorders the questions in a quiz.

### Request Body
```json
{
  "order": ["question-uuid-1", "question-uuid-2", "question-uuid-3"]
}
```

All question UUIDs in the quiz must be provided in the desired order.

### Response `200`
```json
{ "message": "questions reordered" }
```

---

## GET `/teacher/students`
**Auth required:** Yes (teacher)

List students in the institution. If the teacher is assigned to specific groups, this list is restricted to students in those groups.

### Query Params
`page`, `limit`, `search` (name or email), `class_id` (restrict to a specific group/class), `sort` (`total_points`, `average_score`, `last_active`)

### Response `200` (paginated)
```json
[
  {
    "id": "uuid",
    "display_name": "Jane Doe",
    "email": "jane@example.com",
    "total_points": 1500,
    "current_streak": 5,
    "last_active_at": "2026-06-12T10:00:00Z",
    "status": "active",
    "average_score": 85.5
  }
]
```

---

## GET `/teacher/students/{userId}`
**Auth required:** Yes (teacher)

Returns detailed information about a student, including stats and quiz history restricted to this teacher's quizzes.

### Response `200`
```json
{
  "id": "uuid",
  "display_name": "Jane Doe",
  "email": "jane@example.com",
  "status": "active",
  "total_points": 1500,
  "current_streak": 5,
  "longest_streak": 10,
  "average_score": 85.5,
  "quizzes_taken": 8,
  "member_since": "2026-01-01T00:00:00Z",
  "quiz_history": [
    {
      "id": "attempt-uuid",
      "quiz_id": "quiz-uuid",
      "quiz_title": "Math Quiz",
      "score_pct": 90.0,
      "points_delta": 50,
      "completed_at": "2026-06-12T10:00:00Z"
    }
  ],
  "classes": [
    {
      "id": "class-uuid",
      "name": "Class 10-A"
    }
  ]
}
```

---

## GET `/teacher/classes`
**Auth required:** Yes (teacher)

Returns a list of classes (groups) assigned to the teacher.

### Response `200`
```json
[
  {
    "id": "uuid",
    "name": "Class 10-A",
    "description": "Sophomore Math class",
    "invite_code": "INV123",
    "created_at": "2026-01-01T00:00:00Z",
    "student_count": 25
  }
]
```

---

## GET `/teacher/classes/{classId}`
**Auth required:** Yes (teacher)

Returns details of a specific class, including the list of students in the class.

### Response `200`
```json
{
  "id": "uuid",
  "name": "Class 10-A",
  "description": "Sophomore Math class",
  "invite_code": "INV123",
  "created_at": "2026-01-01T00:00:00Z",
  "student_count": 25,
  "average_score": 78.2,
  "students": [
    {
      "id": "uuid",
      "display_name": "Jane Doe",
      "email": "jane@example.com",
      "total_points": 1500,
      "current_streak": 5,
      "last_active_at": "2026-06-12T10:00:00Z",
      "status": "active",
      "average_score": 85.5
    }
  ]
}
```

---

## GET `/teacher/reports/quiz-analytics`
**Auth required:** Yes (teacher)

Returns an analytical report for quizzes created by the teacher.

### Query Params
`page`, `limit`, `date_from` (ISO date `YYYY-MM-DD`), `date_to` (ISO date `YYYY-MM-DD`)

### Response `200` (paginated)
```json
[
  {
    "quiz_id": "uuid",
    "title": "Math Quiz",
    "completion_rate": 88.0,
    "score_dist_high": 15,
    "score_dist_mid": 7,
    "score_dist_low": 3
  }
]
```

---

## GET `/teacher/reports/student-performance`
**Auth required:** Yes (teacher)

Returns a performance report for students under the teacher's instruction.

### Query Params
`class_id`, `date_from` (ISO date `YYYY-MM-DD`), `date_to` (ISO date `YYYY-MM-DD`)

### Response `200`
```json
[
  {
    "id": "uuid",
    "display_name": "Jane Doe",
    "total_points": 1500,
    "current_streak": 5,
    "quizzes_taken": 8,
    "average_score": 85.5
  }
]
```

---

# 10. Institution Admin

All routes require role `institution_admin`. Every response carries an
`X-Request-Id` header (see [Request IDs and rate-limit headers](#request-ids-and-rate-limit-headers)).
Endpoints added for the Institute dashboard redesign are collected in
[Institute dashboard redesign](#institute-dashboard-redesign-migrations-077078);
this section lists each route once, with its current contract.

## GET `/institution/overview`

### Response `200`
```json
{
  "total_students":  120,
  "active_students": 45,
  "total_teachers":  8,
  "total_quizzes":   34,
  "average_score":   71.2,
  "average_score_window_days": 30,
  "top_student": {
    "id": "uuid", "name": "Bob", "points": 3200,
    "average_score_30d": 94.2, "quizzes_30d": 41, "class_name": "Science B"
  },
  "activity_chart":  [{ "day": "2024-03-01", "count": 12 }],
  "top_quizzes": [
    { "id": "uuid", "title": "Biology Ch3", "type": "knowledge_check", "teacher_name": "M. Joshi", "completions": 89 }
  ]
}
```
- `average_score` averages completed attempts in the last 30 days (`average_score_window_days`).
- `active_students` is distinct students with a completed attempt in the last 7 days.
- `top_student` is ranked by lifetime points; its `average_score_30d` is `null` when they have no attempts in that window. It is an object with an empty `name` when the institution has no students.
- `activity_chart` omits days with no completions — treat a missing day as a recorded zero.

---

## GET `/institution/students`
Enrollment-backed roster. Every row has a student account (unclaimed roster rows were retired in migration 085).

### Query Params
| Param | Description |
|-------|-------------|
| `search` | Name or email |
| `status` | `active`, `suspended`, `graduated`, `transferred`, `left`. Omitted = active and suspended |
| `group_id` | Only students in this class |
| `grade`, `section` | Exact match on the enrollment |
| `min_score`, `max_score` | Bounds on `average_score` |
| `inactive_days` | No activity for at least this many days (never-active students included) |
| `sort` | `total_points`, `average_score`, `last_active` (default: name) |
| `page`, `limit` | Pagination (max `limit` 50) |

### Response `200` (paginated)
```json
[
  {
    "enrollment_id": "uuid",
    "id": "uuid",
    "display_name": "Aarya Kulkarni",
    "email": "aarya.k@school.edu",
    "grade": "11", "section": "A",
    "status": "active",
    "total_points": 2480,
    "current_streak": 12,
    "last_active_at": "2026-09-26T03:44:00Z",
    "average_score": 81.2,
    "attempts_count": 46,
    "groups": [{ "id": "uuid", "name": "Physics A" }]
  }
]
```
`attempts_count` is the number of completed attempts behind `average_score`.
`0` means **no attempts**, which is not the same as a 0% average.

---

## GET `/institution/students/ids`
Every enrollment matching the same filters as `GET /institution/students`
(`search`, `status`, `group_id`, `grade`, `section`, score and activity
filters), for "select all matching" across pages. Capped at 5000.

```json
{
  "students": [
    { "enrollment_id": "uuid", "user_id": "uuid-or-null", "display_name": "Aarya Kulkarni", "status": "active" }
  ],
  "truncated": false
}
```

---

## GET `/institution/students/{userId}`
A claimed, live (active or suspended) student. `404` otherwise.

### Query Params
| Param | Description |
|-------|-------------|
| `history_limit` | Quiz history page size, 1–50 (default 20) |
| `history_offset` | Quiz history offset (default 0) |

### Response `200`
```json
{
  "id": "uuid",
  "display_name": "Aarya Kulkarni",
  "email": "aarya.k@school.edu",
  "status": "active",
  "enrollment_id": "uuid",
  "enrollment_status": "active",
  "grade": "11", "section": "A",
  "total_points": 2480,
  "current_streak": 12,
  "longest_streak": 21,
  "average_score": 81.2,
  "quizzes_taken": 46,
  "member_since": "2024-03-01T00:00:00Z",
  "quiz_history": [
    {
      "id": "attempt-uuid", "quiz_id": "uuid", "quiz_title": "Kinematics Speed Round",
      "quiz_type": "play_and_win", "score_pct": 90.0, "points_delta": 135,
      "completed_at": "2026-09-26T03:44:00Z", "time_taken_ms": 252000
    }
  ],
  "history_limit": 20,
  "history_offset": 0,
  "points_ledger": [
    { "id": "uuid", "amount": 135, "reason": "quiz_attempt", "reference_id": "uuid",
      "balance_after": 2480, "expires_at": "2027-03-26T03:44:00Z", "created_at": "2026-09-26T03:44:00Z" }
  ],
  "groups": [{ "id": "uuid", "name": "Physics A", "teacher_names": ["Anil Patil"] }]
}
```
- `average_score` and `quizzes_taken` count completed attempts on this institution's quizzes since the student joined.
- `quiz_history` uses the same window. `time_taken_ms` is `null` when the attempt has no start time.
- `points_ledger` is the student's 50 most recent platform-wide ledger entries. Points aren't per-institution.

---

## PATCH `/institution/students/{userId}/status`
### Request Body
```json
{ "action": "suspend", "reason": "Academic misconduct" }
```
`action`: `suspend` or `reactivate`. Prefer
`PATCH /institution/enrollments/{enrollmentId}/status`, which also handles
graduation and transfer.

### Response `200`
```json
{ "status": "suspended" }
```

---

## GET `/institution/teachers`
### Query Params
| Param | Description |
|-------|-------------|
| `status` | `active`, `pending`, `suspended`, or `needs_action` (pending + suspended) |
| `search` | Name or email |
| `page`, `limit` | Pagination (max `limit` 100) |

### Response `200` (paginated)
```json
[
  {
    "id": "uuid", "display_name": "Anil Patil", "email": "anil.p@school.edu",
    "last_active_at": "2026-09-26T03:44:00Z", "status": "active",
    "verified_at": "2025-06-04T09:00:00Z",
    "quiz_count": 24, "attempt_count": 3410,
    "groups": [{ "id": "uuid", "name": "Physics A" }]
  }
]
```
`verified_at` is `null` for teachers verified before migration 078.

---

## GET `/institution/teachers/counts`
```json
{ "all": 46, "active": 43, "pending": 2, "suspended": 1, "needs_action": 3, "invited": 1 }
```
`invited` counts open invitations. Those aren't accounts yet, so they aren't in `all`.

---

## GET `/institution/teachers/invites`
Open (pending, unexpired) invitations, newest first.
```json
[{ "id": "uuid", "email": "rahul.d@school.edu", "name": "Rahul Desai",
   "invited_at": "2026-09-21T05:00:00Z", "expires_at": "2026-09-28T05:00:00Z" }]
```

## POST `/institution/teachers/invites/{inviteId}/resend`
Emails the same invitation again and restarts its 7-day window. The token is
unchanged, so the first email's link keeps working.

`200 { "email": "...", "expires_at": "..." }` · `404` if the invitation isn't open ·
`502 EMAIL_FAILED` if the email couldn't be sent.

---

## GET `/institution/teachers/{userId}`
### Response `200`
```json
{
  "id": "uuid", "display_name": "Anil Patil", "full_name": "Anil Patil",
  "email": "anil.p@school.edu", "status": "active",
  "last_active_at": "2026-09-26T03:44:00Z", "verified_at": "2025-06-04T09:00:00Z",
  "quiz_count": 24, "attempt_count": 3410, "average_score": 71.5,
  "quizzes": [
    { "id": "uuid", "title": "Kinematics Speed Round", "type": "play_and_win", "status": "published",
      "question_count": 10, "completion_count": 388, "average_score": 74.1,
      "published_at": "2026-09-26T02:40:00Z", "created_at": "2026-09-20T02:40:00Z" }
  ],
  "groups": [{ "id": "uuid", "name": "Physics A", "student_count": 58 }],
  "recent_activity": [{ "text": "Published “Kinematics Speed Round”", "at": "2026-09-26T02:40:00Z" }]
}
```
- `average_score` is weighted by completions across the teacher's quizzes, and is `null` with no completions.
- `quizzes` lists up to 100, newest first.
- `recent_activity` holds the last five quiz publications and topic-request pickups.

---

## PATCH `/institution/teachers/{userId}/status`
Body `{ "action": "suspend" | "reactivate" | "verify", "reason": "..." }`.
`verify` works only on a `pending` teacher (`422 NOT_PENDING` otherwise), sets
`verified_at`, and emails the teacher.

---

## DELETE `/institution/teachers/{userId}`
Removes the teacher from the institution (does not delete their account) and unassigns them from its classes.

### Response `200`
```json
{ "message": "teacher removed from institution" }
```

---

## GET `/institution/groups`
### Response `200`
```json
[
  {
    "id": "uuid", "name": "Physics A", "description": "Grade 11 · morning batch",
    "invite_code": "PHY-11A", "archived_at": null, "created_at": "2025-06-01T00:00:00Z",
    "student_count": 58, "teacher_count": 1, "teacher_names": ["Anil Patil"],
    "average_score_30d": 72.4,
    "current_curriculum": {
      "version_id": "uuid", "name": "Physics", "label": "2026 edition", "subject": "Physics",
      "grade": "11", "revision": 3, "academic_year_name": "2026–27"
    }
  }
]
```
- `average_score_30d` covers completed attempts by the class's current students on this institution's quizzes in the last 30 days. It is `null` with no attempts.
- `current_curriculum` is the live assignment in an academic year that covers today, or `null`.

---

## POST `/institution/groups`
### Request Body
```json
{ "name": "Class 10A", "description": "Optional", "grade": "10", "section": "A" }
```

### Response `201`
```json
{ "id": "uuid", "name": "Class 10A", "invite_code": "ABCD1234", "created_at": "..." }
```

---

## GET `/institution/groups/{groupId}`
### Response `200`
```json
{
  "id": "uuid", "name": "Physics A", "description": "…", "invite_code": "PHY-11A",
  "student_count": 58, "average_score": 72.4,
  "students": [
    {
      "enrollment_id": "uuid", "id": "uuid", "display_name": "Aarya Kulkarni", "email": "…",
      "status": "active",
      "total_points": 2480, "current_streak": 12, "last_active_at": "…",
      "average_score": 81.2,
      "class_average_score": 83.0, "class_attempts": 12,
      "joined_at": "2025-06-12T00:00:00Z"
    }
  ],
  "teachers": [{ "id": "uuid", "display_name": "Anil Patil", "email": "…", "status": "active" }]
}
```
- `class_average_score` covers the student's attempts on quizzes set for this class (`quizzes.group_id`). It is `null` with no attempts; check `class_attempts`.
- `joined_at` is when they joined this class.

---

## PATCH `/institution/groups/{groupId}`
### Request Body
```json
{ "name": "Class 10B", "description": "Updated description", "grade": "10", "section": "B" }
```

### Response `200`
```json
{ "message": "group updated" }
```

---

## DELETE `/institution/groups/{groupId}`
Ends the class (archives it). See "Classes: end, reopen, past classes".

### Response `200`
```json
{ "message": "class ended" }
```

---

## POST `/institution/groups/{groupId}/students`
### Request Body
```json
{ "user_id": "uuid" }
```

### Response `200`
```json
{ "message": "student added to group" }
```

---

## DELETE `/institution/groups/{groupId}/students/{userId}`
### Response `200`
```json
{ "message": "student removed from group" }
```

---

## POST `/institution/groups/{groupId}/teachers`
### Request Body
```json
{ "user_id": "uuid" }
```

### Response `200`
```json
{ "message": "teacher assigned to group" }
```

---

## GET `/institution/reports/student-performance`
Active students ranked by lifetime points, paged.

### Query Params
| Param | Description |
|-------|-------------|
| `group_id` | Only students in this class |
| `date_from`, `date_to` | Restrict the attempts behind `quizzes_taken` and `average_score` (by `completed_at`) |
| `page`, `limit` | Pagination (default `limit` 50, max 200) |

### Response `200` (paginated)
```json
[
  { "id": "uuid", "display_name": "Ishita Deshpande", "total_points": 4120, "current_streak": 21,
    "quizzes_taken": 41, "average_score": 94.2, "class_names": ["Science B"] }
]
```
`average_score` is `0` when `quizzes_taken` is `0`. Show it as "no attempts".

> **Changed:** this endpoint now returns the paginated envelope (`data` + `meta`)
> instead of a bare array, and honours `date_from`/`date_to` (previously ignored).

---

## GET `/institution/reports/teacher-activity`
Per-teacher activity stats for the institution.

### Query Params
| Param | Description |
|-------|-------------|
| `group_id` | Only teachers assigned to this class |
| `date_from`, `date_to` | Restrict attempt aggregates to this date range (ISO timestamp). |
| `page`, `limit` | Pagination (default `page=1`, `limit=20`, max `100`). |

### Response `200`
```json
{
  "data": [
    {
      "teacher_id":      "uuid",
      "display_name":    "Ms. Sharma",
      "quizzes_created": 12,
      "total_attempts":  348,
      "avg_score":       72.4
    }
  ],
  "meta": { "page": 1, "limit": 20, "total": 7 }
}
```

---

## GET `/institution/reports/quiz-analytics`
Per-quiz breakdown with completion rate and score-distribution bands (≥80, 60–79, <60).

### Query Params
| Param | Description |
|-------|-------------|
| `group_id` | Only quizzes set for this class |
| `date_from`, `date_to` | Restrict attempts by `started_at`. |
| `page`, `limit` | Pagination (default `page=1`, `limit=20`, max `100`). |

### Response `200`
```json
{
  "data": [
    {
      "quiz_id":         "uuid",
      "title":           "Algebra Basics",
      "completion_rate": 84.6,
      "score_dist_high": 45,
      "score_dist_mid":  62,
      "score_dist_low":  18
    }
  ],
  "meta": { "page": 1, "limit": 20, "total": 32 }
}
```

---

## GET `/institution/reports/streak-health`
Student counts by streak status: a snapshot of today.

### Query Params
| Param | Description |
|-------|-------------|
| `group_id` | Only students in this class |

### Response `200`
```json
{ "active": 512, "at_risk": 289, "broken": 483, "unclaimed": 37 }
```
- `active` — `current_streak >= 7`
- `at_risk` — `current_streak` between 1 and 6
- `broken` — `current_streak = 0`
- `unclaimed` — roster records with no account yet. They have no streak, so they are reported beside the bands, not in them. Always `0` when `group_id` is set, since unclaimed records can't be in a class.

---

## GET `/institution/reports/points-summary`
Points distribution trend + per-student totals.

### Query Params
| Param | Description |
|-------|-------------|
| `group_id` | Only students in this class (both trend and list) |
| `date_from`, `date_to` | Restrict the `daily_trend` window. Defaults to the last 30 days. |

### Response `200`
```json
{
  "daily_trend": [
    { "date": "2026-04-17", "points_distributed": 1240 }
  ],
  "students": [
    {
      "user_id":       "uuid",
      "display_name":  "Aman R.",
      "total_points":  4820,
      "expiring_soon": 350
    }
  ]
}
```
`expiring_soon` is the sum of positive `points_ledger` entries with `expires_at` within the next 30 days.

---

## GET `/institution/quizzes/{quizId}/results`
Institution-admin view of attempt results for a quiz the institution owns.

### Query Params
| Param | Description |
|-------|-------------|
| `attempts_limit` | Attempts page size, 1–200 (default 50) |
| `attempts_offset` | Attempts offset (default 0) |

### Response `200`
```json
{
  "started":         146,
  "completions":     128,
  "completion_rate": 87.7,
  "avg_score":       71.2,
  "class_names":     ["Chemistry A", "Chemistry B"],
  "per_question_accuracy": [
    { "position": 1, "accuracy_pct": 92.1 }
  ],
  "attempts_total":  146,
  "attempts_limit":  50,
  "attempts_offset": 0,
  "attempts": [
    {
      "attempt_id":    "uuid",
      "student_id":    "uuid",
      "display_name":  "Priya S.",
      "status":        "completed",
      "score_pct":     80.0,
      "points_earned": 240,
      "time_taken_ms": 412300,
      "started_at":    "2026-05-12T10:15:22Z",
      "completed_at":  "2026-05-12T10:22:14Z"
    }
  ]
}
```
- `attempts` includes unfinished attempts: `status` is `in_progress` or `abandoned`, with `score_pct` and `completed_at` set to `null`.
- For an unfinished attempt, `time_taken_ms` runs to its last submitted answer, not to now.
- `class_names` are the classes the quiz is set for, directly or through a curriculum unit it covers.

### Errors
- `404` — quiz not found in this institution.

---

## GET `/institution/settings`
### Response `200`
```json
{
  "name":                  "Springfield Academy",
  "type":                  "school",
  "timezone":              "America/Chicago",
  "verification_status":   "verified",
  "submitted_at":          "2026-09-25T00:00:00Z",
  "reference":             "INST-2F9A1C",
  "open_play_win_quizzes": 3,
  "student_referral_code": "SINST-ABC",
  "teacher_referral_code": "TINST-XYZ",
  "point_rules": {
    "point_multiplier":      1.0,
    "streak_grace_enabled":  true,
    "play_win_score_hidden": false,
    "point_expiry_months":   6
  },
  "pending_code_reset": null
}
```
- `verification_status` is `pending`, `verified` or `suspended`.
- `reference` is a stable short id for support conversations.
- `open_play_win_quizzes` is the count of published Play & Win quizzes still open, for the warning shown before a point-rule change: attempts already started keep the old rules.

---

## PATCH `/institution/settings`
### Request Body
```json
{ "name": "New Name", "timezone": "Europe/London", "type": "university" }
```

### Response `200`
```json
{ "message": "settings updated" }
```
The audit entry records the before/after of each changed field.

---

## PATCH `/institution/settings/point-rules`
### Request Body
```json
{
  "point_multiplier":      1.5,
  "streak_grace_enabled":  true,
  "play_win_score_hidden": false,
  "point_expiry_months":   12
}
```

All fields optional — only provided fields are updated. The audit entry
records the before/after of each changed rule.

### Response `200`
```json
{ "message": "point rules updated" }
```

---

## GET `/institution/audit-log`
### Query Params
| Param | Description |
|-------|-------------|
| `action_type` | One action, e.g. `update_point_rules` |
| `action_group` | `membership`, `academics` or `settings` |
| `date_from`, `date_to` | ISO timestamps (inclusive) |
| `page`, `limit` | Pagination (max `limit` 50) |

### Response `200` (paginated)
```json
[
  {
    "id": "uuid", "timestamp": "2026-09-18T07:00:00Z",
    "admin_name": "Admin", "admin_role": "institution_admin",
    "action_type": "update_admission_policy", "target_type": "institution", "target_id": "uuid",
    "target_label": "Sahyadri Junior College",
    "reason": "custom",
    "changes": [
      { "field": "mode", "before": "verify_first", "after": "custom" },
      { "field": "email_domains", "before": [], "after": ["sahyadri.edu.in"] }
    ]
  }
]
```
- `target_label` is resolved when the entry is read, so older entries get names too. It is `null` when the target no longer exists.
- `changes` is present only for entries that recorded before/after values: settings, point rules, the admission policy, and edit-request reviews.

---

## GET `/institution/quizzes`
The institution's quizzes in every status (the admin roster).

### Query Params
`status`, `type`, `search` (title or teacher name), `page`, `limit`.

Each row is a quiz plus `teacher_name`, `taker_count` (completed attempts),
`average_score`, `started_count` (all attempts), and `completion_rate`
(`taker_count / started_count × 100`, absent while nothing has started).

---

## GET `/institution/quizzes/{quizId}`
Same as `GET /quizzes/{quizId}`.

---

## GET `/institution/topic-requests`
Shared with `GET /teacher/topic-requests`.

### Query Params
| Param | Description |
|-------|-------------|
| `status` | `pending`, `in_progress`, `done`, or `open` (pending + in progress) |
| `assigned` | `none` — only unassigned requests |
| `search` | Topic, subject, description, or the student's name |
| `page`, `limit` | Pagination (max `limit` 100) |

Open states are ordered oldest first; otherwise newest first.

### Response `200` (paginated)
```json
[
  {
    "id": "uuid", "student_id": "uuid", "student_name": "Kabir Mehta",
    "topic": "Rotational motion worked examples", "subject": "Physics", "description": "…",
    "status": "pending", "assigned_to": null, "assigned_to_name": null,
    "created_at": "2026-09-21T05:00:00Z",
    "requester_count": 7, "other_requesters": ["Sneha Gaikwad", "Omkar Bhosale", "Priya Nair"]
  }
]
```
`requester_count` is how many open requests ask for the same topic and subject
(case-insensitive, this one included). `other_requesters` names up to three
of the others.

## GET `/institution/topic-requests/counts`
```json
{ "open": 14, "unassigned": 5, "done": 22 }
```

---

## PATCH `/institution/topic-requests/{requestId}`
Same as `PATCH /teacher/topic-requests/{requestId}`.

---

# 11. Super Admin

Base path: `/admin`
**Auth required:** Yes — roles `super_admin`, `moderator`, or `support_agent` (specific endpoints noted below)

---

## GET `/admin/overview`
**Roles:** all admin

### Response `200`
```json
{
  "total_users":       5200,
  "active_users_week": 340,
  "institutions":      { "pending": 3, "verified": 47, "suspended": 1 },
  "quizzes":           { "published": 210, "pending": 8, "reported": 2 },
  "attempts_today":    124,
  "attempts_week":     890,
  "avg_score_week":    69.3,
  "points_week":       45000,
  "points_all_time":   1200000
}
```

---

## GET `/admin/activity-feed`
**Roles:** all admin
### Query Params
`type` — filter by action type

### Response `200`
Array of recent audit log events.

---

## GET `/admin/institutions`
**Roles:** all admin
### Query Params
`search`, `status`, `type`, `page`, `limit`

### Response `200` (paginated)
Array with `id`, `name`, `type`, `status`, `contact_email`, `verified_at`, `created_at`.

---

## GET `/admin/institutions/queue`
**Roles:** all admin

Returns pending institutions awaiting approval.

### Response `200`
Array with `id`, `name`, `type`, `contact_email`, `submitted_at`.

---

## GET `/admin/institutions/{institutionId}`
**Roles:** all admin

### Response `200`
```json
{
  "id":                    "uuid",
  "name":                  "Springfield Academy",
  "type":                  "school",
  "status":                "verified",
  "contact_email":         "admin@springfield.edu",
  "student_referral_code": "SINST-ABC",
  "teacher_referral_code": "TINST-XYZ",
  "verified_at":           "2024-01-10T00:00:00Z",
  "student_count":         120,
  "teacher_count":         8,
  "quiz_count":            34
}
```

---

## POST `/admin/institutions/{institutionId}/approve`
**Roles:** super_admin only

Approves the institution and generates referral codes.

### Response `200`
```json
{
  "message":               "institution approved",
  "student_referral_code": "SINST-ABC",
  "teacher_referral_code": "TINST-XYZ"
}
```

---

## POST `/admin/institutions/{institutionId}/reject`
**Roles:** super_admin only

### Request Body
```json
{ "reason": "Incomplete documentation" }
```

### Response `200`
```json
{ "message": "institution rejected" }
```

---

## POST `/admin/institutions/{institutionId}/suspend`
**Roles:** super_admin only

### Request Body
```json
{ "reason": "Policy violation" }
```

### Response `200`
```json
{ "message": "institution suspended" }
```

---

## POST `/admin/institutions/{institutionId}/reactivate`
**Roles:** super_admin only

### Response `200`
```json
{ "message": "institution reactivated" }
```

---

## POST `/admin/institutions/{institutionId}/reset-referral-codes`
**Roles:** super_admin only

### Response `200`
```json
{
  "student_referral_code": "SINST-NEW",
  "teacher_referral_code": "TINST-NEW"
}
```

---

## POST `/admin/institutions/{institutionId}/provision-admin`
**Roles:** super_admin only

Provisions an `institution_admin` user account for a verified institution and sends a Supabase email invite.

### Request Body (Optional)
```json
{
  "admin_name": "Tom",
  "admin_email": "tom@school.com"
}
```
If omitted, defaults to the institution contact email and onboarding admin name.

### Response `201`
```json
{
  "message": "Institution admin provisioned. An invite email has been sent...",
  "user_id": "uuid",
  "admin_email": "admin@school.com",
  "admin_name": "School Admin",
  "institution_id": "uuid",
  "institution": "Springfield Academy"
}
```

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 422 | `NOT_VERIFIED` | Institution must be verified first |

---

## GET `/admin/users`
**Roles:** all admin
### Query Params
`search`, `role`, `status`, `institution_id`, `page`, `limit`

### Response `200` (paginated)
Array with `id`, `display_name`, `email`, `role`, `institution`, `status`, `last_active_at`, `total_points`, `current_streak`.

---

## GET `/admin/users/{userId}`
**Roles:** all admin

### Response `200`
```json
{
  "id":             "uuid",
  "display_name":   "Alice Smith",
  "email":          "alice@example.com",
  "role":           "student",
  "status":         "active",
  "institution":    "Springfield Academy",
  "total_points":   1250,
  "current_streak": 5,
  "member_since":   "2024-01-15T00:00:00Z",
  "last_active_at": "2024-03-01T14:00:00Z",
  "recent_attempts": [
    { "id": "uuid", "quiz_title": "Biology Ch3", "score_pct": 85.0, "completed_at": "..." }
  ]
}
```

---

## PATCH `/admin/users/{userId}/suspend`
**Roles:** all admin

### Request Body
```json
{ "reason": "Abuse of platform" }
```

### Response `200`
```json
{ "message": "user suspended" }
```

---

## PATCH `/admin/users/{userId}/reactivate`
**Roles:** all admin

### Response `200`
```json
{ "message": "user reactivated" }
```

---

## DELETE `/admin/users/{userId}`
**Roles:** super_admin only

GDPR soft-delete — anonymises name and email.

### Response `200`
```json
{ "message": "user deleted" }
```

---

## POST `/admin/users/{userId}/points`
**Roles:** super_admin only

### Request Body
```json
{ "amount": 500, "reason": "Competition winner bonus" }
```

`amount` may be negative to deduct points. Balance is floored at 0.

### Response `200`
```json
{ "new_balance": 1750, "adjustment": 500 }
```

---

## POST `/admin/users/{userId}/impersonate`
**Roles:** all admin

### Response `200`
```json
{ "session_id": "uuid", "message": "impersonation session started" }
```

---

## POST `/admin/impersonation/{sessionId}/end`
**Roles:** all admin

### Response `200`
```json
{ "message": "impersonation ended" }
```

---

## POST `/admin/users/{userId}/reset-password`
**Roles:** super_admin only

Triggers a password-reset email for the user via the Supabase Admin API (`generate_link` with `type=recovery`). The email is sent directly by Supabase; no password or link is returned to the caller.

### Response `200`
```json
{ "message": "password reset email sent" }
```

### Error responses
| Status | Condition |
|--------|-----------|
| `404` | User not found or soft-deleted |
| `502` | Supabase rejected the request |

---

## GET `/admin/quizzes/moderation-queue`
**Roles:** all admin

### Response `200`
Array with `id`, `title`, `teacher`, `institution`, `question_count`, `submitted_at`.

---

## POST `/admin/quizzes/{quizId}/approve`
**Roles:** super_admin, moderator

### Response `200`
```json
{ "message": "quiz approved" }
```

---

## POST `/admin/quizzes/{quizId}/reject`
**Roles:** super_admin, moderator

### Request Body
```json
{ "reason": "Incorrect answers detected" }
```

### Response `200`
```json
{ "message": "quiz rejected" }
```

---

## POST `/admin/quizzes/{quizId}/request-edits`
**Roles:** super_admin, moderator

Sends feedback to the teacher without fully rejecting the quiz. Sets `status` to `needs_edits` and stores the feedback text. Only applies to quizzes currently in `pending_approval`.

### Request Body
```json
{ "feedback": "Please add at least one image to question 3." }
```

### Response `200`
```json
{ "message": "edit request sent to teacher" }
```

### Error responses
| Status | Condition |
|--------|-----------|
| `400` | `feedback` is missing or empty |
| `404` | Quiz not found or not in `pending_approval` state |

---

## POST `/admin/quizzes/{quizId}/unpublish`
**Roles:** super_admin only

### Request Body
```json
{ "reason": "Policy violation" }
```

### Response `200`
```json
{ "message": "quiz unpublished" }
```

---

## GET `/admin/reports`
**Roles:** all admin
### Query Params
`status` (`open`, `resolved`), `priority`, `page`, `limit`

### Response `200` (paginated)
Array with `id`, `reporter`, `quiz_title`, `reason`, `status`, `priority`, `created_at`.

---

## POST `/admin/reports/{reportId}/resolve`
**Roles:** all admin

### Request Body
```json
{ "resolution": "remove_quiz" }
```

If `resolution` is `remove_quiz`, the associated quiz is automatically unpublished.

### Response `200`
```json
{ "message": "report resolved" }
```

---

## GET `/admin/point-economy`
**Roles:** super_admin only

### Response `200`
Array of config entries with `key`, `value`, `description`, `updated_at`.

---

## PATCH `/admin/point-economy/{key}`
**Roles:** super_admin only

### Request Body
```json
{ "value": 15, "reason": "Adjusted for Q2 engagement campaign" }
```

`reason` is optional but is written to the audit log when provided.

### Response `200`
```json
{ "message": "config updated" }
```

---

## GET `/admin/announcements`
**Roles:** all admin
### Query Params
`status` (`draft`, `scheduled`, `sent`, `retracted`), `page`, `limit`

### Response `200` (paginated)
Array with `id`, `title`, `body`, `delivery_types`, `audience`, `institution_ids`,
`status`, `scheduled_at`, `sent_at`, `created_at`, and unique learner `reach`.

---

## PATCH `/admin/announcements/{announcementId}/retract`
**Roles:** super_admin, moderator

Retracts a `scheduled` or `sent` announcement. Has no effect on `draft`.

### Response `200`
```json
{ "message": "announcement retracted" }
```

### Error responses
| Status | Condition |
|--------|-----------|
| `404` | Announcement not found or already in `draft`/`retracted` state |

---

## POST `/admin/announcements`
**Roles:** super_admin, moderator

### Request Body
```json
{
  "title":          "Platform Update",
  "body":           "We've added new question types!",
  "cta_label":      "Learn More",
  "cta_url":        "https://...",
  "delivery_types": ["in_app_banner", "in_app_notification"],
  "audience":       "all",
  "institution_ids": [],
  "scheduled_at":   "2024-04-01T09:00:00Z"
}
```

`title` and `body` are required. `delivery_types` can include `in_app_banner`,
`in_app_notification`, and/or `email`. Audience is `all`, `students`, `teachers`,
`institution`, or `country`. Institution targeting requires `institution_ids`.
Moderators' email announcements are created as drafts pending approval.

### Response `201`
```json
{ "id": "uuid", "status": "scheduled" }
```

---

## POST `/admin/announcements/{announcementId}/publish`
**Roles:** super_admin

Approves a draft and queues it for the five-minute announcement dispatcher.

```json
{ "status": "scheduled", "message": "announcement queued" }
```

---

## GET `/admin/audit-log`
**Roles:** super_admin only
### Query Params
`admin_name`, `action_type`, `target_type`, `page`, `limit`

### Response `200` (paginated)
Array of full audit entries with `id`, `timestamp`, `admin_name`, `admin_role`, `action_type`, `target_type`, `target_id`, `reason`, `old_value`, `new_value`.

---

## GET `/admin/admin-accounts`
**Roles:** super_admin only

### Response `200`
Array with `id`, `name`, `email`, `role`, `status`, `created_at`, `accepted_at`.

**Status lifecycle:**
| Status | Meaning |
|--------|---------|
| `pending` | Invite sent, awaiting the admin's first sign-in (acceptance). |
| `invite_failed` | The invite email could not be delivered. Resend to retry. |
| `active` | Invite accepted (first successful sign-in) — full access for their role. |
| `suspended` | Access revoked; can be reactivated. |

`accepted_at` is the timestamp of acceptance (first sign-in); it is `null` while `pending`/`invite_failed`. A `pending`/`invite_failed` admin is automatically promoted to `active` the first time they authenticate.

---

## POST `/admin/admin-accounts`
**Roles:** super_admin only

Provisions a Supabase invite and emails the admin an invite link. The new row is
created `pending` (or `active` if the email already had a Supabase account). If the
invite email fails to send, the row is created `invite_failed`.

### Request Body
```json
{ "name": "Jane Mod", "email": "jane@qwish.in", "role": "moderator" }
```

### Response `201`
```json
{ "id": "uuid", "status": "pending", "message": "admin account created, invite sent" }
```
`status` is one of `pending`, `active`, or `invite_failed`. On `invite_failed` the
`message` reads `"admin account created, but the invite email failed to send"`.

---

## POST `/admin/admin-accounts/{adminId}/resend`
**Roles:** super_admin only

Re-issues the Supabase invite and email for a `pending` or `invite_failed` admin.
Returns `400` if the account is not in an invitable state (e.g. already `active`).

### Response `200`
```json
{ "status": "pending", "message": "invite resent" }
```
`status` is `pending` on success, or `invite_failed` if the email failed again.

---

## PATCH `/admin/admin-accounts/{adminId}`
**Roles:** super_admin only

Cannot modify your own account.

### Request Body
```json
{ "role": "support_agent", "status": "active" }
```

All fields optional.

### Response `200`
```json
{ "message": "admin account updated" }
```

---

## DELETE `/admin/admin-accounts/{adminId}`
**Roles:** super_admin only

Soft-deletes the account (status → `deleted`). Also used to **revoke** a `pending`
invite — the invite link stops working and the admin cannot join. Cannot delete your
own account.

### Response `200`
```json
{ "message": "admin account deleted" }
```

---

## GET `/admin/promos`
**Roles:** all admin
### Query Params
`status` (`draft`, `active`, `inactive`), `page`, `limit`

### Response `200` (paginated)
Array with `id`, `placement`, `title`, `body`, `cta_label`, `cta_url`, `target`,
`institution_ids`, `status`, `start_date`, `end_date`, `created_at`, and unique
learner `impressions`.

---

## POST `/admin/promos`
**Roles:** super_admin, moderator

### Request Body
```json
{
  "title":      "Summer Challenge",
  "body":       "Complete 5 quizzes this week!",
  "cta_label":  "Start Now",
  "cta_url":    "https://...",
  "placement":  "home_banner",
  "target":     "all",
  "status":     "active",
  "institution_ids": [],
  "start_date": "2024-06-01T00:00:00Z",
  "end_date":   "2024-06-30T23:59:59Z"
}
```

`title`, `placement`, and `target` are required. `placement` must be one of
`home_banner`, `quiz_browser_banner`, `splash_interstitial`, `achievement_prompt`.
Target is `all`, `students`, `institution`, or `lapsed`; institution targeting
requires `institution_ids`. A future `start_date` is displayed as scheduled.

### Response `201`
Returns the complete created promo object so the console can render it without a reload.

---

## PATCH `/admin/promos/{promoId}`
**Roles:** super_admin, moderator

Activate or deactivate a promo.

### Request Body
```json
{ "status": "active" }
```

`status` must be `draft`, `active`, or `inactive`.

### Response `200`
```json
{ "message": "promo updated" }
```

---

## DELETE `/admin/promos/{promoId}`
**Roles:** super_admin only

Hard-deletes the promo record.

### Response `200`
```json
{ "message": "promo deleted" }
```

---

## GET `/admin/brands`
**Roles:** all admin
### Query Params
`status` (`pending`, `active`, `suspended`), `industry`, `page`, `limit`

### Response `200` (paginated)
Array with `id`, `name`, `industry`, `contact_email`, `website`, `reward_pool`, `status`, `created_at`.

---

## POST `/admin/brands`
**Roles:** super_admin only

Creates a brand in `pending` status.

### Request Body
```json
{
  "name":          "Acme Corp",
  "industry":      "EdTech",
  "contact_email": "partners@acme.com",
  "website":       "https://acme.com",
  "reward_pool":   5000.00
}
```

`name` is required.

### Response `201`
```json
{ "id": "uuid", "status": "pending" }
```

---

## POST `/admin/brands/{brandId}/approve`
**Roles:** super_admin only

Approves a `pending` brand, setting status to `active`.

### Response `200`
```json
{ "message": "brand approved" }
```

---

## POST `/admin/brands/{brandId}/suspend`
**Roles:** super_admin only

### Response `200`
```json
{ "message": "brand suspended" }
```

---

## POST `/admin/brands/{brandId}/reactivate`
**Roles:** super_admin only

### Response `200`
```json
{ "message": "brand reactivated" }
```

---

## GET `/admin/brands/{brandId}/sponsorship-requests`
**Roles:** all admin

### Response `200`
Array with `id`, `quiz_id`, `quiz_title`, `status`, `reason`, `requested_at`, `reviewed_at`.

---

## POST `/admin/sponsorship-requests/{requestId}/approve`
**Roles:** super_admin, moderator

Approves a `pending` sponsorship request.

### Response `200`
```json
{ "message": "sponsorship request approved" }
```

---

## POST `/admin/sponsorship-requests/{requestId}/reject`
**Roles:** super_admin, moderator

### Request Body
```json
{ "reason": "Brand does not meet content guidelines." }
```

### Response `200`
```json
{ "message": "sponsorship request rejected" }
```

---

# 12. Internal Cron

All cron endpoints require the `X-Cron-Secret` header matching the `CRON_SECRET` environment variable.
They are registered only when `CRON_SECRET` is non-empty (an empty secret would match a missing header
and leave them open), and they take no Supabase JWT.

Scheduling is external: the `type: cron` services in `render.yaml` POST these on the schedules below.
Jobs sharing a slot run as one chained shell command, so ordering is explicit.

| Method | Route | Schedule (UTC) | Description |
|--------|-------|----------------|-------------|
| `POST` | `/internal/cron/close-expired-quizzes` | `0 * * * *` | Closes quizzes past their `expires_at` |
| `POST` | `/internal/cron/abandon-stale-attempts` | `0 * * * *` | Marks stale in-progress attempts `abandoned` |
| `POST` | `/internal/cron/expire-points` | `0 0 * * *` | Deactivates expired point ledger entries |
| `POST` | `/internal/cron/reset-streaks` | `0 0 * * *` | Zeroes streaks past their grace window; sends recovery alerts |
| `POST` | `/internal/cron/rank-change-alerts` | `0 0 * * *` | Notifies users whose leaderboard rank moved |
| `POST` | `/internal/cron/recompute-question-difficulty` | `0 0 * * *` | Recomputes derived question difficulty |
| `POST` | `/internal/cron/streak-nudges` | `0 14 * * *` | Reminds users whose streak is unclaimed today |
| `POST` | `/internal/cron/snapshot-leaderboard` | `1 0 * * 1` | Snapshots weekly leaderboard rankings |
| `POST` | `/internal/cron/weekly-digests` | `0 8 * * 1` | Weekly summary push |
| `POST` | `/internal/cron/weekly-insights-email` | `0 8 * * 1` | Weekly insights email |

Every endpoint is idempotent within its window, so a manual re-trigger is safe:

```bash
curl -fsS -X POST -H "X-Cron-Secret: $CRON_SECRET" \
  https://<api-host>/api/v1/internal/cron/reset-streaks
```

All return:
```json
{ "message": "done" }
```

> In production, these jobs also run automatically in-process via Go tickers — external cron triggers are optional.

---

# 13. Health

## GET `/health`
**Auth required:** No

### Response `200`
```json
{ "status": "ok" }
```

---

# 14. Onboarding

## POST `/onboarding/institution`
**Auth required:** No

Submit an application to register a new institution. Will be marked as 'pending' for super admin review.

### Request Body
```json
{
  "name": "Springfield High",
  "type": "school",
  "contact_email": "principal@springfield.edu",
  "admin_name": "Seymour Skinner",
  "timezone": "America/New_York",
  "phone": "555-0199",
  "website": "https://springfield.edu",
  "city": "Springfield",
  "state": "IL",
  "country": "US"
}
```
> `phone`, `website`, `city`, `state`, `country` are optional.

### Response `201`
```json
{
  "id": "uuid",
  "status": "pending",
  "message": "Your institution application has been submitted...",
  "contact_email": "principal@springfield.edu"
}
```

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 409 | `DUPLICATE_REQUEST` | An application is already pending for this email |

---


## GET `/onboarding/institution/status`
**Auth required:** No

Check the current status of an institution onboarding request via email.

### Query Params
| Param | Description |
|-------|-------------|
| `email` | **Required.** The contact email used in the application. |

### Response `200`
```json
{
  "id": "uuid",
  "name": "Springfield High",
  "status": "pending"
}
```

---

# 15. Contact Form

Public endpoint for brand-website contact submissions. No authentication required.

---

## POST `/contact`
**Auth required:** No

Stores a contact form submission, categorised by topic.

### Request Body
```json
{
  "topic":    "partnership",
  "name":     "Priya Sharma",
  "email":    "priya@example.com",
  "phone":    "+91 9876543210",
  "message":  "We'd love to explore a partnership opportunity.",
  "metadata": {}
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `topic` | Yes | One of the valid topic values (see below) |
| `name` | Yes | Sender's full name |
| `email` | Yes | Sender's email address |
| `phone` | No | Sender's phone number |
| `message` | Yes | Message body |
| `metadata` | No | Optional JSONB object for topic-specific extra fields |

### Valid Topics

| Value | Use-case |
|-------|----------|
| `general` | Generic enquiries |
| `partnership` | Brand / business partnerships |
| `support` | Technical or account help |
| `feedback` | Product feedback |
| `press` | Media / press enquiries |
| `institution_onboarding` | Schools / colleges interested in joining |
| `careers` | Job / internship enquiries |

### Response `201`
```json
{
  "id":      "uuid",
  "message": "Your message has been received. We'll get back to you at priya@example.com."
}
```

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Missing required field or invalid topic |

---

## GET `/admin/contact-submissions`
**Auth required:** Yes
**Roles:** `super_admin`, `moderator`, `support_agent`

Returns a list of contact submissions (up to 100), optionally filtered.

### Query Params
| Param | Description |
|-------|-------------|
| `topic` | Filter by topic (e.g. `support`) |
| `status` | Filter by status (`new`, `in_progress`, `resolved`, `spam`) |

### Response `200`
```json
{
  "count": 2,
  "submissions": [
    {
      "id":         "uuid",
      "topic":      "support",
      "name":       "Priya Sharma",
      "email":      "priya@example.com",
      "phone":      "+91 9876543210",
      "message":    "Help me reset my account.",
      "metadata":   null,
      "status":     "new",
      "created_at": "2026-05-13T07:00:00Z"
    }
  ]
}
```

---

## POST `/admin/contact-submissions/{id}/resolve`
**Auth required:** Yes
**Roles:** `super_admin`, `moderator`, `support_agent`

Updates the status of a contact submission.

### Request Body
```json
{ "status": "resolved" }
```

| Value | Meaning |
|-------|---------|
| `in_progress` | Submission is being handled |
| `resolved` | Submission has been resolved |
| `spam` | Submission is spam |

### Response `200`
```json
{ "status": "resolved" }
```

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Invalid status value |
| 404 | `NOT_FOUND` | Submission not found |

---

# 16. Teacher Invite

Institution admins can invite teachers by email. The invite token is embedded in a sign-up link sent to the teacher. Invites expire after 7 days.

---

## POST `/institution/teachers/invite`
**Auth required:** Yes  
**Roles:** `institution_admin`

Sends an email invitation to join the institution as a teacher.

### Request Body
```json
{
  "email": "teacher@school.edu",
  "name":  "Anil Mehta"
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `email` | Yes | Email address of the teacher to invite |
| `name` | No | Recipient's name (used in the email greeting) |

### Response `201`
```json
{
  "message":    "invite sent",
  "invite_id":  "uuid",
  "email":      "teacher@school.edu",
  "expires_at": "2026-05-20T08:10:00Z"
}
```

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Missing email, teacher already in institution, or pending invite already exists |

### Notes
- A duplicate invite for the same email + institution is rejected while a **pending, non-expired** invite exists.
- The invite link is `https://app.qwish.in/auth/teacher-signup?token=<token>`.

---

# 17. Notification Log

## GET `/admin/notification-log`
**Auth required:** Yes  
**Roles:** `super_admin`

Returns a paginated list of all outbound email send attempts (newest first).

### Query Params
| Param | Description |
|-------|-------------|
| `to_email` | Partial match on recipient address (case-insensitive) |
| `status` | Filter by result — `sent` or `failed` |
| `date_from` | ISO date `YYYY-MM-DD` — inclusive start |
| `date_to` | ISO date `YYYY-MM-DD` — inclusive end |
| `page` | Page number (default `1`) |
| `limit` | Results per page, max `100` (default `50`) |

### Response `200`
```json
{
  "data": [
    {
      "id":         "uuid",
      "to_email":   "teacher@school.edu",
      "subject":    "You're invited to teach on QuizApp",
      "status":     "sent",
      "reference":  "teacher_invite:uuid",
      "created_at": "2026-05-13T08:10:00Z"
    },
    {
      "id":         "uuid",
      "to_email":   "admin@school.edu",
      "subject":    "Your QuizApp Institution Has Been Approved",
      "status":     "failed",
      "error":      "resend error 422: invalid email address",
      "reference":  "institution_approval",
      "created_at": "2026-05-13T07:55:00Z"
    }
  ],
  "meta": { "page": 1, "limit": 50, "total": 2 }
}
```

### `reference` values
| Reference | Triggered by |
|-----------|-------------|
| `institution_approval` | Admin approves an institution |
| `institution_rejection` | Admin rejects an institution |
| `password_reset` | Admin triggers a password reset |
| `teacher_invite:<uuid>` | Institution admin sends a teacher invite |

---

# App Features (Offline · Push Alerts · Dark Mode · Study Groups · Privacy · Insights)

All endpoints below require a Bearer token unless noted. Standard response shape applies.

## Settings — Dark Mode & Privacy

### GET `/users/me/settings`
```json
{ "theme": "auto", "profile_private": true, "recruiter_visible": false }
```
`theme` ∈ `auto | light | dark`. Profiles are **private by default**. Setting `profile_private=false` makes the public profile visible to anyone with access to its link. `recruiter_visible=true` opts the user into recruiter discovery and also makes the public profile visible.

### PATCH `/users/me/settings`
Body (all fields optional):
```json
{ "theme": "dark", "profile_private": false, "recruiter_visible": true }
```
Returns the updated settings. `400 BAD_REQUEST` for an invalid `theme`.

> Privacy enforcement: `GET /users/{userId}/profile` returns `403 PROFILE_PRIVATE` unless the viewer is the owner, a follower, or the target has `profile_private=false` or `recruiter_visible=true`.

## Push Alerts — Notification Preferences

### GET `/users/me/notification-preferences`
```json
{
  "push_rank_changes": true,
  "push_weekly_digest": true,
  "push_streak_nudge": true,
  "push_study_group": true,
  "push_assignments": true,
  "push_championships": true,
  "quiet_hours_enabled": false,
  "quiet_from_minute": 1350,
  "quiet_until_minute": 420,
  "quiet_utc_offset_minutes": 330,
  "email_weekly_insights": true
}
```
Missing row ⇒ all categories enabled by default.

### PATCH `/users/me/notification-preferences`
Body: any subset of the keys above. Returns the merged preferences. Quiet-hour
times are minutes after local midnight (0–1439); the UTC offset is minutes
east of UTC (-720 to 840). Start and end must differ when quiet hours are on.
The app refreshes the offset when opened in a new time zone.

Push alerts are delivered via FCM (existing `/users/me/devices` registration) and also stored as in-app notifications. Cron-driven categories:
- **Rank changes** — daily; fires when global rank improves.
- **Streak nudges** — daily evening; fires if an active streak hasn't been continued today.
- **Weekly digest** — Mondays; weekly recap push.

Quiet hours suppress mobile push delivery between the chosen times, including
overnight windows. In-app notifications are still stored. The championships
preference applies to notifications of kind `championship`; no championship
publisher is currently scheduled.

## Score Insights

### GET `/users/me/insights/weekly`
```json
{
  "week_start": "2026-06-05T00:00:00Z",
  "week_end": "2026-06-12T00:00:00Z",
  "points_this_week": 420,
  "points_last_week": 300,
  "points_delta_pct": 40,
  "quizzes_this_week": 7,
  "avg_score_this_week": 78.5,
  "current_streak": 5,
  "domain": "Software",
  "domain_rank": 12,
  "suggestion": "Strong week! Keep the streak alive and aim to climb your domain leaderboard."
}
```
The same breakdown is emailed weekly to users with `email_weekly_insights=true`.

### GET `/users/me/insights/breakdown`
Lifetime Qwish Score plus question-weighted domain/subdomain performance.

**Qwish Score** is a skill rating on 100–900. Each learner has an ability estimate θ and uncertainty σ; each question has a difficulty on the same scale. Every first-time answer moves θ by how surprising it was (a correct answer on a hard question moves it a lot, on an easy one barely), and shrinks σ. The published value is the conservative `θ − 2σ`, so it rises gradually as evidence accumulates (a new learner starts near 200–300) and step sizes shrink with experience. Idle time widens σ slightly. Repeat answers, streaks, speed and activity do not affect it; those feed points/XP. Learners with no answers show 100.

`components` are lifetime diagnostic fractions (0–1) for accuracy, difficulty, consistency, speed and activity; they no longer feed `qwish_score`. Each domain's `avg_score` is question-weighted accuracy (0–100); `low_sample` is true when fewer than 10 questions have been answered.
```json
{
  "qwish_score": 737.1,
  "components": {
    "accuracy": 0.86, "difficulty": 0.61,
    "consistency": 0.6, "speed": 0.74, "activity": 0.4
  },
  "domains": [
    {
      "slug": "quantitative", "label": "Quantitative",
      "avg_score": 78.0, "questions": 142, "attempts": 14, "low_sample": false,
      "subdomains": [
        { "slug": "quant_percentages", "label": "Percentages", "avg_score": 84.0, "questions": 40, "attempts": 4, "low_sample": false },
        { "slug": "quant_geometry", "label": "Geometry", "avg_score": 61.0, "questions": 4, "attempts": 1, "low_sample": true }
      ]
    }
  ]
}
```

### GET `/users/me/insights/trend?range=4w|12w|all`
The learner's `qwish_score` as it stood at the end of each bucket, for the insights chart. `4w` → 4 weekly buckets, `12w` → 12 weekly, `all` → 12 monthly. Quiet buckets repeat the latest earlier score, and buckets before the first completed attempt show 100. The last bucket equals the current `qwish_score`.
```json
[
  { "label": "5/12", "value": 724.8 },
  { "label": "5/19", "value": 755.6 }
]
```

## Offline Mode

### GET `/offline/pack?since=<version>`
Returns the authenticated user's saved practice quizzes (`type=knowledge_check`,
published, and visible to the user) **including correct answers** so grading
happens on-device. Practice is non-competitive (no points, no leaderboard).
```json
{
  "version": "2026-06-10T11:02:33.21Z",
  "count": 24,
  "quizzes": [
    {
      "id": "uuid", "title": "Arithmetic Basics", "type": "knowledge_check",
      "question_count": 10, "updated_at": "2026-06-10T11:02:33Z",
      "questions": [
        { "id": "uuid", "position": 1, "type": "mcq", "prompt": "2+2?",
          "options": [...], "correct_answer": [...], "time_limit_seconds": 30, "clues": [...] }
      ]
    }
  ],
  "changed": true
}
```
Pass the last `version` as `?since=`; if unchanged the response has `changed=false` and an empty `quizzes` array (keep your cache).

### POST `/offline/sync`
Uploads practice sessions completed offline. Idempotent on `id` (client-generated UUID). Max 200 per batch.
```json
{
  "results": [
    {
      "id": "client-uuid", "quiz_id": "uuid",
      "total_questions": 10, "correct_count": 8, "score_pct": 80,
      "answers": [ ... ], "completed_at": "2026-06-12T09:00:00Z"
    }
  ]
}
```
Response: `{ "received": 1, "stored": 1 }` (`stored` counts only newly-persisted, not re-syncs).

## Study Groups (Private Leagues) & Follows

### POST `/study-groups`
Body: `{ "name": "Batch 2026", "description": "optional" }` → creates a group, caller becomes owner & first member. Returns the group with a unique `invite_code`.

### GET `/study-groups`
Lists groups the caller belongs to (each with `member_count` and the caller's `role`).

### GET `/study-groups/{groupId}`
Group detail. `404` if the caller isn't a member.

### POST `/study-groups/join`
Body: `{ "invite_code": "ABC12XYZ" }` → joins the group. Returns the group. `404` if code invalid.

### POST `/study-groups/{groupId}/leave`
Leaves the group. `403 OWNER_CANNOT_LEAVE` for the owner (archive instead).

### DELETE `/study-groups/{groupId}`
Archives the group (owner only, `403` otherwise).

### GET `/study-groups/{groupId}/leaderboard`
Members ranked by total points (private league). `403` if not a member.
```json
[ { "rank": 1, "user_id": "uuid", "display_name": "Asha", "role": "owner",
    "total_points": 5200, "current_streak": 9, "joined_at": "..." } ]
```

### Follows (batchmates)
- `POST /users/{userId}/follow` — follow a user (`400` self, `404` unknown). `204`.
- `DELETE /users/{userId}/follow` — unfollow. `204`.
- `GET /users/me/following` — users you follow.
- `GET /users/me/followers` — users following you, each with `is_following` (follow-back flag).

## GET `/auth/teacher-invite?token=<token>` (public)
Validates a teacher invite link and returns details for the signup page:
```json
{ "email": "teacher@school.edu", "name": "Anil Mehta",
  "institution_name": "Springfield High", "status": "pending",
  "expires_at": "2026-06-19T10:00:00Z" }
```
`status` ∈ `pending | accepted | expired | revoked`. `404` for unknown token.

## Accepting a teacher invite
The invited teacher authenticates via OTP (`/auth/send-otp` → `/auth/verify-otp`) **with the invited email**, then calls `POST /auth/create-profile` with:
```json
{ "full_name": "Anil Mehta", "invite_token": "<token from the email link>" }
```
The account is created with `role=teacher` linked to the inviting institution, and the invite is marked `accepted`.
Errors: `404 NOT_FOUND` (bad token) · `410 INVITE_EXPIRED|INVITE_ACCEPTED|INVITE_REVOKED` · `403 INVITE_EMAIL_MISMATCH` (session email ≠ invited email).
`invite_token` takes precedence over `referral_code` when both are sent.

---

# Analytics

One metrics engine (`internal/domain/metrics`) serves three roles. The endpoints
differ only in **scope** — which rows the caller is allowed to be answered on.

| Role | Base | Scope |
|---|---|---|
| Super admin | `/admin` | platform-wide, optionally narrowed by `institution_id` |
| Institution admin | `/institution` | the institution on the caller's token, always |
| Teacher | `/teacher` | `classes` (their groups' students) or `quizzes` (quizzes they authored) |

**Scope ids are never read from the query string for institution admins or
teachers.** The institution comes from the token, the teacher comes from the
token, and the teacher's `scope` parameter selects only the *kind*. There is no
request by which one institution admin reads another institution, or one teacher
reads another teacher.

## The scope object

Every analytics response carries:

```json
"scope": { "requested": "teacher_classes", "effective": "institution",
           "reason": "no classes assigned — showing the whole institution" }
```

`reason` is `""` unless the server substituted a different scope. **When it is
non-empty the client must render it.** A teacher assigned to no group is
answered institution-wide (PRD §5.4); without that sentence on screen they read
institution numbers as their own class's.

`requested` and `effective` are `institution`, `teacher_classes`,
`teacher_quizzes`, or `""` for an unscoped super-admin request.

## Two contract rules

1. **Never sum or average a `rate` or `distinct` metric across buckets.** Each
   metric carries a `kind` (`additive` | `rate` | `distinct`). Window figures for
   the latter two come from `totals`, which the server recomputes over the whole
   window. Averaging an average is wrong when bucket volumes differ, and summing
   daily distinct users double-counts anyone active on two days.
2. **Never swallow `dropped`.** A metric the active scope cannot answer is
   excluded and reported with a reason rather than answered at a wider scope.

## GET `/{admin,institution,teacher}/metrics/catalog`

The metric vocabulary, **filtered to what the caller's scope can answer**. Build
every picker from this, never from a hardcoded client list.

```json
{ "metrics": [ { "id": "attempts_completed", "label": "Attempts completed",
                 "group": "Engagement", "unit": "count", "kind": "additive",
                 "scopes": ["institution","teacher_classes","teacher_quizzes"],
                 "hint": "Attempts reaching status=completed…" } ],
  "granularities": ["hour","day","week","month","quarter"],
  "timezone": "Asia/Kolkata",
  "scope": { "requested": "institution", "effective": "institution", "reason": "" } }
```

`scopes` lists the kinds a metric can answer. Cached `private, max-age=300`.

Teacher variant takes `?scope=classes|quizzes` (default `classes`) — the
answerable set changes with it, so refetch the catalog when the scope changes.

## GET `/{admin,institution,teacher}/metrics`

| Param | Default | Notes |
|---|---|---|
| `from` | `to - 29d` | `YYYY-MM-DD`, inclusive |
| `to` | today (IST) | `YYYY-MM-DD`, inclusive |
| `granularity` | `day` | `hour` \| `day` \| `week` \| `month` \| `quarter` |
| `metrics` | all | csv of metric ids |
| `compare` | absent | `previous` \| `year` |
| `scope` | `classes` | **teacher only**; `classes` \| `quizzes` |
| `institution_id` | absent | **super admin only** |

Window caps, by granularity: hour 7d, day 92d, week 366d, month/quarter 1096d.
Exceeding one is a `400` with a message the UI can show, not a silent coarsening.

```json
{ "from": "2026-07-01", "to": "2026-07-30", "granularity": "day",
  "timezone": "Asia/Kolkata", "institution_id": null,
  "scope": { "requested": "institution", "effective": "institution", "reason": "" },
  "series": [ { "bucket": "2026-07-01T00:00:00Z", "attempts_completed": 184 } ],
  "totals": { "attempts_completed": 5210, "avg_score": 68.4 },
  "previous": { "attempts_completed": 4980 },
  "previous_series": [], "previous_from": "2026-06-01", "previous_to": "2026-06-30",
  "dropped": [ { "id": "moderation_actions", "reason": "not institution-scopable" } ] }
```

`previous*` appear only with `compare`. `dropped` appears only when non-empty.

Drop reasons are phrased for the role: `not institution-scopable`,
`not available when scoped to your classes`,
`not available when scoped to your quizzes`, or
`depends on <id>, which is <reason>` for a derived metric whose dependency went.

## GET `/{admin,institution,teacher}/distributions`

Snapshot shapes — "what is the mix right now", which is why they are not a time
series: `score_histogram`, `difficulty_bands`, `streak_bands`, `role_mix`,
`institution_type_mix`, `quiz_status_funnel`.

Under a scope, shapes that cannot be expressed are **omitted** and listed in
`dropped`. `institution_type_mix` drops under every scope; `streak_bands` and
`role_mix` drop under `teacher_quizzes`; `difficulty_bands` and
`quiz_status_funnel` drop under `teacher_classes`.

## GET `/{admin,institution,teacher}/points-liability`

A forward schedule of points about to expire, grouped by month.

```json
{ "as_of": "2026-07-30T12:00:00Z", "timezone": "Asia/Kolkata",
  "total": 412000, "months": [ { "month": "2026-08", "points": 96000 } ],
  "scope": { "requested": "institution", "effective": "institution", "reason": "" } }
```

`?scope=quizzes` returns **`400`**: `points_ledger` has no quiz linkage, and an
empty schedule would read as "nothing is expiring" rather than "not answerable".

## Dashboard layouts

```
GET    /{admin,institution,teacher}/dashboard-layouts
POST   /{admin,institution,teacher}/dashboard-layouts
PUT    /{admin,institution,teacher}/dashboard-layouts/order
PATCH  /{admin,institution,teacher}/dashboard-layouts/{layoutId}
DELETE /{admin,institution,teacher}/dashboard-layouts/{layoutId}
```

Private to the calling account. Super-admin layouts live in
`admin_dashboard_layouts` (owner `admin_accounts.id`); institution-admin and
teacher layouts live in `user_dashboard_layouts` (owner `users.id`). One default
per owner, enforced by a partial unique index.

`layout` is opaque to the server — validated only as a JSON object under 256 KiB,
because widget shapes change every frontend release. A duplicate name is `409`.

## Behaviour change

An unknown `institution_id` on `/admin/metrics` now returns **`400`** rather than
`404`; the parameter is a filter, and a filter naming nothing is a bad request.

---

# Student Management

Implements `docs/superpowers/specs/2026-08-01-student-management-design.md`.

A student record has two halves. The **person** is a `users` row. The
**enrollment** is that person's relationship with one institution, in
`enrollments`. Institution-owned academic fields live on the enrollment and
have no student-facing write path — that table boundary *is* the permission
model, not a per-field permission flag.

A student may hold **live enrollments at up to 2 institutes** (one per
institute). `users.institution_id` is the student's **active** institute — the
one the app is showing — never the definition of membership: every institute
query decides membership from `enrollments`. A student with no enrollment is
institution-less and entirely valid. (Spec: `docs/superpowers/specs/2026-10-03-learning-layer-design.md`.)

Every join lands the student in a **class**. Claim codes, the admissions queue
and student referral codes are retired.

## Student

### POST `/students/join/preview`
Resolve a class code without consuming it.
```json
{ "code": "K7M2QX9P" }
```
Returns `kind` (always `class`), `target_id`, `institution_id`,
`institution_name`, `class_name`, `already_joined`, and `route` — how the
student would be admitted, strongest proof first:

| `route` | Meaning |
|---|---|
| `invite` | A pending invite exists for one of the student's verified emails |
| `domain` | A verified email is on one of the institute's verified domains |
| `code` | The class has joining switched on |

With none of these, the preview fails with `403 JOIN_CLOSED`.

### POST `/students/join/confirm`
```json
{ "code": "K7M2QX9P", "target_id": "uuid-from-preview" }
```
Any `kind` sent by older clients is ignored. Returns `destination`,
`enrollment` and `status: "joined"`. Reuses the live enrollment at that
institute, or creates one (`join_route` recorded) if the student is under the
limit. Consumes a matching invite. Sets the joined institute as active.

Errors: `400 JOIN_CODE_INVALID`, `403 JOIN_CLOSED`, `409 INSTITUTE_LIMIT`
(already at 2 institutes), `409 JOIN_CHANGED`, `403 JOIN_SUSPENDED`,
`403 JOIN_ROLE`.

### POST `/students/join-class`
Legacy one-shot `{ "invite_code": "..." }`: preview + confirm in one call.
Returns the enrollment. Same errors.

### GET `/students/invites`
Pending invites addressed to any of the student's verified emails:
`[{ "id", "institution_name", "class_name" }]`.

### POST `/students/invites/{inviteId}/accept`
Joins the invited class through the normal join path (same limit and errors).
An institute-level invite (created only by migration) creates the enrollment
without a class.

### GET `/users/me/enrollments`
Every live enrollment, active first. Each item is the enrollment plus
`institution_name`, `class_name` and `active`.

### PUT `/users/me/active-institution`
`{ "institution_id": "uuid" }` → `200 {message}`. Only an `active` enrollment can
be the active institute (a suspended one is paused). `404` otherwise.

### POST `/users/me/enrollments/{enrollmentId}/leave`
Ends the student's own enrollment as `left` (`ended_by: student`), removes them
from that institute's live classes (ended classes stay as history), and moves
the active pointer to the other live institute if any. `200 {message}`.

### GET `/users/me/enrollment`
The enrollment at the **active** institute, or `null`. Kept for older clients.

### Secondary emails
| Method | Path | Notes |
|---|---|---|
| GET | `/users/me/emails` | `[{ id, email, verified, verified_at }]` (login email not included) |
| POST | `/users/me/emails` | `{email}` → `201`; sends a 6-digit code (10 min). `400 EMAIL_INVALID` (also disposable), `400 EMAIL_IS_LOGIN`, `409 EMAIL_TAKEN` |
| POST | `/users/me/emails/{id}/verify` | `{code}`. `400 CODE_INVALID`, `429 CODE_LOCKED` after 5 misses |
| POST | `/users/me/emails/{id}/resend` | New code for an unverified address |
| DELETE | `/users/me/emails/{id}` | `200 {message}`. Memberships it earned are kept |

A verified address is unique across all accounts.

### PATCH `/users/me`
Accepts `display_name` and `full_name` (1–120 chars). The student owns one
name; enrollment copies follow it.

### PATCH `/auth/referral-code`
Staff only. Students get an error telling them to join with a class code.

Personal fields (date of birth, gender, phone, address, guardian, highest
qualification) were retired in migration 085; older clients that still send
them are ignored.

### Profile entries (the rest of the CV)
```
GET    /users/me/profile-entries?kind=experience
POST   /users/me/profile-entries
PATCH  /users/me/profile-entries/{entryId}
DELETE /users/me/profile-entries/{entryId}
```
`kind` is one of `experience`, `certification`, `achievement`, `course`.
Education and skills keep their own existing endpoints.

#### Portfolio fields (migration 081)
Create/PATCH also accept, all optional (omitted on PATCH = unchanged, so older
clients keep working):
```
subtype        project | internship | hackathon | certification | achievement
details        {string: string} — keys per subtype, closed set:
               project:       problem, approach, contribution, outcome, mentor, team_type(individual|team)
               internship:    responsibilities, learnings, supervisor, work_mode(onsite|remote|hybrid)
               hackathon:     problem, team, contribution, level(college|state|national|international),
                              result(participant|finalist|winner)
               certification: credential_id, expiry_date(YYYY-MM-DD)
               achievement:   category, role
skills         string[] (≤20)        links    http(s) URL[] (≤5, never fetched)
ongoing        bool (no end_date)    academic_year "2025-26"    semester 1–12 (0 clears)
```
A `subtype` sets `kind` (project/internship → experience, hackathon →
achievement). `end_date` before `start_date` is rejected.

List rows gain `subtype, details, skills, links, ongoing, academic_year,
semester, status, pinned, revision` and `review` (latest decision on the current
revision: `{revision, decision, comment, created_at}`).

`status`: `draft → submitted → reviewed | changes_requested`. **Any PATCH returns
the entry to `draft`** — a review never carries over to edited content.

```
POST /users/me/profile-entries/{entryId}/submit   → {status, revision}
GET  /users/me/profile-entries/{entryId}/reviews  → [{revision, decision, comment, reviewer, created_at}]
PUT  /users/me/profile-entries/{entryId}/pin      {pinned: bool}
```
Submit snapshots an immutable revision (with the student's institution at that
moment). Idempotent when already `submitted`/`reviewed`. Errors:
`400 INCOMPLETE_ENTRY` (message lists missing fields), `409 NO_CHANGES`
(`changes_requested` and not yet edited). Pin errors: `409 PIN_LIMIT` (3 max).

## Teacher

Teachers manage rosters, never identities. Every call is bounded to classes the
teacher is assigned to via `group_teachers`; acting outside that returns
`403 NOT_IN_YOUR_CLASS`.

```
POST   /teacher/classes/{classId}/students        {user_id}
DELETE /teacher/classes/{classId}/students/{userId}
```
The student must already hold a live enrollment at the same institution, so a
teacher cannot pull in an outsider.

### Class joining and invites
```
PATCH  /teacher/classes/{classId}/joining   {joining_enabled}   → {joining_enabled}
POST   /teacher/classes/{classId}/invites   {emails: [...]}     → 201 {created, rejected}
GET    /teacher/classes/{classId}/invites                       → pending invites
DELETE /teacher/invites/{inviteId}                              → 200 {message}
```
Institution admins have the same under `/institution/groups/{groupId}/joining`,
`/institution/groups/{groupId}/invites` and `DELETE /institution/invites/{inviteId}`.

- With joining off, a class code only admits students holding an invite or a
  verified email on one of the institute's verified domains.
- Invites accept 1–500 addresses. Each is judged alone: `rejected[].reason` is
  `invalid`, `not_institute_domain` (only verified institute domains can be
  invited) or `already_member`. Invited addresses get an email.
- `GET /teacher/classes/{classId}` and `GET /institution/groups/{groupId}` return
  `joining_enabled`. Teacher roster rows return `join_route`
  (`domain` | `invite` | `code`).

### Institute email domains (super-admin)
```
GET    /admin/institutions/{institutionId}/domains
POST   /admin/institutions/{institutionId}/domains          {domain}  → 201
DELETE /admin/institutions/{institutionId}/domains/{domain}            → 200 {message}
```
Adding a domain records it as verified — add only after confirming ownership.
`400 DOMAIN_INVALID`, `400 DOMAIN_FREE_MAIL` (free-mail and disposable providers),
`409 DOMAIN_TAKEN` (verified for another institute).

### Auto-end (cron)
`POST /internal/cron/end-inactive-enrollments` (nightly): an enrollment with no
live class at its institute for 90 days ends as `left` (`ended_by: system`); the
student is warned 7 days before. Joining a class clears the warning.

### GET `/teacher/students` — shape change
Now built from enrollments rather than `users`. Rows gain `enrollment_id`,
`grade`, `section` and `join_route`. **Graduated and transferred students no
longer appear**, and `average_score` counts only attempts on or after
`joined_at`, so a transferred-in student does not carry their previous school's
scores into this institution's view.

`GET /teacher/students/{userId}` and `GET /teacher/classes/{classId}` gain the
same fields.

Edit requests were retired in migration 085.

## Institution

Roster create, update and CSV import were retired in migration 085. Students
join through class codes and institute-email invites (see **Teacher → Class
joining and invites**).

### GET `/institution/students`
Now reads from `enrollments`, so graduated students drop off. Each row gains
`enrollment_id`, `grade` and `section`. `average_score` counts
only attempts on or after `joined_at`, so a transferred-in student's previous
school's results never land in this institution's numbers.

Filters: `search`, `status`, `group_id`, `sort`, `page`, `limit`.

### PATCH `/institution/enrollments/{enrollmentId}/status`
```json
{ "status": "graduated", "reason": "" }
```
`status` ∈ `active`, `suspended`, `graduated`, `transferred`. All four are the
same state change, so they are one endpoint.

- `active` / `suspended` mirror onto `users.status`, which is what blocks login.
- `graduated` / `transferred` set `ended_at` and clear `users.institution_id`.
  The student keeps their account, points, streak and CV; the institution keeps
  its own attempt and report data but loses roster access.

`PATCH /institution/students/{userId}/status` still exists with its original
`{action: suspend|reactivate}` body and now routes through the same service.

### PATCH `/institution/enrollments/{enrollmentId}/status`
```json
{ "status": "graduated", "reason": "" }
```
`status` ∈ `active`, `suspended`, `graduated`, `transferred`. All four are the
same state change, so they are one endpoint.

- `active` / `suspended` mirror onto `users.status`, which is what blocks login.
- `graduated` / `transferred` set `ended_at` and clear `users.institution_id`.
  The student keeps their account, points, streak and CV; the institution keeps
  its own attempt and report data but loses roster access.

`PATCH /institution/students/{userId}/status` still exists with its original
`{action: suspend|reactivate}` body and now routes through the same service.

### PATCH `/institution/enrollments/{enrollmentId}/status`
Now audited as `set_enrollment_status`, with the `reason` recorded.

### POST `/institution/enrollments/bulk-status`
```json
{ "enrollment_ids": ["uuid", "uuid"], "status": "graduated", "reason": "Class of 2026" }
```
Applies one status to up to 5000 enrollments, as with the single endpoint.
Each change is independent: one that can't apply is reported and skipped,
and the rest still apply.
```json
{ "updated": 62, "skipped": [{ "enrollment_id": "uuid", "name": "Rohan Pawar", "reason": "not on your roster" }] }
```

## Super Admin

```
GET    /admin/students/search?q=<email|roll|name>   (super_admin + moderator)
POST   /admin/students/merge                        (super_admin)
DELETE /admin/students/{userId}/purge               (super_admin)
```

Student search uses a versioned trigram Bloom prefilter for definite misses and
PostgreSQL trigram indexes for authoritative substring matches.

### Approximate trends

`GET /admin/analytics/trends?hours=24` returns recent attempt volume,
HyperLogLog-estimated unique learners, and Count-Min Sketch/Space-Saving top
domains. `hours` accepts `1` through `2160` (90 days).

### Cursor pagination

`GET /users/me/attempts` continues to accept `page`, but responses now include
`meta.next_cursor`. Pass it as `cursor` to use stable keyset pagination instead
of increasingly expensive deep offsets.

**Merge** folds `merge_user_id` into `keep_user_id`: points sum, attempts,
points ledger and CV entries repoint, enrollments move where they would not
violate the one-live-enrollment rule, and the loser is soft-deleted. Logged to
`audit_log`.

**Purge** is permanent erasure beyond the soft `deleted_at`. Enrollments are
detached (`user_id` nulled, marked `transferred`) rather than deleted, so the
institution keeps its historical roster count.

## Error codes

| Code | Status | Condition |
|---|---|---|
| `JOIN_CODE_INVALID` | 400 | No class with that code |
| `JOIN_CLOSED` | 403 | Class is invite-only and the student has no invite or institute email |
| `INSTITUTE_LIMIT` | 409 | Student already belongs to 2 institutes |
| `LEAVE_SUSPENDED` | 409 | Leaving a suspended enrollment |
| `NOT_IN_YOUR_CLASS` | 403 | Teacher acting outside their class scope |
| `RATE_LIMITED` | 429 | Too many requests; `Retry-After` gives seconds to wait |
| `PUBLISH_BLOCKED` | 422 | Curriculum publish refused; `message` lists every issue, `;`-separated |
| `CHANGE_NOTE_REQUIRED` | 422 | The curriculum requires `change_note` on every save |
| `REVISION_CONFLICT` | 409 | Curriculum draft changed since it was loaded; re-fetch the version to compare |
| `EMAIL_FAILED` | 502 | An invitation email couldn't be sent; nothing else changed |

### Class joining and admissions
Retired by migration 084. Joining is by class code only (see **Student**); open
requests were approved or declined during the migration and students were notified.

### Misconception evidence integrity (migration 075)

Apply `075_misconception_evidence_integrity.sql` before deploying this API version.

- Attempts freeze stable option IDs and concept/misconception mappings alongside the delivered question version. Grading, resumed options, and clues use that snapshot. Label-only answers resolve to the delivered option ID when an exact match exists.
- `learning_evidence.is_correct` represents answer correctness; `timed_out` records the timing gate independently. Game scoring is unchanged. Learning summaries and misconception detection exclude timed-out observations.
- `learning_evidence_misconceptions` stores every diagnostic tag while retaining one base observation per response/concept. New evidence leaves the legacy `misconception_id` column NULL; diagnostic queries use the junction table.
- `GET /teacher/quizzes/{quizId}/response-insights` accepts `window_days` (1–365, default 30). The selected quiz anchors the student/concept pairs; matching evidence spans the authenticated teacher's quizzes within the same institute and requested class scope. Correct-answer counts are calculated per concept before joining diagnostic tags, preventing multi-tag answers from multiplying evidence.
- Responses retain the existing fields and add `automatic_status`, `window_days`, `evidence_question_ids`, `review_status`, `review_reason`, `reviewed_at`, and `reviewed_by`. A teacher review takes precedence in `status` (`confirmed` or `dismissed`); the automatic assessment remains available separately. Reviews remain effective until the teacher changes them. An expired evidence window returns no stale automatic diagnostic row.
- `GET /teacher/questions/{questionId}/learning-map` adds `option_mappings: [{option_id, misconception_id}]`. Send that array as `options` when replacing the map. Correct options, inactive misconceptions, and single-option mappings for ordered-answer questions are rejected; failed replacements leave the previous map intact.

Historical repair is limited to information that was actually recorded: the migration separates recoverable timeout/correctness data and preserves existing diagnostic tags. It cannot recover tags previously discarded by the old uniqueness constraint or reconstruct past mapping edits. Pre-migration attempts retain their question/option snapshots but receive an empty diagnostic mapping; newly started attempts capture the full context. Historical game scores are not rewritten. Coordinate the migration and API rollout so old API processes do not continue writing legacy-only diagnostic tags after migration.

## Membership consistency (migration 076)

Deploy migration 076 with the backend before deploying the updated dashboards.

- Student enrollment is authoritative for institute rosters and account institute labels.
- `GET /api/v1/admin/institutions/{institutionId}/students` returns the same paginated enrollment rows and metadata as `GET /api/v1/institution/students`. It uses the existing platform-admin authentication gate. A roster row can have a null account `id` until claimed.
- Default rosters include active, suspended and pending-claim entries. Explicit graduated/transferred filters return historical rows. Deleted accounts and nonstudents are excluded.
- Pending admission requests remain in Admissions; they become roster entries only after admission completes.
- Migration 076 removes existing super-admin memberships, cancels their open admissions and preserves ended enrollment history. Constraints prevent super-admin institute assignment and class membership; only students may hold live enrollments.
- Legacy affiliated student accounts missing enrollments are backfilled (or linked to a unique matching unclaimed roster row). Historical enrollments and open admissions are not automatically reactivated/approved. Existing live enrollments reconcile stale account institute pointers.
- Both student screens refresh every 30 seconds while visible and on tab focus, reject stale network responses, and offer manual refresh. The platform roster supports pagination beyond 50 students.
- NumPie recognizes platform roles explicitly; unknown roles no longer become students and nonstudents cannot enter the join flow.

## Institute dashboard redesign (migrations 077–078)

Deploy `077_curriculum_structure.sql` and `078_institute_operations.sql` with
this API version. Both are additive: new tables, and nullable or defaulted
columns. Section 10 documents the routes that changed; this section covers
the new ones and the cross-cutting headers.

### Request IDs and rate-limit headers

- Every response carries `X-Request-Id` (e.g. `7f3a-19c2`). Show it on failures so support can find the request.
- Endpoints limited per email (`/auth/send-otp`, `/auth/verify-otp`, `/auth/passkey/login/begin`) return `X-RateLimit-Remaining`: requests left for that email in the current window. A sign-in form can say "2 tries left" before the pause.
- A `429 RATE_LIMITED` response carries `Retry-After` in seconds.
- CORS exposes `X-Request-Id`, `Retry-After`, `X-RateLimit-Remaining`, `X-Import-Skipped` and `Content-Disposition` to browser scripts.

### GET `/users/me/sign-ins`
The caller's 20 most recent successful sign-ins, for a "was that me?" list.
```json
[{ "method": "passkey", "ip": "203.0.113.4", "user_agent": "Mozilla/5.0 …", "at": "2026-09-26T03:44:00Z" }]
```
`method` is `email_code` or `passkey`. Recorded from migration 078 onwards.

### Admissions
Retired by migration 084 (`/institution/admissions/*` removed).

### Curriculum

#### GET `/institution/curricula`
New filters: `subject` (case-insensitive), `grade`, `status` (`draft`|`published`).
Each version adds:
- `updated_at`;
- `chapter_count` and `concept_count`;
- `assigned_classes: [{ group_id, name, academic_year_name }]`, the live assignments in academic years that cover today.

#### GET `/institution/curricula/facets`
`{ "subjects": ["Chemistry", "Physics"], "grades": ["11", "12"] }`, for filters that cover every page.

#### Structure and concept fields
`POST /institution/curricula`, `POST /institution/curricula/{id}/versions` and
`PUT /institution/curriculum-versions/{id}` accept, and
`GET /institution/curriculum-versions/{id}` returns:

```json
{
  "settings": {
    "board": "State board (HSC)", "stream": "Science", "medium": "English",
    "description": "Revised for the 2027 board pattern.",
    "grouping": "terms", "group_count": 2,
    "code_pattern": "{SUBJ}{GRADE}-{CH}{NN}",
    "outcome_framework": "blooms",
    "difficulty_scale": "three",
    "required_fields": ["learning_outcome", "cognitive_level"],
    "weightage_enabled": true,
    "teaching_plan": true,
    "min_students_reported": 10,
    "flag_prerequisite_order": true,
    "require_change_note": false
  },
  "chapters": [{
    "title": "Motion in a straight line",
    "group": 1, "weightage": 12, "planned_periods": 18, "week_from": 3, "week_to": 7, "optional": false,
    "concepts": [{
      "code": "PHY11-K03", "title": "Relative velocity in one dimension", "learning_outcome": "…",
      "cognitive_level": "apply", "difficulty": "core", "teaching_periods": 2, "weight": 15,
      "prerequisites": ["PHY11-K01", "PHY11-K02"],
      "tags": ["frame of reference"], "misconceptions": ["Adds the two speeds without considering direction"],
      "textbook_ref": "", "optional": false
    }]
  }],
  "change_note": "Moved rotation to Term 2",
  "copied_from_version_id": "uuid"
}
```
All of it is optional; existing clients that send only `label`, `subject`,
`grade` and chapter titles and concepts keep working.

Validation on save:
- **Settings values:** `grouping` is `terms` or `units`, with `group_count` 1–12. `outcome_framework` is `blooms` (remember … create) or `three_level` (recall, apply, reason). `difficulty_scale` is `three` (foundational, core, advanced) or `five` (1–5). `min_students_reported` is 1–500 and defaults to 10.
- **Concepts:** `cognitive_level` and `difficulty` must come from the version's chosen framework and scale. `prerequisites` must be codes in the same version, can't include the concept itself, and can't form a cycle.
- **Limits:** tags ≤ 10 × 40 characters, misconceptions ≤ 10 × 300, prerequisites ≤ 20.
- **Change notes:** when the stored version has `require_change_note`, a save without `change_note` fails with `422 CHANGE_NOTE_REQUIRED`.

On read:
- Each concept adds `mapped_questions_previous`: questions mapped to the same code in the curriculum's other editions. Those mappings carry over only while the code is unchanged.
- Versions add `copied_from_label`.

**Publishing** (`POST …/publish`) re-checks the whole version and refuses with
`422 PUBLISH_BLOCKED`, listing every issue in `message`, when:
- a chapter has no concepts;
- a field named in `required_fields` is missing on any concept;
- weightage is enabled and a chapter has none, or the chapters don't total 100%.

#### Revision history
```
GET /institution/curriculum-versions/{id}/history
  → [{ "revision": 5, "action": "saved", "note": "…", "actor_name": "…", "created_at": "…" }]
GET /institution/curriculum-versions/{id}/revisions/{revision}
  → the version's content as saved at that revision (same shape as the save body)
```
`action` is `created`, `saved` or `published`. Revisions are recorded from
migration 077 onwards. Use two snapshots for "Compare with rN". On
`409 REVISION_CONFLICT`, fetch the current version and compare it with the
local draft field by field before saving again.

#### Academic years
- `GET /institution/academic-years` rows add `stats: { assignments, classes_covered, active_classes }`.
- `PATCH /institution/academic-years/{yearId}` takes the same body as create (`name`, `starts_on`, `ends_on`) and is audited as `update_academic_year`.

### GET `/institution/learning-summary/scoped`
Learning evidence for one curriculum version, grouped by chapter, with coverage.

| Param | Description |
|---|---|
| `version_id` | The curriculum version. May be omitted when both of the next two are given — the class's live assignment for that year decides it |
| `group_id` | Only evidence from students in this class |
| `academic_year_id` | Only evidence recorded within the year's dates |

```json
{
  "version": { "id": "uuid", "label": "2026 edition", "subject": "Physics", "grade": "11" },
  "coverage": {
    "students": 58, "concepts": 64, "with_evidence": 41,
    "not_assessed": [{ "code": "PHY11-L05", "title": "Circular motion", "chapter": "Laws of motion" }]
  },
  "min_students": 10,
  "chapters": [{
    "title": "Kinematics",
    "concepts": [{ "concept_id": "uuid", "concept_code": "PHY11-K01", "concept_title": "Displacement vs distance",
                   "students_assessed": 56, "correct_evidence": 148, "error_evidence": 22,
                   "unknown_confidence": 6, "latest_evidence_at": "…" }]
  }]
}
```
- Every concept in the version appears, including ones with no evidence (all counts `0`).
- `min_students` comes from the version's settings: below it, a concept's evidence reads as "too little evidence", not as a low result.
- The unscoped `GET /institution/learning-summary` is unchanged.

### Find a student

Search matches only people connected to this institution: an enrollment in
any state (including unclaimed roster records). An
unconnected email returns nothing. That is deliberate: this endpoint must not
reveal whether an account exists elsewhere on Qwish.

#### GET `/institution/students/find?q=`
`q` is at least 2 characters and matches name or email.
```json
[{ "user_id": "uuid", "enrollment_id": "uuid", "name": "Mira Thakur",
   "email": "mira@x.in", "state": "active" }]
```
`state` is the enrollment status.

#### GET `/institution/students/explain?user_id=` or `?enrollment_id=`
```json
{
  "name": "Mira Thakur", "email": "mira@x.in",
  "user_id": "uuid", "enrollment_id": "uuid",
  "on_roster": true, "account_since": "2024-03-01T00:00:00Z",
  "chain": [
    { "key": "account", "label": "Account", "state": "Exists", "tone": "ok", "detail": "Verified email · student role" },
    { "key": "enrollment", "label": "Institute enrollment", "state": "Active", "tone": "ok", "detail": "" },
    { "key": "classes", "label": "Class membership", "state": "1 class", "tone": "ok", "detail": "Mathematics B" }
  ],
  "diagnosis": { "code": "on_roster", "headline": "On your roster", "detail": "Active enrollment in Mathematics B.", "actions": ["open_profile"] },
  "events": [{ "at": "2026-09-20T10:42:00Z", "what": "Joined the institute", "by": "Mira Thakur" }]
}
```
- `tone` is `ok`, `wait`, `none` or `fail`.
- `diagnosis.code` is one of: `on_roster`, `suspended`, `ended`.
- `actions` names what the admin can do: `open_profile`, `reactivate`.
- `404` when nothing connects the person to the institution.

### Action centre

#### GET `/institution/action-centre?filter=all|mine|unowned|stale&page=&limit=`
Everything waiting on the institution in one queue, oldest first.

The item types are:
- `teacher_verification`;
- `topic_request`: open for more than 5 days.

```json
{
  "items": [{
    "type": "teacher_verification", "id": "uuid", "title": "Priya Nair",
    "subtitle": "Joined 02 Oct · priya@school.edu", "waiting_since": "…", "link_id": "uuid",
    "owner_id": null, "owner_name": null, "assignable": true
  }],
  "queues": [{ "type": "teacher_verification", "count": 2, "oldest": "…" }],
  "counts": { "all": 58, "mine": 9, "unowned": 41, "stale": 3 },
  "page": 1, "limit": 20, "stale_after_days": 7, "updated_at": "…"
}
```
- `stale` means waiting more than `stale_after_days`.
- An approved transfer has `assignable: false` and `owner_name: "Student"`, because the next step is theirs.

#### PUT `/institution/action-centre/owner`
```json
{ "item_type": "teacher_verification", "item_id": "uuid", "owner_id": "uuid" }
```
- The owner must be an admin or teacher at the institution. `owner_id: null` clears the owner.
- The item must be one of the institution's open, assignable items, otherwise `404`.

### Migrations

| Migration | Adds |
|---|---|
| `077_curriculum_structure.sql` | `curriculum_versions.settings` (JSONB), `.copied_from_version_id`, `.updated_at`; `curriculum_chapters.details` and `curriculum_concepts.details` (JSONB); `curriculum_version_revisions` |
| `078_institute_operations.sql` | `promotion_batch_students.revert_outcome`; `users.verified_at`; `user_sign_ins`; `action_item_owners` |

## Teacher forms and identified polls (migration 082)

Phase 1 of `plans/teacher-forms-events-and-polls.md`. A poll is a one-question form; both are `activities`.
Responses are **identified** to organisers — show `identity_disclosure` before submit.

### Organiser — `/teacher/...` and `/institution/...` (same handlers)

Organisers are the author, plus institution admins for anything in their institution. Teachers may target
only classes they teach; `institution_wide` is institution-admin only. Other organisers get `404`.

| Method | Path | Notes |
|---|---|---|
| GET | `/activity-templates` | Editable presets (hackathon interest, event registration, workshop preferences, volunteer application, project proposal, post-event feedback, quick poll) |
| GET | `/activities?status=&kind=` | Up to 200, newest first |
| POST | `/activities` | Draft. Body below → `201 {id, revision}` |
| GET | `/activities/{id}` | `{activity, identity_disclosure, results?}` |
| PUT | `/activities/{id}` | Draft: full body + `revision`. Published/closed: only `{revision, title, description}` (audited); anything else → `409` (duplicate instead) |
| GET | `/activities/{id}/audience-estimate` | `{students, as_of}` |
| POST | `/activities/{id}/publish` | `{revision}`; freezes questions into a version. Retry-safe |
| POST | `/activities/{id}/close` · `/archive` | Idempotent |
| POST | `/activities/{id}/duplicate` | New draft from the published questions → `201 {id}` |
| POST | `/activities/{id}/remind` | Once per activity, open only, non-responders only; else `409` |
| GET | `/activities/{id}/responses?limit=&offset=` | Submitted only (never drafts/withdrawn) + `summary {submitted, eligible_now, eligible_as_of, reach_estimate, reach_estimated_at}` |
| GET | `/activities/{id}/responses.csv` | Formula-escaped; access rechecked per request, nothing cached |

Body: `{kind: form|poll, title, description, questions[], group_ids[], institution_wide, opens_at, closes_at, allow_edit, result_visibility: after_vote|after_close|organisers, revision}`.
Stale `revision` → `409 STALE_REVISION`.

Question: `{id, type, label, help?, required?, options?[{id,label}], min_select?, max_select?, min?, max?, max_length?}`.
Types: `short_text` (≤300), `long_text` (≤5000), `single_choice`, `multiple_choice`, `number`, `date` (`YYYY-MM-DD`), `acknowledgement`.
Ids match `[a-z0-9_-]{1,40}` and are stable. A poll has exactly one choice question.

`state` (server time): `draft | scheduled | open | closed | archived`.

### Student — `/activities` (role `student`)

| Method | Path | Notes |
|---|---|---|
| GET | `/activities?filter=forms\|polls\|mine` | Items addressed to the student, plus ones they responded to; `my_status` |
| GET | `/activities/{id}` | `{activity (eligible), my_response, identity_disclosure, results}`. `404` outside the audience unless they hold a submitted response |
| PUT | `/activities/{id}/response/draft` | Forms only; `{answers}`; partial allowed |
| POST | `/activities/{id}/response` | `{answers: {questionId: value}}`. Identical retry → same receipt. Changing a submission needs `allow_edit`, else `409 ALREADY_SUBMITTED`. Not open → `409 NOT_OPEN` |
| DELETE | `/activities/{id}/response` | Withdraw; `allow_edit` and open only |

Poll `results` (`{respondents, questions[{question_id, counts{optionId:n}}]}`) follow `result_visibility`;
multiple-choice percentages may sum above 100%. Notifications: kind `activity`, reference `activity:{id}:new|reminder`,
push `deep_link` `qwish://activities/{id}`.

## Leadership roles and departments (migration 083)

From `plans/institution-hierarchy-and-access-control.md`, limited to **Director, Principal, Vice Principal, Dean and
Head of Department**. `users.role` is unchanged — an HOD stays a `teacher` and keeps the teacher panel. Access comes
from active `staff_role_assignments`, read on every request (revoke/expiry applies to the next request).

| Role key | Scope | Permissions |
|---|---|---|
| `director`, `principal` | institution only | `departments.read`, `students.read`, `staff.read`, `reports.read`, `activities.publish` |
| `vice_principal` | institution or per department | reads only (no copy of Principal publishing) |
| `dean` | per department | reads only |
| `hod` | per department | reads + `activities.publish` for that department's classes |

One row = one role × one scope (Dean of 3 departments = 3 rows). Permissions never combine across rows.
No role reads individual form/poll responses or manages roles. Classes with no department are reachable
only by institution-scoped roles.

### Institution admin (`/institution/...`)

| Method | Path | Notes |
|---|---|---|
| GET/POST | `/departments` | `{name, code?}`; name unique per institution (case-insensitive) → `409` |
| PATCH | `/departments/{id}` | `{name, code?}` |
| DELETE | `/departments/{id}` | Archive; `409` while active classes or role assignments point at it |
| PUT | `/groups/{groupId}/department` | `{department_id: uuid \| null}` |
| GET | `/staff-role-templates` | Catalogue with labels, scope rule, permissions, plain-language summary |
| GET | `/staff-roles?user_id=&role=&department_id=&include_ended=true` | `state`: `active \| scheduled \| ended \| revoked`; `access_summary` |
| POST | `/staff-roles` | `{user_id, role, department_id?, title?, starts_at?, ends_at?, reason}`. Holder must be an active teacher/admin of this institution. Duplicate active → `409` |
| DELETE | `/staff-roles/{id}` | `{reason}`; idempotent |

All writes are audited atomically to the institution audit log (filter group `access`).

### Role holders (`/leadership/...`, role `teacher` or `institution_admin`)

| Method | Path | Notes |
|---|---|---|
| GET | `/access` | `{assignments, permissions: {key: {institution, department_ids}}}` |
| GET | `/departments` | Departments in scope with class/student counts and HODs |
| GET | `/departments/{id}/summary` | Per-class student count, teachers, 30-day completed attempts and average score |
| GET | `/students?department_id=&q=&limit=&offset=` | `{students, total}` |
| GET | `/staff?department_id=` | Teachers of in-scope classes plus in-scope role holders |

No qualifying assignment → `403`. A `department_id` or id outside scope → `404`.
Forms/polls: Directors and Principals may publish `institution_wide`; HODs may target any class in their department.

## Staff view of student profile and portfolio

`plans/student-portfolio-and-achievements.md`, teacher/institution side. Package `internal/domain/portfolio`.

| Method | Path | Who | Notes |
|---|---|---|---|
| GET | `/teacher/students/{userId}/profile` | teacher (same scope as `/teacher/students`) | Full profile, below |
| GET | `/institution/students/{userId}/profile` | institution admin (active/suspended enrollment) | Same shape, read-only |
| GET | `/teacher/portfolio-reviews?category=&limit=` | teacher | Submissions awaiting review for students in scope, oldest first |
| POST | `/teacher/portfolio-reviews/{revisionId}/decision` | teacher | `{decision: reviewed\|changes_requested, comment}` — comment required for `changes_requested`. `409 STALE_REVISION` once the revision is no longer the one awaiting review; identical retry → `200`. Notifies the student (kind `portfolio_review`) |

Profile: `{student {full_name, email, domain, interests, …},
enrollment, classes, departments, education[], skills[], portfolio[], portfolio_summary}`.

`portfolio[]` holds, per entry, the latest revision submitted **within the caller's institution** — never a live draft:
`{entry_id, revision_id, revision, content, submitted_at, pinned, review_state: submitted|reviewed|changes_requested, review,
awaiting_review, student_editing}`. Restricted fields (`details.supervisor`) are removed from staff views.

---

## Classes: end, reopen, past classes (learning layer 3)

Promotion is gone. `POST /institution/enrollments/promote` and `/institution/promotions*` were removed. A class now ends (it is archived), and its students join the next class with its class code. Existing promotion batches were converted into class membership history.

### Class fields
Groups carry `grade`, `section` (nullable, for reports only, never shown to students) and `kind` (`"class"` or `"remedial"`). These fields appear in:
- `GET /teacher/classes`
- `GET /teacher/classes/{classId}`
- `GET /institution/groups`
- `GET /institution/groups/{groupId}`

The detail endpoints also return `archived_at`. A student who joins a class with a grade takes that grade and section onto their enrollment. Remedial groups never set grade.

- `POST /institution/groups`: `{ "name", "description?", "grade?", "section?" }`.
- `PATCH /institution/groups/{groupId}`: partial; any of `name`, `description`, `grade`, `section`. An empty `grade`/`section` clears it. Changing `grade` relabels the class's current members' report stages; it doesn't change their enrollments. `grade`/`section` over 40 characters return `400`. `404` outside your institute.

### Ending and reopening
| Route | Who | Response |
|---|---|---|
| `DELETE /institution/groups/{groupId}` | institute admin | `200 {"message":"class ended"}` |
| `POST /institution/groups/{groupId}/reopen` | institute admin | `200 {"message":"class reopened"}` |
| `POST /teacher/classes/{classId}/end` | assigned teacher | `200 {"message":"class ended"}` |
| `POST /teacher/classes/{classId}/reopen` | assigned teacher | `200 {"message":"class reopened"}` |

- Ending notifies the class's students that the class ended and prompts them to join their next class.
- Teacher and admin end/reopen both write `archive_group` / `reopen_group` to the institute audit log.
- Reopen works for 90 days after ending. After that it returns `409 CLASS_REOPEN_EXPIRED`. Reopening a live class is a no-op `200`.
- An unknown class, or one outside your scope, returns `404 NOT_FOUND`.
- `GET /teacher/classes?include_ended=1` includes ended classes. They are excluded by default.

### Student side
- `GET /users/me/enrollment` and `GET /users/me/enrollments`: when the student has no live class at that institute and their last class ended, `class_name` is null and `ended_class_name` holds the ended class's name.
- `GET /users/me/past-classes` (student) returns classes the student left or that ended, newest first:
```json
[{ "group_id": "uuid", "class_name": "9-A", "institution_name": "Greenfield School",
   "from": "2025-06-01T00:00:00Z", "to": "2026-04-01T00:00:00Z",
   "assessments": 40, "questions": 100, "correct": 72 }]
```
- `GET /users/me/past-classes/{groupId}/concepts` (student) returns concept accuracy inside that class's window:
```json
[{ "concept_id": "uuid", "title": "Fractions", "correct": 2, "errors": 6 }]
```
  If the student was never in that class, this returns an empty list. A `groupId` that isn't a uuid returns `404`.

### Membership history
Removing a student from a group (or ending their enrollment) records a `group_student_history` row. Leaving or ending an enrollment also removes the student from that institute's ended classes, so a reopen doesn't bring them back. A removal from an ended class records the class's end date as the leave date. Only students with an active or suspended enrollment are notified when a class ends.

The learning report builds its institution stages from graded main classes (current memberships plus this history). Time no graded class covers is its own stage: the enrollment's grade after the last class (or for the whole enrollment if there is none), `"Not recorded"` before or between classes.

## Notices and practice groups (learning layer 4)

### Notices
A notice is a short announcement sent straight to students' notifications (in-app and push). Notices go out immediately; there is no scheduling.

| Route | Who | Response |
|---|---|---|
| `GET /teacher/notices/audiences` | teacher (incl. leadership roles) | `200 Audiences` |
| `POST /teacher/notices` | teacher | `201 Notice` |
| `GET /teacher/notices?page=&limit=` | teacher | `200 [Notice]` |
| `GET /institution/notices/audiences` | institution admin | `200 Audiences` |
| `POST /institution/notices` | institution admin | `201 Notice` |
| `GET /institution/notices?page=&limit=` | institution admin | `200 [Notice]` |

POST is rate-limited to 30 per user per hour. `limit` defaults to 20 (max 100); `page` starts at 1.

**Who can reach what**

| Sender | Can target |
|---|---|
| Teacher | Classes they teach, plus the departments of those classes |
| HOD (any role with `activities.publish` scoped to a department) | Any class in that department, plus that department |
| Institution admin, or a role with institution-wide `activities.publish` (Director, Principal) | Anything, including the whole institute |

`GET .../audiences` returns exactly what the sender may pick:
```json
{ "classes": [{ "id": "uuid", "name": "9-A" }], "departments": [{ "id": "uuid", "name": "Science" }], "institution_wide": false }
```

**Draft** (POST body):
```json
{ "title": "Test on Friday", "body": "Chapter 3", "category": "test",
  "group_ids": ["uuid"], "department_ids": [], "institution_wide": false }
```
- `title` 1–120 characters, `body` 1–2000, `category` one of `event` | `test` | `general`.
- At least one class or department unless `institution_wide`; at most 50 classes plus departments.
- `400` for an invalid draft (message says which field). `403 NOTICE_AUDIENCE` if any chosen class or department is outside the sender's reach, or `institution_wide` without that reach; nothing is sent.

**Notice** (response):
```json
{ "id": "uuid", "title": "Test on Friday", "body": "Chapter 3", "category": "test",
  "created_by_name": "Ms Rao", "institution_wide": false, "group_ids": ["uuid"], "department_ids": [],
  "recipient_count": 32, "created_at": "2026-10-03T09:00:00Z" }
```
The list shows the sender's own notices; institution admins and institution-wide leaders see every notice at the institute.

**Delivery:** `POST` returns once the notice is stored; notifications go out in the background and finish even if the request times out.

**Recipients:** students with an `active` enrollment (suspended or left get nothing) who are in a live targeted class, or in a live class of a targeted department, or everyone at the institute for `institution_wide`. Each student gets one notification per notice even when they're in several targeted classes.

**Notification kind `notice`:** `{ "kind": "notice", "title", "body", "icon": "campaign", "color": "indigo", "reference": "notice:<notice id>" }`. It has no destination; apps show the full text in place.

### Practice (remedial) groups
A practice group is a `groups` row with `kind: "remedial"`, made by a teacher from one of their classes for one concept. It has no grade, joining is off, it never keeps an enrollment alive and it has no leaderboard. Students see only its name; no student-facing payload says "remedial".

`POST /teacher/remedial-groups`
```json
{ "source_class_id": "uuid", "concept_id": "uuid", "name": "Fractions practice", "student_ids": ["uuid"] }
```
→ `201 { "id", "name", "invite_code", "member_count" }`. The teacher becomes its teacher.
- `400`: `name` not 1–80 characters, not 1–200 students, malformed ids, a student not in the source class, or a concept with no evidence at the institute.
- `403 NOT_YOUR_CLASS`: the teacher doesn't teach the (live) source class.

`GET /teacher/remedial-groups/{groupId}/progress` → before/after evidence on the group's concept, split at the group's creation:
```json
{ "concept_id": "uuid", "concept_title": "Fractions", "created_at": "2026-10-03T09:00:00Z",
  "students": [{ "student_id": "uuid", "name": "Asha", "before": { "correct": 1, "errors": 4 }, "after": { "correct": 3, "errors": 1 } }] }
```
`404` unless the caller teaches the group. Membership edits and ending use the normal class routes (`POST/DELETE /teacher/classes/{classId}/students`, `POST /teacher/classes/{classId}/end`). Adding a student who isn't in the source class returns `400`. Practice groups appear in `GET /teacher/classes` with `kind: "remedial"`.
