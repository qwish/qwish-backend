package metrics

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func testWindow() Window {
	return Window{
		From: time.Date(2026, time.July, 1, 0, 0, 0, 0, IST),
		To:   time.Date(2026, time.July, 30, 0, 0, 0, 0, IST),
		Gran: GranDay,
	}
}

func mustSelect(t *testing.T, ids ...string) []MetricDef {
	t.Helper()
	sel, _, err := SelectMetrics(ids, ScopeNone)
	if err != nil {
		t.Fatalf("SelectMetrics(%v): %v", ids, err)
	}
	return sel
}

// generate_series is load-bearing: without it a zero-activity bucket is a
// missing row, and the chart closes the gap, which reads as "flat" rather than
// "nothing happened".
func TestSeriesQueryHasGenerateSeriesSpine(t *testing.T) {
	sql, _ := BuildSeriesQuery(mustSelect(t, "attempts_completed"), testWindow(), Scope{})
	if !strings.Contains(sql, "generate_series") {
		t.Error("series query has no generate_series spine")
	}
	if !strings.Contains(sql, "LEFT JOIN") {
		t.Error("sources must LEFT JOIN onto the spine, or empty buckets vanish")
	}
	if !strings.Contains(sql, "COALESCE") {
		t.Error("unmatched buckets must COALESCE to zero")
	}
}

func TestSeriesQueryArgs(t *testing.T) {
	w := testWindow()
	_, args := BuildSeriesQuery(mustSelect(t, "attempts_completed"), w, Scope{})
	if len(args) != 3 {
		t.Fatalf("len(args) = %d, want 3 (from, to, trunc) when unscoped", len(args))
	}
	if args[0] != w.From {
		t.Errorf("args[0] = %v, want From %v", args[0], w.From)
	}
	// The upper bound is exclusive so the final day is fully included.
	wantTo := w.To.AddDate(0, 0, 1)
	if args[1] != wantTo {
		t.Errorf("args[1] = %v, want To+1day %v", args[1], wantTo)
	}
	if args[2] != "day" {
		t.Errorf("args[2] = %v, want \"day\"", args[2])
	}
}

func TestSeriesQueryPassesInstitutionID(t *testing.T) {
	id := "11111111-1111-1111-1111-111111111111"
	sql, args := BuildSeriesQuery(mustSelect(t, "attempts_completed"), testWindow(), Scope{Kind: ScopeInstitution, ID: id})
	if args[3] != id {
		t.Errorf("args[3] = %v, want the institution id", args[3])
	}
	// The predicate is emitted only when the scope is active, so there is no
	// NULL-tolerant wrapper to bind a nil against.
	if strings.Contains(sql, "IS NULL OR") {
		t.Error("scoped query still carries the old NULL-tolerant wrapper")
	}
	if !strings.Contains(sql, "u.institution_id = $4") {
		t.Error("attempts must scope through users.institution_id")
	}
}

// Metrics sharing a source share its subquery. Two metrics must not become two
// scans of quiz_attempts.
func TestMetricsSharingASourceShareOneSubquery(t *testing.T) {
	sql, _ := BuildSeriesQuery(mustSelect(t, "attempts_started", "attempts_abandoned"), testWindow(), Scope{})
	if n := strings.Count(sql, "FROM quiz_attempts qa JOIN users u"); n != 1 {
		t.Errorf("quiz_attempts is scanned %d times, want 1 — both metrics share the attempts_start source", n)
	}
}

func TestUnrelatedSourcesGetSeparateSubqueries(t *testing.T) {
	sql, _ := BuildSeriesQuery(mustSelect(t, "attempts_completed", "signups"), testWindow(), Scope{})
	if !strings.Contains(sql, "FROM users u") {
		t.Error("signups needs its own users subquery")
	}
	if !strings.Contains(sql, "FROM quiz_attempts qa JOIN users u") {
		t.Error("attempts_completed needs its attempts subquery")
	}
}

// A derived metric is computed in the outer SELECT from other metrics' columns.
func TestDerivedMetricComputedInOuterSelect(t *testing.T) {
	sql, _ := BuildSeriesQuery(mustSelect(t, "abandon_rate"), testWindow(), Scope{})
	if !strings.Contains(sql, "AS abandon_rate") {
		t.Error("abandon_rate is not projected")
	}
	if !strings.Contains(sql, "NULLIF") {
		t.Error("a rate must guard against divide-by-zero with NULLIF")
	}
	for _, dep := range []string{"attempts_abandoned", "attempts_started"} {
		if !strings.Contains(sql, "AS "+dep) {
			t.Errorf("dependency %q is not projected, so the rate has nothing to divide", dep)
		}
	}
}

// The substitution must rewrite a dependency into its source column, never
// leave a bare metric id that Postgres would read as an unknown identifier.
func TestDerivedMetricSubstitutesSourceColumns(t *testing.T) {
	sql, _ := BuildSeriesQuery(mustSelect(t, "abandon_rate"), testWindow(), Scope{})
	ast, _ := Source("attempts_start")
	if !strings.Contains(sql, ast.Key+".attempts_abandoned") {
		t.Errorf("abandon_rate does not read %s.attempts_abandoned; substitution failed", ast.Key)
	}
}

// net_points reads three dependencies. This is the case where a naive
// ReplaceAll over overlapping ids would corrupt the SQL.
func TestMultiDependencyDerivedMetric(t *testing.T) {
	sql, _ := BuildSeriesQuery(mustSelect(t, "net_points"), testWindow(), Scope{})
	pl, _ := Source("ledger")
	for _, dep := range []string{"points_issued", "points_expired", "points_spent"} {
		if !strings.Contains(sql, pl.Key+"."+dep) {
			t.Errorf("net_points does not read %s.%s", pl.Key, dep)
		}
	}
}

func TestEveryProjectedMetricIsAliased(t *testing.T) {
	sel := mustSelect(t) // whole catalog
	sql, _ := BuildSeriesQuery(sel, testWindow(), Scope{})
	for _, m := range sel {
		if !strings.Contains(sql, "AS "+m.ID) {
			t.Errorf("metric %q is not aliased in the projection", m.ID)
		}
	}
}

func TestSeriesQueryOrdersByBucket(t *testing.T) {
	sql, _ := BuildSeriesQuery(mustSelect(t, "signups"), testWindow(), Scope{})
	if !strings.Contains(sql, "ORDER BY") {
		t.Error("series must be ordered by bucket — the chart draws them in order")
	}
}

// Totals recompute over the whole window. A totals query with a generate_series
// spine or a date_trunc would be folding buckets, which is the averaged-average
// bug this separation exists to prevent.
func TestTotalsQueryHasNoBucketing(t *testing.T) {
	sql, _ := BuildTotalsQuery(mustSelect(t, "avg_score", "active_users"), testWindow(), Scope{})
	if strings.Contains(sql, "generate_series") {
		t.Error("totals query must not bucket")
	}
	if strings.Contains(sql, "date_trunc") {
		t.Error("totals query must not date_trunc — it aggregates the whole window")
	}
	if strings.Contains(sql, "GROUP BY") {
		t.Error("totals query must not GROUP BY — one row covers the window")
	}
}

func TestTotalsQueryProjectsRateAndDistinctMetrics(t *testing.T) {
	sel := mustSelect(t, "avg_score", "active_users", "abandon_rate")
	sql, _ := BuildTotalsQuery(sel, testWindow(), Scope{})
	for _, id := range []string{"avg_score", "active_users", "abandon_rate"} {
		if !strings.Contains(sql, "AS "+id) {
			t.Errorf("totals query does not project %q", id)
		}
	}
}

func TestTotalsQueryScopes(t *testing.T) {
	id := "11111111-1111-1111-1111-111111111111"
	sql, args := BuildTotalsQuery(mustSelect(t, "attempts_completed"), testWindow(), Scope{Kind: ScopeInstitution, ID: id})
	// $3, not $4: the totals query takes no date_trunc unit.
	if args[2] != id {
		t.Errorf("args[2] = %v, want the institution id", args[2])
	}
	if !strings.Contains(sql, "u.institution_id = $3") {
		t.Error("totals must honour the institution filter too")
	}
}

// An unscoped query carries no scope placeholder, so the arg counts are one
// lower than when the institution filter was always bound (as NULL or a value).
func TestBuildQueriesWithNoMetricsIsSafe(t *testing.T) {
	sql, args := BuildSeriesQuery(nil, testWindow(), Scope{})
	if sql == "" || len(args) != 3 {
		t.Errorf("empty selection must still produce a valid bucket-only query; got sql=%q args=%v", sql, args)
	}
	tSQL, tArgs := BuildTotalsQuery(nil, testWindow(), Scope{})
	if tSQL == "" || len(tArgs) != 2 {
		t.Errorf("empty selection must still produce a valid totals query; got sql=%q args=%v", tSQL, tArgs)
	}
}

// Deterministic output, so the query plan cache and these assertions both hold.
func TestQueryTextIsDeterministic(t *testing.T) {
	sel := mustSelect(t)
	first, _ := BuildSeriesQuery(sel, testWindow(), Scope{})
	for i := 0; i < 20; i++ {
		again, _ := BuildSeriesQuery(sel, testWindow(), Scope{})
		if again != first {
			t.Fatal("BuildSeriesQuery is not deterministic across calls — map iteration order is leaking")
		}
	}
}

// No source may be joined twice, which would make its alias ambiguous.
func TestNoDuplicateJoinAliases(t *testing.T) {
	sql, _ := BuildSeriesQuery(mustSelect(t), testWindow(), Scope{})
	for _, key := range SourceKeys() {
		s, _ := Source(key)
		if n := strings.Count(sql, ") "+s.Key+" ON "); n > 1 {
			t.Errorf("source alias %q is joined %d times", s.Key, n)
		}
	}
}

// Postgres rejects a statement whose parameter numbers skip one ("could not
// determine data type of parameter $3") and rejects a bind with the wrong
// count. Both builders must therefore use $1..$len(args) with no gaps — the
// totals query has no date_trunc unit, so its numbering differs from the
// series query's and drifts apart easily.
func TestPlaceholdersAreContiguousAndMatchArgs(t *testing.T) {
	// Every scopable and non-scopable source at once, so any source's predicate
	// numbering is covered.
	sel := mustSelect(t)
	build := map[string]func() (string, []any){
		"series": func() (string, []any) { return BuildSeriesQuery(sel, testWindow(), Scope{}) },
		"totals": func() (string, []any) { return BuildTotalsQuery(sel, testWindow(), Scope{}) },
	}
	for name, fn := range build {
		sql, args := fn()
		for n := 1; n <= len(args); n++ {
			if !strings.Contains(sql, fmt.Sprintf("$%d", n)) {
				t.Errorf("%s query passes %d args but never references $%d", name, len(args), n)
			}
		}
		if strings.Contains(sql, fmt.Sprintf("$%d", len(args)+1)) {
			t.Errorf("%s query references $%d but passes only %d args", name, len(args)+1, len(args))
		}
	}
}

func TestSeriesQueryOmitsScopeArgWhenUnscoped(t *testing.T) {
	sql, args := BuildSeriesQuery(mustSelect(t, "attempts_completed"), testWindow(), Scope{})
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
	sc := Scope{Kind: ScopeClasses, ID: "11111111-1111-1111-1111-111111111111"}
	sql, args := BuildSeriesQuery(sel, testWindow(), sc)
	if len(args) != 4 {
		t.Fatalf("scoped series: got %d args, want 4", len(args))
	}
	if args[3] != sc.ID {
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
	sc := Scope{Kind: ScopeQuizzes, ID: "22222222-2222-2222-2222-222222222222"}
	sql, args := BuildTotalsQuery(sel, testWindow(), sc)
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
// to the highest referenced must appear, under every scope kind.
func TestParameterNumberingIsContiguous(t *testing.T) {
	scopes := []Scope{
		{},
		{Kind: ScopeInstitution, ID: "33333333-3333-3333-3333-333333333333"},
		{Kind: ScopeClasses, ID: "33333333-3333-3333-3333-333333333333"},
		{Kind: ScopeQuizzes, ID: "33333333-3333-3333-3333-333333333333"},
	}
	for _, sc := range scopes {
		sel, _, err := SelectMetrics(nil, sc.Kind)
		if err != nil {
			t.Fatalf("SelectMetrics(%q): %v", sc.Kind, err)
		}
		seriesSQL, seriesArgs := BuildSeriesQuery(sel, testWindow(), sc)
		totalsSQL, totalsArgs := BuildTotalsQuery(sel, testWindow(), sc)
		for _, tc := range []struct {
			name string
			sql  string
			args []any
		}{
			{"series", seriesSQL, seriesArgs},
			{"totals", totalsSQL, totalsArgs},
		} {
			for n := 1; n <= len(tc.args); n++ {
				if !strings.Contains(tc.sql, fmt.Sprintf("$%d", n)) {
					t.Errorf("%s/%s: %d args but $%d never appears", sc.Kind, tc.name, len(tc.args), n)
				}
			}
			if strings.Contains(tc.sql, fmt.Sprintf("$%d", len(tc.args)+1)) {
				t.Errorf("%s/%s: references $%d with only %d args",
					sc.Kind, tc.name, len(tc.args)+1, len(tc.args))
			}
		}
	}
}
