# Curriculum foundation — implemented API contract

Implemented locally on 5 September 2026. Migration: `058_curriculum_foundation.sql`.
This is the first backend work package from the institute implementation plan.
It is not the complete learning-foundations phase and has not been deployed.

## Scope

- Institute-owned academic years and curricula.
- Curriculum versions containing one subject/grade and an ordered chapter/concept tree.
- Atomic draft replacement with optimistic revision checks.
- Explicit publication; published metadata and content are immutable.
- Published curriculum assignment to existing groups for an academic year.
- Ended assignment history retained in the database.
- Teacher reads restricted to published versions assigned to classes they teach.
- Transactional audit entries and direct Supabase-client access blocked on all new tables.

No quiz, scoring, response, enrollment, points or existing analytics contract changes.
Question tagging, question/assessment snapshots, class offerings/terms, finer staff
capabilities, assignment delivery, evidence projections and the outbox are subsequent packages.

Curriculum versions are not assessment versions. Their immutability alone does not
make existing quiz results safe for historical before/after learning comparisons.

## Routes

All paths below have the `/api/v1` prefix and use the existing authenticated API
envelope `{ "success": true, "data": ..., "meta": ... }`. Mutation identities and
institution scope come from verified request context, never from the body.

| Method | Path | Result |
|---|---|---|
| GET | `/institution/academic-years` | Academic year array, newest start date first |
| POST | `/institution/academic-years` | Created year; HTTP 201 |
| GET | `/institution/curricula?page=1&limit=20` | Version summary array plus pagination metadata |
| POST | `/institution/curricula` | New curriculum and draft version; `{ "id": "VERSION_UUID" }`, HTTP 201 |
| POST | `/institution/curricula/{curriculumId}/versions` | New independent draft version; `{ "id": "VERSION_UUID" }`, HTTP 201 |
| GET | `/institution/curriculum-versions/{versionId}` | Version summary plus chapter/concept tree |
| PUT | `/institution/curriculum-versions/{versionId}` | Full draft replacement; `{ "revision": 2 }` |
| POST | `/institution/curriculum-versions/{versionId}/publish` | Publish draft; `{ "revision": 3 }` |
| GET | `/institution/groups/{groupId}/curricula` | Active class curriculum assignments |
| POST | `/institution/groups/{groupId}/curricula` | New assignment; `{ "id": "ASSIGNMENT_UUID" }`, HTTP 201 |
| DELETE | `/institution/groups/{groupId}/curricula/{assignmentId}` | End assignment; `{ "status": "ended" }` |
| GET | `/teacher/classes/{groupId}/curricula` | Active assignments for an active class this teacher teaches |
| GET | `/teacher/curriculum-versions/{versionId}` | Published tree actively assigned to at least one class this teacher teaches |

Institution routes require `institution_admin`. Teacher routes require `teacher`.
Both require a nonempty valid institution and user ID. Unassigned teachers do not
inherit institute-wide curriculum access. The curriculum index lists **versions**,
not one row per curriculum; group by `curriculum_id` in a future browser if needed.

## Write bodies

Create an academic year:

```json
{
  "name": "2026–27",
  "starts_on": "2026-06-01",
  "ends_on": "2027-05-31"
}
```

Dates are ISO dates, not timestamps. The end cannot precede the start. This initial
API creates/list years only; correcting a year and migrating its assignments needs
an explicit future workflow. No current-year inference or overlapping-year ban.

Create a curriculum and its first draft:

```json
{
  "name": "Grade 6 Mathematics",
  "label": "2026 edition",
  "subject": "Mathematics",
  "grade": "6",
  "chapters": [
    {
      "title": "Fractions",
      "concepts": [
        {
          "code": "FRACTIONS-01",
          "title": "Equivalent fractions",
          "learning_outcome": "Recognize and construct equivalent fractions."
        }
      ]
    }
  ]
}
```

Create another version with the same body **without `name`**, using its curriculum
ID. Copying a published version can use its text fields, but omit returned IDs,
status, revision and timestamps. New versions receive new chapter/concept IDs.

Replace a draft with the version fields above plus `revision`, omitting `name`.
PUT replaces the complete tree, not a partial patch. Draft chapter/concept IDs are
regenerated on replacement. Only published concept IDs are stable and eligible for
future assessment mappings. A stale revision fails instead of overwriting another
admin's draft. Initial revision is 1; saving and publishing each increment it.

Publish body:

```json
{ "revision": 2 }
```

At least one chapter and at least one concept per chapter are required to publish.
Empty drafts are permitted. Publication does not automatically assign a class.

Class assignment body:

```json
{
  "academic_year_id": "ACADEMIC_YEAR_UUID",
  "version_id": "PUBLISHED_VERSION_UUID"
}
```

The group, academic year and version must belong to the caller's institution.
Archived groups and draft versions cannot receive new assignments. One live
assignment per group/year/curriculum is allowed. A duplicate returns 409; to change
versions, end the old assignment explicitly and assign the replacement. This is
not an automatic historical reclassification. DELETE retains the original row and
sets `ended_at`; there is no hard-delete API.

## Read shapes

Year: `id`, `name`, `starts_on`, `ends_on`.

Version summary: `id`, `curriculum_id`, `name`, `label`, `subject`, `grade`, `status`,
`revision`, `published_at` (null on a draft, RFC3339 timestamp on publication).

Version detail adds `chapters`, each with `id`, `title`, `concepts`. A concept has
`id`, `code`, `title`, `learning_outcome`. Arrays are ordered by their stored
position and return `[]`, not null, when empty. Position is specified by array order
on writes; clients do not submit position numbers.

Assignment: `id` (assignment ID), `group_id`, `academic_year_id`,
`academic_year_name`, `version` (nested version summary, whose `id` is the version ID).

Pagination metadata: `page`, `limit`, `total`. Defaults are 1/20; limit is at most 50.
Teacher class reads and academic-year reads return arrays without pagination.

## Validation and failure behavior

- JSON bodies are capped at 1 MiB; unknown properties and multiple JSON values are rejected.
- Names, labels, grade, subject, chapter titles and concept codes/titles are trimmed and required.
- Name ≤160 characters; year name ≤120; version label/grade ≤80; subject ≤120.
- Chapter/concept titles ≤160; concept code ≤80; learning outcome ≤1,000.
- At most 50 chapters and 500 concepts per version.
- Concept codes must be unique across the submitted version, ignoring case.
- Dates must fall between years 1900 and 2200. NUL characters are rejected in text.
- Curriculum names are unique per institution; version labels per curriculum; year names per institution.

| HTTP | Error code | Meaning |
|---|---|---|
| 400 | `BAD_REQUEST` | Invalid JSON, fields, identifiers or pagination |
| 403 | `FORBIDDEN` | Wrong role or missing institution/user context |
| 404 | `NOT_FOUND` | Absent resource or not visible in this scope; also an ineligible assignment reference |
| 409 | `ACADEMIC_CONFLICT` | Duplicate name, label or active class assignment |
| 409 | `REVISION_CONFLICT` | Another save changed the draft |
| 409 | `VERSION_PUBLISHED` | A published version cannot be edited or published again |
| 422 | `CURRICULUM_INCOMPLETE` | Chapter/concept content is insufficient for publication |

Permission failures do not disclose whether another institute owns an identifier.
Transactions include audit writes, so failure to record the action rolls back the
mutation. Published content has database triggers protecting it even outside the
HTTP service. Cross-institute year/group/version references have composite foreign keys.

## Panel integration sequence

Institute dashboard:

1. Add an Academics area with academic-year and curriculum lists.
2. Build a chapter/concept editor around the draft GET/PUT contract and its revision.
3. Separate save and publish actions. Explain that published content requires a new version.
4. Add curriculum assignment to existing group detail, using explicit year/version choices.
5. Show revision conflicts with reload/review; never automatically retry a stale overwrite.

Teacher panel:

1. Add an assigned-curriculum section within class detail.
2. Read the nested published version and expose concepts in the future question-tagging selector.
3. Provide an honest unassigned state; a teacher cannot author curriculum definitions through this API.

The institute `apiFetch` retains pagination as `{data, meta}` while the teacher client
has a separate `apiFetchWithMeta` helper. Use each client's existing envelope behavior.
No frontend implementation or dependency changes are included in this backend package.

## Verification and rollout

```sh
go test ./...
TEST_DATABASE_URL='postgres:///scratch?host=/path/to/local/socket' go test -race -count=1 ./internal/domain/curriculum
```

The integration tests create and drop uniquely named schemas in a disposable test
database and apply the actual migration to minimal referenced core tables. Provision
the `anon` and `authenticated` database roles first. Do not use a production test URL.
Tests cover lifecycle, HTTP contracts, teacher/institute scope, duplicate assignments,
stale writes, concurrent save/publication, database immutability and blocked direct
client privileges. The entire migration chain was also applied to a fresh local
PostgreSQL database with a minimal Supabase `auth.uid()` stub and API roles.

Migrations run on normal API startup in this repository. Review the new migration
before deployment; no production migration was applied during implementation.
The existing application continues working without curriculum setup. Rollback can
remove the new routes while leaving these additive tables in place; do not drop
published curriculum data to roll back an application release.
