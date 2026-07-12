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

Sends a 6-digit OTP to the given email address. Also indicates whether the email belongs to a new or returning user so the client can prepare the correct next screen.

### Request Body
```json
{ "email": "alice@example.com" }
```

### Response `200`
```json
{
  "message":     "OTP sent",
  "is_new_user": true
}
```

| Field | Type | Description |
|-------|------|-------------|
| `is_new_user` | `bool` | `true` → no account yet, show name & referral fields after OTP. `false` → returning user. |

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Missing `email` |

---

## POST `/auth/verify-otp`
**Auth required:** No

Verifies the OTP and returns session tokens.

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
  "badges":           ["first_quiz", "perfect_score"]
}
```

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
  "top_percentile":     12.5
}
```

| Field | Type | Notes |
|-------|------|-------|
| `top_percentile` | `float` | e.g. `12.5` → "Top 12.5%" of all active users |
| `institution_rank` / `institution_total` | `int` | Omitted if user has no institution |
| `domain_rank` / `domain_total` | `int` | Omitted if user's `domain` is not set |

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

## GET `/users/me/report-card`
**Auth required:** Yes

Generates and downloads a verified PDF report card for the user.

### Response `200`
`Content-Type: application/pdf` with PDF binary data.

---

# 3. Quizzes

## GET `/quizzes`
**Auth required:** Yes (student / teacher / institution_admin)

### Query Params
| Param | Description |
|-------|-------------|
| `type` | Filter by quiz type (`practice`, `play_and_win`) |
| `saved` | `true` to return only saved quizzes |
| `page`, `limit` | Pagination |

### Response `200` (paginated)
```json
[
  {
    "id":             "uuid",
    "title":          "Biology Chapter 3",
    "description":    "Cell division and genetics",
    "type":           "practice",
    "status":         "published",
    "question_count": 10,
    "is_saved":       false,
    "created_by":     "uuid",
    "published_at":   "2024-02-01T00:00:00Z"
  }
]
```

---

## GET `/quizzes/{quizId}`
**Auth required:** Yes

Returns quiz details including all questions (with options, correct answers hidden for students during attempts).

---

## POST `/quizzes/{quizId}/save`
**Auth required:** Yes

Saves a quiz to the user's saved list.

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
{ "deep_link": "quizapp://quiz/uuid" }
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
  ]
}
```

> `performance_badge`: `excellent` (≥75%), `good` (50–74%), `needs_work` (<50%)

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
| `page`, `limit` | — | page=1, limit=50 |

### Response `200` (paginated)
```json
{
  "scope":     "institution",
  "my_rank":   3,
  "my_points": 1250,
  "entries": [
    {
      "rank":           1,
      "user_id":        "uuid",
      "display_name":   "Bob Jones",
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
  "time_limit":  30,
  "expires_at":  "2024-06-01T00:00:00Z"
}
```

`title` is required. `visibility` defaults to `institution`. `domain`/`subdomain` are optional but validated against `/teacher/quizzes/taxonomy` — a subdomain must belong to its domain, else `400`.

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

All routes require role `institution_admin`.

## GET `/institution/overview`

### Response `200`
```json
{
  "total_students":  120,
  "active_students": 45,
  "total_teachers":  8,
  "total_quizzes":   34,
  "average_score":   71.2,
  "top_student":     { "name": "Bob", "points": 3200 },
  "activity_chart":  [{ "day": "2024-03-01", "count": 12 }],
  "top_quizzes":     [{ "id": "uuid", "title": "Biology Ch3", "completions": 89 }]
}
```

---

## GET `/institution/students`
### Query Params
| Param | Description |
|-------|-------------|
| `search` | Name or email search |
| `status` | `active`, `suspended` |
| `group_id` | Filter by group |
| `sort` | `total_points`, `average_score`, `last_active` |
| `page`, `limit` | Pagination |

### Response `200` (paginated)
Array of student rows with `id`, `display_name`, `email`, `total_points`, `current_streak`, `last_active_at`, `status`, `average_score`.

---

## GET `/institution/students/{userId}`
Returns detailed student profile with quiz history and group memberships.

---

## PATCH `/institution/students/{userId}/status`
### Request Body
```json
{ "action": "suspend", "reason": "Academic misconduct" }
```
`action`: `suspend` or `reactivate`

### Response `200`
```json
{ "status": "suspended" }
```

---

## GET `/institution/teachers`
### Query Params
`page`, `limit`

### Response `200` (paginated)
Array of teacher rows with `id`, `display_name`, `email`, `last_active_at`, `status`, `quiz_count`, `attempt_count`.

---

## GET `/institution/teachers/{userId}`
Returns teacher profile with quiz and attempt stats.

---

## PATCH `/institution/teachers/{userId}/status`
Same shape as student status update.

---

## DELETE `/institution/teachers/{userId}`
Removes the teacher from the institution (does not delete their account).

### Response `200`
```json
{ "message": "teacher removed from institution" }
```

---

## GET `/institution/groups`
### Response `200`
Array of groups with `id`, `name`, `description`, `invite_code`, `archived_at`, `created_at`.

---

## POST `/institution/groups`
### Request Body
```json
{ "name": "Class 10A", "description": "Optional" }
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
  "id":            "uuid",
  "name":          "Class 10A",
  "student_count": 28,
  "average_score": 74.5
}
```

---

## PATCH `/institution/groups/{groupId}`
### Request Body
```json
{ "name": "Class 10B", "description": "Updated description" }
```

### Response `200`
```json
{ "message": "group updated" }
```

---

## DELETE `/institution/groups/{groupId}`
Archives the group.

### Response `200`
```json
{ "message": "group archived" }
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
### Query Params
| Param | Description |
|-------|-------------|
| `group_id` | Filter by group |
| `date_from`, `date_to` | Date range filter |

### Response `200`
Array of student performance rows with `id`, `display_name`, `total_points`, `current_streak`, `quizzes_taken`, `average_score`.

---

## GET `/institution/reports/teacher-activity`
Per-teacher activity stats for the institution.

### Query Params
| Param | Description |
|-------|-------------|
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
Institution-wide student counts by streak status.

### Response `200`
```json
{ "active": 512, "at_risk": 289, "broken": 483 }
```
- `active` — `current_streak >= 7`
- `at_risk` — `current_streak` between 1 and 6
- `broken` — `current_streak = 0`

---

## GET `/institution/reports/points-summary`
Points distribution trend + per-student totals.

### Query Params
| Param | Description |
|-------|-------------|
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
Institution-admin view of attempt results for a quiz the institution owns. Mirrors `/teacher/quizzes/{quizId}/results` but scopes by `institution_id` instead of `created_by`.

### Response `200`
```json
{
  "completions":     128,
  "completion_rate": 87.4,
  "avg_score":       71.2,
  "per_question_accuracy": [
    { "position": 1, "accuracy_pct": 92.1 }
  ],
  "attempts": [
    {
      "student_id":    "uuid",
      "display_name":  "Priya S.",
      "score_pct":     80.0,
      "points_earned": 240,
      "time_taken_ms": 412300,
      "completed_at":  "2026-05-12T10:22:14Z"
    }
  ]
}
```

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
  "student_referral_code": "SINST-ABC",
  "teacher_referral_code": "TINST-XYZ",
  "point_rules": {
    "point_multiplier":      1.0,
    "streak_grace_enabled":  true,
    "play_win_score_hidden": false,
    "point_expiry_months":   6
  }
}
```

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

All fields optional — only provided fields are updated.

### Response `200`
```json
{ "message": "point rules updated" }
```

---

## GET `/institution/audit-log`
### Query Params
`page`, `limit`

### Response `200` (paginated)
Array of audit entries scoped to this institution and its users.

---

## GET `/institution/quizzes`
Same as `GET /quizzes` — lists published quizzes for the institution.

---

## GET `/institution/quizzes/{quizId}`
Same as `GET /quizzes/{quizId}`.

---

## GET `/institution/topic-requests`
Same as `GET /teacher/topic-requests`.

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
Array with `id`, `title`, `body`, `delivery_types`, `audience`, `status`, `scheduled_at`, `sent_at`, `created_at`.

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
  "delivery_types": ["in_app"],
  "audience":       "all",
  "institution_id": null,
  "scheduled_at":   "2024-04-01T09:00:00Z"
}
```

`title` and `body` are required. `delivery_types` can include `in_app` and/or `email`.

### Response `201`
```json
{ "id": "uuid", "status": "scheduled" }
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
Array with `id`, `placement`, `title`, `body`, `cta_label`, `cta_url`, `target`, `status`, `start_date`, `end_date`, `created_at`.

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
  "start_date": "2024-06-01T00:00:00Z",
  "end_date":   "2024-06-30T23:59:59Z"
}
```

`title`, `placement`, and `target` are required. `placement` must be one of `home_banner`, `quiz_browser_banner`, `splash_interstitial`, `achievement_prompt`.

### Response `201`
```json
{ "id": "uuid" }
```

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

| Method | Route | Description |
|--------|-------|-------------|
| `POST` | `/internal/cron/expire-points` | Deactivates expired point ledger entries |
| `POST` | `/internal/cron/reset-streaks` | Resets missed streaks at 00:05 UTC |
| `POST` | `/internal/cron/snapshot-leaderboard` | Snapshots weekly leaderboard rankings |
| `POST` | `/internal/cron/close-expired-quizzes` | Closes quizzes past their `expires_at` |

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
`theme` ∈ `auto | light | dark`. Profiles are **private by default**; `recruiter_visible=true` opts the user into public/recruiter discovery.

### PATCH `/users/me/settings`
Body (all fields optional):
```json
{ "theme": "dark", "profile_private": false, "recruiter_visible": true }
```
Returns the updated settings. `400 BAD_REQUEST` for an invalid `theme`.

> Privacy enforcement: `GET /users/{userId}/profile` returns `403 PROFILE_PRIVATE` unless the viewer is the owner, a follower, or the target has `recruiter_visible=true`.

## Push Alerts — Notification Preferences

### GET `/users/me/notification-preferences`
```json
{
  "push_rank_changes": true,
  "push_weekly_digest": true,
  "push_streak_nudge": true,
  "push_study_group": true,
  "email_weekly_insights": true
}
```
Missing row ⇒ all categories enabled by default.

### PATCH `/users/me/notification-preferences`
Body: any subset of the boolean keys above. Returns the merged preferences.

Push alerts are delivered via FCM (existing `/users/me/devices` registration) and also stored as in-app notifications. Cron-driven categories:
- **Rank changes** — daily; fires when global rank improves.
- **Streak nudges** — daily evening; fires if an active streak hasn't been continued today.
- **Weekly digest** — Mondays; weekly recap push.

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
Lifetime Qwish Score breakdown plus question-weighted domain/subdomain performance. `qwish_score` is the weighted sum of the five components (0–100). `components` are lifetime fractions (0–1): accuracy 50%, difficulty 20%, consistency 15%, speed 10%, activity 5%. Each domain's `avg_score` is question-weighted accuracy (0–100); `low_sample` is true when fewer than 10 questions have been answered.
```json
{
  "qwish_score": 72.4,
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
Bucketed average `score_pct` over time for the insights chart. `4w` → 4 weekly buckets, `12w` → 12 weekly, `all` → 12 monthly. Empty buckets carry the previous value forward so the line stays continuous.
```json
[
  { "label": "5/12", "value": 71.0 },
  { "label": "5/19", "value": 74.5 }
]
```

## Offline Mode

### GET `/offline/pack?since=<version>`
Returns the bundle of practice quizzes (`type=knowledge_check`, published, visible to the user) **including correct answers** so grading happens on-device. Practice is non-competitive (no points, no leaderboard).
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
[ { "user_id": "uuid", "display_name": "Asha", "role": "owner",
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
