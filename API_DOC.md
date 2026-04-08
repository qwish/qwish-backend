# Qwish Backend — API Documentation

> **Base URL:** `https://<your-domain>/api/v1`
> **Content-Type:** `application/json` (unless noted otherwise)

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

## Badge Types

| Badge | Awarded When |
|-------|-------------|
| `first_quiz` | First quiz completed |
| `on_a_roll` | 7-day streak reached |
| `unstoppable` | 30-day streak reached |
| `top_10` | Ranked top 10 in institution |
| `perfect_score` | 100% correct answers on a quiz |
| `speed_demon` | Combo >= 3 on a speed_chain question |
| `sharp_mind` | 100% on confidence_based, all very_confident |
| `explorer` | Answered at least one of each of 7 question types |

---

## Question Types

`multiple_choice` · `true_false` · `fill_in_the_blank` · `short_answer` · `match_the_following` · `speed_chain` · `confidence_based`

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
All fields required.

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

### Errors
| Status | Code | Meaning |
|--------|------|---------|
| 400 | - | Missing fields or invalid referral code |
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
| 401 | `INVALID_TOKEN` | Expired or invalid token |

---

## POST `/auth/forgot-password`
**Auth required:** No

Triggers a Supabase password reset email. Always returns success.

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

### Response `200`
```json
{ "message": "logged out" }
```

---

## PATCH `/auth/referral-code`
**Auth required:** Yes

Switch the user to a different institution using a new referral code.

### Request Body
```json
{ "referral_code": "NEW-INST-CODE" }
```

### Response `200`
```json
{ "message": "institution updated" }
```

---

# 2. Users (Self)

**Auth required:** Yes for all.

## GET `/users/me`

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

---

## PATCH `/users/me`

### Request Body
```json
{ "display_name": "Ali" }
```

### Response `200`
Same shape as `GET /users/me`.

---

## DELETE `/users/me`

Soft-deletes and anonymizes the account.

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

### Response `200` (array)
```json
[
  { "badge_type": "first_quiz",    "earned": true,  "earned_at": "2024-01-16T09:00:00Z" },
  { "badge_type": "perfect_score", "earned": false, "earned_at": null }
]
```

---

## GET `/users/me/attempts`

### Query Params: `page`, `limit`

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
> `expiring_soon` is `null` if nothing expires within 30 days.

---

## GET `/users/me/points/ledger`

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
      "expires_at":    "2026-07-07T00:00:00Z",
      "created_at":    "2026-04-07T10:00:00Z"
    }
  ],
  "meta": { "page": 1, "limit": 20, "total": 50 }
}
```

**`reason` values:** `quiz_attempt` · `streak_bonus` · `admin_adjustment` · `expiry`

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

**Streak Milestones:** 7 days · 15 days · 30 days (bonus amounts set via Admin point economy)

---

## GET `/users/{userId}/profile`

Public profile (safe to show other users).

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

---

# 3. Quizzes

**Auth required:** Yes for all.

## GET `/quizzes`

Browse published quizzes for the user's institution.

### Query Params
| Param | Description |
|-------|-------------|
| `type` | `practice` \| `play_and_win` \| `assignment` |
| `saved` | `true` = only bookmarked quizzes |
| `page` | Default 1 |
| `limit` | Default 20, max 50 |

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

---

## GET `/quizzes/{quizId}`

### Response `200`
Same as list item, plus:
```json
{
  "rejection_reason": null,
  "question_types":   ["multiple_choice", "true_false"]
}
```

---

## POST `/quizzes/{quizId}/save`

Bookmark a quiz. Idempotent.

### Response `200`
```json
{ "message": "quiz saved" }
```

---

## DELETE `/quizzes/{quizId}/save`

### Response `200`
```json
{ "message": "quiz unsaved" }
```

---

## GET `/quizzes/{quizId}/share`

### Response `200`
```json
{ "deep_link": "quizapp://quiz/<quizId>" }
```

---

## POST `/quizzes/{quizId}/reports`

Report a quiz.

### Request Body
```json
{
  "reason":      "inappropriate_content",
  "description": "Optional extra context"
}
```

### Response `200`
```json
{ "message": "thanks — we'll review this" }
```

---

## POST `/quizzes/{quizId}/questions/{questionId}/reports`

Report a specific question. Same request body.

---

# 4. Attempts

**Auth required:** Yes for all.

## POST `/quizzes/{quizId}/attempts`

Start a new attempt. Returns questions without correct answers.

> **`play_and_win` allows only ONE completed attempt per user.**

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

---

## POST `/attempts/{attemptId}/answers`

Submit one answer. Call once per question.

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

**`answer` format by question type:**
| Type | Example |
|------|---------|
| `multiple_choice` | `"4"` |
| `true_false` | `true` |
| `fill_in_the_blank` | `"photosynthesis"` |
| `short_answer` | `"Light travels at 3e8 m/s"` |
| `match_the_following` | `{"A":"1","B":"3"}` |
| `speed_chain` | `"correct-option"` |
| `confidence_based` | `"answer text"` |

**`confidence_level` values:** `very_confident` · `confident` · `unsure`

### Response `200`
```json
{
  "is_correct":     true,
  "correct_answer": "4",
  "points_earned":  25,
  "combo_level":    3
}
```

---

## POST `/attempts/{attemptId}/complete`

Finalize the attempt. Awards points, updates streak, grants badges.

### Response `200`
```json
{
  "attempt_id":           "uuid",
  "score_pct":            80.0,
  "performance_badge":    "excellent",
  "points_delta":         200,
  "total_correct":        8,
  "total_questions":      10,
  "streak_bonus_awarded": 0,
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

**`performance_badge`:** `excellent` (>=75%) · `good` (50-74%) · `needs_work` (<50%)

---

## GET `/attempts/{attemptId}`

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

---

# 6. Topic Requests

**Auth required:** Yes

## POST `/topic-requests`

### Request Body
```json
{
  "topic":       "Quadratic Equations",
  "subject":     "Mathematics",
  "description": "Specifically vertex form"
}
```
(`topic` required, others optional)

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

All topic requests by the current student.

### Response `200`
Array of `TopicRequest` objects (same shape as above).

---

# 7. Parent

**Auth required:** Yes for all.

**Flow:** Student generates code → shares with parent → parent links → student accepts.

## POST `/parent/link-invite`
**Role:** `student`

### Response `200`
```json
{ "invite_code": "a1b2c3d4" }
```

---

## POST `/parent/link`

Parent submits invite code.

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

---

## POST `/parent/link/{linkId}/accept`
**Role:** `student`

### Response `200`
```json
{ "message": "parent link activated" }
```

---

## DELETE `/parent/link/{linkId}`

Either party can revoke.

### Response `200`
```json
{ "message": "link revoked" }
```

---

## GET `/parent/children`

### Response `200`
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

Must be the actively linked parent.

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

---

# 8. Upload

**Auth required:** Yes | **Role:** `teacher`, `super_admin`, or `moderator`

## POST `/upload/image`

**Content-Type:** `multipart/form-data`

### Request Fields
| Field | Type | Description |
|-------|------|-------------|
| `file` | file | JPEG, PNG, or WebP; max 5 MB |
| `prefix` | string | Storage folder (default: `quiz-images`) |

### Response `201`
```json
{ "url": "https://media.yourdomain.com/quiz-images/uuid.jpg" }
```

---

# 9. Teacher Routes

**Auth required:** Yes | **Role:** `teacher`

## GET `/teacher/quizzes`

### Query Params
`status` (optional filter), `page`, `limit`

### Response `200`
Paginated quiz list (same shape as `GET /quizzes`).

---

## POST `/teacher/quizzes`

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

(`title` required)

### Response `201`
Quiz object with `status: "draft"`.

---

## PATCH `/teacher/quizzes/{quizId}`

Must own quiz, must be in `draft` status. Same body as POST.

### Response `200`
Updated quiz object.

---

## POST `/teacher/quizzes/{quizId}/questions`

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

**`correct_answer` formats:**
| Type | Format |
|------|--------|
| `multiple_choice` | `"4"` |
| `true_false` | `true` or `false` |
| `fill_in_the_blank` | `"photosynthesis"` |
| `short_answer` | `"any text"` |
| `match_the_following` | `{"A":"1","B":"3"}` |
| `speed_chain` | `"correct-option"` |
| `confidence_based` | `"correct answer"` |

### Response `201`
Full question object.

---

## PATCH `/teacher/quizzes/{quizId}/questions/{questionId}`

Same body as add question.

### Response `200`
```json
{ "message": "question updated" }
```

---

## DELETE `/teacher/quizzes/{quizId}/questions/{questionId}`

### Response `200`
```json
{ "message": "question deleted" }
```

---

## POST `/teacher/quizzes/{quizId}/publish`

Must own quiz, must have at least 1 question.

- `visibility: institution` → `published` immediately
- `visibility: public` → `pending_approval`

### Response `200`
```json
{ "status": "published" }
```

---

## GET `/teacher/quizzes/{quizId}/results`

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

---

## GET `/teacher/topic-requests`

### Query Params
`status`, `page`, `limit`

---

## PATCH `/teacher/topic-requests/{requestId}`

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

| Method | Path | Description |
|--------|------|-------------|
| GET | `/institution/overview` | Stats for the institution |
| GET | `/institution/students` | Paginated student list |
| GET | `/institution/students/{userId}` | Student profile |
| PATCH | `/institution/students/{userId}/status` | Update status: `{status}` |
| GET | `/institution/teachers` | Paginated teacher list |
| GET | `/institution/teachers/{userId}` | Teacher profile |
| PATCH | `/institution/teachers/{userId}/status` | Update status |
| DELETE | `/institution/teachers/{userId}` | Remove teacher |
| GET | `/institution/groups` | All groups |
| POST | `/institution/groups` | Create group: `{name, description}` |
| GET | `/institution/groups/{groupId}` | Group details |
| PATCH | `/institution/groups/{groupId}` | Update group |
| DELETE | `/institution/groups/{groupId}` | Archive group |
| POST | `/institution/groups/{groupId}/students` | Add student: `{user_id}` |
| DELETE | `/institution/groups/{groupId}/students/{userId}` | Remove student |
| POST | `/institution/groups/{groupId}/teachers` | Add teacher: `{user_id}` |
| GET | `/institution/quizzes` | Browse published quizzes |
| GET | `/institution/quizzes/{quizId}` | Quiz detail |
| GET | `/institution/topic-requests` | Topic requests |
| PATCH | `/institution/topic-requests/{requestId}` | Update topic request |
| GET | `/institution/reports/student-performance` | Performance report |
| GET | `/institution/settings` | Institution settings |
| PATCH | `/institution/settings` | Update settings |
| PATCH | `/institution/settings/point-rules` | Update point multiplier rules |
| GET | `/institution/audit-log` | Admin audit log |

---

# 11. Super Admin Routes

**Auth required:** Yes | **Role:** `super_admin`, `moderator`, or `support_agent`

All endpoints under `/admin/`.

| Method | Path | Role | Description |
|--------|------|------|-------------|
| GET | `/admin/overview` | all | Platform-wide metrics |
| GET | `/admin/activity-feed` | all | Recent activity |
| GET | `/admin/institutions` | all | All institutions |
| GET | `/admin/institutions/queue` | all | Pending approval queue |
| GET | `/admin/institutions/{id}` | all | Institution detail |
| POST | `/admin/institutions/{id}/approve` | super_admin | Approve institution |
| POST | `/admin/institutions/{id}/reject` | super_admin | Reject: `{reason}` |
| POST | `/admin/institutions/{id}/suspend` | super_admin | Suspend |
| POST | `/admin/institutions/{id}/reactivate` | super_admin | Reactivate |
| POST | `/admin/institutions/{id}/reset-referral-codes` | super_admin | Regenerate referral codes |
| GET | `/admin/users` | all | All users |
| GET | `/admin/users/{userId}` | all | User detail |
| PATCH | `/admin/users/{userId}/suspend` | all | Suspend user |
| PATCH | `/admin/users/{userId}/reactivate` | all | Reactivate user |
| DELETE | `/admin/users/{userId}` | super_admin | Delete user |
| POST | `/admin/users/{userId}/points` | super_admin | Adjust points: `{amount, reason}` |
| POST | `/admin/users/{userId}/impersonate` | all | Start impersonation (returns session_id + access_token) |
| POST | `/admin/impersonation/{sessionId}/end` | all | End impersonation |
| GET | `/admin/quizzes/moderation-queue` | all | Quizzes pending approval |
| POST | `/admin/quizzes/{quizId}/approve` | super_admin, moderator | Approve quiz |
| POST | `/admin/quizzes/{quizId}/reject` | super_admin, moderator | Reject: `{reason}` |
| POST | `/admin/quizzes/{quizId}/unpublish` | super_admin | Unpublish quiz |
| GET | `/admin/reports` | all | Open content reports |
| POST | `/admin/reports/{reportId}/resolve` | all | Resolve report |
| GET | `/admin/point-economy` | super_admin | Point economy config |
| PATCH | `/admin/point-economy/{key}` | super_admin | Update config: `{value}` |
| POST | `/admin/announcements` | all | Broadcast: `{title, message, scope}` |
| GET | `/admin/audit-log` | super_admin | All admin actions log |
| GET | `/admin/admin-accounts` | super_admin | Admin accounts list |
| POST | `/admin/admin-accounts` | super_admin | Create admin: `{email, full_name, role}` |
| PATCH | `/admin/admin-accounts/{adminId}` | super_admin | Update admin account |
| DELETE | `/admin/admin-accounts/{adminId}` | super_admin | Delete admin account |

---

# 12. Health Check

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
| 400 | - | Missing field or validation failure |
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
API Request -> 401 -> POST /auth/refresh -> store new tokens -> retry original request
```

## Quiz Attempt Lifecycle
```
POST /quizzes/{id}/attempts          -> attempt_id + questions
  for each question:
    POST /attempts/{id}/answers      -> is_correct + points_earned
POST /attempts/{id}/complete         -> final score + badges awarded
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
