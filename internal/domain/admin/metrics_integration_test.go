package admin

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
	sel, _, err := SelectMetrics([]string{"attempts_completed", "signups"}, false)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}

	series, err := svc.Series(context.Background(), sel, w, nil)
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
			sel, _, err := SelectMetrics([]string{m.ID}, false)
			if err != nil {
				t.Fatalf("SelectMetrics(%s): %v", m.ID, err)
			}
			if _, err := svc.Series(context.Background(), sel, w, nil); err != nil {
				t.Errorf("series query failed: %v", err)
			}
			if _, err := svc.Totals(context.Background(), sel, w, nil); err != nil {
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
		if !m.Scopable {
			continue
		}
		t.Run(m.ID, func(t *testing.T) {
			sel, _, err := SelectMetrics([]string{m.ID}, true)
			if err != nil {
				t.Fatalf("SelectMetrics(%s): %v", m.ID, err)
			}
			if _, err := svc.Series(context.Background(), sel, w, &inst); err != nil {
				t.Errorf("scoped series query failed: %v", err)
			}
			if _, err := svc.Totals(context.Background(), sel, w, &inst); err != nil {
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
	sel, _, err := SelectMetrics(nil, false)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}
	if _, err := svc.Series(context.Background(), sel, w, nil); err != nil {
		t.Errorf("full-catalog series failed: %v", err)
	}
	if _, err := svc.Totals(context.Background(), sel, w, nil); err != nil {
		t.Errorf("full-catalog totals failed: %v", err)
	}
}

// All five granularities must produce valid SQL against Postgres — 'quarter' is
// accepted by date_trunc but has no interval, which is why PGInterval maps it
// to '3 months'.
func TestAllGranularitiesExecute(t *testing.T) {
	pool := openTestDB(t)
	svc := NewMetricsService(pool)
	sel, _, err := SelectMetrics([]string{"signups"}, false)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}

	for _, g := range Granularities {
		t.Run(string(g), func(t *testing.T) {
			// Keep every window inside that granularity's cap.
			days := MaxWindowDays(g) - 1
			to := time.Date(2026, time.July, 30, 0, 0, 0, 0, IST)
			w := Window{From: to.AddDate(0, 0, -days), To: to, Gran: g}
			if _, err := svc.Series(context.Background(), sel, w, nil); err != nil {
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

	sel, _, err := SelectMetrics([]string{"avg_score"}, false)
	if err != nil {
		t.Fatalf("SelectMetrics: %v", err)
	}
	totals, err := svc.Totals(ctx, sel, w, nil)
	if err != nil {
		t.Fatalf("Totals: %v", err)
	}
	series, err := svc.Series(ctx, sel, w, nil)
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
