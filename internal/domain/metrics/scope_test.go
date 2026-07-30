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
