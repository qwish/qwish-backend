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

## POST `/auth/send-otp`
**Auth required:** No

Sends a 6-digit OTP to the given email address. Always returns success to prevent email enumeration.

### Request Body
```json
{ "email": "alice@example.com" }
```

### Response `200`
```json
{ "message": "if that email is valid, an OTP has been sent" }
```

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Missing `email` |

---

## POST `/auth/verify-otp`
**Auth required:** No

Verifies the OTP. Handles both **login** (returning user) and **signup** (new user) in a single call.

- If the user already exists → returns `is_new_user: false`
- If the user is new → creates the account and returns `is_new_user: true` (`full_name` is required in this case)

### Request Body
```json
{
  "email":         "alice@example.com",
  "otp":           "123456",
  "full_name":     "Alice Smith",
  "referral_code": "SINST-ABC"
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `email` | Yes | |
| `otp` | Yes | 6-digit code from email |
| `full_name` | **New users only** | Required on first login; ignored for returning users |
| `referral_code` | No | Determines institution and role (`student` or `teacher`). Only applied on first login. |

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

### Response `201` — New user
```json
{
  "user": {
    "id":           "uuid",
    "full_name":    "Alice Smith",
    "display_name": "Alice Smith",
    "email":        "alice@example.com",
    "role":         "student",
    "institution":  { "id": "uuid", "name": "Springfield Academy" }
  },
  "access_token":  "eyJ...",
  "refresh_token": "eyJ...",
  "is_new_user":   true
}
```

> `institution` is `null` if no referral code was provided.

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Missing `email` or `otp` |
| 400 | `BAD_REQUEST` | New user but `full_name` is missing |
| 400 | `BAD_REQUEST` | Invalid or inactive referral code |
| 401 | `INVALID_OTP` | OTP is wrong or expired |

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

## POST `/teacher/quizzes`
**Auth required:** Yes (teacher)

### Request Body
```json
{
  "title":       "Biology Chapter 3",
  "description": "Cell division and genetics",
  "type":        "practice",
  "visibility":  "institution",
  "time_limit":  30,
  "expires_at":  "2024-06-01T00:00:00Z"
}
```

`title` is required. `visibility` defaults to `institution`.

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
```json
{ "message": "updated" }
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
{ "value": 15 }
```

### Response `200`
```json
{ "message": "config updated" }
```

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
Array with `id`, `name`, `email`, `role`, `status`, `created_at`.

---

## POST `/admin/admin-accounts`
**Roles:** super_admin only

### Request Body
```json
{ "name": "Jane Mod", "email": "jane@qwish.com", "role": "moderator" }
```

### Response `201`
```json
{ "id": "uuid", "message": "admin account created, invite sent" }
```

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

Cannot delete your own account.

### Response `200`
```json
{ "message": "admin account deleted" }
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
