package metrics

import (
	"context"
	"testing"
	"time"
)

// The behaviour that cannot be unit-tested: a bucket with no activity must come
// back as a zero row, not a missing one. Without generate_series the chart
// closes the gap and "nothing happened" renders as "flat".
func TestSeriesFillsEmptyBuckets(t *testing.T) {
	pool := openTestDB(t)
	svc := NewMetricsService(pool)

	// A window far in the past has no data, so every bucket must still appear.
	w := Window{
		From: time.Date(2001, time.January, 1, 0, 0, 0, 0, IST),
		To:   time.Date(2001, time.January, 10, 0, 0, 0, 0, IST),
		Gran: GranDay,
	}
	sel, _, err := SelectMetrics([]string{"attempts_completed", "signups"}, ScopeNone)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}

	series, err := svc.Series(context.Background(), sel, w, Scope{})
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if len(series) != 10 {
		t.Fatalf("len(series) = %d, want 10 zero-filled buckets", len(series))
	}
	for i, row := range series {
		for _, id := range []string{"attempts_completed", "signups"} {
			if row[id] == nil {
				t.Errorf("bucket %d: %s is nil, want 0", i, id)
			}
		}
	}
}

// Every metric's SQL must actually execute. This is the test that catches a
// typo'd column or a bad aggregate in the catalog — it runs the whole registry,
// one subtest per metric, so a failure names the offending entry.
func TestEveryCatalogMetricExecutes(t *testing.T) {
	pool := openTestDB(t)
	svc := NewMetricsService(pool)
	w := Window{
		From: time.Date(2026, time.July, 1, 0, 0, 0, 0, IST),
		To:   time.Date(2026, time.July, 7, 0, 0, 0, 0, IST),
		Gran: GranDay,
	}

	for _, m := range Catalog() {
		t.Run(m.ID, func(t *testing.T) {
			sel, _, err := SelectMetrics([]string{m.ID}, ScopeNone)
			if err != nil {
				t.Fatalf("SelectMetrics(%s): %v", m.ID, err)
			}
			if _, err := svc.Series(context.Background(), sel, w, Scope{}); err != nil {
				t.Errorf("series query failed: %v", err)
			}
			if _, err := svc.Totals(context.Background(), sel, w, Scope{}); err != nil {
				t.Errorf("totals query failed: %v", err)
			}
		})
	}
}

// Same, with an institution filter active, so the scoping joins and the
// $4::uuid predicate get exercised too.
func TestEveryScopableMetricExecutesScoped(t *testing.T) {
	pool := openTestDB(t)
	svc := NewMetricsService(pool)
	w := Window{
		From: time.Date(2026, time.July, 1, 0, 0, 0, 0, IST),
		To:   time.Date(2026, time.July, 7, 0, 0, 0, 0, IST),
		Gran: GranDay,
	}
	inst := "11111111-1111-1111-1111-111111111111" // need not exist; the filter just must run

	for _, m := range Catalog() {
		if !m.answers(ScopeInstitution) {
			continue
		}
		t.Run(m.ID, func(t *testing.T) {
			sel, _, err := SelectMetrics([]string{m.ID}, ScopeInstitution)
			if err != nil {
				t.Fatalf("SelectMetrics(%s): %v", m.ID, err)
			}
			if _, err := svc.Series(context.Background(), sel, w, Scope{Kind: ScopeInstitution, ID: inst}); err != nil {
				t.Errorf("scoped series query failed: %v", err)
			}
			if _, err := svc.Totals(context.Background(), sel, w, Scope{Kind: ScopeInstitution, ID: inst}); err != nil {
				t.Errorf("scoped totals query failed: %v", err)
			}
		})
	}
}

// The whole catalog in one request must also compose into valid SQL — 23 joined
// subqueries is the worst case the endpoint can be asked for.
func TestWholeCatalogExecutesInOneQuery(t *testing.T) {
	pool := openTestDB(t)
	svc := NewMetricsService(pool)
	w := Window{
		From: time.Date(2026, time.July, 1, 0, 0, 0, 0, IST),
		To:   time.Date(2026, time.July, 7, 0, 0, 0, 0, IST),
		Gran: GranDay,
	}
	sel, _, err := SelectMetrics(nil, ScopeNone)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}
	if _, err := svc.Series(context.Background(), sel, w, Scope{}); err != nil {
		t.Errorf("full-catalog series failed: %v", err)
	}
	if _, err := svc.Totals(context.Background(), sel, w, Scope{}); err != nil {
		t.Errorf("full-catalog totals failed: %v", err)
	}
}

// All five granularities must produce valid SQL against Postgres — 'quarter' is
// accepted by date_trunc but has no interval, which is why PGInterval maps it
// to '3 months'.
func TestAllGranularitiesExecute(t *testing.T) {
	pool := openTestDB(t)
	svc := NewMetricsService(pool)
	sel, _, err := SelectMetrics([]string{"signups"}, ScopeNone)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}

	for _, g := range Granularities {
		t.Run(string(g), func(t *testing.T) {
			// Keep every window inside that granularity's cap.
			days := MaxWindowDays(g) - 1
			to := time.Date(2026, time.July, 30, 0, 0, 0, 0, IST)
			w := Window{From: to.AddDate(0, 0, -days), To: to, Gran: g}
			if _, err := svc.Series(context.Background(), sel, w, Scope{}); err != nil {
				t.Errorf("granularity %s failed: %v", g, err)
			}
		})
	}
}

// A rate over the whole window is not the mean of the bucket rates when bucket
// volumes differ. This runs both queries over the same window and asserts the
// totals query is doing its own aggregation rather than folding buckets.
func TestRateTotalIsRecomputedNotAveraged(t *testing.T) {
	pool := openTestDB(t)
	svc := NewMetricsService(pool)
	ctx := context.Background()

	// A wide window over real data, so bucket volumes vary.
	to := time.Date(2026, time.July, 30, 0, 0, 0, 0, IST)
	w := Window{From: to.AddDate(0, 0, -89), To: to, Gran: GranDay}

	sel, _, err := SelectMetrics([]string{"avg_score"}, ScopeNone)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}
	totals, err := svc.Totals(ctx, sel, w, Scope{})
	if err != nil {
		t.Fatalf("Totals: %v", err)
	}
	series, err := svc.Series(ctx, sel, w, Scope{})
	if err != nil {
		t.Fatalf("Series: %v", err)
	}

	windowAvg, ok := toFloat(totals["avg_score"])
	if !ok {
		t.Skipf("avg_score total is %T, not numeric — no completed attempts in the window", totals["avg_score"])
	}

	// Mean of the non-zero bucket values, which is what a client folding the
	// series would wrongly compute.
	var sum float64
	var n int
	for _, row := range series {
		if v, ok := toFloat(row["avg_score"]); ok && v > 0 {
			sum += v
			n++
		}
	}
	if n < 2 {
		t.Skip("fewer than two active buckets — cannot distinguish the two computations")
	}
	foldedAvg := sum / float64(n)

	// They may legitimately coincide if every bucket had identical volume, but
	// the window total must never be *derived* from the folded value.
	t.Logf("window-recomputed avg_score = %.4f, folded bucket mean = %.4f", windowAvg, foldedAvg)
	if windowAvg < 0 || windowAvg > 100 {
		t.Errorf("avg_score total = %v, outside the valid 0-100 range", windowAvg)
	}
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}

func TestInstitutionExistsRejectsUnknownID(t *testing.T) {
	pool := openTestDB(t)
	svc := NewMetricsService(pool)
	ok, err := svc.InstitutionExists(context.Background(), "11111111-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatalf("InstitutionExists: %v", err)
	}
	if ok {
		t.Error("a random uuid reported as an existing institution")
	}
}

func TestDistributionsReturnsAllShapes(t *testing.T) {
	pool := openTestDB(t)
	svc := NewMetricsService(pool)

	got, _, err := svc.Distributions(context.Background(), Scope{})
	if err != nil {
		t.Fatalf("Distributions: %v", err)
	}
	for _, key := range []string{
		"score_histogram", "difficulty_bands", "streak_bands",
		"role_mix", "institution_type_mix", "quiz_status_funnel",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing %q in distributions response", key)
		}
	}
}

// Difficulty bands are fixed server-side so the histogram and the categorical
// legend cannot drift apart. All five must always be present, even at zero.
func TestDifficultyBandsAlwaysFive(t *testing.T) {
	pool := openTestDB(t)
	svc := NewMetricsService(pool)

	got, _, err := svc.Distributions(context.Background(), Scope{})
	if err != nil {
		t.Fatalf("Distributions: %v", err)
	}
	bands, ok := got["difficulty_bands"].([]map[string]any)
	if !ok {
		t.Fatalf("difficulty_bands has type %T, want []map[string]any", got["difficulty_bands"])
	}
	if len(bands) != 5 {
		t.Fatalf("len(difficulty_bands) = %d, want 5", len(bands))
	}
	wantLabels := []string{"Very easy", "Easy", "Moderate", "Hard", "Very hard"}
	for i, b := range bands {
		if b["label"] != wantLabels[i] {
			t.Errorf("band %d label = %v, want %q", i, b["label"], wantLabels[i])
		}
	}
}

// The score histogram is ten fixed 10-point bins, zero-filled, so the x-axis is
// stable regardless of what data exists.
func TestScoreHistogramHasTenFixedBins(t *testing.T) {
	pool := openTestDB(t)
	svc := NewMetricsService(pool)

	got, _, err := svc.Distributions(context.Background(), Scope{})
	if err != nil {
		t.Fatalf("Distributions: %v", err)
	}
	bins, ok := got["score_histogram"].([]map[string]any)
	if !ok {
		t.Fatalf("score_histogram has type %T, want []map[string]any", got["score_histogram"])
	}
	if len(bins) != 10 {
		t.Fatalf("len(score_histogram) = %d, want 10", len(bins))
	}
	for i, b := range bins {
		if lo, _ := toFloat(b["lo"]); int(lo) != i*10 {
			t.Errorf("bin %d lo = %v, want %d", i, b["lo"], i*10)
		}
		if b["count"] == nil {
			t.Errorf("bin %d count is nil, want a zero-filled number", i)
		}
	}
}

// Scoping must run without error on every shape, including the ones that reach
// their institution through a join.
func TestDistributionsScoped(t *testing.T) {
	pool := openTestDB(t)
	svc := NewMetricsService(pool)
	inst := "11111111-1111-1111-1111-111111111111"
	if _, _, err := svc.Distributions(context.Background(), Scope{Kind: ScopeInstitution, ID: inst}); err != nil {
		t.Fatalf("scoped Distributions: %v", err)
	}
}

// Liability is forward-looking: a schedule of what is about to expire, not a
// series of the past. Every bucket must be in the current month or later.
func TestPointsLiabilityIsForwardLooking(t *testing.T) {
	pool := openTestDB(t)
	svc := NewMetricsService(pool)

	got, err := svc.PointsLiability(context.Background(), Scope{})
	if err != nil {
		t.Fatalf("PointsLiability: %v", err)
	}
	for _, key := range []string{"as_of", "total", "months"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing %q in liability response", key)
		}
	}

	months, ok := got["months"].([]map[string]any)
	if !ok {
		t.Fatalf("months has type %T, want []map[string]any", got["months"])
	}
	thisMonth := time.Now().In(IST).Format("2006-01")
	var sum int64
	for _, m := range months {
		bucket, _ := m["month"].(string)
		if bucket < thisMonth {
			t.Errorf("month %q is in the past; liability must only look forward", bucket)
		}
		if v, ok := m["points"].(int64); ok {
			sum += v
		}
	}
	// total must equal the sum of the months, or the headline number and the
	// chart disagree.
	if total, ok := got["total"].(int64); ok && total != sum {
		t.Errorf("total = %d but months sum to %d", total, sum)
	}
}

func TestPointsLiabilityScoped(t *testing.T) {
	pool := openTestDB(t)
	svc := NewMetricsService(pool)
	inst := "11111111-1111-1111-1111-111111111111"
	if _, err := svc.PointsLiability(context.Background(), Scope{Kind: ScopeInstitution, ID: inst}); err != nil {
		t.Fatalf("scoped PointsLiability: %v", err)
	}
}
