# Role-Scoped Analytics — Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serve the existing analytics engine to institution admins and teachers with metrics scoped to what each role owns, without loosening what a super admin sees.

**Architecture:** The metrics engine moves out of `internal/domain/admin` into a shared `internal/domain/metrics` package. Its single optional `institution_id` filter is generalized into a `Scope` (kind + id) resolved from the caller's JWT, with each SQL source declaring a predicate per scope kind. Sources that cannot answer a kind cause their metrics to be dropped with a reason rather than answered wrongly. Institution and teacher get their own thin handlers plus a `user_dashboard_layouts` table for saved layouts.

**Tech Stack:** Go 1.26, chi v5, pgx/v5, Postgres (Supabase), file-based migrations run on boot.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-07-30-role-scoped-analytics-design.md`. Read it before Task 1.
- Migrations are **append-only and ordered by filename**. The next number is `029`. Never edit a shipped migration.
- Scope ids are **never** read from request query params for institution or teacher. Institution comes from `middleware.GetInstitutionID(r)`, teacher from `middleware.GetUserID(r)`. The `scope=` param selects only the *kind*.
- Postgres rejects a statement that skips a parameter number. Placeholder numbering must stay contiguous per query: series `$1` from, `$2` to-exclusive, `$3` date_trunc unit, `$4` scope id; totals `$1`, `$2`, `$3` scope id.
- Bucketing timezone is `Asia/Kolkata`, via the existing `IST = time.FixedZone("IST", 5*60*60+30*60)`.
- A `rate` or `distinct` metric is never summed or averaged across buckets. Window figures come from `Totals`, which recomputes over the whole window.
- Existing behaviour for `/admin/*` must not change. Every existing test keeps passing with no assertion edits.
- Build/test commands: `go build ./...`, `go test ./...`. Database integration tests are skipped unless `TEST_DATABASE_URL` points at a scratch database (see `openTestDB`).
- Work on branch `analytics-role-scoped` (already created, holds the spec commit).

---

## File Structure

**New package `internal/domain/metrics/`** — the shared engine. One responsibility per file:

| File | Responsibility |
|---|---|
| `catalog.go` | sources, scope kinds, metric definitions, `SelectMetrics` |
| `sql.go` | series and totals query composition |
| `window.go` | window parsing, granularity, caps |
| `service.go` | `Series`, `Totals`, `Distributions`, `PointsLiability` |
| `scope.go` | `Scope`, `ScopeKind`, predicate rendering, arg building |
| `catalog_test.go`, `sql_test.go`, `window_test.go`, `scope_test.go` | unit tests |
| `integration_test.go`, `fixtures_test.go`, `testdb_test.go` | database tests |

**New shared handler `internal/domain/metrics/handler.go`** — one `Handler` serving all three roles, parameterized by a `ScopeResolver` func supplied at wiring. This is what stops institution and teacher handlers from re-deriving window parsing, compare logic and the response envelope.

**Modified:**

| File | Change |
|---|---|
| `internal/domain/admin/metrics*.go` | deleted; the admin route wires the shared handler with a super-admin resolver |
| `internal/domain/admin/layouts.go` | `LayoutsService`/`LayoutsHandler` parameterized on table + owner column |
| `cmd/api/main.go` | wire three metrics handlers and two layouts handlers; register institution and teacher routes |
| `migrations/029_user_dashboard_layouts.sql` | new table + three scope indexes |
| `API_DOC.md` | §12 extended |

---

## Task 1: Extract the engine into `internal/domain/metrics` (no behaviour change)

**Files:**
- Create: `internal/domain/metrics/{catalog,sql,window,service}.go` (moved content)
- Create: `internal/domain/metrics/{catalog_test,sql_test,window_test,integration_test,testdb_test}.go` (moved content)
- Delete: `internal/domain/admin/metrics_catalog.go`, `metrics_sql.go`, `metrics_window.go`, `metrics.go`, and their four test files plus `testdb_test.go`
- Modify: `internal/domain/admin/metrics_handler.go`, `internal/domain/admin/layouts_integration_test.go`, `cmd/api/main.go`

**Interfaces:**
- Consumes: nothing.
- Produces: package `metrics` exporting `Catalog()`, `Lookup`, `SelectMetrics`, `MetricDef`, `Kind`, `DroppedMetric`, `Source`, `SourceKeys`, `BuildSeriesQuery`, `BuildTotalsQuery`, `Window`, `Granularity`, `Granularities`, `ParseGranularity`, `ResolveWindow`, `MaxWindowDays`, `IST`, `MetricsService`, `NewMetricsService`. Signatures are unchanged in this task.

- [ ] **Step 1: Move the four source files and their tests**

```bash
cd qwish-backend
mkdir -p internal/domain/metrics
git mv internal/domain/admin/metrics_catalog.go  internal/domain/metrics/catalog.go
git mv internal/domain/admin/metrics_sql.go      internal/domain/metrics/sql.go
git mv internal/domain/admin/metrics_window.go   internal/domain/metrics/window.go
git mv internal/domain/admin/metrics.go          internal/domain/metrics/service.go
git mv internal/domain/admin/metrics_catalog_test.go     internal/domain/metrics/catalog_test.go
git mv internal/domain/admin/metrics_sql_test.go         internal/domain/metrics/sql_test.go
git mv internal/domain/admin/metrics_window_test.go      internal/domain/metrics/window_test.go
git mv internal/domain/admin/metrics_integration_test.go internal/domain/metrics/integration_test.go
```

`testdb_test.go` is used by both `metrics_integration_test.go` and `layouts_integration_test.go`. Copy it rather than moving it — both packages need an `openTestDB`:

```bash
cp internal/domain/admin/testdb_test.go internal/domain/metrics/testdb_test.go
```

- [ ] **Step 2: Change the package clause in all eight moved files**

In each file under `internal/domain/metrics/`, replace the first line `package admin` with `package metrics`.

```bash
sed -i '' '1s/^package admin$/package metrics/' internal/domain/metrics/*.go
```

- [ ] **Step 3: Point the admin metrics handler at the new package**

`internal/domain/admin/metrics_handler.go` — add the import and qualify every moved identifier. The file keeps its current logic; only names change.

```go
import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/metrics"
	"github.com/qwish/backend/internal/middleware"
)

type MetricsHandler struct{ svc *metrics.MetricsService }

func NewMetricsHandler(db *pgxpool.Pool) *MetricsHandler {
	return &MetricsHandler{svc: metrics.NewMetricsService(db)}
}
```

The identifiers needing a `metrics.` prefix in this file: `Catalog()`, `Granularities`, `ResolveWindow`, `SelectMetrics`, `dateLayout`. `dateLayout` is unexported, so export it from the new package as `metrics.DateLayout`:

In `internal/domain/metrics/window.go`, rename the constant and its uses within the package:

```go
// DateLayout is the wire format for every date the analytics API accepts or
// returns. Exported because handlers in other packages format with it.
const DateLayout = "2006-01-02"
```

```bash
# inside the metrics package only
sed -i '' 's/\bdateLayout\b/DateLayout/g' internal/domain/metrics/*.go
```

- [ ] **Step 4: Build and run the full suite**

Run: `go build ./... && go test ./...`
Expected: PASS. Database tests report `SKIP` unless `TEST_DATABASE_URL` is set. If it is set, they pass unchanged — this task changed no SQL.

- [ ] **Step 5: Commit**

```bash
git add -A internal/domain/metrics internal/domain/admin cmd/api
git commit -m "refactor: extract analytics engine into internal/domain/metrics

Moves the catalog, SQL builders, window logic and service out of the admin
package so institution and teacher handlers can share one implementation.
No behaviour change: the admin handler is repointed and every existing test
passes unedited."
```

---

## Task 2: `Scope` type and per-source scope predicates

**Files:**
- Create: `internal/domain/metrics/scope.go`
- Create: `internal/domain/metrics/scope_test.go`
- Modify: `internal/domain/metrics/catalog.go` (`source` struct, `sources` map, `SelectMetrics`)
- Modify: `internal/domain/metrics/catalog_test.go` (the `SelectMetrics` call sites)

**Interfaces:**
- Consumes: package `metrics` from Task 1.
- Produces:
  - `type ScopeKind string` with constants `ScopeNone`, `ScopeInstitution`, `ScopeClasses`, `ScopeQuizzes`
  - `type Scope struct { Kind ScopeKind; ID string }`
  - `func (s Scope) Active() bool`
  - `func ParseScopeKind(string) (ScopeKind, error)` — accepts `"classes"` → `ScopeClasses`, `"quizzes"` → `ScopeQuizzes`, `""` → `ScopeClasses` (the teacher default)
  - `source.Scopes map[ScopeKind]string`
  - `func scopePredicate(s source, kind ScopeKind, n int) string`
  - `func SelectMetrics(ids []string, kind ScopeKind) ([]MetricDef, []DroppedMetric, error)` — the `scoped bool` parameter is replaced
  - `MetricDef.Scopes []ScopeKind` (JSON `scopes`) replacing `Scopable bool`

- [ ] **Step 1: Write the failing tests**

Create `internal/domain/metrics/scope_test.go`:

```go
package metrics

import (
	"strings"
	"testing"
)

func TestParseScopeKind(t *testing.T) {
	cases := []struct {
		in      string
		want    ScopeKind
		wantErr bool
	}{
		{"", ScopeClasses, false},
		{"classes", ScopeClasses, false},
		{"quizzes", ScopeQuizzes, false},
		{"institution", "", true}, // not a teacher-selectable kind
		{"nonsense", "", true},
	}
	for _, c := range cases {
		got, err := ParseScopeKind(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseScopeKind(%q): want error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseScopeKind(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseScopeKind(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Every source must either declare a predicate for a kind or omit the key
// entirely. A source that silently answers a kind it cannot scope is how a
// teacher ends up reading institution-wide numbers as their own.
func TestEverySourceScopeIsRenderable(t *testing.T) {
	kinds := []ScopeKind{ScopeInstitution, ScopeClasses, ScopeQuizzes}
	for _, key := range SourceKeys() {
		s, _ := Source(key)
		for _, k := range kinds {
			tmpl, ok := s.Scopes[k]
			if !ok {
				continue
			}
			if !strings.Contains(tmpl, "%d") {
				t.Errorf("source %q kind %q: predicate %q has no %%d placeholder",
					key, k, tmpl)
			}
			got := scopePredicate(s, k, 4)
			if !strings.Contains(got, "$4") {
				t.Errorf("source %q kind %q: rendered %q, want a $4", key, k, got)
			}
			if strings.Contains(got, "%") {
				t.Errorf("source %q kind %q: rendered %q still holds a verb", key, k, got)
			}
		}
	}
}

func TestScopePredicateEmptyWhenUnsupported(t *testing.T) {
	s, _ := Source("audit") // moderation source, scopable by nothing
	for _, k := range []ScopeKind{ScopeInstitution, ScopeClasses, ScopeQuizzes} {
		if got := scopePredicate(s, k, 4); got != "" {
			t.Errorf("audit under %q: got %q, want empty", k, got)
		}
	}
	if got := scopePredicate(s, ScopeNone, 4); got != "" {
		t.Errorf("audit unscoped: got %q, want empty", got)
	}
}

// An unscoped request must not emit a predicate at all — the old
// "($n IS NULL OR col = $n)" form is gone, so a nil arg has nowhere to bind.
func TestScopeNoneEmitsNoPredicate(t *testing.T) {
	s, _ := Source("attempts_done")
	if got := scopePredicate(s, ScopeNone, 4); got != "" {
		t.Errorf("attempts_done unscoped: got %q, want empty", got)
	}
}

func TestSelectMetricsDropsByKind(t *testing.T) {
	// topic_requests has an institution column but no teacher linkage.
	_, dropped, err := SelectMetrics([]string{"topic_requests"}, ScopeClasses)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}
	if len(dropped) != 1 || dropped[0].ID != "topic_requests" {
		t.Fatalf("want topic_requests dropped, got %+v", dropped)
	}
	if dropped[0].Reason == "" {
		t.Error("dropped metric carries no reason")
	}

	// Same metric survives an institution scope.
	sel, dropped, err := SelectMetrics([]string{"topic_requests"}, ScopeInstitution)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}
	if len(dropped) != 0 || len(sel) != 1 {
		t.Fatalf("institution scope: sel=%d dropped=%+v", len(sel), dropped)
	}
}

// A derived metric whose dependency drops must drop too, naming the dependency.
func TestDerivedMetricCascadesOnKind(t *testing.T) {
	_, dropped, err := SelectMetrics([]string{"avg_points_per_attempt"}, ScopeQuizzes)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}
	var found bool
	for _, d := range dropped {
		if d.ID == "avg_points_per_attempt" {
			found = true
			if !strings.Contains(d.Reason, "points_issued") {
				t.Errorf("reason %q does not name the missing dependency", d.Reason)
			}
		}
	}
	if !found {
		t.Fatalf("avg_points_per_attempt should drop under quizzes scope; dropped=%+v", dropped)
	}
}

// Unscoped selection still returns the whole catalog.
func TestSelectMetricsUnscopedDropsNothing(t *testing.T) {
	sel, dropped, err := SelectMetrics(nil, ScopeNone)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("unscoped: dropped %+v", dropped)
	}
	if len(sel) != len(Catalog()) {
		t.Errorf("unscoped: selected %d of %d", len(sel), len(Catalog()))
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/domain/metrics/ -run 'Scope|SelectMetrics|Derived' -v`
Expected: FAIL — compile error, `undefined: ScopeKind`, `undefined: ParseScopeKind`.

- [ ] **Step 3: Write `scope.go`**

Create `internal/domain/metrics/scope.go`:

```go
package metrics

import "fmt"

// ScopeKind names the dimension a request is filtered on. The kind decides
// which sources can answer at all: a source with no predicate for the active
// kind has its metrics dropped with a reason rather than answered platform-wide.
type ScopeKind string

const (
	ScopeNone        ScopeKind = ""                // platform-wide; super admin only
	ScopeInstitution ScopeKind = "institution"     // one institution
	ScopeClasses     ScopeKind = "teacher_classes" // students in a teacher's groups
	ScopeQuizzes     ScopeKind = "teacher_quizzes" // quizzes a teacher authored
)

// Scope is a resolved filter: the kind, plus the id it filters on. ID is empty
// exactly when Kind is ScopeNone.
type Scope struct {
	Kind ScopeKind
	ID   string
}

func (s Scope) Active() bool { return s.Kind != ScopeNone && s.ID != "" }

// Arg is the value bound to the scope placeholder, or nil when no placeholder
// was emitted. Callers append it only when Active.
func (s Scope) Arg() any {
	if !s.Active() {
		return nil
	}
	return s.ID
}

// ParseScopeKind reads the teacher-facing `scope` query parameter. Only the two
// teacher kinds are selectable: institution scope is derived from the token, so
// accepting it here would let a teacher widen their own view.
func ParseScopeKind(s string) (ScopeKind, error) {
	switch s {
	case "", "classes":
		return ScopeClasses, nil
	case "quizzes":
		return ScopeQuizzes, nil
	default:
		return "", fmt.Errorf("unknown scope %q; valid values are classes, quizzes", s)
	}
}

// DropReason is the message shown to the caller when a metric cannot answer the
// active scope. Phrased for the role that will read it.
func DropReason(kind ScopeKind) string {
	switch kind {
	case ScopeInstitution:
		return "not institution-scopable"
	case ScopeClasses:
		return "not available when scoped to your classes"
	case ScopeQuizzes:
		return "not available when scoped to your quizzes"
	default:
		return "not scopable"
	}
}

// scopePredicate renders a source's filter for the active kind at parameter
// position n, or "" when the source cannot answer that kind.
func scopePredicate(s source, kind ScopeKind, n int) string {
	if kind == ScopeNone {
		return ""
	}
	tmpl, ok := s.Scopes[kind]
	if !ok {
		return ""
	}
	return fmt.Sprintf(tmpl, n)
}

// classMembers is the set of students taught by $n. Used by every
// student-centred source under ScopeClasses.
const classMembers = `SELECT gs.user_id
             FROM group_students gs
             JOIN group_teachers gt ON gt.group_id = gs.group_id
            WHERE gt.user_id = $%d`

// authoredQuizzes is the set of quizzes written by $n.
const authoredQuizzes = `SELECT id FROM quizzes WHERE created_by = $%d`
```

- [ ] **Step 4: Rewrite the `source` struct and the `sources` map**

In `internal/domain/metrics/catalog.go`, replace the `ScopeCol string` field with a per-kind map:

```go
// source is one subquery in the composed series query. Metrics sharing a source
// share its scan, which is why requesting four metrics does not run thirty-five
// subqueries.
type source struct {
	Key      string // join alias — must be unique across sources
	From     string // FROM clause, including any scoping join
	BucketOn string // the timestamp column bucketed on
	Where    string // extra predicate, or "" for none
	// Scopes holds one predicate template per answerable scope kind. The
	// template carries a single %d for the parameter position. A missing key
	// means the source cannot answer that kind, and its metrics drop.
	Scopes map[ScopeKind]string
}
```

Then rewrite each entry. The full map — replace the existing `var sources` block wholesale:

```go
var sources = map[string]source{
	// Completions bucket on completed_at. Scoped by the taker's institution,
	// the taker's class membership, or the quiz's author.
	"attempts_done": {
		Key:      "ad",
		From:     "quiz_attempts qa JOIN users u ON u.id = qa.user_id",
		BucketOn: "qa.completed_at",
		Where:    "qa.status = 'completed'",
		Scopes: map[ScopeKind]string{
			ScopeInstitution: "u.institution_id = $%d",
			ScopeClasses:     "u.id IN (" + classMembers + ")",
			ScopeQuizzes:     "qa.quiz_id IN (" + authoredQuizzes + ")",
		},
	},
	// Starts and abandons bucket on started_at — an abandoned attempt never
	// gets a completed_at.
	"attempts_start": {
		Key:      "ast",
		From:     "quiz_attempts qa JOIN users u ON u.id = qa.user_id",
		BucketOn: "qa.started_at",
		Scopes: map[ScopeKind]string{
			ScopeInstitution: "u.institution_id = $%d",
			ScopeClasses:     "u.id IN (" + classMembers + ")",
			ScopeQuizzes:     "qa.quiz_id IN (" + authoredQuizzes + ")",
		},
	},
	"responses": {
		Key:      "qr",
		From:     "question_responses qr JOIN quiz_attempts qa ON qa.id = qr.attempt_id JOIN users u ON u.id = qa.user_id",
		BucketOn: "qr.submitted_at",
		Scopes: map[ScopeKind]string{
			ScopeInstitution: "u.institution_id = $%d",
			ScopeClasses:     "u.id IN (" + classMembers + ")",
			ScopeQuizzes:     "qa.quiz_id IN (" + authoredQuizzes + ")",
		},
	},
	// Practice sessions carry no quiz link, so they cannot answer a quizzes
	// scope at all.
	"practice": {
		Key:      "pr",
		From:     "practice_sessions ps JOIN users u ON u.id = ps.user_id",
		BucketOn: "ps.completed_at",
		Scopes: map[ScopeKind]string{
			ScopeInstitution: "u.institution_id = $%d",
			ScopeClasses:     "u.id IN (" + classMembers + ")",
		},
	},
	"signup": {
		Key:      "su",
		From:     "users u",
		BucketOn: "u.created_at",
		Where:    "u.deleted_at IS NULL",
		Scopes: map[ScopeKind]string{
			ScopeInstitution: "u.institution_id = $%d",
			ScopeClasses:     "u.id IN (" + classMembers + ")",
		},
	},
	"inst_new": {
		Key:      "inew",
		From:     "institutions i",
		BucketOn: "i.created_at",
		Where:    "i.deleted_at IS NULL",
	},
	"inst_verified": {
		Key:      "iver",
		From:     "institutions i",
		BucketOn: "i.verified_at",
	},
	"ledger": {
		Key:      "pl",
		From:     "points_ledger pl JOIN users u ON u.id = pl.user_id",
		BucketOn: "pl.created_at",
		Scopes: map[ScopeKind]string{
			ScopeInstitution: "u.institution_id = $%d",
			ScopeClasses:     "u.id IN (" + classMembers + ")",
		},
	},
	// Quiz-authoring sources have no class linkage. Answering a class-scoped
	// question with an authorship-scoped number gives a plausible figure that
	// means something else, so they drop under ScopeClasses instead.
	"quiz_new": {
		Key:      "qn",
		From:     "quizzes q",
		BucketOn: "q.created_at",
		Where:    "q.deleted_at IS NULL",
		Scopes: map[ScopeKind]string{
			ScopeInstitution: "q.institution_id = $%d",
			ScopeQuizzes:     "q.created_by = $%d",
		},
	},
	"quiz_pub": {
		Key:      "qp",
		From:     "quizzes q",
		BucketOn: "q.published_at",
		Where:    "q.deleted_at IS NULL",
		Scopes: map[ScopeKind]string{
			ScopeInstitution: "q.institution_id = $%d",
			ScopeQuizzes:     "q.created_by = $%d",
		},
	},
	"quiz_appr": {
		Key:      "qap",
		From:     "quizzes q",
		BucketOn: "q.approved_at",
		Where:    "q.deleted_at IS NULL",
		Scopes: map[ScopeKind]string{
			ScopeInstitution: "q.institution_id = $%d",
			ScopeQuizzes:     "q.created_by = $%d",
		},
	},
	"question_new": {
		Key:      "qs",
		From:     "questions qn JOIN quizzes q ON q.id = qn.quiz_id",
		BucketOn: "q.created_at",
		Where:    "q.deleted_at IS NULL",
		Scopes: map[ScopeKind]string{
			ScopeInstitution: "q.institution_id = $%d",
			ScopeQuizzes:     "q.created_by = $%d",
		},
	},
	"topicreq": {
		Key:      "tr",
		From:     "topic_requests tr",
		BucketOn: "tr.created_at",
		Scopes: map[ScopeKind]string{
			ScopeInstitution: "tr.institution_id = $%d",
		},
	},
	"report_new": {
		Key:      "rn",
		From:     "reports r",
		BucketOn: "r.created_at",
	},
	"report_done": {
		Key:      "rd",
		From:     "reports r",
		BucketOn: "r.resolved_at",
	},
	"audit": {
		Key:      "al",
		From:     "audit_log al",
		BucketOn: `al."timestamp"`, // timestamp is a reserved word
	},
	"contact_new": {
		Key:      "cn",
		From:     "contact_submissions cs",
		BucketOn: "cs.created_at",
	},
	"contact_done": {
		Key:      "cd",
		From:     "contact_submissions cs",
		BucketOn: "cs.resolved_at",
	},
	"impersonation": {
		Key:      "im",
		From:     "impersonation_sessions ims",
		BucketOn: "ims.started_at",
	},
	"badge": {
		Key:      "bd",
		From:     "badges b",
		BucketOn: "b.earned_at",
	},
	"follow": {
		Key:      "fl",
		From:     "user_follows uf",
		BucketOn: "uf.created_at",
	},
	"pview": {
		Key:      "pv",
		From:     "profile_views pv",
		BucketOn: "pv.viewed_at",
	},
	"notif": {
		Key:      "nl",
		From:     "notification_log nl",
		BucketOn: "nl.created_at",
	},
}
```

- [ ] **Step 5: Replace `MetricDef.Scopable` with `MetricDef.Scopes`**

The catalog endpoint must tell the UI which kinds a metric answers, not just a boolean. In `catalog.go`:

```go
type MetricDef struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Group string `json:"group"`
	Unit  string `json:"unit"` // count | percent | points | seconds
	Kind  Kind   `json:"kind"`
	// Scopes is the set of scope kinds this metric can answer, filled in by
	// init() from its source (or, for a derived metric, from the intersection
	// of its dependencies').
	Scopes []ScopeKind `json:"scopes"`
	Hint   string      `json:"hint"`
	Source string      `json:"-"` // subquery group; "" means derived
	Expr   string      `json:"-"` // aggregate, or derived SQL over Needs columns
	Needs  []string    `json:"-"` // metric ids a derived metric reads
}
```

Delete the `Scopable: true` / `Scopable: false` field from all 35 catalog entries — the value is now computed. Then add the computation at the bottom of `catalog.go`:

```go
// scopeOrder is the stable order Scopes is reported in.
var scopeOrder = []ScopeKind{ScopeInstitution, ScopeClasses, ScopeQuizzes}

// init fills MetricDef.Scopes from each metric's source, and a derived metric's
// from the intersection of its dependencies'. Computing it removes the class of
// bug where a source gains a scope and a hand-maintained boolean does not.
func init() {
	for i := range catalog {
		m := &catalog[i]
		if m.Source != "" {
			s := sources[m.Source]
			for _, k := range scopeOrder {
				if _, ok := s.Scopes[k]; ok {
					m.Scopes = append(m.Scopes, k)
				}
			}
			continue
		}
		for _, k := range scopeOrder {
			ok := len(m.Needs) > 0
			for _, need := range m.Needs {
				dep, found := Lookup(need)
				if !found || !dep.answers(k) {
					ok = false
					break
				}
			}
			if ok {
				m.Scopes = append(m.Scopes, k)
			}
		}
	}
}

func (m MetricDef) answers(k ScopeKind) bool {
	if k == ScopeNone {
		return true
	}
	for _, s := range m.Scopes {
		if s == k {
			return true
		}
	}
	return false
}
```

`init()` runs after `catalog` and `sources` are initialised, and `Lookup` reads the same slice being mutated — safe because the dependency pass only reads `Scopes` of *sourced* metrics, which the first loop has already filled, and `TestNoDerivedMetricDependsOnADerivedMetric` (already in `catalog_test.go`) guarantees no derived metric depends on another.

- [ ] **Step 6: Rewrite `SelectMetrics` to take a kind**

Replace the existing function in `catalog.go`:

```go
// SelectMetrics resolves the requested ids (empty means everything), pulls in
// the dependencies of derived metrics, and removes anything that cannot honour
// the active scope kind.
func SelectMetrics(ids []string, kind ScopeKind) ([]MetricDef, []DroppedMetric, error) {
	want := map[string]bool{}
	if len(ids) == 0 {
		for _, m := range catalog {
			want[m.ID] = true
		}
	} else {
		var unknown []string
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, ok := Lookup(id); !ok {
				unknown = append(unknown, id)
				continue
			}
			want[id] = true
		}
		if len(unknown) > 0 {
			return nil, nil, fmt.Errorf("unknown metric(s): %s", strings.Join(unknown, ", "))
		}
	}

	// A derived metric is useless without the columns it divides, so pull its
	// dependencies in. One pass suffices — TestNoDerivedMetricDependsOnADerived
	// Metric enforces that no derived metric depends on another.
	for id := range want {
		m, _ := Lookup(id)
		for _, need := range m.Needs {
			want[need] = true
		}
	}

	// Drop sourced metrics the kind cannot answer, then drop any derived metric
	// whose dependency just went away — a rate projecting from a removed column
	// is a broken query, not a partial answer.
	dropped := map[string]string{}
	for id := range want {
		m, _ := Lookup(id)
		if m.Source != "" && !m.answers(kind) {
			dropped[id] = DropReason(kind)
		}
	}
	for id := range want {
		m, _ := Lookup(id)
		if m.Source != "" || dropped[id] != "" {
			continue
		}
		for _, need := range m.Needs {
			if dropped[need] != "" {
				dropped[id] = fmt.Sprintf("depends on %s, which is %s", need, DropReason(kind))
				break
			}
		}
	}

	var selected []MetricDef
	var out []DroppedMetric
	for _, m := range catalog { // iterate the catalog for a stable order
		if !want[m.ID] {
			continue
		}
		if reason, gone := dropped[m.ID]; gone {
			out = append(out, DroppedMetric{ID: m.ID, Reason: reason})
			continue
		}
		selected = append(selected, m)
	}
	return selected, out, nil
}
```

Delete the now-unused `const reasonNotScopable`.

- [ ] **Step 7: Update the existing call sites in `catalog_test.go` and `integration_test.go`**

Every `SelectMetrics(ids, false)` becomes `SelectMetrics(ids, ScopeNone)`; every `SelectMetrics(ids, true)` becomes `SelectMetrics(ids, ScopeInstitution)`. Any assertion reading `.Scopable` becomes `.answers(ScopeInstitution)`.

```bash
grep -rn "SelectMetrics(\|Scopable" internal/domain/metrics/
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test ./internal/domain/metrics/ -v`
Expected: PASS, including the six new scope tests. `go build ./...` still fails at this point — `sql.go` and `service.go` still take `instID *string`; Task 3 fixes that. Confirm the failure is only those two files:

Run: `go build ./... 2>&1 | head -20`
Expected: errors naming `BuildSeriesQuery` / `scopePredicate` argument types only.

- [ ] **Step 9: Commit**

```bash
git add internal/domain/metrics
git commit -m "feat(metrics): scope kinds replace the institution-only filter

Each source now declares a SQL predicate per scope kind it can answer;
a missing key drops the metric with a role-appropriate reason instead of
answering platform-wide. MetricDef.Scopes is computed from the source rather
than hand-maintained."
```

---

## Task 3: SQL builders take a `Scope`

**Files:**
- Modify: `internal/domain/metrics/sql.go`
- Modify: `internal/domain/metrics/sql_test.go`

**Interfaces:**
- Consumes: `Scope`, `ScopeKind`, `scopePredicate` from Task 2.
- Produces:
  - `func BuildSeriesQuery(sel []MetricDef, w Window, sc Scope) (string, []any)`
  - `func BuildTotalsQuery(sel []MetricDef, w Window, sc Scope) (string, []any)`
  - The `instArg`, `seriesArgs`, `totalsArgs` helpers are deleted; argument assembly moves inline.

- [ ] **Step 1: Write the failing tests**

Append to `internal/domain/metrics/sql_test.go`:

```go
func TestSeriesQueryOmitsScopeArgWhenUnscoped(t *testing.T) {
	sel, _, err := SelectMetrics([]string{"attempts_completed"}, ScopeNone)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}
	w := Window{From: time.Date(2026, 1, 1, 0, 0, 0, 0, IST), To: time.Date(2026, 1, 7, 0, 0, 0, 0, IST), Gran: GranDay}

	sql, args := BuildSeriesQuery(sel, w, Scope{})
	if len(args) != 3 {
		t.Fatalf("unscoped series: got %d args, want 3 (from, to, trunc)", len(args))
	}
	if strings.Contains(sql, "$4") {
		t.Errorf("unscoped series references $4:\n%s", sql)
	}
	if strings.Contains(sql, "institution_id") {
		t.Errorf("unscoped series carries an institution predicate:\n%s", sql)
	}
}

func TestSeriesQueryBindsScopeAtFour(t *testing.T) {
	sel, _, err := SelectMetrics([]string{"attempts_completed"}, ScopeClasses)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}
	w := Window{From: time.Date(2026, 1, 1, 0, 0, 0, 0, IST), To: time.Date(2026, 1, 7, 0, 0, 0, 0, IST), Gran: GranDay}

	sql, args := BuildSeriesQuery(sel, w, Scope{Kind: ScopeClasses, ID: "11111111-1111-1111-1111-111111111111"})
	if len(args) != 4 {
		t.Fatalf("scoped series: got %d args, want 4", len(args))
	}
	if args[3] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("scope arg = %v, want the teacher id", args[3])
	}
	if !strings.Contains(sql, "group_teachers") {
		t.Errorf("class scope did not emit the membership subquery:\n%s", sql)
	}
	if !strings.Contains(sql, "$4") {
		t.Errorf("scoped series never binds $4:\n%s", sql)
	}
}

func TestTotalsQueryBindsScopeAtThree(t *testing.T) {
	sel, _, err := SelectMetrics([]string{"quizzes_created"}, ScopeQuizzes)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}
	w := Window{From: time.Date(2026, 1, 1, 0, 0, 0, 0, IST), To: time.Date(2026, 1, 7, 0, 0, 0, 0, IST), Gran: GranDay}

	sql, args := BuildTotalsQuery(sel, w, Scope{Kind: ScopeQuizzes, ID: "22222222-2222-2222-2222-222222222222"})
	if len(args) != 3 {
		t.Fatalf("scoped totals: got %d args, want 3", len(args))
	}
	if !strings.Contains(sql, "q.created_by = $3") {
		t.Errorf("quizzes scope did not bind created_by at $3:\n%s", sql)
	}
	if strings.Contains(sql, "$4") {
		t.Errorf("totals query references $4:\n%s", sql)
	}
}

// Postgres rejects a statement that skips a parameter number. Every $n from 1
// to the highest referenced must appear.
func TestParameterNumberingIsContiguous(t *testing.T) {
	w := Window{From: time.Date(2026, 1, 1, 0, 0, 0, 0, IST), To: time.Date(2026, 1, 7, 0, 0, 0, 0, IST), Gran: GranDay}
	kinds := []Scope{
		{},
		{Kind: ScopeInstitution, ID: "33333333-3333-3333-3333-333333333333"},
		{Kind: ScopeClasses, ID: "33333333-3333-3333-3333-333333333333"},
		{Kind: ScopeQuizzes, ID: "33333333-3333-3333-3333-333333333333"},
	}
	for _, sc := range kinds {
		sel, _, err := SelectMetrics(nil, sc.Kind)
		if err != nil {
			t.Fatalf("SelectMetrics(%q): %v", sc.Kind, err)
		}
		for _, tc := range []struct {
			name string
			sql  string
			args []any
		}{
			{"series", mustFirst(BuildSeriesQuery(sel, w, sc)), mustSecond(BuildSeriesQuery(sel, w, sc))},
			{"totals", mustFirst(BuildTotalsQuery(sel, w, sc)), mustSecond(BuildTotalsQuery(sel, w, sc))},
		} {
			for n := 1; n <= len(tc.args); n++ {
				if !strings.Contains(tc.sql, fmt.Sprintf("$%d", n)) {
					t.Errorf("%s/%s: %d args but $%d never appears", sc.Kind, tc.name, len(tc.args), n)
				}
			}
			if strings.Contains(tc.sql, fmt.Sprintf("$%d", len(tc.args)+1)) {
				t.Errorf("%s/%s: references $%d with only %d args", sc.Kind, tc.name, len(tc.args)+1, len(tc.args))
			}
		}
	}
}

func mustFirst(s string, _ []any) string { return s }
func mustSecond(_ string, a []any) []any { return a }
```

Add `"fmt"`, `"strings"` and `"time"` to the test file's imports if not already present.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/domain/metrics/ -run 'Query|Parameter' -v`
Expected: FAIL — compile error, `BuildSeriesQuery` takes `*string`, not `Scope`.

- [ ] **Step 3: Rewrite the builders**

In `internal/domain/metrics/sql.go`, delete `instArg`, `seriesArgs`, `totalsArgs` and `scopePredicate`'s old definition (the new one lives in `scope.go`). Replace the two builders:

```go
// BuildSeriesQuery composes the bucketed query. $1=from, $2=to (exclusive),
// $3=date_trunc unit, and $4=the scope id when the scope is active. An inactive
// scope emits no predicate and no fourth argument — Postgres cannot type a
// placeholder that no expression references.
func BuildSeriesQuery(sel []MetricDef, w Window, sc Scope) (string, []any) {
	groups, keys := sourceGroups(sel)

	var b strings.Builder
	b.WriteString("WITH buckets AS (\n")
	b.WriteString("  SELECT generate_series(\n")
	b.WriteString("    date_trunc($3, $1::timestamptz AT TIME ZONE 'Asia/Kolkata'),\n")
	b.WriteString("    date_trunc($3, $2::timestamptz AT TIME ZONE 'Asia/Kolkata'),\n")
	b.WriteString(fmt.Sprintf("    '%s'::interval\n", w.Gran.PGInterval()))
	b.WriteString("  ) AS bucket\n)\n")

	// Sourced metrics read their source alias; derived metrics are computed from
	// those columns, so they are projected after.
	proj := []string{"b.bucket"}
	for _, k := range keys {
		s, _ := Source(k)
		for _, m := range groups[k] {
			proj = append(proj, fmt.Sprintf("COALESCE(%s.%s, 0) AS %s", s.Key, m.ID, m.ID))
		}
	}
	for _, m := range sel {
		if m.Source != "" {
			continue
		}
		proj = append(proj, fmt.Sprintf("COALESCE(%s, 0) AS %s", derivedExpr(m, true), m.ID))
	}

	b.WriteString("SELECT " + strings.Join(proj, ",\n       ") + "\n")
	b.WriteString("FROM buckets b\n")

	scopeArgN := 4
	for _, k := range keys {
		s, _ := Source(k)
		var exprs []string
		for _, m := range groups[k] {
			exprs = append(exprs, fmt.Sprintf("%s AS %s", m.Expr, m.ID))
		}
		b.WriteString(fmt.Sprintf(
			"LEFT JOIN (\n  SELECT date_trunc($3, %s AT TIME ZONE 'Asia/Kolkata') AS bucket,\n         %s\n  FROM %s\n  %s\n  GROUP BY 1\n) %s ON %s.bucket = b.bucket\n",
			s.BucketOn,
			strings.Join(exprs, ",\n         "),
			s.From,
			whereClause(
				s.Where,
				fmt.Sprintf("%s >= $1", s.BucketOn),
				fmt.Sprintf("%s < $2", s.BucketOn),
				scopePredicate(s, sc.Kind, scopeArgN),
			),
			s.Key, s.Key))
	}

	b.WriteString("ORDER BY b.bucket")

	args := []any{w.From, w.To.AddDate(0, 0, 1), w.Gran.PGTrunc()}
	if sc.Active() {
		args = append(args, sc.ID)
	}
	return b.String(), args
}
```

and:

```go
// BuildTotalsQuery aggregates the whole window with no bucketing. $1=from,
// $2=to (exclusive), and $3=the scope id when active — there is no date_trunc
// unit here, so the scope filter takes $3 rather than the series query's $4.
// Rate and distinct metrics are only correct this way — folding bucket values
// would average an average or double-count a user active on two days.
func BuildTotalsQuery(sel []MetricDef, w Window, sc Scope) (string, []any) {
	groups, keys := sourceGroups(sel)

	var proj []string
	for _, k := range keys {
		s, _ := Source(k)
		for _, m := range groups[k] {
			proj = append(proj, fmt.Sprintf("COALESCE(%s.%s, 0) AS %s", s.Key, m.ID, m.ID))
		}
	}
	for _, m := range sel {
		if m.Source != "" {
			continue
		}
		proj = append(proj, fmt.Sprintf("COALESCE(%s, 0) AS %s", derivedExpr(m, true), m.ID))
	}
	if len(proj) == 0 {
		proj = append(proj, "1 AS placeholder")
	}

	var b strings.Builder
	b.WriteString("SELECT " + strings.Join(proj, ",\n       ") + "\n")

	// Each source collapses to exactly one row, so cross-joining them composes
	// the window's totals without any grouping.
	scopeArgN := 3
	for i, k := range keys {
		s, _ := Source(k)
		var exprs []string
		for _, m := range groups[k] {
			exprs = append(exprs, fmt.Sprintf("%s AS %s", m.Expr, m.ID))
		}
		clause := "FROM"
		if i > 0 {
			clause = "CROSS JOIN"
		}
		b.WriteString(fmt.Sprintf("%s (\n  SELECT %s\n  FROM %s\n  %s\n) %s\n",
			clause,
			strings.Join(exprs, ",\n         "),
			s.From,
			whereClause(
				s.Where,
				fmt.Sprintf("%s >= $1", s.BucketOn),
				fmt.Sprintf("%s < $2", s.BucketOn),
				scopePredicate(s, sc.Kind, scopeArgN),
			),
			s.Key))
	}
	if len(keys) == 0 {
		b.WriteString("FROM (SELECT 1) AS noop\n")
	}

	args := []any{w.From, w.To.AddDate(0, 0, 1)}
	if sc.Active() {
		args = append(args, sc.ID)
	}
	return b.String(), args
}
```

- [ ] **Step 4: Fix the existing call sites in `sql_test.go`**

Every `BuildSeriesQuery(sel, w, nil)` becomes `BuildSeriesQuery(sel, w, Scope{})`; every `BuildSeriesQuery(sel, w, &instID)` becomes `BuildSeriesQuery(sel, w, Scope{Kind: ScopeInstitution, ID: instID})`. Same for `BuildTotalsQuery`. Any assertion expecting the old `($4::uuid IS NULL OR ...)` text must be rewritten to expect a bare `= $4` — the NULL-tolerant form is gone.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/domain/metrics/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/metrics
git commit -m "feat(metrics): SQL builders take a Scope

Predicates are emitted only when the scope is active, so an unscoped query
carries no scope placeholder at all. Adds a test that parameter numbering is
contiguous under every kind — Postgres rejects a skipped \$n."
```

---

## Task 4: Service methods take a `Scope`, and `Distributions` reports what it dropped

**Files:**
- Modify: `internal/domain/metrics/service.go`
- Create: `internal/domain/metrics/service_test.go`
- Modify: `internal/domain/metrics/integration_test.go` (call sites)

**Interfaces:**
- Consumes: `Scope` (Task 2), the builders (Task 3).
- Produces:
  - `func (s *MetricsService) Series(ctx context.Context, sel []MetricDef, w Window, sc Scope) ([]map[string]any, error)`
  - `func (s *MetricsService) Totals(ctx context.Context, sel []MetricDef, w Window, sc Scope) (map[string]any, error)`
  - `func (s *MetricsService) Distributions(ctx context.Context, sc Scope) (map[string]any, []DroppedMetric, error)`
  - `func (s *MetricsService) PointsLiability(ctx context.Context, sc Scope) (map[string]any, error)` — returns `ErrScopeUnsupported` for `ScopeQuizzes`
  - `var ErrScopeUnsupported = errors.New("this shape cannot be scoped that way")`
  - `func userScopePred(sc Scope, alias string, n int) string` — unexported helper for the snapshot queries
  - `func quizScopePred(sc Scope, alias string, n int) string`

The snapshot queries in `Distributions` are hand-written SQL, not composed from `sources`, so they need their own predicate helpers. Each shape declares which helper it uses:

| Shape | Predicate | Dropped under |
|---|---|---|
| `score_histogram` | user-based (`u`) + quiz-based fallback | never (all three kinds answerable: quizzes scope filters `qa.quiz_id`) |
| `difficulty_bands` | quiz-based (`q`) | `teacher_classes` |
| `streak_bands` | user-based (`u`) | `teacher_quizzes` |
| `role_mix` | user-based (`users`) | `teacher_quizzes` |
| `institution_type_mix` | none | every active scope |
| `quiz_status_funnel` | quiz-based (`quizzes`) | `teacher_classes` |

- [ ] **Step 1: Write the failing tests**

Create `internal/domain/metrics/service_test.go`:

```go
package metrics

import (
	"strings"
	"testing"
)

func TestUserScopePred(t *testing.T) {
	cases := []struct {
		sc       Scope
		want     string
		contains string
	}{
		{Scope{}, "TRUE", ""},
		{Scope{Kind: ScopeInstitution, ID: "x"}, "", "u.institution_id = $1"},
		{Scope{Kind: ScopeClasses, ID: "x"}, "", "group_teachers"},
		{Scope{Kind: ScopeQuizzes, ID: "x"}, "FALSE", ""},
	}
	for _, c := range cases {
		got := userScopePred(c.sc, "u", 1)
		if c.want != "" && got != c.want {
			t.Errorf("userScopePred(%q) = %q, want %q", c.sc.Kind, got, c.want)
		}
		if c.contains != "" && !strings.Contains(got, c.contains) {
			t.Errorf("userScopePred(%q) = %q, want it to contain %q", c.sc.Kind, got, c.contains)
		}
	}
}

func TestQuizScopePred(t *testing.T) {
	if got := quizScopePred(Scope{}, "q", 1); got != "TRUE" {
		t.Errorf("unscoped = %q, want TRUE", got)
	}
	if got := quizScopePred(Scope{Kind: ScopeQuizzes, ID: "x"}, "q", 1); !strings.Contains(got, "q.created_by = $1") {
		t.Errorf("quizzes scope = %q", got)
	}
	if got := quizScopePred(Scope{Kind: ScopeClasses, ID: "x"}, "q", 1); got != "FALSE" {
		t.Errorf("classes scope = %q, want FALSE", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/domain/metrics/ -run ScopePred -v`
Expected: FAIL — `undefined: userScopePred`.

- [ ] **Step 3: Add the helpers and rewire the service**

At the top of `internal/domain/metrics/service.go`:

```go
// ErrScopeUnsupported is returned when a snapshot shape cannot be expressed
// under the requested scope at all. Callers turn it into a 400: an empty
// schedule reads as "nothing is expiring" rather than "not answerable".
var ErrScopeUnsupported = errors.New("this shape cannot be scoped that way")

// userScopePred filters a hand-written snapshot query on a users alias.
// "TRUE" for an unscoped request keeps the surrounding SQL valid without a
// branch per query; "FALSE" marks a shape the caller must drop instead of run.
func userScopePred(sc Scope, alias string, n int) string {
	switch sc.Kind {
	case ScopeNone:
		return "TRUE"
	case ScopeInstitution:
		return fmt.Sprintf("%s.institution_id = $%d", alias, n)
	case ScopeClasses:
		return fmt.Sprintf(`%s.id IN (SELECT gs.user_id
			     FROM group_students gs
			     JOIN group_teachers gt ON gt.group_id = gs.group_id
			    WHERE gt.user_id = $%d)`, alias, n)
	default: // ScopeQuizzes has no user linkage
		return "FALSE"
	}
}

// quizScopePred is the same for a quizzes alias.
func quizScopePred(sc Scope, alias string, n int) string {
	switch sc.Kind {
	case ScopeNone:
		return "TRUE"
	case ScopeInstitution:
		return fmt.Sprintf("%s.institution_id = $%d", alias, n)
	case ScopeQuizzes:
		return fmt.Sprintf("%s.created_by = $%d", alias, n)
	default: // ScopeClasses has no quiz linkage
		return "FALSE"
	}
}

// scopeArgs is the argument slice for a snapshot query: one element when the
// scope is active, none otherwise.
func scopeArgs(sc Scope) []any {
	if !sc.Active() {
		return nil
	}
	return []any{sc.ID}
}
```

Add `"errors"` to the imports.

`Series` and `Totals` become:

```go
func (s *MetricsService) Series(ctx context.Context, sel []MetricDef, w Window, sc Scope) ([]map[string]any, error) {
	sql, args := BuildSeriesQuery(sel, w, sc)
	// … existing body, unchanged …
}

func (s *MetricsService) Totals(ctx context.Context, sel []MetricDef, w Window, sc Scope) (map[string]any, error) {
	sql, args := BuildTotalsQuery(sel, w, sc)
	// … existing body, unchanged …
}
```

- [ ] **Step 4: Rewrite `Distributions` to drop what it cannot answer**

```go
// Distributions returns snapshot shapes — "what is the mix right now" — which
// is why they do not live in /metrics. Shapes the active scope cannot express
// are omitted and reported, matching how /metrics reports dropped metrics.
func (s *MetricsService) Distributions(ctx context.Context, sc Scope) (map[string]any, []DroppedMetric, error) {
	out := map[string]any{}
	var dropped []DroppedMetric
	args := scopeArgs(sc)
	reason := DropReason(sc.Kind)

	drop := func(id string) { dropped = append(dropped, DroppedMetric{ID: id, Reason: reason}) }

	// Score histogram answers every kind: the quizzes scope filters the
	// attempt's quiz rather than the taker.
	hist, err := s.labelledHistogram(ctx, sc)
	if err != nil {
		return nil, nil, err
	}
	out["score_histogram"] = hist

	if quizScopePred(sc, "q", 1) == "FALSE" {
		drop("difficulty_bands")
	} else {
		bands, err := s.countBands(ctx, difficultyBands, `
			SELECT COUNT(*)
			FROM questions qn
			JOIN quizzes q ON q.id = qn.quiz_id
			WHERE q.deleted_at IS NULL
			  AND qn.difficulty >= e.lo AND qn.difficulty < e.hi
			  AND `+quizScopePred(sc, "q", 1), args...)
		if err != nil {
			return nil, nil, err
		}
		out["difficulty_bands"] = bands
	}

	// streaks has no institution column, so it scopes through users.
	if userScopePred(sc, "u", 1) == "FALSE" {
		drop("streak_bands")
	} else {
		streaks, err := s.countBands(ctx, streakBands, `
			SELECT COUNT(*)
			FROM streaks st
			JOIN users u ON u.id = st.user_id
			WHERE u.deleted_at IS NULL
			  AND st.current_streak >= e.lo AND st.current_streak < e.hi
			  AND `+userScopePred(sc, "u", 1), args...)
		if err != nil {
			return nil, nil, err
		}
		out["streak_bands"] = streaks
	}

	if userScopePred(sc, "users", 1) == "FALSE" {
		drop("role_mix")
	} else if out["role_mix"], err = s.labelledCounts(ctx, `
		SELECT role AS label, COUNT(*) AS count
		FROM users
		WHERE deleted_at IS NULL AND `+userScopePred(sc, "users", 1)+`
		GROUP BY 1 ORDER BY 2 DESC`, args...); err != nil {
		return nil, nil, err
	}

	// Institutions have no parent institution, so this shape answers no scope.
	if sc.Active() {
		drop("institution_type_mix")
	} else if out["institution_type_mix"], err = s.labelledCounts(ctx, `
		SELECT type AS label, COUNT(*) AS count
		FROM institutions WHERE deleted_at IS NULL GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return nil, nil, err
	}

	if quizScopePred(sc, "quizzes", 1) == "FALSE" {
		drop("quiz_status_funnel")
	} else if out["quiz_status_funnel"], err = s.labelledCounts(ctx, `
		SELECT status AS label, COUNT(*) AS count
		FROM quizzes
		WHERE deleted_at IS NULL AND `+quizScopePred(sc, "quizzes", 1)+`
		GROUP BY 1 ORDER BY 2 DESC`, args...); err != nil {
		return nil, nil, err
	}

	return out, dropped, nil
}
```

Declare `var err error` at the top of the function so the `else if … err =` assignments compile.

`countBands` currently takes `inst any` and binds it as `$1` with the band arrays at `$2`/`$3`. Change its signature to `countBands(ctx context.Context, bands []band, query string, args ...any)` and build the outer query's placeholders from `len(args)`:

```go
// The query must reference the band edges as the lateral columns e.lo and e.hi
// (not placeholders: inside a LATERAL, $1 is still a query parameter and would
// bind to the whole array). The caller's own placeholders occupy $1..$n, so the
// two edge arrays follow them.
func (s *MetricsService) countBands(
	ctx context.Context, bands []band, query string, args ...any,
) ([]map[string]any, error) {
	los := make([]float64, len(bands))
	his := make([]float64, len(bands))
	for i, b := range bands {
		los[i], his[i] = b.Lo, b.Hi
	}
	loN, hiN := len(args)+1, len(args)+2
	full := fmt.Sprintf(
		`SELECT e.ord, c.count
		   FROM unnest($%d::float8[], $%d::float8[]) WITH ORDINALITY AS e(lo, hi, ord)
		   CROSS JOIN LATERAL (%s) AS c(count)
		  ORDER BY e.ord`, loN, hiN, query)

	rows, err := s.db.Query(ctx, full, append(append([]any{}, args...), los, his)...)
	// … existing scan loop, unchanged …
}
```

`labelledHistogram` takes the scope directly:

```go
func (s *MetricsService) labelledHistogram(ctx context.Context, sc Scope) ([]map[string]any, error) {
	// The quizzes scope filters the attempt's quiz; the others filter the taker.
	pred := userScopePred(sc, "u", 1)
	if sc.Kind == ScopeQuizzes {
		pred = "qa.quiz_id IN (SELECT id FROM quizzes WHERE created_by = $1)"
	}
	rows, err := s.db.Query(ctx, `
		WITH bins AS (SELECT generate_series(0, 90, 10) AS lo)
		SELECT b.lo, b.lo + 10 AS hi, COALESCE(c.n, 0) AS count
		FROM bins b
		LEFT JOIN (
		  SELECT LEAST(FLOOR(qa.score_pct / 10) * 10, 90) AS lo, COUNT(*) AS n
		  FROM quiz_attempts qa
		  JOIN users u ON u.id = qa.user_id
		  WHERE qa.status = 'completed' AND qa.score_pct IS NOT NULL
		    AND `+pred+`
		  GROUP BY 1
		) c ON c.lo = b.lo
		ORDER BY b.lo`, scopeArgs(sc)...)
	// … existing scan loop, unchanged …
}
```

- [ ] **Step 5: Rewrite `PointsLiability`**

```go
// PointsLiability is a schedule of points about to expire, grouped by month.
// Not a time series of the past, which is why it is its own endpoint.
// Served by idx_ledger_expires_positive from migration 019.
func (s *MetricsService) PointsLiability(ctx context.Context, sc Scope) (map[string]any, error) {
	// points_ledger has no quiz linkage. Returning an empty schedule would read
	// as "nothing is expiring", so refuse instead.
	if sc.Kind == ScopeQuizzes {
		return nil, ErrScopeUnsupported
	}

	rows, err := s.db.Query(ctx, `
		SELECT to_char(date_trunc('month', pl.expires_at AT TIME ZONE 'Asia/Kolkata'), 'YYYY-MM') AS month,
		       SUM(pl.amount) AS points
		FROM points_ledger pl
		JOIN users u ON u.id = pl.user_id
		WHERE pl.amount > 0
		  AND pl.expires_at IS NOT NULL
		  AND pl.expires_at > now()
		  AND `+userScopePred(sc, "u", 1)+`
		GROUP BY 1
		ORDER BY 1`, scopeArgs(sc)...)
	// … existing scan loop and envelope, unchanged …
}
```

Keep the rest of the function body (month/total accumulation, `as_of`, `timezone`) exactly as it is.

- [ ] **Step 6: Fix the call sites in `integration_test.go`**

`svc.Series(ctx, sel, w, nil)` → `svc.Series(ctx, sel, w, Scope{})`. Same for `Totals`. `svc.Distributions(ctx, nil)` now returns three values: `data, _, err := svc.Distributions(ctx, Scope{})`.

- [ ] **Step 7: Run the tests**

Run: `go test ./internal/domain/metrics/ -v`
Expected: PASS. `go build ./...` still fails only in `internal/domain/admin/metrics_handler.go`, which Task 5 replaces.

- [ ] **Step 8: Commit**

```bash
git add internal/domain/metrics
git commit -m "feat(metrics): service methods take a Scope

Distributions now omits and reports shapes the active scope cannot express
instead of answering them platform-wide; points-liability refuses a quizzes
scope outright, since an empty schedule reads as 'nothing is expiring'."
```

---

## Task 5: Shared handler with a pluggable scope resolver

**Files:**
- Create: `internal/domain/metrics/handler.go`
- Create: `internal/domain/metrics/handler_test.go`
- Delete: `internal/domain/admin/metrics_handler.go`
- Modify: `cmd/api/main.go:84` (wiring), `cmd/api/main.go:414-417` (admin routes)

**Interfaces:**
- Consumes: everything from Tasks 2–4.
- Produces:
  - `type ScopeResolver func(r *http.Request) (Scope, ScopeNote, error)`
  - `type ScopeNote struct { Requested ScopeKind; Effective ScopeKind; Reason string }` with JSON tags `requested`, `effective`, `reason`
  - `func NewHandler(db *pgxpool.Pool, resolve ScopeResolver) *Handler`
  - `func (h *Handler) Catalog|Metrics|Distributions|PointsLiability(w http.ResponseWriter, r *http.Request)`
  - `var ErrBadScopeRequest` — a resolver signalling "reply 400 with this message"; carries the message via `fmt.Errorf`

Task 6 supplies the institution resolver, Task 7 the teacher resolver.

- [ ] **Step 1: Write the failing test**

Create `internal/domain/metrics/handler_test.go`:

```go
package metrics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The catalog a role sees must contain only metrics that role can actually be
// answered on. A picker built from a wider catalog offers metrics that will
// always drop.
func TestCatalogFiltersByScopeKind(t *testing.T) {
	h := &Handler{resolve: func(*http.Request) (Scope, ScopeNote, error) {
		return Scope{Kind: ScopeQuizzes, ID: "t"}, ScopeNote{Requested: ScopeQuizzes, Effective: ScopeQuizzes}, nil
	}}

	rec := httptest.NewRecorder()
	h.Catalog(rec, httptest.NewRequest(http.MethodGet, "/teacher/metrics/catalog", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Data struct {
			Metrics []MetricDef `json:"metrics"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body.String())
	}
	if len(body.Data.Metrics) == 0 {
		t.Fatal("quizzes catalog is empty")
	}
	for _, m := range body.Data.Metrics {
		if !m.answers(ScopeQuizzes) {
			t.Errorf("metric %q cannot answer a quizzes scope but is advertised", m.ID)
		}
	}
	// A student-centred metric must not appear.
	for _, m := range body.Data.Metrics {
		if m.ID == "signups" {
			t.Error("signups advertised under a quizzes scope")
		}
	}
}

func TestUnscopedCatalogIsWhole(t *testing.T) {
	h := &Handler{resolve: func(*http.Request) (Scope, ScopeNote, error) {
		return Scope{}, ScopeNote{}, nil
	}}
	rec := httptest.NewRecorder()
	h.Catalog(rec, httptest.NewRequest(http.MethodGet, "/admin/metrics/catalog", nil))

	var body struct {
		Data struct {
			Metrics []MetricDef `json:"metrics"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data.Metrics) != len(Catalog()) {
		t.Errorf("unscoped catalog has %d of %d metrics", len(body.Data.Metrics), len(Catalog()))
	}
}
```

The response envelope is whatever `middleware.JSON` produces; the test reads `data`. If `middleware.JSON` writes the payload at the top level rather than under `data`, adjust the two structs to match — check `internal/middleware/respond.go:26` before running.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/domain/metrics/ -run Catalog -v`
Expected: FAIL — `undefined: Handler`.

- [ ] **Step 3: Write the handler**

Create `internal/domain/metrics/handler.go`. This is the old `admin/metrics_handler.go` with the institution lookup replaced by the injected resolver:

```go
package metrics

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

const bucketTimezone = "Asia/Kolkata"

// ErrBadScopeRequest wraps a resolver failure the caller caused, so the handler
// answers 400 with the resolver's message instead of an opaque 500.
var ErrBadScopeRequest = errors.New("bad scope request")

// ScopeNote reports what the caller asked for versus what was actually applied.
// A teacher with no classes is answered institution-wide; without this field
// they would read those numbers as their own class's.
type ScopeNote struct {
	Requested ScopeKind `json:"requested"`
	Effective ScopeKind `json:"effective"`
	Reason    string    `json:"reason"`
}

// ScopeResolver derives the caller's scope from their token and, for teachers,
// the `scope` query parameter. It never reads an id from the query string.
type ScopeResolver func(r *http.Request) (Scope, ScopeNote, error)

type Handler struct {
	svc     *MetricsService
	resolve ScopeResolver
}

func NewHandler(db *pgxpool.Pool, resolve ScopeResolver) *Handler {
	return &Handler{svc: NewMetricsService(db), resolve: resolve}
}

// failed logs the cause before the opaque 500. Without this a query error is
// invisible — the client sees "an unexpected error occurred" and the server
// says nothing.
func failed(w http.ResponseWriter, what string, err error) {
	log.Printf("analytics: %s: %v", what, err)
	middleware.InternalError(w)
}

// scope runs the resolver and writes the error response itself. ok=false means
// a response has already been written.
func (h *Handler) scope(w http.ResponseWriter, r *http.Request) (Scope, ScopeNote, bool) {
	sc, note, err := h.resolve(r)
	if err != nil {
		if errors.Is(err, ErrBadScopeRequest) {
			middleware.BadRequest(w, err.Error())
		} else {
			failed(w, "resolve scope", err)
		}
		return Scope{}, ScopeNote{}, false
	}
	return sc, note, true
}

// Catalog advertises the metric vocabulary the caller's scope can actually
// answer, so every picker is built from the server rather than its own copy.
func (h *Handler) Catalog(w http.ResponseWriter, r *http.Request) {
	sc, note, ok := h.scope(w, r)
	if !ok {
		return
	}

	all := Catalog()
	visible := make([]MetricDef, 0, len(all))
	for _, m := range all {
		if m.answers(sc.Kind) {
			visible = append(visible, m)
		}
	}

	w.Header().Set("Cache-Control", "private, max-age=300")
	middleware.JSON(w, http.StatusOK, map[string]any{
		"metrics":       visible,
		"granularities": Granularities,
		"timezone":      bucketTimezone,
		"scope":         note,
	})
}

func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	win, err := ResolveWindow(q.Get("from"), q.Get("to"), q.Get("granularity"), time.Now())
	if err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}

	compare := q.Get("compare")
	switch compare {
	case "", "previous", "year":
	default:
		middleware.BadRequest(w, "compare must be 'previous' or 'year'")
		return
	}

	sc, note, ok := h.scope(w, r)
	if !ok {
		return
	}

	var ids []string
	if raw := strings.TrimSpace(q.Get("metrics")); raw != "" {
		ids = strings.Split(raw, ",")
	}
	sel, dropped, err := SelectMetrics(ids, sc.Kind)
	if err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}

	series, err := h.svc.Series(r.Context(), sel, win, sc)
	if err != nil {
		failed(w, "series", err)
		return
	}
	totals, err := h.svc.Totals(r.Context(), sel, win, sc)
	if err != nil {
		failed(w, "totals", err)
		return
	}

	data := map[string]any{
		"from":        win.From.Format(DateLayout),
		"to":          win.To.Format(DateLayout),
		"granularity": win.Gran,
		"timezone":    bucketTimezone,
		"series":      series,
		"totals":      totals,
		"scope":       note,
	}
	// institution_id is kept for the super-admin console, which reads it today.
	if sc.Kind == ScopeInstitution {
		data["institution_id"] = sc.ID
	} else {
		data["institution_id"] = nil
	}
	// dropped is present only when something was excluded, so the UI can treat
	// its presence as "tell the caller" rather than checking for an empty array.
	if len(dropped) > 0 {
		data["dropped"] = dropped
	}

	if compare != "" {
		prev := win.Previous()
		if compare == "year" {
			prev = win.LastYear()
		}
		prevTotals, err := h.svc.Totals(r.Context(), sel, prev, sc)
		if err != nil {
			failed(w, "previous totals", err)
			return
		}
		prevSeries, err := h.svc.Series(r.Context(), sel, prev, sc)
		if err != nil {
			failed(w, "previous series", err)
			return
		}
		data["previous"] = prevTotals
		data["previous_series"] = prevSeries
		data["previous_from"] = prev.From.Format(DateLayout)
		data["previous_to"] = prev.To.Format(DateLayout)
	}

	middleware.JSON(w, http.StatusOK, data)
}

func (h *Handler) Distributions(w http.ResponseWriter, r *http.Request) {
	sc, note, ok := h.scope(w, r)
	if !ok {
		return
	}
	shapes, dropped, err := h.svc.Distributions(r.Context(), sc)
	if err != nil {
		failed(w, "distributions", err)
		return
	}
	data := map[string]any{"scope": note}
	for k, v := range shapes {
		data[k] = v
	}
	if len(dropped) > 0 {
		data["dropped"] = dropped
	}
	middleware.JSON(w, http.StatusOK, data)
}

func (h *Handler) PointsLiability(w http.ResponseWriter, r *http.Request) {
	sc, note, ok := h.scope(w, r)
	if !ok {
		return
	}
	data, err := h.svc.PointsLiability(r.Context(), sc)
	if err != nil {
		if errors.Is(err, ErrScopeUnsupported) {
			middleware.BadRequest(w,
				"points liability cannot be scoped to your quizzes; the points ledger has no quiz linkage")
			return
		}
		failed(w, "points liability", err)
		return
	}
	data["scope"] = note
	middleware.JSON(w, http.StatusOK, data)
}
```

- [ ] **Step 4: Replace the admin metrics handler with a resolver**

Delete `internal/domain/admin/metrics_handler.go`:

```bash
git rm internal/domain/admin/metrics_handler.go
```

Create `internal/domain/admin/metrics_scope.go` — the super-admin resolver, which keeps today's optional `institution_id` filter:

```go
package admin

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/metrics"
)

// MetricsScopeResolver keeps the super admin's optional institution filter: no
// parameter means platform-wide, a valid one narrows to that institution.
// Unlike the institution and teacher resolvers, this one reads an id from the
// query — a super admin is allowed to look at any institution.
func MetricsScopeResolver(db *pgxpool.Pool) metrics.ScopeResolver {
	svc := metrics.NewMetricsService(db)
	return func(r *http.Request) (metrics.Scope, metrics.ScopeNote, error) {
		raw := strings.TrimSpace(r.URL.Query().Get("institution_id"))
		if raw == "" {
			return metrics.Scope{}, metrics.ScopeNote{}, nil
		}
		exists, err := svc.InstitutionExists(r.Context(), raw)
		if err != nil {
			// An unparseable uuid surfaces here as a query error, not a 500 —
			// the caller sent a bad filter, so say so.
			return metrics.Scope{}, metrics.ScopeNote{},
				fmt.Errorf("%w: institution_id must be a valid uuid", metrics.ErrBadScopeRequest)
		}
		if !exists {
			return metrics.Scope{}, metrics.ScopeNote{},
				fmt.Errorf("%w: institution not found", metrics.ErrBadScopeRequest)
		}
		note := metrics.ScopeNote{
			Requested: metrics.ScopeInstitution,
			Effective: metrics.ScopeInstitution,
		}
		return metrics.Scope{Kind: metrics.ScopeInstitution, ID: raw}, note, nil
	}
}
```

This changes one existing behaviour: a nonexistent institution id was a `404`, and is now a `400`. Note it in the API doc task (Task 9).

- [ ] **Step 5: Rewire `cmd/api/main.go`**

Line 84 becomes:

```go
metricsH := metrics.NewHandler(pool, admin.MetricsScopeResolver(pool))
```

Add `"github.com/qwish/backend/internal/domain/metrics"` to the import block. The four admin routes at lines 414–417 are unchanged — `metricsH` still has `Catalog`, `Metrics`, `Distributions`, `PointsLiability`.

- [ ] **Step 6: Build and run everything**

Run: `go build ./... && go test ./...`
Expected: PASS, all packages.

- [ ] **Step 7: Commit**

```bash
git add -A internal/domain cmd/api
git commit -m "feat(metrics): one handler, pluggable scope resolver

The admin handler becomes a resolver that keeps its optional institution_id
filter; the shared handler owns window parsing, compare and the response
envelope so the institution and teacher routes cannot drift from it.
The catalog endpoint now returns only metrics the caller's scope can answer."
```

---

## Task 6: Migration 029 — `user_dashboard_layouts` and the scope indexes

**Files:**
- Create: `migrations/029_user_dashboard_layouts.sql`

**Interfaces:**
- Consumes: nothing.
- Produces: table `user_dashboard_layouts (id, user_id, name, layout, is_default, sort, created_at, updated_at)` and indexes `idx_user_layouts_user`, `idx_user_layouts_one_default`, `idx_group_students_user`, `idx_group_teachers_user`, `idx_quizzes_created_by`.

- [ ] **Step 1: Write the migration**

Create `migrations/029_user_dashboard_layouts.sql`:

```sql
-- ============================================================================
-- Migration 029: Per-user dashboard layouts + role-scoped analytics indexes
-- ============================================================================
-- Institution admins and teachers live in `users`, not `admin_accounts`, so
-- they cannot share 028's table. Same shape, different owner.
--
-- `layout` is opaque to the server: widget shapes change with every frontend
-- release, and a server-side schema for them would need a migration each time.
-- The API validates only that it is a JSON object under a size cap.
-- ============================================================================

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

-- The only read pattern: this user's layouts, in display order.
CREATE INDEX IF NOT EXISTS idx_user_layouts_user
    ON user_dashboard_layouts (user_id, sort);

-- At most one default per user, enforced in the database rather than trusting
-- every write path to clear the previous one.
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_layouts_one_default
    ON user_dashboard_layouts (user_id)
    WHERE is_default;

-- ── Scope-join indexes ──────────────────────────────────────────────────────
-- The teacher scopes filter on columns nothing has filtered on before.
-- group_students and group_teachers are keyed (group_id, user_id), so the
-- primary keys do not serve a user_id-leading lookup, and the class-membership
-- subquery runs once per analytics source.

CREATE INDEX IF NOT EXISTS idx_group_students_user
    ON group_students (user_id);

CREATE INDEX IF NOT EXISTS idx_group_teachers_user
    ON group_teachers (user_id, group_id);

CREATE INDEX IF NOT EXISTS idx_quizzes_created_by
    ON quizzes (created_by)
    WHERE deleted_at IS NULL;
```

- [ ] **Step 2: Verify it applies**

The migrator runs on boot and records applied versions in `schema_migrations`. Against a scratch database:

```bash
DATABASE_URL="$TEST_DATABASE_URL" go run ./cmd/api 2>&1 | head -30
```
Expected: a log line for migration 029 applying, then the server starting. Stop it with Ctrl-C.

Then confirm the objects exist:

```bash
psql "$TEST_DATABASE_URL" -c '\d user_dashboard_layouts' -c '\di idx_group_students_user idx_group_teachers_user idx_quizzes_created_by'
```
Expected: the table with eight columns, and three index rows.

- [ ] **Step 3: Commit**

```bash
git add migrations/029_user_dashboard_layouts.sql
git commit -m "feat(db): user_dashboard_layouts and role-scoped analytics indexes"
```

---

## Task 7: `LayoutsService` parameterized on table and owner column

**Files:**
- Modify: `internal/domain/admin/layouts.go`
- Modify: `internal/domain/admin/layouts_integration_test.go`

**Interfaces:**
- Consumes: migration 029 (Task 6).
- Produces:
  - `func NewLayoutsService(db *pgxpool.Pool, table, ownerCol string) *LayoutsService`
  - `func NewLayoutsHandler(db *pgxpool.Pool, table, ownerCol string, owner func(*http.Request) string) *LayoutsHandler`
  - Behaviour, error values (`ErrDuplicateName`, `ErrNotFound`, `ErrLayoutTooBig`) and the 256 KiB cap are unchanged.

- [ ] **Step 1: Write the failing test**

Append to `internal/domain/admin/layouts_integration_test.go`:

```go
// The user-owned table must behave identically to the admin-owned one, since
// both are served by the same service. This is the test that catches a missed
// table name in one of the seven queries.
func TestUserLayoutsRoundTrip(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewLayoutsService(pool, "user_dashboard_layouts", "user_id")

	// A throwaway user; the layout cascades away with it.
	var userID string
	err := pool.QueryRow(ctx, `
		INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		VALUES (gen_random_uuid(), 'Layout Test', 'Layout', $1, 'teacher')
		RETURNING id`, "layout-test-"+time.Now().Format("20060102150405")+"@example.test").Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	l, err := svc.Create(ctx, userID, "Default", json.RawMessage(`{"widgets":[]}`), true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !l.IsDefault {
		t.Error("created layout is not the default")
	}

	if _, err := svc.Create(ctx, userID, "Default", json.RawMessage(`{}`), false); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("duplicate name: got %v, want ErrDuplicateName", err)
	}

	list, err := svc.List(ctx, userID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List returned %d layouts, want 1", len(list))
	}

	name := "Renamed"
	if _, err := svc.Update(ctx, userID, l.ID, &name, nil, nil, nil); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Another user must not reach this row.
	if _, err := svc.Update(ctx, userID+"", "00000000-0000-0000-0000-000000000000", &name, nil, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: got %v, want ErrNotFound", err)
	}

	if err := svc.Delete(ctx, userID, l.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
```

Add `"encoding/json"`, `"errors"`, `"time"` to that file's imports if missing.

- [ ] **Step 2: Run the test to verify it fails**

Run: `TEST_DATABASE_URL=... go test ./internal/domain/admin/ -run UserLayouts -v`
Expected: FAIL — `too many arguments in call to NewLayoutsService`.

- [ ] **Step 3: Parameterize the service**

In `internal/domain/admin/layouts.go`:

```go
// LayoutsService serves one layouts table. The table and owner column are
// supplied at construction from compile-time constants at the wiring site —
// never from request data — so interpolating them into the SQL is safe.
//
// Two tables exist because layouts cascade with their owner, and the two owner
// kinds live in different tables: super admins in admin_accounts, institution
// admins and teachers in users.
type LayoutsService struct {
	db       *pgxpool.Pool
	table    string
	ownerCol string
}

func NewLayoutsService(db *pgxpool.Pool, table, ownerCol string) *LayoutsService {
	return &LayoutsService{db: db, table: table, ownerCol: ownerCol}
}
```

Then rewrite each of the seven SQL strings to interpolate `s.table` and `s.ownerCol`. Every literal `admin_dashboard_layouts` becomes `%s` bound to `s.table`, and every literal `admin_id` becomes `%s` bound to `s.ownerCol`. Example — `List`:

```go
func (s *LayoutsService) List(ctx context.Context, ownerID string) ([]Layout, error) {
	rows, err := s.db.Query(ctx, fmt.Sprintf(
		`SELECT `+layoutCols+` FROM %s
		  WHERE %s = $1 ORDER BY sort, created_at`, s.table, s.ownerCol), ownerID)
	// … existing body, unchanged …
}
```

and `Create`:

```go
	if isDefault {
		if _, err := tx.Exec(ctx, fmt.Sprintf(
			`UPDATE %s SET is_default = false, updated_at = now()
			  WHERE %s = $1 AND is_default`, s.table, s.ownerCol), ownerID); err != nil {
			return Layout{}, err
		}
	}

	l, err := scanLayout(tx.QueryRow(ctx, fmt.Sprintf(
		`INSERT INTO %s (%s, name, layout, is_default, sort)
		 VALUES ($1, $2, $3, $4,
		   COALESCE((SELECT MAX(sort) + 1 FROM %s WHERE %s = $1), 0))
		 RETURNING `+layoutCols, s.table, s.ownerCol, s.table, s.ownerCol),
		ownerID, strings.TrimSpace(name), []byte(layout), isDefault))
```

Apply the same treatment to `Update`, `Delete`, `Reorder`, and the ownership `EXISTS` check. Rename the `adminID` parameter to `ownerID` throughout. Add `"fmt"` to the imports.

- [ ] **Step 4: Parameterize the handler's owner lookup**

```go
type LayoutsHandler struct {
	svc   *LayoutsService
	owner func(*http.Request) string
}

// NewLayoutsHandler binds a layouts table to the function that names its owner.
// The admin console reads GetAdminID; institution and teacher panels read
// GetUserID, because their accounts live in `users`.
func NewLayoutsHandler(
	db *pgxpool.Pool, table, ownerCol string, owner func(*http.Request) string,
) *LayoutsHandler {
	return &LayoutsHandler{svc: NewLayoutsService(db, table, ownerCol), owner: owner}
}

// requireOwner resolves the owner from the token. The id is empty when the
// token is not of the expected kind, which is a 403 rather than a 500.
func (h *LayoutsHandler) requireOwner(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := h.owner(r)
	if id == "" {
		middleware.Forbidden(w)
		return "", false
	}
	return id, true
}
```

Replace every `requireAdmin` call with `requireOwner`. Keep the rest of each handler method unchanged.

- [ ] **Step 5: Run the tests**

Run: `TEST_DATABASE_URL=... go test ./internal/domain/admin/ -v`
Expected: PASS, both the existing admin-layout tests and the new user-layout test.

Then: `go build ./...` — this fails at `cmd/api/main.go:85` until Step 6.

- [ ] **Step 6: Rewire `cmd/api/main.go`**

```go
layoutsH := admin.NewLayoutsHandler(pool, "admin_dashboard_layouts", "admin_id", middleware.GetAdminID)
userLayoutsH := admin.NewLayoutsHandler(pool, "user_dashboard_layouts", "user_id", middleware.GetUserID)
```

`userLayoutsH` is registered in Tasks 8 and 9. `go build ./...` will report it as unused until then; add the routes in the same session, or temporarily assign `_ = userLayoutsH`.

- [ ] **Step 7: Commit**

```bash
git add internal/domain/admin cmd/api
git commit -m "refactor(layouts): parameterize the table and owner column

Institution admins and teachers live in users, not admin_accounts, so they
need their own table. Table and owner column are wiring-time constants, never
request data."
```

---

## Task 8: Institution analytics routes

**Files:**
- Create: `internal/domain/institution/metrics_scope.go`
- Modify: `cmd/api/main.go` (wiring + routes inside the existing `/institution` block)
- Create: `internal/domain/metrics/institution_scope_test.go`

**Interfaces:**
- Consumes: `metrics.ScopeResolver`, `metrics.NewHandler` (Task 5); `admin.NewLayoutsHandler` (Task 7).
- Produces: `func institution.MetricsScopeResolver() metrics.ScopeResolver` — no database handle needed; the institution id comes from the token.

- [ ] **Step 1: Write the failing test**

Create `internal/domain/institution/metrics_scope_test.go`:

```go
package institution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qwish/backend/internal/domain/metrics"
	"github.com/qwish/backend/internal/middleware"
)

// The institution scope comes from the token and nothing else. A query
// parameter naming another institution must have no effect.
func TestInstitutionScopeIgnoresQueryParams(t *testing.T) {
	resolve := MetricsScopeResolver()

	req := httptest.NewRequest(http.MethodGet,
		"/institution/metrics?institution_id=99999999-9999-9999-9999-999999999999&scope=quizzes", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyInstID, "11111111-1111-1111-1111-111111111111")
	req = req.WithContext(ctx)

	sc, note, err := resolve(req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sc.Kind != metrics.ScopeInstitution {
		t.Errorf("kind = %q, want institution", sc.Kind)
	}
	if sc.ID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("id = %q, want the token's institution", sc.ID)
	}
	if note.Effective != metrics.ScopeInstitution || note.Requested != metrics.ScopeInstitution {
		t.Errorf("note = %+v", note)
	}
}

// A token with no institution cannot be answered at all.
func TestInstitutionScopeRejectsMissingInstitution(t *testing.T) {
	resolve := MetricsScopeResolver()
	req := httptest.NewRequest(http.MethodGet, "/institution/metrics", nil)

	if _, _, err := resolve(req); err == nil {
		t.Fatal("want an error when the token carries no institution")
	}
}
```

`middleware.ContextKeyUserID` and `middleware.ContextKeyInstID` are already exported constants (`internal/middleware/auth.go:15-17`). Their `contextKey` type is unexported, which is fine: another package can pass an exported constant of an unexported type to `context.WithValue`. No middleware change is needed.

Confirm before writing the test: `grep -n "ContextKeyUserID\|ContextKeyInstID" internal/middleware/auth.go`

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/domain/institution/ -v`
Expected: FAIL — `undefined: MetricsScopeResolver`.

- [ ] **Step 3: Write the resolver**

Create `internal/domain/institution/metrics_scope.go`:

```go
package institution

import (
	"fmt"
	"net/http"

	"github.com/qwish/backend/internal/domain/metrics"
	"github.com/qwish/backend/internal/middleware"
)

// MetricsScopeResolver pins every institution-admin analytics request to the
// institution on the caller's token. No query parameter widens or redirects it:
// there is deliberately no code path by which an institution admin names
// another institution.
func MetricsScopeResolver() metrics.ScopeResolver {
	return func(r *http.Request) (metrics.Scope, metrics.ScopeNote, error) {
		instID := middleware.GetInstitutionID(r)
		if instID == "" {
			return metrics.Scope{}, metrics.ScopeNote{},
				fmt.Errorf("%w: your account is not linked to an institution", metrics.ErrBadScopeRequest)
		}
		note := metrics.ScopeNote{
			Requested: metrics.ScopeInstitution,
			Effective: metrics.ScopeInstitution,
		}
		return metrics.Scope{Kind: metrics.ScopeInstitution, ID: instID}, note, nil
	}
}
```

- [ ] **Step 4: Register the routes**

In `cmd/api/main.go`, next to the other handler constructions:

```go
instMetricsH := metrics.NewHandler(pool, institution.MetricsScopeResolver())
```

Inside the existing `r.Route("/institution", …)` block, after the reports routes:

```go
					// Analytics. Scope is pinned to the caller's institution by
					// the resolver — there is no institution_id parameter here.
					// /metrics/catalog precedes /metrics so chi does not read
					// "catalog" as a wildcard segment.
					r.Get("/metrics/catalog", instMetricsH.Catalog)
					r.Get("/metrics", instMetricsH.Metrics)
					r.Get("/distributions", instMetricsH.Distributions)
					r.Get("/points-liability", instMetricsH.PointsLiability)

					// Dashboard layouts — private to the calling user.
					// /order precedes {layoutId} so chi does not capture
					// "order" as an id.
					r.Get("/dashboard-layouts", userLayoutsH.List)
					r.Post("/dashboard-layouts", userLayoutsH.Create)
					r.Put("/dashboard-layouts/order", userLayoutsH.Reorder)
					r.Patch("/dashboard-layouts/{layoutId}", userLayoutsH.Update)
					r.Delete("/dashboard-layouts/{layoutId}", userLayoutsH.Delete)
```

- [ ] **Step 5: Build, test, and smoke the route**

Run: `go build ./... && go test ./...`
Expected: PASS.

With a scratch database and an institution-admin bearer token:

```bash
DATABASE_URL="$TEST_DATABASE_URL" ./bin/api &
curl -s -H "Authorization: Bearer $INST_TOKEN" \
  'http://localhost:8080/api/v1/institution/metrics/catalog' | head -c 400
curl -s -H "Authorization: Bearer $INST_TOKEN" \
  'http://localhost:8080/api/v1/institution/metrics?metrics=attempts_completed,moderation_actions&from=2026-07-01&to=2026-07-30' | head -c 600
```
Expected: the catalog omits moderation and social metrics; the metrics call returns `dropped` naming `moderation_actions` with reason `not institution-scopable`, and `scope.effective` is `institution`.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/institution cmd/api
git commit -m "feat(institution): analytics endpoints scoped to the caller's institution

Scope comes from the token; no query parameter can widen or redirect it."
```

---

## Task 9: Teacher analytics routes, both scope kinds, and the unassigned fallback

**Files:**
- Create: `internal/domain/teacher/metrics_scope.go`
- Create: `internal/domain/teacher/metrics_scope_test.go`
- Modify: `cmd/api/main.go` (wiring + routes inside the existing `/teacher` block)

**Interfaces:**
- Consumes: `metrics.ScopeResolver` (Task 5), `metrics.ParseScopeKind` (Task 2).
- Produces: `func teacher.MetricsScopeResolver(db *pgxpool.Pool) metrics.ScopeResolver`.

- [ ] **Step 1: Write the failing test**

Create `internal/domain/teacher/metrics_scope_test.go`:

```go
package teacher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qwish/backend/internal/domain/metrics"
	"github.com/qwish/backend/internal/middleware"
)

func request(t *testing.T, url, userID, instID string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	ctx := req.Context()
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, userID)
	if instID != "" {
		ctx = context.WithValue(ctx, middleware.ContextKeyInstID, instID)
	}
	return req.WithContext(ctx)
}

func TestTeacherScopeDefaultsToClasses(t *testing.T) {
	pool := openTestDB(t)
	f := seedTeacherFixture(t, pool)
	resolve := MetricsScopeResolver(pool)

	sc, note, err := resolve(request(t, "/teacher/metrics", f.TeacherID, f.InstitutionID))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sc.Kind != metrics.ScopeClasses {
		t.Errorf("kind = %q, want teacher_classes", sc.Kind)
	}
	if sc.ID != f.TeacherID {
		t.Errorf("id = %q, want the teacher id", sc.ID)
	}
	if note.Reason != "" {
		t.Errorf("assigned teacher got a fallback reason: %q", note.Reason)
	}
}

func TestTeacherScopeQuizzes(t *testing.T) {
	pool := openTestDB(t)
	f := seedTeacherFixture(t, pool)
	resolve := MetricsScopeResolver(pool)

	sc, note, err := resolve(request(t, "/teacher/metrics?scope=quizzes", f.TeacherID, f.InstitutionID))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sc.Kind != metrics.ScopeQuizzes || note.Effective != metrics.ScopeQuizzes {
		t.Errorf("kind = %q, note = %+v", sc.Kind, note)
	}
}

// PRD §5.4: an unassigned teacher sees all institution students. Analytics must
// honour that and must say so, or the teacher reads institution-wide numbers as
// their own class's.
func TestUnassignedTeacherFallsBackToInstitution(t *testing.T) {
	pool := openTestDB(t)
	f := seedTeacherFixture(t, pool)
	resolve := MetricsScopeResolver(pool)

	sc, note, err := resolve(request(t, "/teacher/metrics", f.LonerTeacherID, f.InstitutionID))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sc.Kind != metrics.ScopeInstitution {
		t.Errorf("kind = %q, want institution", sc.Kind)
	}
	if sc.ID != f.InstitutionID {
		t.Errorf("id = %q, want the institution id", sc.ID)
	}
	if note.Requested != metrics.ScopeClasses || note.Effective != metrics.ScopeInstitution {
		t.Errorf("note = %+v", note)
	}
	if note.Reason == "" {
		t.Error("fallback carries no reason for the UI to show")
	}
}

// The quizzes scope never falls back: a teacher with no classes may still have
// authored quizzes, and "no classes" is not a reason to widen that view.
func TestUnassignedTeacherQuizzesScopeDoesNotFallBack(t *testing.T) {
	pool := openTestDB(t)
	f := seedTeacherFixture(t, pool)
	resolve := MetricsScopeResolver(pool)

	sc, note, err := resolve(request(t, "/teacher/metrics?scope=quizzes", f.LonerTeacherID, f.InstitutionID))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sc.Kind != metrics.ScopeQuizzes || note.Reason != "" {
		t.Errorf("kind = %q, note = %+v", sc.Kind, note)
	}
}

func TestTeacherScopeRejectsUnknownKind(t *testing.T) {
	pool := openTestDB(t)
	f := seedTeacherFixture(t, pool)
	resolve := MetricsScopeResolver(pool)

	_, _, err := resolve(request(t, "/teacher/metrics?scope=everything", f.TeacherID, f.InstitutionID))
	if err == nil {
		t.Fatal("want an error for an unknown scope")
	}
}
```

- [ ] **Step 2: Write the fixture helper and test-db helper**

Create `internal/domain/teacher/testdb_test.go`:

```go
package teacher

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// openTestDB connects to TEST_DATABASE_URL, or skips the test. Point it at a
// scratch database — these tests write rows.
func openTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping database integration test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping TEST_DATABASE_URL: %v", err)
	}
	return pool
}
```

Create `internal/domain/teacher/fixtures_test.go`:

```go
package teacher

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// teacherFixture is one institution holding two teachers and two students:
//
//	TeacherID       — assigned to GroupID, which contains StudentID
//	LonerTeacherID  — assigned to no group
//	OtherStudentID  — in the institution but in no group
//
// Everything cascades away with the institution except users, which are
// deleted explicitly because users.institution_id has no ON DELETE clause.
type teacherFixture struct {
	InstitutionID  string
	TeacherID      string
	LonerTeacherID string
	StudentID      string
	OtherStudentID string
	GroupID        string
	QuizID         string
	OtherQuizID    string
}

func seedTeacherFixture(t *testing.T, pool *pgxpool.Pool) teacherFixture {
	t.Helper()
	ctx := context.Background()
	// A per-run suffix keeps the unique constraints on email and referral codes
	// satisfied when the suite runs twice against the same scratch database.
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var f teacherFixture

	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}

	must("institution", pool.QueryRow(ctx, `
		INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
		VALUES ('Fixture School '||$1, 'school', 'fixture-'||$1||'@example.test', 'S'||$1, 'T'||$1, 'verified')
		RETURNING id`, tag).Scan(&f.InstitutionID))

	newUser := func(role, label string, dest *string) {
		t.Helper()
		must(label, pool.QueryRow(ctx, `
			INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
			VALUES (gen_random_uuid(), $1, $1, $2, $3, $4)
			RETURNING id`,
			label+" "+tag, label+"-"+tag+"@example.test", role, f.InstitutionID).Scan(dest))
	}
	newUser("teacher", "assigned-teacher", &f.TeacherID)
	newUser("teacher", "loner-teacher", &f.LonerTeacherID)
	newUser("student", "group-student", &f.StudentID)
	newUser("student", "other-student", &f.OtherStudentID)

	must("group", pool.QueryRow(ctx, `
		INSERT INTO groups (institution_id, name, invite_code)
		VALUES ($1, 'Fixture Class', 'INV'||$2)
		RETURNING id`, f.InstitutionID, tag).Scan(&f.GroupID))

	_, err := pool.Exec(ctx,
		`INSERT INTO group_teachers (group_id, user_id) VALUES ($1, $2)`, f.GroupID, f.TeacherID)
	must("group_teachers", err)
	_, err = pool.Exec(ctx,
		`INSERT INTO group_students (group_id, user_id) VALUES ($1, $2)`, f.GroupID, f.StudentID)
	must("group_students", err)

	must("quiz", pool.QueryRow(ctx, `
		INSERT INTO quizzes (institution_id, created_by, title, type, status)
		VALUES ($1, $2, 'Fixture Quiz', 'knowledge_check', 'published')
		RETURNING id`, f.InstitutionID, f.TeacherID).Scan(&f.QuizID))
	must("other quiz", pool.QueryRow(ctx, `
		INSERT INTO quizzes (institution_id, created_by, title, type, status)
		VALUES ($1, $2, 'Other Quiz', 'knowledge_check', 'published')
		RETURNING id`, f.InstitutionID, f.LonerTeacherID).Scan(&f.OtherQuizID))

	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id = ANY($1)`,
			[]string{f.QuizID, f.OtherQuizID})
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id = ANY($1)`,
			[]string{f.QuizID, f.OtherQuizID})
		pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, f.GroupID)
		pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`,
			[]string{f.TeacherID, f.LonerTeacherID, f.StudentID, f.OtherStudentID})
		pool.Exec(ctx, `DELETE FROM institutions WHERE id = $1`, f.InstitutionID)
	})

	return f
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `TEST_DATABASE_URL=... go test ./internal/domain/teacher/ -run Scope -v`
Expected: FAIL — `undefined: MetricsScopeResolver`.

- [ ] **Step 4: Write the resolver**

Create `internal/domain/teacher/metrics_scope.go`:

```go
package teacher

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/metrics"
	"github.com/qwish/backend/internal/middleware"
)

// fallbackReason is shown verbatim in the panel. An unassigned teacher is
// answered institution-wide, and without this sentence they would read those
// numbers as their own class's.
const fallbackReason = "no classes assigned — showing the whole institution"

// MetricsScopeResolver picks the teacher's scope from the `scope` parameter and
// their group assignments. The id always comes from the token: `scope` selects
// the kind and nothing else.
//
// PRD §5.4: a teacher assigned to no group sees all institution students. That
// rule already governs the student list (hasGroupAssignments), and analytics
// honours it here — reporting the substitution rather than performing it
// silently.
func MetricsScopeResolver(db *pgxpool.Pool) metrics.ScopeResolver {
	return func(r *http.Request) (metrics.Scope, metrics.ScopeNote, error) {
		kind, err := metrics.ParseScopeKind(r.URL.Query().Get("scope"))
		if err != nil {
			return metrics.Scope{}, metrics.ScopeNote{},
				fmt.Errorf("%w: %s", metrics.ErrBadScopeRequest, err)
		}

		teacherID := middleware.GetUserID(r)
		if teacherID == "" {
			return metrics.Scope{}, metrics.ScopeNote{},
				fmt.Errorf("%w: no teacher on this token", metrics.ErrBadScopeRequest)
		}

		note := metrics.ScopeNote{Requested: kind, Effective: kind}

		// The quizzes scope never falls back: a teacher with no classes may
		// still have authored quizzes, and "no classes" is not a reason to
		// widen that view.
		if kind == metrics.ScopeQuizzes {
			return metrics.Scope{Kind: kind, ID: teacherID}, note, nil
		}

		assigned, err := hasGroups(r.Context(), db, teacherID)
		if err != nil {
			return metrics.Scope{}, metrics.ScopeNote{}, err
		}
		if assigned {
			return metrics.Scope{Kind: metrics.ScopeClasses, ID: teacherID}, note, nil
		}

		instID := middleware.GetInstitutionID(r)
		if instID == "" {
			return metrics.Scope{}, metrics.ScopeNote{},
				fmt.Errorf("%w: you have no classes and no institution to fall back to",
					metrics.ErrBadScopeRequest)
		}
		note.Effective = metrics.ScopeInstitution
		note.Reason = fallbackReason
		return metrics.Scope{Kind: metrics.ScopeInstitution, ID: instID}, note, nil
	}
}

// hasGroups is the analytics counterpart of hasGroupAssignments, which takes a
// *http.Request and swallows its error. This one reports the error, because a
// failed lookup here would silently widen a teacher's scope.
func hasGroups(ctx context.Context, db *pgxpool.Pool, teacherID string) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM group_teachers WHERE user_id = $1)`, teacherID).Scan(&exists)
	return exists, err
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `TEST_DATABASE_URL=... go test ./internal/domain/teacher/ -v`
Expected: PASS, five scope tests.

- [ ] **Step 6: Register the routes**

In `cmd/api/main.go`:

```go
teacherMetricsH := metrics.NewHandler(pool, teacher.MetricsScopeResolver(pool))
```

Inside the existing `r.Route("/teacher", …)` block, after the reports routes:

```go
					// Analytics. `scope` selects classes or quizzes; the id it
					// filters on always comes from the token.
					// /metrics/catalog precedes /metrics so chi does not read
					// "catalog" as a wildcard segment.
					r.Get("/metrics/catalog", teacherMetricsH.Catalog)
					r.Get("/metrics", teacherMetricsH.Metrics)
					r.Get("/distributions", teacherMetricsH.Distributions)
					r.Get("/points-liability", teacherMetricsH.PointsLiability)

					// Dashboard layouts — private to the calling user.
					r.Get("/dashboard-layouts", userLayoutsH.List)
					r.Post("/dashboard-layouts", userLayoutsH.Create)
					r.Put("/dashboard-layouts/order", userLayoutsH.Reorder)
					r.Patch("/dashboard-layouts/{layoutId}", userLayoutsH.Update)
					r.Delete("/dashboard-layouts/{layoutId}", userLayoutsH.Delete)
```

- [ ] **Step 7: Build and run everything**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/domain/teacher cmd/api
git commit -m "feat(teacher): analytics endpoints for class and quiz scopes

An unassigned teacher is answered institution-wide per PRD 5.4, and the
response says so — scope.effective and scope.reason — so the panel can tell
them rather than passing institution numbers off as their class's."
```

---

## Task 10: End-to-end scope isolation tests

**Files:**
- Create: `internal/domain/metrics/scope_integration_test.go`
- Modify: `internal/domain/metrics/testdb_test.go` (add the fixture helper, duplicated from Task 9's)

**Interfaces:**
- Consumes: everything.
- Produces: nothing consumed by later tasks.

These are the tests that would catch a wrong predicate — the unit tests only prove the SQL *text* is right.

- [ ] **Step 1: Copy the fixture helper into the metrics package**

The fixture in Task 9 lives in `package teacher`. Copy `fixtures_test.go` to `internal/domain/metrics/fixtures_test.go` and change its package clause to `metrics` and the type/function names to `scopeFixture` / `seedScopeFixture`. Duplication is correct here: a test helper exported for cross-package use would have to live in non-test code and ship in the binary.

- [ ] **Step 2: Write the failing tests**

Create `internal/domain/metrics/scope_integration_test.go`:

```go
package metrics

import (
	"context"
	"testing"
	"time"
)

// window covering the seeded attempts.
func fixtureWindow() Window {
	now := time.Now().In(IST)
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, IST)
	return Window{From: today.AddDate(0, 0, -7), To: today, Gran: GranDay}
}

// seedAttempt records one completed attempt by user on quiz, today.
func seedAttempt(t *testing.T, f scopeFixture, userID, quizID string) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO quiz_attempts (quiz_id, user_id, status, score_pct, started_at, completed_at)
		VALUES ($1, $2, 'completed', 80, now(), now())`, quizID, userID)
	if err != nil {
		t.Fatalf("seed attempt: %v", err)
	}
}

func totalFor(t *testing.T, svc *MetricsService, sc Scope, id string) float64 {
	t.Helper()
	sel, _, err := SelectMetrics([]string{id}, sc.Kind)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}
	totals, err := svc.Totals(context.Background(), sel, fixtureWindow(), sc)
	if err != nil {
		t.Fatalf("Totals: %v", err)
	}
	v, ok := totals[id]
	if !ok {
		t.Fatalf("totals has no %q: %+v", id, totals)
	}
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	default:
		t.Fatalf("totals[%q] is %T", id, v)
		return 0
	}
}

// A class scope must count the teacher's own students and no one else's.
func TestClassScopeExcludesOutsiders(t *testing.T) {
	pool := openTestDB(t)
	f := seedScopeFixture(t, pool)
	svc := NewMetricsService(pool)

	seedAttempt(t, f, f.StudentID, f.QuizID)      // in the class
	seedAttempt(t, f, f.OtherStudentID, f.QuizID) // same institution, no class

	classScope := Scope{Kind: ScopeClasses, ID: f.TeacherID}
	if got := totalFor(t, svc, classScope, "attempts_completed"); got != 1 {
		t.Errorf("class scope counted %v attempts, want 1", got)
	}

	instScope := Scope{Kind: ScopeInstitution, ID: f.InstitutionID}
	if got := totalFor(t, svc, instScope, "attempts_completed"); got != 2 {
		t.Errorf("institution scope counted %v attempts, want 2", got)
	}
}

// A quizzes scope must count attempts on the teacher's quizzes regardless of
// who took them, and none on another author's quiz.
func TestQuizScopeExcludesOtherAuthors(t *testing.T) {
	pool := openTestDB(t)
	f := seedScopeFixture(t, pool)
	svc := NewMetricsService(pool)

	seedAttempt(t, f, f.OtherStudentID, f.QuizID)      // their quiz, outsider taker
	seedAttempt(t, f, f.StudentID, f.OtherQuizID)      // their student, other author

	quizScope := Scope{Kind: ScopeQuizzes, ID: f.TeacherID}
	if got := totalFor(t, svc, quizScope, "attempts_completed"); got != 1 {
		t.Errorf("quiz scope counted %v attempts, want 1", got)
	}
}

// Distributions must omit shapes it cannot express rather than answering them
// institution-wide.
func TestDistributionsDropsUnanswerableShapes(t *testing.T) {
	pool := openTestDB(t)
	f := seedScopeFixture(t, pool)
	svc := NewMetricsService(pool)

	shapes, dropped, err := svc.Distributions(context.Background(),
		Scope{Kind: ScopeQuizzes, ID: f.TeacherID})
	if err != nil {
		t.Fatalf("Distributions: %v", err)
	}
	if _, present := shapes["streak_bands"]; present {
		t.Error("streak_bands answered under a quizzes scope")
	}
	var sawStreaks bool
	for _, d := range dropped {
		if d.ID == "streak_bands" {
			sawStreaks = true
		}
	}
	if !sawStreaks {
		t.Errorf("streak_bands not reported as dropped: %+v", dropped)
	}
	if _, present := shapes["score_histogram"]; !present {
		t.Error("score_histogram should answer a quizzes scope")
	}
}

func TestPointsLiabilityRefusesQuizScope(t *testing.T) {
	pool := openTestDB(t)
	f := seedScopeFixture(t, pool)
	svc := NewMetricsService(pool)

	if _, err := svc.PointsLiability(context.Background(),
		Scope{Kind: ScopeQuizzes, ID: f.TeacherID}); err == nil {
		t.Fatal("want ErrScopeUnsupported")
	}
}
```

The fixture struct needs the pool available to `seedAttempt`; add a `pool *pgxpool.Pool` field to `scopeFixture` in `fixtures_test.go` and set it in the seeder.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `TEST_DATABASE_URL=... go test ./internal/domain/metrics/ -run 'ScopeExcludes|Distributions|Liability' -v`
Expected: FAIL initially only if a predicate is wrong. If the implementation from Tasks 2–4 is correct they pass immediately — that is a valid outcome for a verification task. What must not happen is a compile error or a skip: if they skip, `TEST_DATABASE_URL` is not set and the task is not done.

- [ ] **Step 4: Confirm they pass and that the whole suite is green**

Run: `TEST_DATABASE_URL=... go test ./... -v`
Expected: PASS everywhere, no skips in `internal/domain/{metrics,teacher,admin}`.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/metrics
git commit -m "test(metrics): end-to-end scope isolation

Proves a teacher's class scope excludes non-class students in the same
institution, a quiz scope excludes other authors' quizzes, and the snapshot
shapes drop rather than widen."
```

---

## Task 11: API documentation

**Files:**
- Modify: `API_DOC.md` (§12)

**Interfaces:**
- Consumes: the finished endpoints.
- Produces: nothing.

- [ ] **Step 1: Extend §12**

Add to the existing analytics section:

1. **Scope contract.** Every analytics response carries `scope: { requested, effective, reason }`. `reason` is `null` unless `effective` differs from `requested`. Clients must render `reason` when it is non-null.
2. **`MetricDef.scopes`.** The `scopable` boolean is replaced by `scopes: ScopeKind[]`, listing which of `institution`, `teacher_classes`, `teacher_quizzes` the metric answers. `/metrics/catalog` already filters to the caller's kind, so a client normally does not need to read this — it exists for the super-admin console, whose scope changes per request.
3. **Institution endpoints.** `GET /institution/metrics{,/catalog}`, `/institution/distributions`, `/institution/points-liability`, and the five `/institution/dashboard-layouts` verbs. Same parameters as the admin routes **except** `institution_id`, which is rejected as meaningless: the scope is the caller's institution.
4. **Teacher endpoints.** The same set plus `scope=classes|quizzes` (default `classes`). Document the unassigned fallback and that `points-liability?scope=quizzes` returns `400`.
5. **Distributions `dropped`.** `/distributions` now returns a `dropped` array under a scope, and omits the shapes it names.
6. **Behaviour change to note:** an unknown `institution_id` on `/admin/metrics` now returns `400` rather than `404`.

- [ ] **Step 2: Cross-check the frontend docs**

The two frontend API docs get their own Analytics sections in their own plans, but their claims must match this one. Confirm the paths and parameter names written here are what the frontends will consume:

```bash
grep -rn "institution/metrics\|teacher/metrics" ../qwish-institute-dashboard/API_DOC.md ../qwish-teacher-panel/API_DOC.md
```
Expected: no matches yet — those sections are written in the frontend plans.

- [ ] **Step 3: Commit**

```bash
git add API_DOC.md
git commit -m "docs: role-scoped analytics endpoints and the scope contract"
```

---

## Done when

- `go build ./... && go test ./...` passes with no skips when `TEST_DATABASE_URL` is set.
- `/admin/metrics` returns byte-identical data to before for a request with no `institution_id`.
- An institution admin's token returns only institution-scopable metrics, with the rest in `dropped`.
- A teacher with classes gets class-scoped numbers; a teacher without gets institution numbers plus a non-null `scope.reason`.
- `curl` against `/teacher/points-liability?scope=quizzes` returns `400` with an explanatory message.
