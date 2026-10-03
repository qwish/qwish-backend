package user

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The public profile carries the learner's standing, not just counters:
// score and percentile, accuracy, consistency and subject strengths.
func TestPublicProfileShowsStanding(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1 // temp tables below live on this one session
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, `
 CREATE TEMP TABLE users(id text,display_name text,institution_id text,total_points int,current_streak int,longest_streak int,profile_private bool,recruiter_visible bool,deleted_at timestamptz,status text,role text,member_since timestamptz);
 CREATE TEMP TABLE institutions(id text,name text);
 CREATE TEMP TABLE user_follows(follower_id text,followee_id text);
 CREATE TEMP TABLE leaderboard_scores(user_id text,qwish_score float8);
 CREATE TEMP TABLE quizzes(id text,domain text,subdomain text);
 CREATE TEMP TABLE domains(slug text,label text,sort int);
 CREATE TEMP TABLE subdomains(slug text,label text,sort int);
 CREATE TEMP TABLE quiz_attempts(id text,user_id text,quiz_id text,status text,completed_at timestamptz,total_correct int,total_questions int);
 CREATE TEMP TABLE question_responses(attempt_id text,is_correct bool);
 CREATE TEMP TABLE badges(user_id text,badge_type text,earned_at timestamptz);
 INSERT INTO institutions VALUES('i','Greenfield College');
 INSERT INTO users VALUES('me','Asha','i',900,4,9,false,false,NULL,'active','student','2026-01-15'),
   ('p1','P1',NULL,0,0,0,false,false,NULL,'active','student',now()),('p2','P2',NULL,0,0,0,false,false,NULL,'active','student',now()),
   ('p3','P3',NULL,0,0,0,false,false,NULL,'active','student',now()),('t','T',NULL,0,0,0,false,false,NULL,'active','teacher',now());
 INSERT INTO leaderboard_scores VALUES('me',640),('p1',300),('p2',500),('p3',700),('t',900);
 INSERT INTO domains VALUES('cs','Computer Science',1),('apt','Aptitude',2),('verbal','Verbal Ability',3);
 INSERT INTO quizzes VALUES('q1','cs','dsa'),('q2','apt','num'),('q3','verbal','rc');
 INSERT INTO quiz_attempts VALUES('a1','me','q1','completed',now()-interval '1 day',9,10),('a2','me','q1','completed',now()-interval '2 days',8,10),
   ('a3','me','q2','completed',now()-interval '2 days',6,10),('a4','me','q3','completed',now()-interval '60 days',2,3),
   ('a5','me','q2','abandoned',now(),0,10);
 INSERT INTO question_responses SELECT a, n<=c FROM (VALUES('a1',9,10),('a2',8,10),('a3',6,10),('a4',2,3)) v(a,c,t), generate_series(1,t) n;
 INSERT INTO badges VALUES('me','first_quiz',now()),('me','streak_7',now());
 `)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewService(pool).GetPublicProfile(ctx, "viewer", "me")
	if err != nil {
		t.Fatal(err)
	}
	if p.QwishScore != 640 {
		t.Fatalf("qwish score = %v", p.QwishScore)
	}
	// Above 2 of the 3 other students (teachers excluded): ceil(2/3*99+1) = 67.
	if p.Percentile != 67 {
		t.Fatalf("percentile = %d, want 67", p.Percentile)
	}
	if p.QuestionsAnswered != 33 || p.Accuracy == nil || *p.Accuracy < 75.7 || *p.Accuracy > 75.8 {
		t.Fatalf("questions=%d accuracy=%v, want 33 and 25/33", p.QuestionsAnswered, p.Accuracy)
	}
	if p.ActiveDays30 != 2 || p.MemberSince == nil || p.MemberSince.Year() != 2026 {
		t.Fatalf("active days=%d member since=%v", p.ActiveDays30, p.MemberSince)
	}
	// Strongest first; Verbal has 3 answers, too few to call a strength.
	if len(p.Strengths) != 2 || p.Strengths[0].Label != "Computer Science" || p.Strengths[0].Accuracy != 85 ||
		p.Strengths[1].Label != "Aptitude" || p.Strengths[1].Questions != 10 {
		t.Fatalf("strengths = %+v", p.Strengths)
	}
	if p.BadgeCount != 2 {
		t.Fatalf("badge count = %d", p.BadgeCount)
	}
}
