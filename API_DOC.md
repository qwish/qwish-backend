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

Tokens are obtained via `/auth/login` or `/auth/signup`.

---

## Standard Response Shapes

### Success (single object)
```json
{ ...fields... }
```

### Success (paginated list)
```json
{
  "data": [ ...items... ],
  "meta": { "page": 1, "limit": 20, "total": 123 }
}
```

### Error
```json
{
  "error": "ERROR_CODE",
  "message": "Human-readable description"
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
| `page` | 1 | - |
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
| `speed_demon` | `speed_chain` question with combo >= 3 |
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
| `clue_reveal` | `base_points × (2 - deduction × clues_used)`, min `base × 0.5` | 0 |
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
- Final points are then multiplied by the institution's `point_multiplier`
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

## POST `/auth/signup`
**Auth required:** No

### Request Body
```json
{
  "full_name":     "Alice Smith",
  "email":         "alice@example.com",
  "password":      "SecurePass123!",
  "referral_code": "INST-STU-ABC1"
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `full_name` | Yes | |
| `email` | Yes | |
| `password` | Yes | |
| `referral_code` | No | If omitted, user is created without an institution. The referral code determines the `role` (`student` or `teacher`). |

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
  },
  "access_token":  "eyJ...",
  "refresh_token": "eyJ..."
}
```

> `institution` is `null` if no referral code was provided.

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | - | Missing `full_name`, `email`, or `password` |
| 400 | - | Invalid or inactive referral code |
| 409 | `EMAIL_IN_USE` | Email already registered |

---

## POST `/auth/login`
**Auth required:** No

### Request Body
```json
{
  "email":    "alice@example.com",
  "password": "SecurePass123!"
}
```

Both fields required.

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
| 400 | - | Missing email or password |
| 401 | `INVALID_CREDENTIALS` | Wrong email or password |

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
| 400 | - | Missing `refresh_token` |
| 401 | `INVALID_TOKEN` | Expired or invalid token |

---

## POST `/auth/forgot-password`
**Auth required:** No

Triggers a Supabase OTP/reset email. Always returns success to prevent email enumeration.

### Request Body
```json
{ "email": "alice@example.com" }
```

### Response `200`
```json
{ "message": "if that email is registered, a reset link has been sent" }
```

---

## POST `/auth/logout`
**Auth required:** Yes

Invalidates the current access token via Supabase.

### Response `200`
```json
{ "message": "logged out" }
```

---

## PATCH `/auth/referral-code`
**Auth required:** Yes

Switch the authenticated user to a different institution using a new referral code.

### Request Body
```json
{ "referral_code": "NEW-INST-CODE" }
```

### Response `200`
```json
{ "message": "institution updated" }
```

### Errors
| Status | Meaning |
|--------|---------|
| 400 | Missing `referral_code` or code is invalid/inactive |

---

# 2. Users (Self)

**Auth required:** Yes for all.

## GET `/users/me`

Returns the full profile of the authenticated user.

### Response `200`
```json
{
  "id":             "uuid",
  "full_name":      "Alice Smith",
  "display_name":   "Alice",
  "email":          "alice@example.com",
  "role":           "student",
  "institution_id": "uuid",
  "institution":    { "id": "uuid", "name": "Springfield Academy" },
  "status":         "active",
  "total_points":   1250,
  "current_streak": 5,
  "longest_streak": 12,
  "member_since":   "2024-01-15T00:00:00Z",
  "last_active_at": "2026-04-07T10:30:00Z"
}
```

> `institution` and `institution_id` are omitted if the user has no institution. `last_active_at` is updated each time an attempt is started.

---

## PATCH `/users/me`

Update the authenticated user's profile. Only `display_name` is currently editable.

### Request Body
```json
{ "display_name": "Ali" }
```

### Response `200`
Same shape as `GET /users/me`.

---

## DELETE `/users/me`

Soft-deletes and anonymizes the account. Sets `status = 'deleted'`, clears `full_name`, `display_name`, and `email`.

### Response `200`
```json
{ "message": "account deleted" }
```

---

## GET `/users/me/stats`

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

Returns all 8 badge types, with earned status for each.

### Response `200` (array, always 8 items)
```json
[
  { "badge_type": "first_quiz",    "earned": true,  "earned_at": "2024-01-16T09:00:00Z" },
  { "badge_type": "on_a_roll",     "earned": false, "earned_at": null },
  { "badge_type": "unstoppable",   "earned": false, "earned_at": null },
  { "badge_type": "top_10",        "earned": false, "earned_at": null },
  { "badge_type": "perfect_score", "earned": false, "earned_at": null },
  { "badge_type": "speed_demon",   "earned": false, "earned_at": null },
  { "badge_type": "sharp_mind",    "earned": false, "earned_at": null },
  { "badge_type": "explorer",      "earned": false, "earned_at": null }
]
```

> `earned_at` is `null` (field omitted) when `earned` is `false`.

---

## GET `/users/me/attempts`

Completed attempts only, newest first.

### Query Params
| Param | Default | Max |
|-------|---------|-----|
| `page` | 1 | - |
| `limit` | 20 | 50 |

### Response `200`
```json
{
  "data": [
    {
      "id":           "uuid",
      "quiz_id":      "uuid",
      "quiz_title":   "Algebra Basics",
      "score_pct":    85.0,
      "points_delta": 120,
      "status":       "completed",
      "completed_at": "2026-04-07T10:00:00Z"
    }
  ],
  "meta": { "page": 1, "limit": 20, "total": 34 }
}
```

---

## GET `/users/me/points`

### Response `200`
```json
{
  "total_points": 1250,
  "expiring_soon": {
    "amount":     200,
    "expires_at": "2026-05-01T00:00:00Z"
  }
}
```

> `expiring_soon` is `null` if no points expire within the next 30 days.

---

## GET `/users/me/points/ledger`

Full transaction history, newest first.

### Query Params: `page`, `limit`

### Response `200`
```json
{
  "data": [
    {
      "id":            "uuid",
      "amount":        120,
      "reason":        "quiz_attempt",
      "reference_id":  "attempt-uuid",
      "balance_after": 1250,
      "expires_at":    "2026-10-07T00:00:00Z",
      "created_at":    "2026-04-07T10:00:00Z"
    }
  ],
  "meta": { "page": 1, "limit": 20, "total": 50 }
}
```

**`reason` values:** `quiz_attempt` · `streak_bonus` · `admin_adjustment` · `expiry`

> `amount` can be negative (deduction or expiry). `reference_id` is the attempt UUID for `quiz_attempt` entries, otherwise omitted.

---

## GET `/users/me/streak`

### Response `200`
```json
{
  "current_streak":        5,
  "longest_streak":        12,
  "grace_window_active":   false,
  "next_milestone":        7,
  "progress_to_milestone": 5
}
```

**Streak Milestones:** 7 days · 15 days · 30 days

> `next_milestone` returns `30` once the user has passed all milestones.
> `progress_to_milestone` equals `current_streak`.

---

## GET `/users/{userId}/profile`

Public profile view — safe to display to other users.

### Response `200`
```json
{
  "id":                "uuid",
  "display_name":      "Alice",
  "institution":       "Springfield Academy",
  "total_points":      1250,
  "current_streak":    5,
  "longest_streak":    12,
  "quizzes_completed": 34,
  "badges":            ["first_quiz", "on_a_roll"]
}
```

> `institution` is an empty string if the user has no institution. `badges` is an array of only the earned badge type strings (not all 8).

### Errors
| Status | Meaning |
|--------|---------|
| 404 | User not found or inactive |

---

# 3. Quizzes

**Auth required:** Yes for all.

## GET `/quizzes`

Browse published quizzes for the authenticated user's institution.

### Query Params
| Param | Values | Description |
|-------|--------|-------------|
| `type` | `practice` \| `play_and_win` \| `assignment` | Filter by quiz type |
| `saved` | `true` | Only return bookmarked quizzes |
| `page` | integer | Default 1 |
| `limit` | integer | Default 20, max 50 |

### Response `200`
```json
{
  "data": [
    {
      "id":             "uuid",
      "institution_id": "uuid",
      "created_by":     "uuid",
      "teacher_name":   "Mr. Johnson",
      "title":          "Algebra Basics",
      "description":    "Covers linear equations",
      "type":           "practice",
      "visibility":     "institution",
      "status":         "published",
      "question_count": 10,
      "ends_at":        null,
      "published_at":   "2026-03-01T08:00:00Z",
      "group_id":       null,
      "created_at":     "2026-02-28T12:00:00Z"
    }
  ],
  "meta": { "page": 1, "limit": 20, "total": 45 }
}
```

**Quiz `status` values:** `draft` · `pending_approval` · `published` · `rejected` · `closed`

> Only `published` quizzes appear in this listing. Results are ordered by `published_at` descending.

---

## GET `/quizzes/{quizId}`

### Response `200`
Same as list item, plus:
```json
{
  "rejection_reason": null,
  "question_types":   ["multiple_choice", "speed_chain"]
}
```

> `question_types` lists the distinct question types present in the quiz.

### Errors
| Status | Meaning |
|--------|---------|
| 404 | Quiz not found |

---

## POST `/quizzes/{quizId}/save`

Bookmark a quiz. Idempotent.

### Response `200`
```json
{ "message": "quiz saved" }
```

---

## DELETE `/quizzes/{quizId}/save`

Remove bookmark.

### Response `200`
```json
{ "message": "quiz unsaved" }
```

---

## GET `/quizzes/{quizId}/share`

Get a deep-link for sharing the quiz.

### Response `200`
```json
{ "deep_link": "quizapp://quiz/<quizId>" }
```

---

## POST `/quizzes/{quizId}/reports`

Report a quiz for review.

### Request Body
```json
{
  "reason":      "inappropriate_content",
  "description": "Optional extra context"
}
```

| Field | Required |
|-------|----------|
| `reason` | Yes |
| `description` | No |

> Reports are auto-escalated to `priority: "high"` when 3 or more open reports exist for the same quiz.

### Response `200`
```json
{ "message": "thanks — we'll review this" }
```

---

## POST `/quizzes/{quizId}/questions/{questionId}/reports`

Report a specific question within a quiz. Same request body as quiz report. Always returns `200`.

---

# 4. Attempts

**Auth required:** Yes for all.

## POST `/quizzes/{quizId}/attempts`

Start a new attempt. Returns questions without correct answers.

> **`play_and_win` quizzes allow only ONE completed attempt per user.** Returns `400` if the user already completed one.

### Response `201`
```json
{
  "attempt_id": "uuid",
  "quiz_id":    "uuid",
  "questions": [
    {
      "id":                 "uuid",
      "quiz_id":            "uuid",
      "position":           1,
      "type":               "multiple_choice",
      "prompt":             "What is 2 + 2?",
      "media_url":          null,
      "options":            ["3", "4", "5", "6"],
      "time_limit_seconds": 30,
      "clues":              null
    }
  ]
}
```

> `correct_answer` is **never** included in this response. `options` is an empty array `[]` for question types that don't use them. `clues` is `null` unless the question type is `clue_reveal`.

### Errors
| Status | Meaning |
|--------|---------|
| 400 | Quiz not found, not published, or already completed (`play_and_win`) |

---

## POST `/attempts/{attemptId}/answers`

Submit one answer. Can be called multiple times per question (upserts — last answer wins).

### Request Body
```json
{
  "question_id":      "uuid",
  "answer":           "4",
  "time_taken_ms":    8500,
  "confidence_level": "very_confident",
  "clues_used":       0,
  "combo_level":      2
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `question_id` | Yes | |
| `answer` | Yes | See format by type below |
| `time_taken_ms` | No | Milliseconds taken to answer |
| `confidence_level` | No | Required for `confidence_based` questions: `very_confident` \| `pretty_sure` \| `not_sure` |
| `clues_used` | No | Number of clues revealed (for `clue_reveal` questions) |
| `combo_level` | No | Current combo streak (for `speed_chain` questions) |

**`answer` format by question type:**
| Type | Format | Example |
|------|--------|---------|
| `multiple_choice` | String | `"4"` |
| `confidence_based` | String | `"photosynthesis"` |
| `eliminate_wrong` | String | `"correct option"` |
| `puzzle` | String | `"correct option"` |
| `speed_chain` | String | `"correct option"` |
| `arrange_order` | JSON array of strings | `["first","second","third"]` |
| `clue_reveal` | String | `"answer text"` |

### Response `200`
```json
{
  "is_correct":     true,
  "correct_answer": "4",
  "points_earned":  25,
  "combo_level":    3
}
```

> `combo_level` in the response is the submitted `combo_level + 1`, to be used as the next question's `combo_level`. `correct_answer` reflects the stored value in its native JSON format.

### Errors
| Status | Meaning |
|--------|---------|
| 400 | Missing `question_id`, attempt not found, attempt not in-progress, or question not found |

---

## POST `/attempts/{attemptId}/complete`

Finalize the attempt. Awards points, updates streak, grants badges.

> Must be called after submitting answers. Unanswered questions count as wrong (0 points).

### Response `200`
```json
{
  "attempt_id":           "uuid",
  "score_pct":            80.0,
  "performance_badge":    "excellent",
  "points_delta":         200,
  "total_correct":        8,
  "total_questions":      10,
  "streak_bonus_awarded": 50,
  "badges_awarded":       ["first_quiz"],
  "question_breakdown": [
    {
      "position":          1,
      "question_snippet":  "What is 2 + 2?",
      "student_answer":    "4",
      "correct_answer":    "4",
      "is_correct":        true,
      "points":            25
    }
  ]
}
```

**`performance_badge` values:**
| Value | Threshold |
|-------|-----------|
| `excellent` | Score ≥ 75% |
| `good` | Score 50–74% |
| `needs_work` | Score < 50% |

> `streak_bonus_awarded` is `0` if no milestone was hit. `badges_awarded` is an empty array `[]` if no new badges were earned. `question_snippet` is truncated to 80 characters.

### Errors
| Status | Meaning |
|--------|---------|
| 400 | Attempt not found or already completed |

---

## GET `/attempts/{attemptId}`

Get the result of a completed (or in-progress) attempt.

### Response `200`
```json
{
  "attempt_id":      "uuid",
  "quiz_id":         "uuid",
  "status":          "completed",
  "score_pct":       80.0,
  "points_delta":    200,
  "total_correct":   8,
  "total_questions": 10,
  "completed_at":    "2026-04-07T10:05:00Z"
}
```

> `completed_at` is `null` for in-progress attempts.

### Errors
| Status | Meaning |
|--------|---------|
| 404 | Attempt not found or belongs to another user |

---

# 5. Leaderboard

**Auth required:** Yes

## GET `/leaderboard`

### Query Params
| Param | Values | Default |
|-------|--------|---------|
| `scope` | `institution` \| `global` | `institution` |
| `page` | integer | 1 |
| `limit` | integer, max 100 | 50 |

### Response `200`
```json
{
  "data": {
    "scope":     "institution",
    "my_rank":   7,
    "my_points": 1250,
    "entries": [
      {
        "rank":           1,
        "user_id":        "uuid",
        "display_name":   "TopStudent",
        "total_points":   5000,
        "current_streak": 15
      }
    ]
  },
  "meta": { "page": 1, "limit": 50, "total": 120 }
}
```

> Only users with `status = 'active'` and `role IN ('student', 'teacher')` appear on the leaderboard. `my_rank` is calculated as the number of users with more points + 1.

---

# 6. Topic Requests

**Auth required:** Yes

## POST `/topic-requests`

Submit a topic request to teachers/institution.

### Request Body
```json
{
  "topic":       "Quadratic Equations",
  "subject":     "Mathematics",
  "description": "Specifically vertex form"
}
```

| Field | Required |
|-------|----------|
| `topic` | Yes |
| `subject` | No |
| `description` | No |

### Response `201`
```json
{
  "id":          "uuid",
  "student_id":  "uuid",
  "topic":       "Quadratic Equations",
  "subject":     "Mathematics",
  "description": "Specifically vertex form",
  "status":      "pending",
  "assigned_to": null,
  "created_at":  "2026-04-08T09:00:00Z"
}
```

**`status` values:** `pending` · `in_progress` · `done` · `rejected`

---

## GET `/topic-requests/mine`

All topic requests submitted by the authenticated student.

### Response `200`
Array of `TopicRequest` objects (same shape as `POST` response).

---

# 7. Parent

**Auth required:** Yes for all.

**Flow:** Student generates code → shares with parent → parent links → student accepts.

## POST `/parent/link-invite`
**Role:** `student` only

Generates an 8-character invite code.

### Response `200`
```json
{ "invite_code": "a1b2c3d4" }
```

### Errors
| Status | Meaning |
|--------|---------|
| 403 | Authenticated user is not a student |

---

## POST `/parent/link`

Parent submits an invite code to initiate a link request.

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
| Status | Meaning |
|--------|---------|
| 400 | Missing `invite_code` |
| 404 | Invite code not found or already used |

---

## POST `/parent/link/{linkId}/accept`
**Role:** `student` (must be the student who generated the code)

### Response `200`
```json
{ "message": "parent link activated" }
```

### Errors
| Status | Meaning |
|--------|---------|
| 400 | Link not found or already processed |

---

## DELETE `/parent/link/{linkId}`

Either party (parent or student) can revoke an active link. Sets status to `revoked`.

### Response `200`
```json
{ "message": "link revoked" }
```

---

## GET `/parent/children`

Lists all actively linked children for the authenticated parent.

### Response `200` (array)
```json
[
  {
    "id":             "uuid",
    "display_name":   "Bob Smith",
    "total_points":   400,
    "current_streak": 3
  }
]
```

---

## GET `/parent/children/{studentId}/overview`

Detailed view of a linked child. The parent must have an active link with the student.

### Response `200`
```json
{
  "student_id":      "uuid",
  "display_name":    "Bob Smith",
  "total_points":    400,
  "current_streak":  3,
  "quizzes_taken":   12,
  "average_score":   68.5,
  "recent_attempts": [
    {
      "id":           "uuid",
      "quiz_title":   "Algebra Basics",
      "score_pct":    75.0,
      "points_delta": 90,
      "completed_at": "2026-04-07T10:00:00Z"
    }
  ],
  "badges": ["first_quiz", "on_a_roll"]
}
```

> `recent_attempts` returns the last 5 completed attempts. `badges` contains only earned badge type strings.

### Errors
| Status | Meaning |
|--------|---------|
| 403 | No active link between this parent and the student |

---

# 8. Upload

**Auth required:** Yes | **Role:** `teacher`, `super_admin`, or `moderator`

## POST `/upload/image`

**Content-Type:** `multipart/form-data`

### Request Fields
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `file` | file | Yes | JPEG, PNG, or WebP; max 5 MB |
| `prefix` | string | No | Storage folder (default: `quiz-images`) |

### Response `201`
```json
{ "url": "https://media.yourdomain.com/quiz-images/uuid.jpg" }
```

---

# 9. Teacher Routes

**Auth required:** Yes | **Role:** `teacher`

## GET `/teacher/quizzes`

Lists all quizzes created by the authenticated teacher.

### Query Params
| Param | Description |
|-------|-------------|
| `status` | Filter by status: `draft` \| `pending_approval` \| `published` \| `rejected` \| `closed` |
| `page` | Default 1 |
| `limit` | Default 20, max 50 |

### Response `200`
Paginated quiz list (same shape as `GET /quizzes`). Results ordered by `created_at` descending.

---

## POST `/teacher/quizzes`

Create a new quiz. Starts in `draft` status.

### Request Body
```json
{
  "title":       "Algebra Basics",
  "description": "Covers ch.1-3",
  "type":        "practice",
  "visibility":  "institution",
  "group_id":    null,
  "ends_at":     null
}
```

| Field | Required | Values / Notes |
|-------|----------|----------------|
| `title` | Yes | |
| `description` | No | |
| `type` | No | `practice` \| `play_and_win` \| `assignment` |
| `visibility` | No | `institution` (default) \| `public` |
| `group_id` | No | UUID of a class group to restrict access |
| `ends_at` | No | ISO 8601 datetime; quiz auto-closes after this time |

### Response `201`
Quiz object with `status: "draft"`.

---

## PATCH `/teacher/quizzes/{quizId}`

Update a quiz. Must be the quiz owner. Quiz must be in `draft` status.

Same body fields as `POST /teacher/quizzes` (all optional).

### Response `200`
Updated quiz object.

### Errors
| Status | Meaning |
|--------|---------|
| 500 | Not owner or quiz is not in `draft` status |

---

## POST `/teacher/quizzes/{quizId}/questions`

Add a question to a quiz. Must be the quiz owner.

### Request Body
```json
{
  "position":           1,
  "type":               "multiple_choice",
  "prompt":             "What is 2 + 2?",
  "media_url":          null,
  "options":            ["3", "4", "5", "6"],
  "correct_answer":     "4",
  "time_limit_seconds": 30,
  "clues":              null
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `prompt` | Yes | |
| `type` | Yes | See Question Types table |
| `position` | No | Display order |
| `media_url` | No | URL to an image (use `/upload/image` first) |
| `options` | No | Array of strings. Defaults to `[]` if omitted |
| `correct_answer` | No | See format by type below |
| `time_limit_seconds` | No | Defaults to 15 |
| `clues` | No | For `clue_reveal` type only |

**`correct_answer` format by question type:**
| Type | Format |
|------|--------|
| `multiple_choice`, `confidence_based`, `eliminate_wrong`, `puzzle`, `speed_chain`, `clue_reveal` | `"string"` |
| `arrange_order` | `["item1","item2","item3"]` |

### Response `201`
Full question object including `id`, `quiz_id`, all submitted fields.

> Adding a question automatically increments the quiz's `question_count`.

### Errors
| Status | Meaning |
|--------|---------|
| 400 | Missing `prompt` or `type` |
| 403 | Not the quiz owner |

---

## PATCH `/teacher/quizzes/{quizId}/questions/{questionId}`

Update a question. Must be the quiz owner.

Same body as `POST /teacher/quizzes/{quizId}/questions` (all fields optional).

### Response `200`
```json
{ "message": "question updated" }
```

### Errors
| Status | Meaning |
|--------|---------|
| 403 | Not the quiz owner |

---

## DELETE `/teacher/quizzes/{quizId}/questions/{questionId}`

Delete a question. Must be the quiz owner. Automatically decrements `question_count`.

### Response `200`
```json
{ "message": "question deleted" }
```

### Errors
| Status | Meaning |
|--------|---------|
| 403 | Not the quiz owner |

---

## POST `/teacher/quizzes/{quizId}/publish`

Publish a quiz. Must be the quiz owner and quiz must have at least 1 question.

- `visibility: institution` → status becomes `published` immediately
- `visibility: public` → status becomes `pending_approval` (requires moderator/admin review)

Can also re-publish a `rejected` quiz.

### Response `200`
```json
{ "status": "published" }
```

### Errors
| Status | Meaning |
|--------|---------|
| 400 | Quiz has no questions, not the owner, or quiz is not in `draft`/`rejected` status |

---

## GET `/teacher/quizzes/{quizId}/results`

Analytics for a quiz. Must be the quiz owner.

### Response `200`
```json
{
  "total_attempts":  45,
  "completion_rate": 88.9,
  "average_score":   71.2,
  "question_count":  10,
  "per_question": [
    {
      "question_id":      "uuid",
      "position":         1,
      "prompt":           "What is 2 + 2?",
      "total_responses":  40,
      "correct_count":    38,
      "accuracy_pct":     95.0
    }
  ]
}
```

> `completion_rate` = completed attempts / all started attempts × 100.

### Errors
| Status | Meaning |
|--------|---------|
| 404 | Quiz not found or not the owner |

---

## GET `/teacher/topic-requests`

Topic requests within the teacher's institution.

### Query Params: `status`, `page`, `limit`

### Response `200`
Paginated array of `TopicRequest` objects.

---

## PATCH `/teacher/topic-requests/{requestId}`

Update a topic request status or assignment.

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

# 10. Institution Admin Routes

**Auth required:** Yes | **Role:** `institution_admin`

All endpoints under `/institution/`.

## GET `/institution/overview`

Dashboard stats for the admin's institution.

### Response `200`
```json
{
  "total_students":  120,
  "active_students": 45,
  "total_teachers":  8,
  "total_quizzes":   30,
  "average_score":   72.3,
  "top_student":     { "name": "Alice Smith", "points": 5000 },
  "activity_chart":  [
    { "day": "2026-04-01", "count": 12 }
  ],
  "top_quizzes": [
    { "id": "uuid", "title": "Algebra Basics", "completions": 45 }
  ]
}
```

> `active_students` = distinct students who completed a quiz in the last 7 days. `average_score` = average over the current calendar month. `activity_chart` = quizzes completed per day over the last 30 days (up to 30 entries). `top_quizzes` = up to 5 quizzes by completion count.

---

## Students

| Method | Path | Description |
|--------|------|-------------|
| GET | `/institution/students` | Paginated student list (`page`, `limit`) |
| GET | `/institution/students/{userId}` | Student detail |
| PATCH | `/institution/students/{userId}/status` | Update status: `{ "status": "active" \| "suspended" }` |

---

## Teachers

| Method | Path | Description |
|--------|------|-------------|
| GET | `/institution/teachers` | Paginated teacher list (`page`, `limit`) |
| GET | `/institution/teachers/{userId}` | Teacher detail |
| PATCH | `/institution/teachers/{userId}/status` | Update status |
| DELETE | `/institution/teachers/{userId}` | Remove teacher from institution |

---

## Groups (Classes)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/institution/groups` | All groups |
| POST | `/institution/groups` | Create group: `{ "name": "...", "description": "..." }` |
| GET | `/institution/groups/{groupId}` | Group detail |
| PATCH | `/institution/groups/{groupId}` | Update group name/description |
| DELETE | `/institution/groups/{groupId}` | Archive group |
| POST | `/institution/groups/{groupId}/students` | Add student: `{ "user_id": "uuid" }` |
| DELETE | `/institution/groups/{groupId}/students/{userId}` | Remove student from group |
| POST | `/institution/groups/{groupId}/teachers` | Add teacher: `{ "user_id": "uuid" }` |

---

## Quizzes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/institution/quizzes` | Browse published quizzes (same as `GET /quizzes`) |
| GET | `/institution/quizzes/{quizId}` | Quiz detail |

---

## Topic Requests

| Method | Path | Description |
|--------|------|-------------|
| GET | `/institution/topic-requests` | All topic requests (`status`, `page`, `limit`) |
| PATCH | `/institution/topic-requests/{requestId}` | Update status/assignment |

---

## Reports & Settings

| Method | Path | Description |
|--------|------|-------------|
| GET | `/institution/reports/student-performance` | Aggregated student performance report |
| GET | `/institution/settings` | Institution settings |
| PATCH | `/institution/settings` | Update settings (e.g. timezone, name) |
| PATCH | `/institution/settings/point-rules` | Update institution point multiplier |
| GET | `/institution/audit-log` | Admin action audit log for this institution |

---

# 11. Super Admin Routes

**Auth required:** Yes | **Role:** `super_admin`, `moderator`, or `support_agent`

All endpoints under `/admin/`. Role restrictions are noted per endpoint.

---

## Overview & Activity

| Method | Path | Role | Description |
|--------|------|------|-------------|
| GET | `/admin/overview` | all | Platform-wide metrics |
| GET | `/admin/activity-feed` | all | Recent admin actions (last 50); optional `?type=<action_type>` filter |

### `GET /admin/overview` Response `200`
```json
{
  "total_users":       5000,
  "active_users_week": 1200,
  "institutions": {
    "pending":   3,
    "verified":  45,
    "suspended": 2
  },
  "quizzes": {
    "published": 300,
    "pending":   12,
    "reported":  5
  },
  "attempts_today":  450,
  "attempts_week":   3200,
  "avg_score_week":  71.5,
  "points_week":     128000,
  "points_all_time": 2500000
}
```

### `GET /admin/activity-feed` Response `200` (array)
```json
[
  {
    "id":          "uuid",
    "timestamp":   "2026-04-08T09:00:00Z",
    "admin_name":  "Super Admin",
    "action_type": "approve_institution",
    "target_type": "institution",
    "target_id":   "uuid"
  }
]
```

---

## Institutions

| Method | Path | Role | Description |
|--------|------|------|-------------|
| GET | `/admin/institutions` | all | Paginated list; query: `search`, `status`, `type`, `page`, `limit` |
| GET | `/admin/institutions/queue` | all | Pending approval queue |
| GET | `/admin/institutions/{institutionId}` | all | Institution detail |
| POST | `/admin/institutions/{institutionId}/approve` | `super_admin` | Approve institution |
| POST | `/admin/institutions/{institutionId}/reject` | `super_admin` | Reject: `{ "reason": "..." }` |
| POST | `/admin/institutions/{institutionId}/suspend` | `super_admin` | Suspend institution |
| POST | `/admin/institutions/{institutionId}/reactivate` | `super_admin` | Reactivate suspended institution |
| POST | `/admin/institutions/{institutionId}/reset-referral-codes` | `super_admin` | Regenerate all referral codes |

---

## Users

| Method | Path | Role | Description |
|--------|------|------|-------------|
| GET | `/admin/users` | all | All users; supports pagination |
| GET | `/admin/users/{userId}` | all | User detail |
| PATCH | `/admin/users/{userId}/suspend` | all | Suspend user |
| PATCH | `/admin/users/{userId}/reactivate` | all | Reactivate suspended user |
| DELETE | `/admin/users/{userId}` | `super_admin` | Permanently delete user |
| POST | `/admin/users/{userId}/points` | `super_admin` | Adjust points: `{ "amount": 100, "reason": "admin_adjustment" }` |
| POST | `/admin/users/{userId}/impersonate` | all | Start impersonation session; returns `{ "session_id": "uuid", "access_token": "eyJ..." }` |
| POST | `/admin/impersonation/{sessionId}/end` | all | End impersonation session |

---

## Quiz Moderation

| Method | Path | Role | Description |
|--------|------|------|-------------|
| GET | `/admin/quizzes/moderation-queue` | all | Quizzes with `status: pending_approval` |
| POST | `/admin/quizzes/{quizId}/approve` | `super_admin`, `moderator` | Approve quiz → `published` |
| POST | `/admin/quizzes/{quizId}/reject` | `super_admin`, `moderator` | Reject: `{ "reason": "..." }` → `rejected` |
| POST | `/admin/quizzes/{quizId}/unpublish` | `super_admin` | Unpublish quiz → `closed` |

---

## Content Reports

| Method | Path | Role | Description |
|--------|------|------|-------------|
| GET | `/admin/reports` | all | Open content reports |
| POST | `/admin/reports/{reportId}/resolve` | all | Mark report resolved |

---

## Point Economy

| Method | Path | Role | Description |
|--------|------|------|-------------|
| GET | `/admin/point-economy` | `super_admin` | All config key-value pairs |
| PATCH | `/admin/point-economy/{key}` | `super_admin` | Update single config: `{ "value": 15 }` |

**Configurable keys:** `base_points_per_question` · `performance_bonus_pct_75` · `deduction_pct_below_50` · `streak_bonus_7_day` · `streak_bonus_15_day` · `streak_bonus_30_day` · `combo_multiplier_step` · `clue_reveal_deduction_per_clue` · `points_expiry_months` · `confidence_multiplier_table`

---

## Announcements

| Method | Path | Role | Description |
|--------|------|------|-------------|
| POST | `/admin/announcements` | all | Broadcast: `{ "title": "...", "message": "...", "scope": "global" \| "institution" }` |

---

## Audit Log & Admin Accounts

| Method | Path | Role | Description |
|--------|------|------|-------------|
| GET | `/admin/audit-log` | `super_admin` | Full platform audit log |
| GET | `/admin/admin-accounts` | `super_admin` | List all admin accounts |
| POST | `/admin/admin-accounts` | `super_admin` | Create admin: `{ "email": "...", "full_name": "...", "role": "moderator" \| "support_agent" }` |
| PATCH | `/admin/admin-accounts/{adminId}` | `super_admin` | Update admin account |
| DELETE | `/admin/admin-accounts/{adminId}` | `super_admin` | Delete admin account |

---

# 12. Internal Cron Endpoints

**Auth required:** `X-Cron-Secret` header matching server config (not a JWT)

These endpoints are called by the scheduler (automatically in production via in-process goroutines, or externally via HTTP).

| Method | Path | Description | Schedule |
|--------|------|-------------|----------|
| POST | `/internal/cron/expire-points` | Expire points past their `expires_at` date; inserts negative ledger entries | Nightly at 00:00 UTC |
| POST | `/internal/cron/reset-streaks` | Activate grace windows and reset broken streaks | Daily at 00:05 UTC |
| POST | `/internal/cron/snapshot-leaderboard` | Save weekly leaderboard snapshot | Every Monday at 00:01 UTC |
| POST | `/internal/cron/close-expired-quizzes` | Set status to `closed` for quizzes past `ends_at` | Hourly |

### Response `200` (all)
```json
{ "message": "done" }
```

---

# 13. Health Check

## GET `/health`
**Auth required:** No

### Response `200`
```json
{ "status": "ok" }
```

---

# Error Reference

| Status | Code | Cause |
|--------|------|-------|
| 400 | - | Missing required field, validation failure, or business rule violation |
| 401 | `INVALID_CREDENTIALS` | Wrong email or password |
| 401 | `INVALID_TOKEN` | Expired or invalid refresh token |
| 403 | - | Insufficient role or resource ownership mismatch |
| 404 | - | Resource not found |
| 409 | `EMAIL_IN_USE` | Duplicate email on signup |
| 500 | - | Unexpected server error |

---

# Frontend Integration Tips

## Token Refresh Pattern
```
API Request → 401 → POST /auth/refresh → store new tokens → retry original request
```

## Quiz Attempt Lifecycle
```
POST /quizzes/{id}/attempts              → attempt_id + questions (no correct answers)
  for each question:
    POST /attempts/{id}/answers          → is_correct + points_earned + next combo_level
POST /attempts/{id}/complete             → final score + badges_awarded + question_breakdown
```

## Combo Level (speed_chain)
```
Start with combo_level = 0
After each correct answer: next combo_level = response.combo_level
After a wrong answer: reset combo_level = 0
```

## Confidence Level (confidence_based)
```
Send confidence_level with each confidence_based question answer.
Values: "very_confident" | "pretty_sure" | "not_sure"
Omitting defaults to "not_sure" multiplier.
```

## Pagination
```dart
// Dart / Flutter example
final res = await http.get(Uri.parse('$base/quizzes?page=1&limit=20'));
final body = jsonDecode(res.body);
final items = body['data'] as List;
final meta = body['meta'];  // { page, limit, total }
final hasMore = meta['page'] * meta['limit'] < meta['total'];
```

## CORS
The server allows origins configured via `ALLOWED_ORIGINS` env var (comma-separated). Set to `*` for wildcard.
Allowed methods: `GET, POST, PATCH, DELETE, OPTIONS`.
Allowed headers: `Authorization, Content-Type`.
