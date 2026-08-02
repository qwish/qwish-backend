# Student Management UI — Design Spec

Date: 2026-08-02
Status: Approved for planning
Scope: `qwish-institute-dashboard`, `qwish-teacher-panel`
Depends on: `2026-08-01-student-management-design.md` (shipped)

## Problem

The student-management backend from the 2026-08-01 spec is fully implemented and
both dashboards are wired to it, but the screens on top of it do not let anyone
run a roster.

- **The teacher panel is thin.** Its list renders name, email, points, streak,
  average score and last-active only. `GET /teacher/students` already returns
  `roll_number`, `grade` and `section`; the table drops them. There is no class
  filter, and no UI at all for `POST /teacher/classes/{classId}/students` or
  `DELETE /teacher/classes/{classId}/students/{userId}`, so a teacher cannot add
  or remove a student from their own class.
- **No bulk or roster workflow.** Neither app supports multi-select. Every
  lifecycle change is one row at a time. Import produces claim codes but the
  list gives no way to see or hand out a pending student's code, so the
  import → distribute → claim loop dead-ends.
- **It looks unfinished.** Raw tables, `window.confirm` for irreversible
  actions such as graduate and transfer-out, an ad-hoc `notice` string in place
  of toasts, and two panels styled inconsistently despite sharing a token set.
- **The institute detail page is weak.** `/students/[id]` cannot edit
  institution-owned enrollment fields, manage group membership, or show the
  pending edit-requests that belong to that student.

This is entirely a frontend gap. Every endpoint the work needs already exists.

## Approach

A shared roster kit, copied into each app rather than extracted into a package.

Both apps already duplicate `components/charts/` and `components/widgets/`
verbatim, so copying follows the convention already in the repo and needs no
build, workspace or deploy changes. Extracting a `@qwish/ui` package is the
right eventual move, but it restructures two apps to fix five files, and none
of that restructuring is visible on the screens this spec is about. Deduping
stays cheap whenever the UI stops churning.

Bulk actions are client-side sequential loops over the existing per-student
endpoints. No new backend routes.

## Design System

The two apps' `globals.css` token sets are identical for everything the kit
touches: `canvas`, `card`, `elevated`, `input`, `border`, `border-subtle`,
`foreground`, `muted`, `subtle`, `brand`, `success`, `warning`, `danger` and
their `-muted` variants. Primitives therefore port between the apps verbatim.

The teacher panel references `text-primary` and `focus:border-primary` in
several places. No `--color-primary` token exists in its `globals.css`, so those
classes render as nothing. They become `brand` as part of this work.

## Components — Shared Roster Kit

New directory `src/components/ui/` in each app, byte-identical between them.

| File | Purpose |
|---|---|
| `data-table.tsx` | Column definitions, optional row selection (per-row checkbox, header select-all, a "N selected" action bar), sort, sticky header, skeleton and empty slots, horizontal scroll for wide rosters |
| `drawer.tsx` | Right-side sheet. Escape and backdrop close, focus trap, returns focus to the trigger |
| `confirm-dialog.tsx` | Replaces `window.confirm`. Title, body, destructive variant, async pending state |
| `toast.tsx` | Provider plus `useToast()`. Replaces the ad-hoc `notice`/`actionError` state in both apps |
| `field.tsx` | Label, input or select, inline error message. Used by every roster form |
| `status-badge.tsx` | Moves from `components/students/` in the institute dashboard; copied into the teacher panel |

Roughly 600 lines in total. Each primitive takes props and owns no roster
knowledge, so it can be read and changed without reference to the screens using
it. The toast provider mounts in each app's `(dashboard)/layout.tsx`.

## Screens

### Institute roster — `/students`

- Selection column drives an action bar: Assign to group, Suspend, Reactivate,
  Graduate, Transfer out.
- Bulk execution is a sequential client-side loop over the existing per-student
  endpoints, showing a running count and, on completion, a list of the rows that
  failed and why. Partial success is reported, not hidden. Carries a
  `// ponytail: client-side loop, add a batch endpoint if rosters exceed ~500`
  comment.
- Filters extend from search and status to include grade, section and group.
- Claim codes become usable: `pending_claim` rows expose a copy-code action, and
  the toolbar offers "Download codes CSV" for the current filter, built in the
  browser from the list response. No new endpoint.
- `window.confirm` calls become `ConfirmDialog`, keeping the existing copy that
  states what actually happens to the student's account. Errors become toasts.

### Institute student detail — `/students/[id]`

- Header: name, status badge, roll number, grade, section, and the lifecycle
  actions from the list.
- An Enrollment card whose Edit opens a Drawer writing
  `PATCH /institution/enrollments/{enrollmentId}` for `roll_number`, `grade`,
  `section` and `admission_date`. A `ROLL_NUMBER_TAKEN` response renders as an
  inline error on the roll-number field, not a toast.
- A Groups card adding and removing membership through the existing group
  endpoints.
- Pending edit-requests for this student, listed inline with Approve and Reject,
  using the same `PATCH /institution/edit-requests/{id}` call as the
  `/edit-requests` page.

### Teacher roster — `/students`

- Roll no., Grade and Section columns, read from the payload that already
  carries them. Read-only, per the ownership table in the 2026-08-01 spec.
- A class filter populated from `listClasses()`. With a class selected, an "Add
  student" control and a per-row Remove appear, wired to the existing
  class-student endpoints; with no class selected the roster is read-only,
  because those endpoints are addressed by class.
- "Suggest a correction" survives, moved into a per-row action menu.
  `NOT_IN_YOUR_CLASS` renders as a toast naming the student.

### Teacher student detail — `/students/detail`

- The institute detail layout minus write access: identity and enrollment fields
  read-only, progress, and the classes the student shares with this teacher.
- Each institution-owned field carries a "Suggest a correction" action opening
  the existing propose-edit dialog pre-filled with that field.

## Error Handling

- API failures surface through the toast provider by default.
- Codes that belong to a specific input render inline instead:
  `ROLL_NUMBER_TAKEN` on the roll-number field, validation failures on their own
  fields. `NOT_IN_YOUR_CLASS` stays a toast, since it is about the actor rather
  than a field.
- Bulk loops never abort the whole run on one failure. They complete, then
  report which rows failed.

## Testing

Neither Next app has a test runner configured, and this spec does not add one.
Verification is:

- `bun run lint` and `bun run build` clean in both apps.
- A manual pass per screen: bulk suspend with one row forced to fail reports a
  partial result; a duplicate roll number shows an inline field error rather
  than a toast; a teacher's add/remove only appears with a class selected;
  claim-code CSV downloads match the active filter.

## Out of Scope

- Backend changes of any kind. Every endpoint this UI needs exists.
- Batch API endpoints. The client-side loop is the deliberate first version.
- Extracting a shared `@qwish/ui` package. Revisit once these screens settle.
- The super-admin console and numpie. Their student surfaces are separate work.
- Adding a test framework to the Next apps.
