package admin

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// scopePredicate returns the NULL-tolerant institution filter for a source, so
// one query text serves both scoped and unscoped requests. `n` is the parameter
// position: the totals query has no date_trunc unit, so its placeholders are one
// lower than the series query's. Postgres rejects a statement that skips a
// parameter number ("could not determine data type of parameter $3"), so the
// numbering has to be contiguous per query.
func scopePredicate(s source, n int) string {
	if s.ScopeCol == "" {
		return ""
	}
	return fmt.Sprintf("($%d::uuid IS NULL OR %s = $%d)", n, s.ScopeCol, n)
}

// sourceGroups buckets the selected metrics by source, dropping derived ones,
// and returns the source keys in a stable order.
func sourceGroups(sel []MetricDef) (map[string][]MetricDef, []string) {
	groups := map[string][]MetricDef{}
	for _, m := range sel {
		if m.Source == "" {
			continue
		}
		groups[m.Source] = append(groups[m.Source], m)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic SQL text, so the query plan cache holds
	return groups, keys
}

var (
	idPatternMu sync.Mutex
	idPatterns  = map[string]*regexp.Regexp{}
)

// idPattern matches a metric id only as a whole word. Using a word boundary
// rather than a plain substring replace means a future metric id that contains
// another one (points_issued vs points_issued_net) cannot corrupt the SQL.
func idPattern(id string) *regexp.Regexp {
	idPatternMu.Lock()
	defer idPatternMu.Unlock()
	if re, ok := idPatterns[id]; ok {
		return re
	}
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(id) + `\b`)
	idPatterns[id] = re
	return re
}

// derivedExpr rewrites a derived metric's expression, replacing each dependency
// id with the source column it actually reads.
func derivedExpr(m MetricDef, coalesce bool) string {
	expr := m.Expr
	for _, need := range m.Needs {
		dep, ok := Lookup(need)
		if !ok || dep.Source == "" {
			continue
		}
		ds, ok := Source(dep.Source)
		if !ok {
			continue
		}
		col := ds.Key + "." + need
		if coalesce {
			col = fmt.Sprintf("COALESCE(%s, 0)", col)
		}
		expr = idPattern(need).ReplaceAllString(expr, col)
	}
	return expr
}

func whereClause(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(kept, "\n      AND ")
}

func instArg(instID *string) any {
	if instID == nil {
		return nil
	}
	return *instID
}

func seriesArgs(w Window, instID *string) []any {
	// Upper bound is exclusive so the final day is fully included.
	return []any{w.From, w.To.AddDate(0, 0, 1), w.Gran.PGTrunc(), instArg(instID)}
}

// The totals query does not bucket, so it takes no date_trunc unit.
func totalsArgs(w Window, instID *string) []any {
	return []any{w.From, w.To.AddDate(0, 0, 1), instArg(instID)}
}

// BuildSeriesQuery composes the bucketed query. $1=from, $2=to (exclusive),
// $3=date_trunc unit, $4=institution id or nil.
func BuildSeriesQuery(sel []MetricDef, w Window, instID *string) (string, []any) {
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
				scopePredicate(s, 4),
			),
			s.Key, s.Key))
	}

	b.WriteString("ORDER BY b.bucket")
	return b.String(), seriesArgs(w, instID)
}

// BuildTotalsQuery aggregates the whole window with no bucketing. $1=from,
// $2=to (exclusive), $3=institution id or nil — there is no date_trunc unit
// here, so the institution filter takes $3 rather than the series query's $4.
// Rate and
// distinct metrics are only correct this way — folding bucket values would
// average an average or double-count a user active on two days.
func BuildTotalsQuery(sel []MetricDef, w Window, instID *string) (string, []any) {
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
				scopePredicate(s, 3),
			),
			s.Key))
	}
	if len(keys) == 0 {
		b.WriteString("FROM (SELECT 1) AS noop\n")
	}

	return b.String(), totalsArgs(w, instID)
}
