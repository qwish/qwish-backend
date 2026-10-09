package attempt

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/quiz"
	"github.com/qwish/backend/internal/domain/streak"
)

func TestProgressListsOnlyEarlierAttemptsInSameDomain(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	var domainA, domainB string
	if err = p.QueryRow(ctx, `SELECT slug FROM domains ORDER BY slug LIMIT 1`).Scan(&domainA); err != nil {
		t.Skip("no domains seeded")
	}
	if err = p.QueryRow(ctx, `SELECT slug FROM domains WHERE slug<>$1 ORDER BY slug LIMIT 1`, domainA).Scan(&domainB); err != nil {
		t.Skip("need two domains")
	}

	tag := uuid.NewString()
	var teacher, student, other string
	for _, u := range []struct {
		dst  *string
		role string
	}{{&teacher, "teacher"}, {&student, "student"}, {&other, "student"}} {
		if err = p.QueryRow(ctx, `INSERT INTO users(supabase_uid,full_name,display_name,email,role) VALUES(gen_random_uuid(),'U','U',$1,$2) RETURNING id`, tag+uuid.NewString()+"@example.test", u.role).Scan(u.dst); err != nil {
			t.Fatal(err)
		}
	}
	quizIn := func(domain string) string {
		var id string
		if err := p.QueryRow(ctx, `INSERT INTO quizzes(created_by,title,type,visibility,status,domain) VALUES($1,$2,'knowledge_check','public','published',$3) RETURNING id`, teacher, tag, domain).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	qa1, qa2, qa3, qb := quizIn(domainA), quizIn(domainA), quizIn(domainA), quizIn(domainB)
	defer func() {
		p.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id IN ($1,$2,$3,$4)`, qa1, qa2, qa3, qb)
		p.Exec(ctx, `DELETE FROM quizzes WHERE id IN ($1,$2,$3,$4)`, qa1, qa2, qa3, qb)
		p.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2,$3)`, teacher, student, other)
	}()
	base := time.Now().Add(-time.Hour)
	attempt := func(user, quizID string, score float64, at time.Time) string {
		var id string
		if err := p.QueryRow(ctx, `INSERT INTO quiz_attempts(user_id,quiz_id,status,score_pct,total_correct,total_questions,started_at,completed_at) VALUES($1,$2,'completed',$3,1,2,$4,$4) RETURNING id`, user, quizID, score, at).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := attempt(student, qa1, 40, base)
	attempt(student, qb, 90, base.Add(time.Minute))  // other domain
	attempt(other, qa1, 99, base.Add(2*time.Minute)) // other learner
	target := attempt(student, qa2, 70, base.Add(3*time.Minute))
	attempt(student, qa3, 80, base.Add(4*time.Minute)) // later

	svc := NewService(p, quiz.NewService(p), streak.NewService(p))
	got, err := svc.GetProgress(ctx, student, target)
	if err != nil {
		t.Fatal(err)
	}
	if got.Domain == nil || *got.Domain != domainA {
		t.Fatalf("domain = %v, want %s", got.Domain, domainA)
	}
	if got.PreviousTotal != 1 || len(got.Previous) != 1 || got.Previous[0].AttemptID != first {
		t.Fatalf("previous = %+v (total %d), want only %s", got.Previous, got.PreviousTotal, first)
	}
	if _, err := svc.GetProgress(ctx, other, target); err == nil {
		t.Fatal("another learner read this attempt's progress")
	}
}
