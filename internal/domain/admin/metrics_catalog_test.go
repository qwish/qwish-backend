package admin

import (
	"strings"
	"testing"
)

func TestCatalogIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range Catalog() {
		if seen[m.ID] {
			t.Errorf("duplicate metric id %q", m.ID)
		}
		seen[m.ID] = true

		if m.Label == "" || m.Group == "" || m.Hint == "" {
			t.Errorf("%s: Label, Group and Hint are all required — the UI renders them", m.ID)
		}
		switch m.Kind {
		case KindAdditive, KindRate, KindDistinct:
		default:
			t.Errorf("%s: invalid Kind %q", m.ID, m.Kind)
		}
		switch m.Unit {
		case "count", "percent", "points", "seconds":
		default:
			t.Errorf("%s: invalid Unit %q", m.ID, m.Unit)
		}
		if m.Expr == "" {
			t.Errorf("%s: Expr is required", m.ID)
		}
		if m.Source == "" && len(m.Needs) == 0 {
			t.Errorf("%s: a derived metric (no Source) must declare Needs", m.ID)
		}
	}
}

// Every Source referenced by a metric must exist, and every Needs entry must
// name a real metric. This is the check that catches a typo'd registry edit.
func TestCatalogReferencesResolve(t *testing.T) {
	for _, m := range Catalog() {
		if m.Source != "" {
			if _, ok := Source(m.Source); !ok {
				t.Errorf("%s: unknown source %q", m.ID, m.Source)
			}
		}
		for _, need := range m.Needs {
			if _, ok := Lookup(need); !ok {
				t.Errorf("%s: Needs references unknown metric %q", m.ID, need)
			}
		}
	}
}

// A derived metric can only be scopable if everything it reads is scopable —
// otherwise its numerator and denominator would cover different populations.
func TestDerivedMetricScopabilityIsConsistent(t *testing.T) {
	for _, m := range Catalog() {
		if m.Source != "" || !m.Scopable {
			continue
		}
		for _, need := range m.Needs {
			dep, _ := Lookup(need)
			if !dep.Scopable {
				t.Errorf("%s is scopable but depends on non-scopable %q", m.ID, need)
			}
		}
	}
}

// A derived metric must not depend on another derived metric: SelectMetrics
// resolves dependencies in a single pass, so a chain would silently drop one.
func TestNoDerivedMetricDependsOnADerivedMetric(t *testing.T) {
	for _, m := range Catalog() {
		if m.Source != "" {
			continue
		}
		for _, need := range m.Needs {
			dep, _ := Lookup(need)
			if dep.Source == "" {
				t.Errorf("%s depends on derived metric %q; SelectMetrics only resolves one level", m.ID, need)
			}
		}
	}
}

// Every source must be reachable from some metric. An orphan source is dead
// SQL that no test will ever execute.
func TestEverySourceIsUsed(t *testing.T) {
	used := map[string]bool{}
	for _, m := range Catalog() {
		if m.Source != "" {
			used[m.Source] = true
		}
	}
	for _, key := range SourceKeys() {
		if !used[key] {
			t.Errorf("source %q is not referenced by any metric", key)
		}
	}
}

// Source join aliases must be unique, or two subqueries collide in the
// composed SQL and Postgres rejects the whole query.
func TestSourceAliasesAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, key := range SourceKeys() {
		s, _ := Source(key)
		if prev, dup := seen[s.Key]; dup {
			t.Errorf("sources %q and %q share the alias %q", prev, key, s.Key)
		}
		seen[s.Key] = key
	}
}

func TestRateMetricsUsePercentOrSeconds(t *testing.T) {
	for _, m := range Catalog() {
		if m.Kind == KindRate && m.Unit == "count" {
			t.Errorf("%s: a rate metric with unit=count is almost certainly mislabelled", m.ID)
		}
	}
}

func TestLookup(t *testing.T) {
	if m, ok := Lookup("attempts_completed"); !ok || m.Kind != KindAdditive {
		t.Errorf("Lookup(attempts_completed) = %+v, %v; want an additive metric", m, ok)
	}
	if _, ok := Lookup("not_a_metric"); ok {
		t.Error("Lookup(not_a_metric) returned ok=true")
	}
}

func TestSelectMetricsDefaultsToEverything(t *testing.T) {
	sel, dropped, err := SelectMetrics(nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sel) != len(Catalog()) {
		t.Errorf("len(selected) = %d, want the whole catalog (%d)", len(sel), len(Catalog()))
	}
	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want empty when unscoped", dropped)
	}
}

func TestSelectMetricsRejectsUnknownID(t *testing.T) {
	_, _, err := SelectMetrics([]string{"attempts_completed", "bogus_metric"}, false)
	if err == nil {
		t.Fatal("expected an error naming the unknown metric")
	}
	if !strings.Contains(err.Error(), "bogus_metric") {
		t.Errorf("error %q does not name the offending id", err.Error())
	}
}

// The core scoping guarantee: with an institution filter active, a non-scopable
// metric is dropped with a reason rather than silently returning platform-wide
// numbers under an institution heading.
func TestSelectMetricsDropsNonScopableWhenScoped(t *testing.T) {
	sel, dropped, err := SelectMetrics([]string{"attempts_completed", "moderation_actions"}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sel) != 1 || sel[0].ID != "attempts_completed" {
		t.Errorf("selected = %+v, want only attempts_completed", sel)
	}
	if len(dropped) != 1 || dropped[0].ID != "moderation_actions" {
		t.Fatalf("dropped = %+v, want moderation_actions", dropped)
	}
	if dropped[0].Reason == "" {
		t.Error("dropped entries must carry a reason — the UI shows it")
	}
}

func TestSelectMetricsKeepsNonScopableWhenUnscoped(t *testing.T) {
	sel, dropped, err := SelectMetrics([]string{"moderation_actions"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sel) != 1 || len(dropped) != 0 {
		t.Errorf("selected=%+v dropped=%+v; want the metric kept when no institution filter is active", sel, dropped)
	}
}

// Requesting only a derived metric must pull in the metrics it reads, or the
// SQL builder has no columns to divide.
func TestSelectMetricsPullsInDependencies(t *testing.T) {
	sel, _, err := SelectMetrics([]string{"abandon_rate"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := map[string]bool{}
	for _, m := range sel {
		got[m.ID] = true
	}
	for _, want := range []string{"abandon_rate", "attempts_abandoned", "attempts_started"} {
		if !got[want] {
			t.Errorf("selected %v is missing %q", got, want)
		}
	}
}

// A derived metric whose dependency is non-scopable must itself be dropped when
// scoped, not left projecting from columns that were removed.
func TestScopedSelectionNeverLeavesADerivedMetricWithoutItsDeps(t *testing.T) {
	sel, _, err := SelectMetrics(nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	present := map[string]bool{}
	for _, m := range sel {
		present[m.ID] = true
	}
	for _, m := range sel {
		if m.Source != "" {
			continue
		}
		for _, need := range m.Needs {
			if !present[need] {
				t.Errorf("derived metric %q survived scoping but its dependency %q was dropped", m.ID, need)
			}
		}
	}
}
