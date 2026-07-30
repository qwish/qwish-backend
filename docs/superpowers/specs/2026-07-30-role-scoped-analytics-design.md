# Role-Scoped Analytics — Institution Dashboard & Teacher Panel

Status: **approved, not implemented.** Written 2026-07-30.

Brings the customizable analytics dashboard already shipped in `qwish-super-admin`
to `qwish-institute-dashboard` and `qwish-teacher-panel`, with metrics scoped to
what each role is allowed to see.

Builds on `qwish-super-admin/docs/superpowers/specs/2026-07-30-super-admin-dashboard-design.md`
(implemented). That spec's metric catalog, SQL composition, guardrails and chart
kit are the starting point; this one generalizes the scope model and ports the
frontend.

---

## 1. What exists today

The backend already has a real metrics engine in `internal/domain/admin`:

| File | Role |
|---|---|
| `metrics_catalog.go` | 23 sources, 35 metric definitions, `Kind` (additive/rate/distinct), `SelectMetrics` with drop-and-cascade |
| `metrics_sql.go` | composes the bucketed series query and the un-bucketed totals query |
| `metrics_window.go` | window parsing, granularity, guardrails |
| `metrics.go` | `Series`, `Totals`, `Distributions`, `PointsLiability` |
| `metrics_handler.go` | `/admin/metrics{,/catalog}`, `/admin/distributions`, `/admin/points-liability` |
| `layouts.go` + `migrations/028_admin_dashboard_layouts.sql` | per-admin private dashboard layouts |

The frontend chart kit lives in `qwish-super-admin/src/components/{charts,widgets,analytics}`
and `src/lib/metrics.ts` — roughly 2,700 lines, hand-rolled on recharts.

Scoping today is a single optional `institution_id` query param, applied through
`source.ScopeCol` and `scopePredicate()`. Sources with no `ScopeCol` are dropped
from a scoped request with a reason, which the UI shows.

**The gap:** institution admins and teachers have no analytics surface at all,
and the one scope dimension that exists (institution) does not describe what a
teacher owns.

---

## 2. Decisions taken

| Question | Decision |
|---|---|
| What is "a teacher's data"? | **Both**, as a UI toggle: *My classes* (students in their groups) and *My quizzes* (quizzes they authored) |
| How much surface to port? | **Full parity** — metric picker, widget canvas, saved layouts |
| Code sharing across the three Next apps? | **Copy into each app.** No monorepo/workspace change |
| Where do non-admin layouts live? | **New `user_dashboard_layouts` table** keyed on `users(id)`; `admin_dashboard_layouts` untouched |

The copy decision accepts three drifting copies of the chart kit. It was chosen
over a workspace package because converting three independently-deployed apps
into a workspace changes every build and deploy path and forces a shared
design-token contract the apps do not currently have.

---

## 3. Subsystem A — Backend

### 3.1 Extract the shared engine

Move to a new package `internal/domain/metrics`:

```
internal/domain/admin/metrics_catalog.go   → internal/domain/metrics/catalog.go
internal/domain/admin/metrics_sql.go       → internal/domain/metrics/sql.go
internal/domain/admin/metrics_window.go    → internal/domain/metrics/window.go
internal/domain/admin/metrics.go           → internal/domain/metrics/service.go
```

Tests move with their code (`metrics_catalog_test.go`, `metrics_sql_test.go`,
`metrics_window_test.go`, `metrics_integration_test.go`, `testdb_test.go`).

`admin`, `institution` and `teacher` each keep a thin handler that resolves the
caller's scope and delegates. `admin/metrics_handler.go` stays where it is and
changes only its imports and its scope-resolution call.

The alternative — three copies of a 1,000-line SQL builder that must independently
agree on `rate` vs `additive` semantics — is how the same number ends up different
on two screens.

### 3.2 Scope becomes a kind, not a column

Replace `source.ScopeCol string` with a per-kind predicate map:

```go
type ScopeKind string

const (
    ScopeNone        ScopeKind = ""
    ScopeInstitution ScopeKind = "institution"
    ScopeClasses     ScopeKind = "teacher_classes"
    ScopeQuizzes     ScopeKind = "teacher_quizzes"
)

type Scope struct {
    Kind ScopeKind
    ID   string // institution id, or teacher user id
}

type source struct {
    Key      string
    From     string
    BucketOn string
    Where    string
    Scopes   map[ScopeKind]string // predicate template; %d is the param position
}
```

`scopePredicate(s, kind, n)` returns `fmt.Sprintf(s.Scopes[kind], n)`, or `""`
when the source cannot answer that kind. An unscoped request (`ScopeNone`) keeps
today's behaviour: no predicate, platform-wide.

The NULL-tolerant `($n::uuid IS NULL OR col = $n)` form is no longer needed —
scope presence is now known at query-build time, so an unscoped request omits the
predicate and the parameter entirely. Parameter numbering stays contiguous per
query (series: `$1` from, `$2` to-exclusive, `$3` trunc unit, `$4` scope id;
totals: `$1`, `$2`, `$3` scope id).

### 3.3 Scope predicates per source

The class-membership subquery, referred to below as `CLASS_MEMBERS($n)`:

```sql
SELECT gs.user_id
  FROM group_students gs
  JOIN group_teachers gt ON gt.group_id = gs.group_id
 WHERE gt.user_id = $n
```

| Source | `institution` | `teacher_classes` | `teacher_quizzes` |
|---|---|---|---|
| `attempts_done`, `attempts_start` | `u.institution_id = $n` | `u.id IN (CLASS_MEMBERS($n))` | `qa.quiz_id IN (SELECT id FROM quizzes WHERE created_by = $n)` |
| `responses` | `u.institution_id = $n` | `u.id IN (CLASS_MEMBERS($n))` | `qa.quiz_id IN (SELECT id FROM quizzes WHERE created_by = $n)` |
| `practice` | `u.institution_id = $n` | `u.id IN (CLASS_MEMBERS($n))` | — dropped (practice sessions carry no quiz link) |
| `signup` | `u.institution_id = $n` | `u.id IN (CLASS_MEMBERS($n))` | — dropped |
| `ledger` | `u.institution_id = $n` | `u.id IN (CLASS_MEMBERS($n))` | — dropped |
| `quiz_new`, `quiz_pub`, `quiz_appr`, `question_new` | `q.institution_id = $n` | — dropped | `q.created_by = $n` |
| `topicreq` | `tr.institution_id = $n` | — dropped | — dropped |
| `inst_new`, `inst_verified` | — | — | — |
| `report_new`, `report_done`, `audit`, `contact_new`, `contact_done`, `impersonation` | — | — | — |
| `badge`, `follow`, `pview`, `notif` | — | — | — |

Quiz-authoring sources are **dropped** under `teacher_classes` rather than
silently reinterpreted as "quizzes this teacher authored". A quiz has no class
linkage; answering a class-scoped question with an authorship-scoped number
produces a plausible figure that means something else. The UI shows the drop
reason and the teacher switches scope.

`SelectMetrics(ids []string, kind ScopeKind)` replaces the `scoped bool`
parameter. Its existing two-pass logic is unchanged: drop what the kind cannot
answer, then drop any derived metric whose dependency just went away.

Drop reasons become kind-specific, e.g.
`"not available when scoped to your classes"`,
`"not available when scoped to your quizzes"`,
`"not institution-scopable"`.

### 3.4 Unassigned-teacher fallback

`internal/domain/teacher/handler.go:24` (`hasGroupAssignments`) encodes PRD §5.4:
a teacher assigned to no group sees all institution students. Analytics must
honour the same rule, and must say when it does.

The teacher handler resolves the effective scope before building any query:

```
requested = teacher_classes AND no rows in group_teachers for this teacher
    → effective = institution, ID = the teacher's institution_id
```

The response carries both:

```json
"scope": {
  "requested": "teacher_classes",
  "effective": "institution",
  "reason": "no classes assigned — showing the whole institution"
}
```

`reason` is `null` when requested and effective agree. Without this field a
teacher reads institution-wide numbers as their own class's performance.

### 3.5 Routes

```
GET  /api/v1/institution/metrics/catalog
GET  /api/v1/institution/metrics              from,to,granularity,metrics,compare
GET  /api/v1/institution/distributions
GET  /api/v1/institution/points-liability
GET  /api/v1/institution/dashboard-layouts
POST /api/v1/institution/dashboard-layouts
PUT  /api/v1/institution/dashboard-layouts/order
PATCH  /api/v1/institution/dashboard-layouts/{layoutId}
DELETE /api/v1/institution/dashboard-layouts/{layoutId}

GET  /api/v1/teacher/metrics/catalog?scope=classes|quizzes
GET  /api/v1/teacher/metrics?scope=classes|quizzes   (default: classes)
GET  /api/v1/teacher/distributions?scope=classes|quizzes
GET  /api/v1/teacher/points-liability?scope=classes  (scope=quizzes → 400)
     …/teacher/dashboard-layouts, same five verbs
```

Registered inside the existing `r.Route("/institution", …)` and
`r.Route("/teacher", …)` blocks in `cmd/api/main.go`, which already carry
`mw.RequireRole("institution_admin")` and `mw.RequireRole("teacher")`.

`/metrics/catalog` is registered **before** `/metrics` in both blocks, matching
the comment at `cmd/api/main.go:411` — otherwise chi reads `catalog` as a
wildcard segment.

**Scope is never a client-supplied id.** The institution handler takes the
institution from `middleware.GetInstitutionID(r)`; the teacher handler takes the
teacher from `middleware.GetUserID(r)`. `scope=` selects only the *kind*. There
is no code path by which an institution admin can name another institution or a
teacher can name another teacher.

`points-liability` under `scope=quizzes` returns `400` rather than an empty
schedule: `points_ledger` has no quiz linkage, and an empty chart reads as
"nothing expiring" rather than "not answerable".

The catalog endpoint is scope-filtered — it returns only metrics answerable under
the requested kind, so the picker never offers something that will drop.

### 3.6 Layouts storage

New migration, `migrations/029_user_dashboard_layouts.sql`, mirroring 028:

```sql
CREATE TABLE IF NOT EXISTS user_dashboard_layouts (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 60),
  layout     JSONB NOT NULL,
  is_default BOOLEAN NOT NULL DEFAULT false,
  sort       INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, name)
);

CREATE INDEX IF NOT EXISTS idx_user_layouts_user
    ON user_dashboard_layouts (user_id, sort);

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_layouts_one_default
    ON user_dashboard_layouts (user_id)
    WHERE is_default;
```

`LayoutsService` is parameterized on table name and owner column at construction
(`NewLayoutsService(db, "admin_dashboard_layouts", "admin_id")` /
`NewLayoutsService(db, "user_dashboard_layouts", "user_id")`). Both identifiers
are compile-time constants supplied at wiring, never request data. Everything
else — the 256 KiB cap, the opaque-JSON contract, the `23505` → 409 mapping —
is unchanged.

Institution admins and teachers share the table; the owning `user_id` and the
role gate on the route separate them. A teacher's stored widgets each carry their
own `scope`, so one layout can mix class-scoped and quiz-scoped widgets.

### 3.7 Indexes

The new scope joins scan columns the platform has not previously filtered on.
Neither `019_performance_indexes.sql` nor `027_analytics_indexes.sql` covers any
of the three; all are new, added in migration 029 alongside 3.6:

- `group_students (user_id)` — the class-membership subquery reads it per source
- `group_teachers (user_id, group_id)` — drives `CLASS_MEMBERS`
- `quizzes (created_by)` — the `teacher_quizzes` predicate

`group_students` and `group_teachers` are keyed `(group_id, user_id)`, so the
existing primary keys do not serve a `user_id`-leading lookup.

### 3.8 Tests — `internal/domain/metrics/`

The migrated tests must keep passing unchanged apart from the package name and
the `SelectMetrics` signature. New coverage:

1. Every catalog metric, under each of the four scope kinds, either resolves to
   valid SQL or drops with a non-empty reason. Table-driven; a new metric with a
   missing `Scopes` entry fails the test rather than shipping silently.
2. SQL snapshot per scope kind for a representative metric set.
3. Parameter numbering is contiguous in both builders under every kind (series
   `$4`, totals `$3`).
4. Integration: a teacher scoped to `classes` sees attempts by their group's
   students and not by another teacher's group's students.
5. Integration: a teacher scoped to `quizzes` sees attempts on quizzes they
   authored and not on another author's quizzes in the same institution.
6. Integration: a teacher with zero `group_teachers` rows requesting
   `teacher_classes` gets `effective: "institution"` and a non-null `reason`.
7. Integration: an institution admin's request cannot be widened — no query
   param produces a row outside their `institution_id`.
8. Derived-metric cascade: under `teacher_quizzes`, `avg_points_per_attempt`
   drops because `points_issued` dropped, with a reason naming the dependency.

---

## 4. Subsystem B — Frontend

Applied twice, to `qwish-institute-dashboard` and `qwish-teacher-panel`. Both use
bun, Next 16, React 19, Tailwind v4, and already depend on recharts. Neither uses
shadcn/ui.

### 4.1 Dependencies

Add to both: `@dnd-kit/core`, `@dnd-kit/sortable`, `@dnd-kit/utilities` (widget
canvas reordering), `clsx` and `tailwind-merge` (the `cn()` helper the ported
components use). `recharts` is already present. The super-admin chart kit is
hand-rolled on recharts, so no shadcn dependency comes with it.

### 4.2 Files ported per app

```
src/components/charts/primitives.tsx
src/components/charts/chart-frame.tsx
src/components/charts/series-chart.tsx
src/components/charts/bar-charts.tsx
src/components/charts/stat-tile.tsx
src/components/charts/index.ts
src/components/widgets/canvas.tsx
src/components/widgets/registry.ts
src/components/widgets/widget-config.tsx
src/components/widgets/widget-view.tsx
src/components/analytics/control-row.tsx
src/lib/metrics.ts
src/app/(dashboard)/analytics/page.tsx
```

`src/lib/utils.ts` gains `cn()` if absent. `src/lib/metrics.ts` is repointed at
`/institution/*` or `/teacher/*` and goes through the app's existing
`apiClient.ts` / `useApi.ts` rather than super-admin's fetch layer.

A nav entry is added to `src/components/Sidebar.tsx` after Reports:
`{ href: "/analytics", label: "Analytics", icon: LineChart }`.

### 4.3 Theming

Both target apps ship an identical dark-only token set in `globals.css`
(`--color-canvas #0A0A0A`, `--color-card #18181B`, `--color-border #2A2A2E`,
`--color-foreground #FAFAFA`, `--color-muted #A1A1AA`, `--color-subtle #8B8B93`,
brand coral `#FF6B6B`, `--color-info #6366F1`). Porting is a retheme, not a
rewrite: super-admin's zinc/oklch utility classes are swapped for these tokens.

Neither app defines `--chart-*` slots. Five categorical slots are added to each
`globals.css`.

**The palette must be re-validated, not copied.** `ANALYTICS_SPEC.md` §4's
five-slot set was validated against super-admin's card surface `#14191C`; the
card here is `#18181B`. The `scripts/validate_palette.js` that spec cites no
longer exists in the repo, so validation follows the `dataviz` skill's colour
formula and validator at implementation time. Dark-only means one pass, not two.

Two constraints carry over verbatim:

- Coral `#FF6B6B` is brand/action only. A data series in coral reads as a button.
- Status mint/amber/red stay reserved for good/warning/critical, always with an
  icon and label, never colour alone.

### 4.4 Control row

One row above the charts. Filters never sit between charts.

| Control | Institute | Teacher |
|---|---|---|
| Date range (7d / 30d / 90d / custom) | yes | yes |
| Granularity (day / week / month) | yes | yes |
| Compare to previous | yes | yes |
| Metric picker (built from `/metrics/catalog`) | yes | yes |
| Export CSV of the resolved window | yes | yes |
| **Scope toggle — My classes / My quizzes** | — | yes |

All of it is URL state (`?from=&to=&g=&compare=&scope=`) so a view is linkable.
Changing `scope` refetches the catalog, because the set of answerable metrics
changes with it; metrics selected under the old scope that the new one cannot
answer are dropped from the selection and reported in the drop note (4.5).

The metric picker is always built from the catalog endpoint, never from a
hardcoded client list.

### 4.5 Two server signals that must render

Swallowing either of these produces a screen of numbers that quietly mean
something other than what the label says.

1. **`dropped[]`** — an inline note under the picker naming each dropped metric
   and its server-supplied reason ("Topic requests — not available when scoped to
   your quizzes").
2. **Teacher scope fallback** — when `scope.effective !== scope.requested`, a
   banner above the control row carrying `scope.reason`: "No classes assigned —
   showing the whole institution."

### 4.6 Widget canvas

Ported as-is: add/remove/resize widgets, dnd-kit reordering, per-widget metric and
chart-form configuration, named layouts saved against
`/{institution,teacher}/dashboard-layouts`, at most one default per user.

Teacher widgets persist their own `scope`, so a single layout can hold both
class-scoped and quiz-scoped widgets. Each widget renders its scope in its
header — two identically-titled widgets with different scopes are otherwise
indistinguishable.

### 4.7 Kind discipline

`lib/metrics.ts` already carries the rule and it survives the port: a `rate` or
`distinct` metric is **never** summed or averaged across buckets; window figures
come from the server's `totals`, which recomputes over the whole window. This is
the one contract violation that yields believable wrong numbers rather than an
error.

### 4.8 Documentation

- `qwish-backend/API_DOC.md` §12 gains the two role variants, the `scope`
  parameter, the `scope` response object, and the `400` on
  `points-liability?scope=quizzes`.
- `qwish-institute-dashboard/API_DOC.md` and `qwish-teacher-panel/API_DOC.md`
  each gain an "Analytics" section covering their own slice.

### 4.9 Out of scope

Neither Next app has a test suite configured, and none is added here. Backend
tests (3.8) carry the correctness burden; the frontend contract they protect is
the `kind` rule in 4.7 and the two signals in 4.5.

---

## 5. Build order

1. Extract `internal/domain/metrics`, tests moving with it, no behaviour change.
   Verify the super-admin analytics page still works before touching scope.
2. Scope kinds: `Scope`, `Scopes` map, `scopePredicate`, `SelectMetrics(ids, kind)`.
   Rewire the admin handler onto the new signature.
3. Index audit + `user_dashboard_layouts` migration.
4. Institution endpoints (scope fixed from JWT) — the simpler role, and it
   exercises the whole path.
5. Teacher endpoints: both scope kinds plus the unassigned fallback.
6. Institute frontend: chart kit port, retheme, palette validation, control row,
   stat tiles, charts.
7. Institute widget canvas + layouts.
8. Teacher frontend: same, plus the scope toggle and the fallback banner.
9. API docs.

Steps 1–2 unblock everything; the frontend cannot fake a series. Step 6 produces
the retheme both apps share, so step 8 is a copy rather than a second design pass.
