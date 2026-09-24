package user

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLearningReportEvidence(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	// Session-local tables isolate this regression from both production data
	// and other integration tests. No persistent schema is changed.
	_, err = pool.Exec(ctx, `
 CREATE TEMP TABLE users(id text,display_name text,member_since timestamptz,current_streak int,longest_streak int,deleted_at timestamptz,role text,status text);
 CREATE TEMP TABLE quizzes(id text,institution_id text,domain text);
 CREATE TEMP TABLE quiz_attempts(id text,user_id text,quiz_id text,completed_at timestamptz,total_correct int,total_questions int,score_pct float8,status text);
 CREATE TEMP TABLE institutions(id text,name text);
 CREATE TEMP TABLE enrollments(id text,user_id text,institution_id text,status text,ended_at timestamptz,joined_at timestamptz,created_at timestamptz,grade text);
 CREATE TEMP TABLE promotion_batches(id text,to_grade text,created_at timestamptz,reverted_at timestamptz);
 CREATE TEMP TABLE promotion_batch_students(enrollment_id text,batch_id text,prior_grade text,outcome text);
 CREATE TEMP TABLE user_education(id text,user_id text,institution_name text,degree text,field text,start_year int,end_year int,is_current bool);
 CREATE TEMP TABLE learning_assignments(id text,status text,due_at timestamptz,available_at timestamptz);
 CREATE TEMP TABLE learning_assignment_recipients(assignment_id text,student_id text,status text);
 INSERT INTO users VALUES('me','Sample Learner',now()-interval '1 year',3,7,NULL,'student','active'),('empty','New Learner',now(),0,0,NULL,'student','active');
 INSERT INTO users SELECT 'peer'||n,'Peer',now(),0,0,NULL,'student','active' FROM generate_series(1,6) n;
 INSERT INTO institutions VALUES('school','Sample School');
 INSERT INTO enrollments VALUES('e','me','school','active',NULL,now()-interval '100 days',now()-interval '100 days','Grade 10');
 INSERT INTO promotion_batches VALUES('p','Grade 10',now()-interval '40 days',NULL);
 INSERT INTO promotion_batch_students VALUES('e','p','Grade 9','promoted');
 INSERT INTO quizzes SELECT 'q'||n,'school','mathematics' FROM generate_series(1,3)n;
 INSERT INTO quizzes VALUES('public',NULL,'logic');
 INSERT INTO quiz_attempts SELECT 'a'||n,'me','q'||n,now()-CASE WHEN n=1 THEN interval '60 days' ELSE interval '10 days' END,6,10,60,'completed' FROM generate_series(1,3)n;
 INSERT INTO quiz_attempts VALUES('retake','me','q1',now(),10,10,100,'completed'),('pub','me','public',now(),8,10,80,'completed');
 INSERT INTO quiz_attempts SELECT 'peer'||p||'q'||q,'peer'||p,'q'||q,now(),CASE WHEN p<=3 THEN 4 ELSE 6 END,10,CASE WHEN p<=3 THEN 40 ELSE 60 END,'completed' FROM generate_series(1,6)p CROSS JOIN generate_series(1,3)q;
 INSERT INTO learning_assignments VALUES('homework','published',now()-interval '1 day',NULL);
 INSERT INTO learning_assignment_recipients VALUES('homework','me','started');
 UPDATE quiz_attempts SET score_pct=99;
 `)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewService(pool).GetLearningReport(ctx, "me")
	if err != nil {
		t.Fatal(err)
	}
	if r.Assessments != 4 || r.Questions != 40 || r.Correct != 26 {
		t.Fatalf("retake changed evidence: %+v", r)
	}
	if len(r.Stages) != 2 || r.Stages[0].Grade != "Grade 9" || r.Stages[0].Assessments != 1 || r.Stages[1].Assessments != 2 {
		t.Fatalf("stage attribution: %+v", r.Stages)
	}
	if r.Overdue != 1 || r.Submitted != 0 {
		t.Fatalf("assignment counts: %+v", r)
	}
	for _, d := range r.Domains {
		if d.Name == "mathematics" {
			if d.PeerStanding == nil || *d.PeerStanding != 50 {
				t.Fatalf("ties/cohort standing: %+v", d)
			}
		} else if d.PeerStanding != nil {
			t.Fatal("small cohort exposed")
		}
	}
	empty, err := NewService(pool).GetLearningReport(ctx, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if empty.Questions != 0 || empty.RecentAccuracy != nil || len(empty.Stages) != 0 {
		t.Fatalf("empty report fabricates results: %+v", empty)
	}
}

func TestReportPDFOffsetsAndPagination(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	r := &LearningReport{Name: "A (name) \\ with PDF delimiters", Generated: now, MemberSince: now}
	for i := 0; i < 30; i++ {
		r.Domains = append(r.Domains, ReportDomain{Name: strings.Repeat("Long domain ", 12), Questions: 30, Correct: 24, Assessments: 3})
	}
	pdf := string(renderLearningReport(r))
	parts := strings.Split(pdf, "startxref\n")
	if len(parts) != 2 {
		t.Fatal("missing xref")
	}
	start, err := strconv.Atoi(strings.Split(parts[1], "\n")[0])
	if err != nil || !strings.HasPrefix(pdf[start:], "xref\n") {
		t.Fatal("invalid xref position")
	}
	lines := strings.Split(pdf[start:], "\n")
	var zero, count int
	fmt.Sscanf(lines[1], "%d %d", &zero, &count)
	for i := 1; i < count; i++ {
		offset, err := strconv.Atoi(lines[2+i][:10])
		if err != nil || !strings.HasPrefix(pdf[offset:], fmt.Sprintf("%d 0 obj", i)) {
			t.Fatalf("invalid object offset %d", i)
		}
	}
	if strings.Count(pdf, "/Type /Page ") < 3 {
		t.Fatal("long report was not paginated")
	}
	if strings.Contains(pdf, r.Name) {
		t.Fatal("unescaped content in PDF")
	}
	// Optional, explicitly requested test artifact for visual inspection.
	if path := os.Getenv("REPORT_PREVIEW_PATH"); path != "" {
		r.Domains = []ReportDomain{{Name: "Mathematics", Questions: 60, Correct: 51, Assessments: 4}, {Name: "Logical reasoning", Questions: 20, Correct: 11, Assessments: 2}}
		r.Stages = []ReportStage{{Institution: "Sample School", Grade: "Grade 10", Status: "active", Start: now.AddDate(-1, 0, 0), Assessments: 4, Questions: 60, Correct: 51}}
		r.Name = "Sample Learner (synthetic preview)"
		r.Assessments = 6
		r.Questions = 80
		r.Correct = 62
		first := now.AddDate(0, -2, 0)
		r.MemberSince = now.AddDate(-1, 0, 0)
		r.FirstEvidence, r.LastEvidence = &first, &now
		r.ActiveDays, r.Streak, r.LongestStreak = 5, 3, 7
		if err := os.WriteFile(path, renderLearningReport(r), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
