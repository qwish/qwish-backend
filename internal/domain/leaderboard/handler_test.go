package leaderboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

func TestGetRejectsUnknownScopeBeforeDatabaseAccess(t *testing.T) {
	h := NewHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard?scope=domain&domain=quantitative", nil)
	recorder := httptest.NewRecorder()

	h.Get(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if !strings.Contains(recorder.Body.String(), "scope must be institution or global") {
		t.Fatalf("response did not explain valid scopes: %s", recorder.Body.String())
	}
}

func openTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping database integration test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping TEST_DATABASE_URL: %v", err)
	}
	return pool
}

func TestGetReturnsOnlyEligibleStudents(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())

	var institutionID, studentID, teacherID string
	mustScan := func(label string, row interface{ Scan(...any) error }, dest ...any) {
		t.Helper()
		if err := row.Scan(dest...); err != nil {
			t.Fatalf("seed %s: %v", label, err)
		}
	}
	mustScan("institution", pool.QueryRow(ctx, `
		INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
		VALUES ('Leaderboard School '||$1, 'school', 'leaderboard-'||$1||'@example.test', 'LS'||$1, 'LT'||$1, 'verified')
		RETURNING id`, tag), &institutionID)
	mustScan("student", pool.QueryRow(ctx, `
		INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id, total_points, domain)
		VALUES (gen_random_uuid(), 'Eligible Student', 'Eligible Student', $1, 'student', $2, 100, $3)
		RETURNING id`, "leaderboard-student-"+tag+"@example.test", institutionID, "fixture-"+tag), &studentID)
	mustScan("teacher", pool.QueryRow(ctx, `
		INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id, total_points, domain)
		VALUES (gen_random_uuid(), 'High-scoring Teacher', 'High-scoring Teacher', $1, 'teacher', $2, 9999, $3)
		RETURNING id`, "leaderboard-teacher-"+tag+"@example.test", institutionID, "fixture-"+tag), &teacherID)

	quizIDs := make([]string, 0, quizzesRequiredToUnlock)
	for i := 0; i < quizzesRequiredToUnlock; i++ {
		var quizID string
		mustScan("quiz", pool.QueryRow(ctx, `
			INSERT INTO quizzes (institution_id, created_by, title, type, status)
			VALUES ($1, $2, $3, 'knowledge_check', 'published') RETURNING id`,
			institutionID, teacherID, fmt.Sprintf("Leaderboard fixture %s-%d", tag, i)), &quizID)
		quizIDs = append(quizIDs, quizID)
		if _, err := pool.Exec(ctx, `
			INSERT INTO quiz_attempts (quiz_id, user_id, status, completed_at)
			VALUES ($1, $2, 'completed', now())`, quizID, studentID); err != nil {
			t.Fatalf("seed completed attempt: %v", err)
		}
	}

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id = ANY($1::uuid[])`, quizIDs)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id = ANY($1::uuid[])`, quizIDs)
		pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1::uuid[])`, []string{studentID, teacherID})
		pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, institutionID)
	})

	for _, scope := range []string{"institution", "global"} {
		t.Run(scope, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard?scope="+scope+"&domain=fixture-"+tag, nil)
			reqCtx := context.WithValue(req.Context(), middleware.ContextKeyUserID, studentID)
			reqCtx = context.WithValue(reqCtx, middleware.ContextKeyRole, "student")
			reqCtx = context.WithValue(reqCtx, middleware.ContextKeyInstID, institutionID)
			recorder := httptest.NewRecorder()

			NewHandler(pool).Get(recorder, req.WithContext(reqCtx))

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			var response struct {
				Data struct {
					MyRank  int     `json:"my_rank"`
					Entries []Entry `json:"entries"`
				} `json:"data"`
				Meta middleware.Meta `json:"meta"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Meta.Total != 1 || len(response.Data.Entries) != 1 {
				t.Fatalf("leaderboard includes non-students: total=%d entries=%+v", response.Meta.Total, response.Data.Entries)
			}
			if response.Data.Entries[0].UserID != studentID || response.Data.MyRank != 1 {
				t.Fatalf("student result/rank = entries=%+v my_rank=%d", response.Data.Entries, response.Data.MyRank)
			}
		})
	}
}
