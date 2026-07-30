package metrics

import (
	"fmt"
	"sort"
	"strings"
)

type Kind string

const (
	KindAdditive Kind = "additive" // bucket values may be summed to a total
	KindRate     Kind = "rate"     // must be recomputed over the whole window
	KindDistinct Kind = "distinct" // COUNT(DISTINCT ...); also not summable
)

// source is one subquery in the composed series query. Metrics sharing a source
// share its scan, which is why requesting four metrics does not run thirty-five
// subqueries.
type source struct {
	Key      string // join alias — must be unique across sources
	From     string // FROM clause, including any scoping join
	BucketOn string // the timestamp column bucketed on
	Where    string // extra predicate, or "" for none
	ScopeCol string // institution column for filtering, or "" if not scopable
}

var sources = map[string]source{
	// Completions bucket on completed_at. Scoped by the taker's institution.
	"attempts_done": {
		Key:      "ad",
		From:     "quiz_attempts qa JOIN users u ON u.id = qa.user_id",
		BucketOn: "qa.completed_at",
		Where:    "qa.status = 'completed'",
		ScopeCol: "u.institution_id",
	},
	// Starts and abandons bucket on started_at — an abandoned attempt never
	// gets a completed_at.
	"attempts_start": {
		Key:      "ast",
		From:     "quiz_attempts qa JOIN users u ON u.id = qa.user_id",
		BucketOn: "qa.started_at",
		ScopeCol: "u.institution_id",
	},
	"responses": {
		Key:      "qr",
		From:     "question_responses qr JOIN quiz_attempts qa ON qa.id = qr.attempt_id JOIN users u ON u.id = qa.user_id",
		BucketOn: "qr.submitted_at",
		ScopeCol: "u.institution_id",
	},
	"practice": {
		Key:      "pr",
		From:     "practice_sessions ps JOIN users u ON u.id = ps.user_id",
		BucketOn: "ps.completed_at",
		ScopeCol: "u.institution_id",
	},
	"signup": {
		Key:      "su",
		From:     "users u",
		BucketOn: "u.created_at",
		Where:    "u.deleted_at IS NULL",
		ScopeCol: "u.institution_id",
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
		ScopeCol: "u.institution_id",
	},
	"quiz_new": {
		Key:      "qn",
		From:     "quizzes q",
		BucketOn: "q.created_at",
		Where:    "q.deleted_at IS NULL",
		ScopeCol: "q.institution_id",
	},
	"quiz_pub": {
		Key:      "qp",
		From:     "quizzes q",
		BucketOn: "q.published_at",
		Where:    "q.deleted_at IS NULL",
		ScopeCol: "q.institution_id",
	},
	"quiz_appr": {
		Key:      "qap",
		From:     "quizzes q",
		BucketOn: "q.approved_at",
		Where:    "q.deleted_at IS NULL",
		ScopeCol: "q.institution_id",
	},
	"question_new": {
		Key:      "qs",
		From:     "questions qn JOIN quizzes q ON q.id = qn.quiz_id",
		BucketOn: "q.created_at",
		Where:    "q.deleted_at IS NULL",
		ScopeCol: "q.institution_id",
	},
	"topicreq": {
		Key:      "tr",
		From:     "topic_requests tr",
		BucketOn: "tr.created_at",
		ScopeCol: "tr.institution_id",
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

func Source(key string) (source, bool) {
	s, ok := sources[key]
	return s, ok
}

// SourceKeys returns every source key in a stable order.
func SourceKeys() []string {
	out := make([]string, 0, len(sources))
	for k := range sources {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type MetricDef struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Group    string   `json:"group"`
	Unit     string   `json:"unit"` // count | percent | points | seconds
	Kind     Kind     `json:"kind"`
	Scopable bool     `json:"scopable"`
	Hint     string   `json:"hint"`
	Source   string   `json:"-"` // subquery group; "" means derived
	Expr     string   `json:"-"` // aggregate, or derived SQL over Needs columns
	Needs    []string `json:"-"` // metric ids a derived metric reads
}

var catalog = []MetricDef{
	// ── Engagement ──────────────────────────────────────────────────────────
	{ID: "attempts_started", Label: "Attempts started", Group: "Engagement", Unit: "count",
		Kind: KindAdditive, Scopable: true, Source: "attempts_start", Expr: "COUNT(*)",
		Hint: "Attempts begun, bucketed on started_at. Scoped by the taker's institution."},
	{ID: "attempts_completed", Label: "Attempts completed", Group: "Engagement", Unit: "count",
		Kind: KindAdditive, Scopable: true, Source: "attempts_done", Expr: "COUNT(*)",
		Hint: "Attempts reaching status=completed, bucketed on completed_at. Scoped by the taker's institution."},
	{ID: "attempts_abandoned", Label: "Attempts abandoned", Group: "Engagement", Unit: "count",
		Kind: KindAdditive, Scopable: true, Source: "attempts_start",
		Expr: "COUNT(*) FILTER (WHERE qa.status = 'abandoned')",
		Hint: "Attempts left in progress for over 2 hours and swept to status=abandoned. Bucketed on started_at."},
	{ID: "abandon_rate", Label: "Abandonment rate", Group: "Engagement", Unit: "percent",
		Kind: KindRate, Scopable: true, Needs: []string{"attempts_abandoned", "attempts_started"},
		Expr: "attempts_abandoned::float / NULLIF(attempts_started, 0) * 100",
		Hint: "Abandoned as a share of started, recomputed over the whole window."},
	{ID: "completion_rate", Label: "Completion rate", Group: "Engagement", Unit: "percent",
		Kind: KindRate, Scopable: true, Needs: []string{"attempts_completed", "attempts_started"},
		Expr: "attempts_completed::float / NULLIF(attempts_started, 0) * 100",
		Hint: "Completed as a share of started, recomputed over the whole window."},
	{ID: "avg_score", Label: "Average score", Group: "Engagement", Unit: "percent",
		Kind: KindRate, Scopable: true, Source: "attempts_done", Expr: "AVG(qa.score_pct)",
		Hint: "Mean score_pct across completed attempts, recomputed over the whole window."},
	{ID: "median_time_to_complete", Label: "Median time to complete", Group: "Engagement", Unit: "seconds",
		Kind: KindRate, Scopable: true, Source: "attempts_done",
		Expr: "percentile_cont(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (qa.completed_at - qa.started_at)))",
		Hint: "Median completed_at minus started_at, in seconds."},
	{ID: "active_users", Label: "Active users", Group: "Engagement", Unit: "count",
		Kind: KindDistinct, Scopable: true, Source: "attempts_done", Expr: "COUNT(DISTINCT qa.user_id)",
		Hint: "Distinct users completing an attempt. Not summable across buckets."},
	{ID: "questions_answered", Label: "Questions answered", Group: "Engagement", Unit: "count",
		Kind: KindAdditive, Scopable: true, Source: "responses", Expr: "COUNT(*)",
		Hint: "Individual question responses, bucketed on submitted_at."},
	{ID: "practice_sessions", Label: "Practice sessions", Group: "Engagement", Unit: "count",
		Kind: KindAdditive, Scopable: true, Source: "practice", Expr: "COUNT(*)",
		Hint: "Completed practice sessions."},

	// ── Growth ──────────────────────────────────────────────────────────────
	{ID: "signups", Label: "Signups", Group: "Growth", Unit: "count",
		Kind: KindAdditive, Scopable: true, Source: "signup", Expr: "COUNT(*)",
		Hint: "New non-deleted users, bucketed on created_at."},
	{ID: "institutions_registered", Label: "Institutions registered", Group: "Growth", Unit: "count",
		Kind: KindAdditive, Scopable: false, Source: "inst_new", Expr: "COUNT(*)",
		Hint: "New institution records. Not institution-scopable."},
	{ID: "institutions_verified", Label: "Institutions verified", Group: "Growth", Unit: "count",
		Kind: KindAdditive, Scopable: false, Source: "inst_verified", Expr: "COUNT(*)",
		Hint: "Institutions reaching verified_at. Not institution-scopable."},
	{ID: "median_time_to_verify", Label: "Median time to verify", Group: "Growth", Unit: "seconds",
		Kind: KindRate, Scopable: false, Source: "inst_verified",
		Expr: "percentile_cont(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (i.verified_at - i.created_at)))",
		Hint: "Median verified_at minus created_at. Not institution-scopable."},

	// ── Economy ─────────────────────────────────────────────────────────────
	{ID: "points_issued", Label: "Points issued", Group: "Economy", Unit: "points",
		Kind: KindAdditive, Scopable: true, Source: "ledger",
		Expr: "COALESCE(SUM(pl.amount) FILTER (WHERE pl.amount > 0), 0)",
		Hint: "Sum of positive ledger amounts."},
	{ID: "points_expired", Label: "Points expired", Group: "Economy", Unit: "points",
		Kind: KindAdditive, Scopable: true, Source: "ledger",
		Expr: "COALESCE(-SUM(pl.amount) FILTER (WHERE pl.reason = 'expiry'), 0)",
		Hint: "Points removed by the expiry job, as a positive number."},
	{ID: "points_spent", Label: "Points spent", Group: "Economy", Unit: "points",
		Kind: KindAdditive, Scopable: true, Source: "ledger",
		Expr: "COALESCE(-SUM(pl.amount) FILTER (WHERE pl.amount < 0 AND pl.reason <> 'expiry'), 0)",
		Hint: "Negative ledger amounts other than expiry, as a positive number."},
	{ID: "net_points", Label: "Net points", Group: "Economy", Unit: "points",
		Kind: KindAdditive, Scopable: true,
		Needs: []string{"points_issued", "points_expired", "points_spent"},
		Expr:  "points_issued - points_expired - points_spent",
		Hint:  "Issued minus expired minus spent."},
	{ID: "avg_points_per_attempt", Label: "Avg points per attempt", Group: "Economy", Unit: "points",
		Kind: KindRate, Scopable: true, Needs: []string{"points_issued", "attempts_completed"},
		Expr: "points_issued::float / NULLIF(attempts_completed, 0)",
		Hint: "Points issued divided by completed attempts, recomputed over the whole window."},

	// ── Content ─────────────────────────────────────────────────────────────
	{ID: "quizzes_created", Label: "Quizzes created", Group: "Content", Unit: "count",
		Kind: KindAdditive, Scopable: true, Source: "quiz_new", Expr: "COUNT(*)",
		Hint: "New quizzes. Scoped by the quiz's owning institution."},
	{ID: "quizzes_published", Label: "Quizzes published", Group: "Content", Unit: "count",
		Kind: KindAdditive, Scopable: true, Source: "quiz_pub", Expr: "COUNT(*)",
		Hint: "Quizzes reaching published_at. Scoped by the quiz's owning institution."},
	{ID: "quizzes_approved", Label: "Quizzes approved", Group: "Content", Unit: "count",
		Kind: KindAdditive, Scopable: true, Source: "quiz_appr", Expr: "COUNT(*)",
		Hint: "Quizzes reaching approved_at. Scoped by the quiz's owning institution."},
	{ID: "questions_authored", Label: "Questions authored", Group: "Content", Unit: "count",
		Kind: KindAdditive, Scopable: true, Source: "question_new", Expr: "COUNT(*)",
		Hint: "Questions on quizzes created in the bucket."},
	{ID: "topic_requests", Label: "Topic requests", Group: "Content", Unit: "count",
		Kind: KindAdditive, Scopable: true, Source: "topicreq", Expr: "COUNT(*)",
		Hint: "Topic requests raised by institutions."},

	// ── Moderation & ops — none are institution-scopable ────────────────────
	{ID: "reports_opened", Label: "Reports opened", Group: "Moderation", Unit: "count",
		Kind: KindAdditive, Scopable: false, Source: "report_new", Expr: "COUNT(*)",
		Hint: "Content reports filed. Reports carry no institution linkage."},
	{ID: "reports_resolved", Label: "Reports resolved", Group: "Moderation", Unit: "count",
		Kind: KindAdditive, Scopable: false, Source: "report_done", Expr: "COUNT(*)",
		Hint: "Reports reaching resolved_at. Not institution-scopable."},
	{ID: "median_time_to_resolve_report", Label: "Median time to resolve report", Group: "Moderation", Unit: "seconds",
		Kind: KindRate, Scopable: false, Source: "report_done",
		Expr: "percentile_cont(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (r.resolved_at - r.created_at)))",
		Hint: "Median resolved_at minus created_at. Not institution-scopable."},
	{ID: "moderation_actions", Label: "Moderation actions", Group: "Moderation", Unit: "count",
		Kind: KindAdditive, Scopable: false, Source: "audit", Expr: "COUNT(*)",
		Hint: "Admin actions recorded in the audit log. Platform-wide by nature."},
	{ID: "contact_opened", Label: "Contact forms received", Group: "Moderation", Unit: "count",
		Kind: KindAdditive, Scopable: false, Source: "contact_new", Expr: "COUNT(*)",
		Hint: "Contact submissions received. Not institution-scopable."},
	{ID: "contact_resolved", Label: "Contact forms resolved", Group: "Moderation", Unit: "count",
		Kind: KindAdditive, Scopable: false, Source: "contact_done", Expr: "COUNT(*)",
		Hint: "Contact submissions reaching resolved_at. Not institution-scopable."},
	{ID: "impersonation_sessions", Label: "Impersonation sessions", Group: "Moderation", Unit: "count",
		Kind: KindAdditive, Scopable: false, Source: "impersonation", Expr: "COUNT(*)",
		Hint: "Admin impersonation sessions started — a security metric. Not institution-scopable."},

	// ── Social & retention ──────────────────────────────────────────────────
	{ID: "badges_earned", Label: "Badges earned", Group: "Social", Unit: "count",
		Kind: KindAdditive, Scopable: false, Source: "badge", Expr: "COUNT(*)",
		Hint: "Badges awarded. Badges carry no institution linkage."},
	{ID: "follows_created", Label: "Follows created", Group: "Social", Unit: "count",
		Kind: KindAdditive, Scopable: false, Source: "follow", Expr: "COUNT(*)",
		Hint: "New follow relationships."},
	{ID: "profile_views", Label: "Profile views", Group: "Social", Unit: "count",
		Kind: KindAdditive, Scopable: false, Source: "pview", Expr: "COUNT(*)",
		Hint: "Profile view events."},
	{ID: "notifications_sent", Label: "Notifications sent", Group: "Social", Unit: "count",
		Kind: KindAdditive, Scopable: false, Source: "notif", Expr: "COUNT(*)",
		Hint: "Notification log entries."},
}

func Catalog() []MetricDef { return catalog }

func Lookup(id string) (MetricDef, bool) {
	for _, m := range catalog {
		if m.ID == id {
			return m, true
		}
	}
	return MetricDef{}, false
}

type DroppedMetric struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

const reasonNotScopable = "not institution-scopable"

// SelectMetrics resolves the requested ids (empty means everything), pulls in
// the dependencies of derived metrics, and removes anything that cannot honour
// an active institution filter.
func SelectMetrics(ids []string, scoped bool) ([]MetricDef, []DroppedMetric, error) {
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

	// Drop non-scopable metrics first, then drop any derived metric whose
	// dependency just went away — a rate projecting from a removed column is a
	// broken query, not a partial answer.
	dropped := map[string]string{}
	for id := range want {
		m, _ := Lookup(id)
		if scoped && !m.Scopable {
			dropped[id] = reasonNotScopable
		}
	}
	for id := range want {
		m, _ := Lookup(id)
		if m.Source != "" || dropped[id] != "" {
			continue
		}
		for _, need := range m.Needs {
			if dropped[need] != "" {
				dropped[id] = fmt.Sprintf("depends on %s, which is %s", need, reasonNotScopable)
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
