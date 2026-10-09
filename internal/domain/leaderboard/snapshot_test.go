package leaderboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qwish/backend/internal/middleware"
)

func TestSnapshotPositionsAndTies(t *testing.T) {
	s := &rankSnapshot{total: 200, entries: []Entry{{QwishScore: 800}, {QwishScore: 700}, {QwishScore: 700}}, buckets: map[int]int{71: 1, 61: 2, 51: 197}}
	for _, tt := range []struct {
		score   float64
		rank    int
		percent int
	}{
		{800, 1, 0}, {750, 2, 0}, {700, 2, 0}, {600, 0, 100},
	} {
		rank, p := s.position(tt.score, 3)
		if rank != tt.rank || (tt.percent == 0 && p != nil) || (tt.percent != 0 && (p == nil || *p != tt.percent)) {
			t.Fatalf("score %v: rank=%d percent=%v", tt.score, rank, p)
		}
	}
	if got := s.page(50, 3); len(got) != 0 {
		t.Fatal("page beyond snapshot must be empty")
	}
	if rank, p := (&rankSnapshot{}).position(500, 100); rank != 0 || p != nil {
		t.Fatal("empty scope should not rank")
	}
}

func TestSnapshotCutoffsAndLiveEligibility(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	tag := fmt.Sprintf("snapshot-%d", time.Now().UnixNano())
	var inst string
	if err := pool.QueryRow(ctx, `INSERT INTO institutions(name,type,contact_email,student_referral_code,teacher_referral_code,status)
 VALUES($1,'school',$1||'@example.test',$1||'S',$1||'T','verified') RETURNING id`, tag).Scan(&inst); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id=$1`, inst)
		pool.Exec(ctx, `DELETE FROM users WHERE domain=$1`, tag)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, inst)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO users(supabase_uid,full_name,display_name,email,role,status,domain)
 SELECT gen_random_uuid(),'snapshot',n::text,$1||'-'||n||'@example.test','student','active',$1 FROM generate_series(1,10005) n`, tag); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO leaderboard_scores(user_id,qwish_score,completed_quizzes)
 SELECT id,800-display_name::int*0.01,5 FROM users WHERE domain=$1`, tag); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO enrollments(institution_id,user_id,full_name,status,joined_at)
 SELECT $1,id,'snapshot','active',now() FROM users WHERE domain=$2 AND display_name::int<=105`, inst, tag); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(pool)
	for _, tt := range []struct {
		scope        string
		n, rank, cap int
	}{
		{"institution", 100, 100, 100}, {"institution", 101, 0, 100},
		{"global", 10000, 10000, 10000}, {"global", 10001, 0, 10000},
	} {
		t.Run(fmt.Sprintf("%s-%d", tt.scope, tt.n), func(t *testing.T) {
			var uid string
			if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE domain=$1 AND display_name=$2`, tag, fmt.Sprint(tt.n)).Scan(&uid); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard?scope="+tt.scope+"&domain="+tag, nil)
			reqCtx := context.WithValue(req.Context(), middleware.ContextKeyUserID, uid)
			reqCtx = context.WithValue(reqCtx, middleware.ContextKeyRole, "student")
			callerInstitution := inst
			if tt.scope == "global" {
				callerInstitution = ""
			}
			reqCtx = context.WithValue(reqCtx, middleware.ContextKeyInstID, callerInstitution)
			rec := httptest.NewRecorder()
			h.Get(rec, req.WithContext(reqCtx))
			if rec.Code != 200 {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var response struct {
				Data struct {
					Rank    int  `json:"my_rank"`
					Percent *int `json:"my_top_percent"`
					Cutoff  int  `json:"rank_cutoff"`
				}
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Data.Rank != tt.rank || response.Data.Cutoff != tt.cap {
				t.Fatalf("response=%s", rec.Body.String())
			}
			if tt.rank == 0 && (response.Data.Percent == nil || *response.Data.Percent < 1 || *response.Data.Percent > 100) {
				t.Fatal("missing top percentage")
			}
			if tt.rank != 0 && response.Data.Percent != nil {
				t.Fatal("exact rank also exposes percentage")
			}
			// Warm requests do one live lookup, not a personal-rank population query.
			before := pool.Stat().AcquireCount()
			rec = httptest.NewRecorder()
			h.Get(rec, req.WithContext(reqCtx))
			if rec.Code != 200 || pool.Stat().AcquireCount()-before != 1 {
				t.Fatal("warm request must do exactly one DB lookup")
			}
			if _, err := pool.Exec(ctx, `UPDATE leaderboard_scores SET completed_quizzes=4 WHERE user_id=$1`, uid); err != nil {
				t.Fatal(err)
			}
			rec = httptest.NewRecorder()
			h.Get(rec, req.WithContext(reqCtx))
			if rec.Code != http.StatusForbidden {
				t.Fatal("cached snapshot bypassed live eligibility")
			}
			pool.Exec(ctx, `UPDATE leaderboard_scores SET completed_quizzes=5 WHERE user_id=$1`, uid)
		})
	}
	s, err := h.snapshot(ctx, "global", "", tag)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.entries) != 10000 || s.total != 10005 {
		t.Fatalf("entries=%d total=%d", len(s.entries), s.total)
	}
	if len(s.page(100, 10000)) != 0 {
		t.Fatal("list exposes ranks beyond national cutoff")
	}
	// Scope mismatches must not return a rank or percentage from a cached board.
	var outsider string
	pool.QueryRow(ctx, `SELECT id FROM users WHERE domain=$1 AND display_name='10005'`, tag).Scan(&outsider)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard?scope=institution&domain="+tag, nil)
	reqCtx := context.WithValue(req.Context(), middleware.ContextKeyUserID, outsider)
	reqCtx = context.WithValue(reqCtx, middleware.ContextKeyRole, "student")
	reqCtx = context.WithValue(reqCtx, middleware.ContextKeyInstID, inst)
	rec := httptest.NewRecorder()
	h.Get(rec, req.WithContext(reqCtx))
	var response struct {
		Data struct {
			Rank    int  `json:"my_rank"`
			Percent *int `json:"my_top_percent"`
		}
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || response.Data.Rank != 0 || response.Data.Percent != nil {
		t.Fatalf("outsider response=%s", rec.Body.String())
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET domain=NULL WHERE id=$1`, outsider); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h.Get(rec, req.WithContext(reqCtx))
	if rec.Code != 200 {
		t.Fatalf("null domain lookup failed: %s", rec.Body.String())
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET domain=$1 WHERE id=$2`, tag, outsider); err != nil {
		t.Fatal(err)
	}
	h.ClearCache()
	fresh, err := h.snapshot(ctx, "global", "", tag)
	if err != nil {
		t.Fatal(err)
	}
	if fresh == s {
		t.Fatal("invalidation reused old snapshot")
	}
}
