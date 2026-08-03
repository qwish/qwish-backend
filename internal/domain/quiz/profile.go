package quiz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/qwish/backend/internal/middleware"
)

// Profiling for the student quiz list. Runs EXPLAIN (ANALYZE, BUFFERS) against
// the *real* query built by studentListWhere/studentListSelect, so the numbers
// reflect production SQL rather than a hand-copied approximation.
//
// EXPLAIN ANALYZE executes the statement. Everything here is a SELECT, and no
// caller-supplied SQL is ever interpolated — scenario inputs only ever land in
// bound placeholders.

type ProfileCase struct {
	Name     string          `json:"name"`
	SQL      string          `json:"sql"`
	Args     []interface{}   `json:"args"`
	WallMS   float64         `json:"wall_ms"`
	PlanMS   float64         `json:"plan_ms,omitempty"`
	ExecMS   float64         `json:"exec_ms,omitempty"`
	NodeType string          `json:"top_node,omitempty"`
	SeqScans []string        `json:"seq_scanned_tables,omitempty"`
	Plan     json.RawMessage `json:"plan,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type ProfileResult struct {
	RanAt        time.Time     `json:"ran_at"`
	IncludePlans bool          `json:"include_plans"`
	Cases        []ProfileCase `json:"cases"`
}

// explain runs one EXPLAIN and fills in timings. Errors are captured per-case
// so one bad scenario doesn't kill the whole report.
func (s *Service) explain(ctx context.Context, name, sql string, args []interface{}, includePlan bool) ProfileCase {
	c := ProfileCase{Name: name, SQL: sql, Args: args}
	if c.Args == nil {
		c.Args = []interface{}{}
	}

	var raw []byte
	t0 := time.Now()
	err := s.db.QueryRow(ctx,
		`EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) `+sql, args...).Scan(&raw)
	c.WallMS = float64(time.Since(t0).Microseconds()) / 1000
	if err != nil {
		c.Error = err.Error()
		return c
	}

	// FORMAT JSON returns a one-element array of {Plan, Planning Time, Execution Time}.
	var wrapper []struct {
		Plan         map[string]interface{} `json:"Plan"`
		PlanningTime float64                `json:"Planning Time"`
		ExecTime     float64                `json:"Execution Time"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil || len(wrapper) == 0 {
		c.Error = "could not parse EXPLAIN output"
		return c
	}
	c.PlanMS = wrapper[0].PlanningTime
	c.ExecMS = wrapper[0].ExecTime
	if nt, ok := wrapper[0].Plan["Node Type"].(string); ok {
		c.NodeType = nt
	}
	c.SeqScans = findSeqScans(wrapper[0].Plan)
	if includePlan {
		c.Plan = raw
	}
	return c
}

// findSeqScans walks the plan tree and reports tables hit by a Seq Scan — the
// headline signal for a missing or unusable index.
func findSeqScans(node map[string]interface{}) []string {
	var out []string
	if node["Node Type"] == "Seq Scan" {
		if rel, ok := node["Relation Name"].(string); ok {
			out = append(out, rel)
		}
	}
	if kids, ok := node["Plans"].([]interface{}); ok {
		for _, k := range kids {
			if m, ok := k.(map[string]interface{}); ok {
				out = append(out, findSeqScans(m)...)
			}
		}
	}
	return out
}

// ProfileStudentList profiles the count and list halves of ListForStudent
// across the scenarios that exercise different index paths.
// institutionID and userID are optional; scenarios needing them are skipped
// when empty, since a bogus UUID would profile an empty result set.
func (s *Service) ProfileStudentList(ctx context.Context, institutionID, userID, search string, limit, offset int, includePlans bool) ProfileResult {
	if limit <= 0 {
		limit = 20
	}
	if search == "" {
		search = "math"
	}

	type scenario struct {
		name       string
		inst, srch string
		saved      string
	}
	scenarios := []scenario{
		{name: "public browse", inst: ""},
		{name: "search (public)", inst: "", srch: search},
	}
	if institutionID != "" {
		scenarios = append(scenarios,
			scenario{name: "institution browse", inst: institutionID},
			scenario{name: "institution + search", inst: institutionID, srch: search})
	}
	if userID != "" {
		scenarios = append(scenarios, scenario{name: "saved filter", inst: institutionID, saved: "true"})
	}

	res := ProfileResult{RanAt: time.Now().UTC(), IncludePlans: includePlans}
	for _, sc := range scenarios {
		where, args := studentListWhere(sc.inst, "", sc.saved, sc.srch, userID)
		argN := len(args) + 1

		res.Cases = append(res.Cases,
			s.explain(ctx, sc.name+" — COUNT", `SELECT COUNT(*) FROM quizzes q WHERE `+where, args, includePlans))

		listArgs := append(append([]interface{}{}, args...), limit, offset)
		res.Cases = append(res.Cases, s.explain(ctx, sc.name+" — LIST",
			studentListSelect+where+fmt.Sprintf(` ORDER BY q.published_at DESC LIMIT $%d OFFSET $%d`, argN, argN+1),
			listArgs, includePlans))
	}
	return res
}

// Profile handles GET /internal/profile/quiz-list.
func (h *Handler) Profile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	// EXPLAIN ANALYZE actually runs each query; cap the whole report so a bad
	// plan can't tie up a connection indefinitely.
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	res := h.svc.ProfileStudentList(ctx,
		q.Get("institution_id"), q.Get("user_id"), q.Get("search"),
		limit, offset, q.Get("plans") == "true")

	middleware.JSON(w, http.StatusOK, res)
}
