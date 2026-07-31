# Class Management, Handover, and Weak-Domain Focus Groups

Status: **approved, not implemented.** Written 2026-07-31.

Gives teachers roster control over the classes they are assigned, lets them carve
those classes into weak-domain focus groups that receive targeted quizzes, and
gives institution admins a real handover flow for moving a class between
teachers.

---

## 1. What exists today

| Concern | State |
|---|---|
| Classes | `groups` (`id`, `institution_id`, `name`, `description`, `invite_code`, `archived_at`) |
| Membership | `group_students` and `group_teachers`, both keyed `(group_id, user_id)` |
| Teacher access | **Read-only**: `GET /teacher/classes`, `GET /teacher/classes/{classId}` |
| Class writes | All on the institution admin: create, update, archive, add/remove student, **add** teacher |
| Quiz targeting | `quizzes.group_id` already exists |
| Content taxonomy | `domains` / `subdomains` + `quizzes.domain` / `.subdomain` (migration 020) |
| Per-student weakness | Already computed — `user.GetInsightsBreakdown` returns `DomainPerf` per domain |
| Qwish score | `scoring.CalculateQwishScore`, fed by aggregates over **all** completed attempts |
| Leaderboards | Rank on the denormalised `users.total_points` column, not on attempts |

Three gaps: a teacher cannot add or remove a student from their own class; there
is no `RemoveTeacherFromGroup`, so a class cannot be taken off a teacher who has
left; and `groups` has no nesting, so a focus group has nowhere to live.

## 2. Decisions taken

| Question | Decision |
|---|---|
| Who creates classes? | **Institution admin.** Teachers manage the roster of classes assigned to them |
| Who creates focus groups? | **Teachers**, inside their own classes |
| How is a focus group populated? | Suggested from weak-domain data, then edited by hand; membership fixed thereafter |
| What can a teacher edit on a student? | Roster only — never profile, email or account status |
| Handover | Admin manages the full teacher list, with one owner per class |
| Remedial quiz → Qwish score | Counts at **50%** |
| Remedial quiz → points | Earns **50%**, into a separate balance that never affects rank |
| Remedial quiz → rankings | Excluded from every leaderboard |
| Remedial quiz → streak | **Counts.** Remedial work is real work |
| Who sees a remedial result? | The student, their teacher, their institution, their parent. Never peers |

---

## 3. Data model — migration 030

```sql
ALTER TABLE groups ADD COLUMN IF NOT EXISTS parent_id UUID REFERENCES groups(id) ON DELETE CASCADE;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS focus_domain    TEXT REFERENCES domains(slug);
ALTER TABLE groups ADD COLUMN IF NOT EXISTS focus_subdomain TEXT REFERENCES subdomains(slug);

ALTER TABLE group_teachers ADD COLUMN IF NOT EXISTS is_owner BOOLEAN NOT NULL DEFAULT false;
CREATE UNIQUE INDEX IF NOT EXISTS idx_group_one_owner
    ON group_teachers (group_id) WHERE is_owner;

ALTER TABLE quiz_attempts ADD COLUMN IF NOT EXISTS score_weight NUMERIC(3,2) NOT NULL DEFAULT 1.0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS remedial_points BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_groups_parent ON groups (parent_id) WHERE parent_id IS NOT NULL;
```

### 3.1 A focus group is a child group

`parent_id` set, `focus_domain` set. Nesting is **one level**: a group with a
parent may not itself become a parent. A pod-of-a-pod has no meaning and would
break every roster query that assumes two levels. Enforced in the service on
create, and by a trigger so a direct `UPDATE` cannot bypass it:

```sql
CREATE OR REPLACE FUNCTION groups_one_level() RETURNS trigger AS $$
BEGIN
  IF NEW.parent_id IS NOT NULL
     AND EXISTS (SELECT 1 FROM groups WHERE id = NEW.parent_id AND parent_id IS NOT NULL) THEN
    RAISE EXCEPTION 'a focus group cannot be nested inside another focus group';
  END IF;
  IF NEW.parent_id IS NOT NULL
     AND EXISTS (SELECT 1 FROM groups WHERE parent_id = NEW.id) THEN
    RAISE EXCEPTION 'a class with focus groups cannot itself become one';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_groups_one_level
  BEFORE INSERT OR UPDATE OF parent_id ON groups
  FOR EACH ROW EXECUTE FUNCTION groups_one_level();
```

### 3.2 Teacher access to a focus group is inherited, never stored

No `group_teachers` rows are written for a focus group. Its teachers are its
parent's teachers, resolved through the join.

This is what makes handover work: the admin moves the class, and every focus
group inside it follows with nothing to reassign and no chance of a pod being
left pointing at a teacher who has gone.

### 3.3 Remedial is stamped at completion, not derived at read time

An attempt is remedial when its quiz's `group_id` resolves to a group with
`parent_id IS NOT NULL`. That is resolved **once**, when the attempt completes,
into `quiz_attempts.score_weight = 0.5`.

Storing the weight rather than recomputing it means re-parenting, renaming or
archiving a focus group later never rewrites a student's history. A score that
changes retroactively because a teacher tidied up their classes is a bug the
student cannot explain and the teacher cannot see.

`score_weight` defaults to `1.0`, so every existing attempt keeps its meaning
with no backfill.

---

## 4. The scoring rules

### 4.1 Qwish score — half

`user.GetInsightsBreakdown` aggregates over every completed attempt. Each
aggregate gains the weight:

| Component | Today | With weight |
|---|---|---|
| Accuracy | `SUM(total_correct) / SUM(total_questions)` | `SUM(total_correct * w) / SUM(total_questions * w)` |
| Difficulty | `SUM(q.difficulty) FILTER (is_correct) / SUM(q.difficulty)` | both sums `* a.score_weight` |
| Speed | `AVG(speed_factor)` over correct responses | weighted mean, `w` per response's attempt |
| Activity | `COUNT(*)` of completed attempts | `SUM(score_weight)` |
| Consistency | streak | unchanged — the streak itself is unweighted |

`scoring.QwishScoreFactors` changes shape: `TotalCorrect`, `TotalQuestions` and
`ActivityCount` become `float64`, because a half-weighted attempt does not yield
whole questions. `CalculateQwishScore`'s arithmetic is unchanged — it already
divides floats — but every caller is updated with it. There are two:
`attempt.CompleteAttempt` and `user.GetInsightsBreakdown`.

The same weighting applies to the per-attempt `score_pct` written at completion:
a remedial attempt's own score is *not* halved — the student sees the real score
they earned. Only the contribution to the lifetime aggregate is halved.

### 4.2 Points — half, into a separate balance

At completion, a remedial attempt earns `finalPoints * 0.5`, rounded down. That
amount:

- **is** written to `points_ledger` with `reason = 'quiz_attempt'`, so the
  economy reports and expiry job see it like any other issuance;
- **is** added to `users.remedial_points`;
- **is not** added to `users.total_points`.

The student's balance is `total_points + remedial_points`, and their profile
shows the combined figure.

There is no student-facing spend path today — the only negative ledger writers
are the admin's `manual_adjustment` and the expiry job — so nothing needs to
choose which balance to debit yet. When redemption is built, it must debit
`remedial_points` first, so the rank-bearing balance is spent last. Recorded
here because that ordering is not obvious from either column's name.

### 4.3 Rankings — nothing to change

Every leaderboard already ranks on `users.total_points`, which remedial work
never touches. Rank exclusion is therefore a property of *where the points
land*, not a filter each future query has to remember to apply. That is the
whole reason for the separate column.

### 4.4 Streak — nothing to change

The streak keys on completion date, not on points or weight. A student who does
only remedial work keeps their streak, which is the intended message.

### 4.5 Visibility

| Audience | Sees a remedial result? |
|---|---|
| The student | Yes — score, breakdown, per-question review, exactly as any quiz |
| Their teacher | Yes |
| Institution admin | Yes |
| Parent | Yes |
| Peers | **No** — suppressed from class result lists and public profile activity |

Peer suppression is `WHERE score_weight = 1` on the surfaces that show one
student's attempts to another: `quiz.TeacherResults` is a teacher surface and
keeps them; `user.GetPublicProfile` activity and any class-facing result list
drop them.

---

## 5. API

### 5.1 Teacher — roster and focus groups

```
POST   /api/v1/teacher/classes/{classId}/students            {user_id}
DELETE /api/v1/teacher/classes/{classId}/students/{userId}
GET    /api/v1/teacher/classes/{classId}/weak-domains        ?threshold=60 (default 60)
GET    /api/v1/teacher/classes/{classId}/subgroups
POST   /api/v1/teacher/classes/{classId}/subgroups           {name, focus_domain, focus_subdomain?, student_ids[]}
PATCH  /api/v1/teacher/subgroups/{subgroupId}                {name?, focus_domain?, focus_subdomain?}
DELETE /api/v1/teacher/subgroups/{subgroupId}                archives
POST   /api/v1/teacher/subgroups/{subgroupId}/students       {user_id}
DELETE /api/v1/teacher/subgroups/{subgroupId}/students/{userId}
```

Every one authorises the same way: the caller must hold a `group_teachers` row
for the class, or for the focus group's parent. **The PRD §5.4 unassigned-teacher
fallback grants read of institution students; it never grants write.** A teacher
with no class assignments can see the institution's students and can modify
nobody's roster.

`GET /weak-domains` returns, for the class, each domain with its average score,
question count and low-sample flag, plus the students below `threshold` with
their own per-domain figures. It is the seeding query for the create flow and
reuses the aggregation already written for `GetInsightsBreakdown`.

### 5.2 Institution — handover

```
GET    /api/v1/institution/groups/{groupId}/teachers
POST   /api/v1/institution/groups/{groupId}/teachers          {user_id, is_owner?}
DELETE /api/v1/institution/groups/{groupId}/teachers/{userId}
PATCH  /api/v1/institution/groups/{groupId}/owner             {user_id}
```

`DELETE .../teachers/{userId}` does not exist today and is the reason a class
cannot currently be taken off a departed teacher.

---

## 6. Edge cases

| Case | Behaviour |
|---|---|
| Focus group inside a focus group | `400`, message names the one-level rule |
| Student added to a focus group but not in its parent class | `409`, naming the parent |
| Student removed from a class | Removed from every focus group of that class, in one transaction |
| Class archived | Focus groups cascade-archive with it |
| Removing a class's last teacher | `409` — a class always has exactly one owner. Transfer first, or archive the class |
| Removing the owner | `409` until ownership is transferred. Handover is add → transfer → remove |
| Handover with focus groups | Nothing to do — pod access is inherited from the parent (§3.2) |
| Same student in two focus groups of one class | Allowed. Two domains are two different weaknesses |
| Student leaves a focus group | Past attempts keep `score_weight = 0.5`. History does not move |
| Focus group archived with quizzes assigned | Soft archive. Quizzes keep their `group_id`; attempts keep their weight |
| Teacher removed from a class | Loses roster and focus-group access immediately — every query scopes through `group_teachers` |
| Quiz targeted at a focus group by a teacher who does not own the parent | `403` on quiz create/update |
| A student in the class but in no focus group | Unaffected; ordinary quizzes are unweighted |

---

## 7. Frontend

### 7.1 Teacher panel — `qwish-teacher-panel`

`/classes` stays the list. New `/classes/[id]` with three regions:

1. **Roster** — table of students with their overall score; add via a search
   across institution students; remove with a confirm that names what is lost.
2. **Weak domains** — the class's per-domain averages, ordered weakest first,
   each row expanding to the students below the threshold. Low-sample domains
   (< 10 answered questions) are labelled, not ranked.
3. **Focus groups** — cards showing name, target domain, member count, and the
   quizzes assigned. Creating one is a single flow: pick a domain and threshold →
   matching students appear pre-ticked with their scores → untick, add others,
   name it → create.

Assigning a quiz to a focus group reuses the existing quiz create/edit screen
with the group preselected.

### 7.2 Institute dashboard — `qwish-institute-dashboard`

`/groups/[id]` gains a **Teachers** section: the list with an owner badge, add,
remove, and transfer ownership. Transfer is the handover, and its confirmation
names exactly what moves — the roster, the focus groups, and the class's quizzes.

### 7.3 Out of scope

Neither Next app has component tests and none are added. Backend tests carry the
correctness burden (§8).

---

## 8. Tests

Unit, no database:

1. One-level nesting rejected by the service before it reaches the trigger.
2. Weighted Qwish aggregates: a half-weighted attempt moves the score by half
   what a full one does, for identical answers.
3. Points split: `finalPoints * 0.5`, rounded down, and never into `total_points`.

Integration, `TEST_DATABASE_URL`:

4. Removing a student from a class removes them from its focus groups, atomically.
5. Archiving a class archives its focus groups.
6. Removing the owner is refused; transfer then remove succeeds.
7. Handover: the new teacher can read the class's focus groups without any
   `group_teachers` row being written for them.
8. **The load-bearing one:** a remedial attempt raises `remedial_points`, leaves
   `total_points` unchanged, leaves the student's leaderboard rank unchanged, and
   moves the Qwish score by half what the same attempt on an unweighted quiz
   would.
9. A teacher with no class assignments can list institution students and cannot
   modify any roster.
10. The one-level trigger rejects a direct `UPDATE groups SET parent_id`.

---

## 9. Plan decomposition

This spans three repos and produces three implementation plans, in this order:

1. `qwish-backend` — migration, scoring, and every endpoint. Nothing downstream
   works without it.
2. `qwish-teacher-panel` — `/classes/[id]`, roster, weak domains, focus groups.
3. `qwish-institute-dashboard` — the Teachers section and handover.

## 10. Build order

1. Migration 030 + the nesting trigger.
2. `score_weight` stamping at attempt completion, and the points split.
3. Weighted Qwish aggregates.
4. Teacher roster endpoints.
5. `weak-domains` query.
6. Focus-group endpoints, with the cascade and nesting rules.
7. Institution teacher-list endpoints, with the owner invariant.
8. Teacher panel `/classes/[id]`.
9. Institute dashboard Teachers section.
10. API docs in all three repos.

Steps 1–3 are the ones that change existing behaviour for every student; they
land and are verified before any new surface is built on them.
