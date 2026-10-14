package metrics

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Attempt-start metrics read the attempt_daily_starts rollup at day and coarser
// granularity. The rollup must follow every change to quiz_attempts that moves
// a start or an abandonment, and the metrics read from it must equal a recount
// of the raw attempts.
func TestStartRollupMatchesRawAttempts(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	tag := fmt.Sprintf("starts%d", time.Now().UnixNano())
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	var inst, teacher, quizA, quizB string
	must("institution", pool.QueryRow(ctx, `INSERT INTO institutions(name,type,contact_email,status,student_referral_code,teacher_referral_code)
		VALUES($1,'school',$1||'@example.test','verified',$1||'s',$1||'t') RETURNING id`, tag).Scan(&inst))
	must("teacher", pool.QueryRow(ctx, `INSERT INTO users(supabase_uid,full_name,display_name,email,role)
		VALUES(gen_random_uuid(),$1,$1,$1||'@example.test','teacher') RETURNING id`, "t"+tag).Scan(&teacher))
	for _, q := range []*string{&quizA, &quizB} {
		must("quiz", pool.QueryRow(ctx, `INSERT INTO quizzes(created_by,title,type,visibility,status) VALUES($1,$2,'knowledge_check','public','published') RETURNING id`, teacher, tag).Scan(q))
	}
	students := make([]string, 3)
	for i := range students {
		must("student", pool.QueryRow(ctx, `INSERT INTO users(supabase_uid,full_name,display_name,email,role,institution_id)
			VALUES(gen_random_uuid(),$1,$1,$1||'@example.test','student',$2) RETURNING id`, fmt.Sprintf("s%d%s", i, tag), inst).Scan(&students[i]))
	}
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id IN ($1,$2)`, quizA, quizB)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id IN ($1,$2)`, quizA, quizB)
		pool.Exec(ctx, `DELETE FROM users WHERE full_name LIKE '%'||$1`, tag)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, inst)
	})

	day := func(d, hour int) time.Time { return time.Date(2026, time.March, d, hour, 0, 0, 0, IST) }
	attempt := func(student, quiz string, at time.Time, status string) string {
		var id string
		must("attempt", pool.QueryRow(ctx, `INSERT INTO quiz_attempts(quiz_id,user_id,status,started_at) VALUES($1,$2,$3,$4) RETURNING id`,
			quiz, student, status, at).Scan(&id))
		return id
	}
	// Spread over days, hours near midnight (IST bucketing), quizzes and statuses.
	a1 := attempt(students[0], quizA, day(2, 0), "in_progress")
	attempt(students[0], quizA, day(2, 23), "abandoned")
	a3 := attempt(students[1], quizA, day(3, 9), "abandoned")
	a4 := attempt(students[1], quizB, day(3, 23), "in_progress")
	a5 := attempt(students[2], quizB, day(5, 12), "in_progress")
	attempt(students[2], quizB, day(9, 1), "abandoned")

	// Every kind of change the rollup must follow.
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		must(sql, err)
	}
	exec(`UPDATE quiz_attempts SET status='abandoned' WHERE id=$1`, a1)                                           // starts abandoned
	exec(`UPDATE quiz_attempts SET status='in_progress' WHERE id=$1`, a3)                                         // un-abandoned
	exec(`UPDATE quiz_attempts SET status='completed', completed_at=started_at+interval '5 min' WHERE id=$1`, a5) // completes: no start change
	exec(`UPDATE quiz_attempts SET combo_level=combo_level+1, last_answer_at=now() WHERE id=$1`, a4)              // answer path: no start change
	exec(`UPDATE quiz_attempts SET started_at=$2 WHERE id=$1`, a4, day(6, 10))                                    // moves day
	exec(`UPDATE quiz_attempts SET user_id=$2 WHERE id=$1`, a3, students[2])                                      // changes taker
	exec(`DELETE FROM quiz_attempts WHERE id=(SELECT id FROM quiz_attempts WHERE user_id=$1 AND status='abandoned' AND quiz_id=$2 LIMIT 1)`, students[0], quizA)

	// 1. The rollup equals a recount of the raw attempts.
	var mismatches int
	must("rollup check", pool.QueryRow(ctx, `
		WITH raw AS (
		  SELECT user_id, quiz_id, date_trunc('day', started_at AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata' AS d,
		         count(*) AS started, count(*) FILTER (WHERE status='abandoned') AS abandoned
		  FROM quiz_attempts WHERE quiz_id IN ($1,$2) GROUP BY 1,2,3),
		roll AS (
		  SELECT user_id, quiz_id, started_at AS d, started, abandoned FROM attempt_daily_starts
		  WHERE quiz_id IN ($1,$2) AND (started<>0 OR abandoned<>0))
		SELECT count(*) FROM (SELECT * FROM raw EXCEPT SELECT * FROM roll UNION ALL SELECT * FROM roll EXCEPT SELECT * FROM raw) diff`,
		quizA, quizB).Scan(&mismatches))
	if mismatches != 0 {
		t.Fatalf("rollup differs from raw attempts in %d rows", mismatches)
	}

	// 2. Series and totals read from the rollup equal the raw computation.
	svc := NewMetricsService(pool)
	sel, _, err := SelectMetrics([]string{"attempts_started", "attempts_abandoned", "abandon_rate"}, ScopeInstitution)
	must("select", err)
	scope := Scope{Kind: ScopeInstitution, ID: inst}
	for _, gran := range []Granularity{GranDay, GranWeek} {
		w := Window{From: day(1, 0), To: day(10, 0), Gran: gran}
		if sql, _ := BuildSeriesQuery(sel, w, scope); !strings.Contains(sql, "attempt_daily_starts") {
			t.Fatalf("%s series does not read the rollup", gran)
		}
		series, err := svc.Series(ctx, sel, w, scope)
		must("series", err)
		got := map[string][2]int64{}
		for _, row := range series {
			s, _ := toFloat(row["attempts_started"])
			a, _ := toFloat(row["attempts_abandoned"])
			if s != 0 || a != 0 {
				got[fmt.Sprint(row["bucket"])] = [2]int64{int64(s), int64(a)}
			}
		}
		rows, err := pool.Query(ctx, `
			SELECT date_trunc($3, qa.started_at AT TIME ZONE 'Asia/Kolkata'),
			       count(*), count(*) FILTER (WHERE qa.status='abandoned')
			FROM quiz_attempts qa JOIN users u ON u.id=qa.user_id
			WHERE u.institution_id=$4 AND qa.started_at >= $1 AND qa.started_at < $2 GROUP BY 1`,
			w.From, w.To.AddDate(0, 0, 1), w.Gran.PGTrunc(), inst)
		must("raw series", err)
		want := map[string][2]int64{}
		var totalStarted, totalAbandoned int64
		for rows.Next() {
			var b time.Time
			var s, a int64
			must("scan", rows.Scan(&b, &s, &a))
			want[fmt.Sprint(b)] = [2]int64{s, a}
			totalStarted += s
			totalAbandoned += a
		}
		rows.Close()
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s series: got %v, want %v", gran, got, want)
		}

		totals, err := svc.Totals(ctx, sel, w, scope)
		must("totals", err)
		s, _ := toFloat(totals["attempts_started"])
		a, _ := toFloat(totals["attempts_abandoned"])
		rate, _ := toFloat(totals["abandon_rate"])
		wantRate := float64(totalAbandoned) / float64(totalStarted) * 100
		if int64(s) != totalStarted || int64(a) != totalAbandoned || fmt.Sprintf("%.6f", rate) != fmt.Sprintf("%.6f", wantRate) {
			t.Errorf("%s totals: started=%v abandoned=%v rate=%v, want %d %d %.4f", gran, s, a, rate, totalStarted, totalAbandoned, wantRate)
		}
	}
}
