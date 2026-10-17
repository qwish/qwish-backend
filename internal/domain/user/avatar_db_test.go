package user

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAvatarWardrobeAndMood(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var id, quiz string
	if err := db.QueryRow(ctx, `INSERT INTO users(supabase_uid,full_name,display_name,email,role) VALUES(gen_random_uuid(),'L','L',$1,'student') RETURNING id`,
		uuid.NewString()+"@a.test").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO quizzes(created_by,title,type,status) VALUES($1,'Q','knowledge_check','published') RETURNING id`, id).Scan(&quiz); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(ctx, `DELETE FROM quiz_attempts WHERE user_id=$1`, id)
		db.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, quiz)
		db.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
	})
	s := &Service{db: db}
	mood := func() string {
		t.Helper()
		wr, err := s.GetAvatarWardrobe(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return wr.Mood
	}
	attempt := func(ago string, questions int) {
		t.Helper()
		db.Exec(ctx, `DELETE FROM quiz_attempts WHERE user_id=$1`, id)
		if _, err := db.Exec(ctx, `INSERT INTO quiz_attempts(quiz_id,user_id,status,completed_at,total_questions)
			VALUES($1,$2,'completed',now()-$3::interval,$4)`, quiz, id, ago, questions); err != nil {
			t.Fatal(err)
		}
	}

	// Mood follows effort and return, never scores.
	if m := mood(); m != "smile" {
		t.Fatalf("new learner mood = %q, want smile", m)
	}
	attempt("2 hours", 12)
	if m := mood(); m != "delighted" {
		t.Fatalf("after a long session mood = %q, want delighted", m)
	}
	attempt("2 hours", 4)
	if m := mood(); m != "engaged" {
		t.Fatalf("after a short session mood = %q, want engaged", m)
	}
	attempt("5 days", 4)
	if m := mood(); m != "resting" {
		t.Fatalf("after five quiet days mood = %q, want resting", m)
	}

	// Only earned outfits can be worn, at their tier.
	if err := s.WearAvatarOutfit(ctx, id, "chef"); !errors.Is(err, ErrOutfitNotEarned) {
		t.Fatalf("wearing an unearned outfit: err = %v", err)
	}
	db.Exec(ctx, `INSERT INTO user_avatar_outfits(user_id,persona,tier) VALUES($1,'chef',2),($1,'pilot',1)`, id)
	if err := s.WearAvatarOutfit(ctx, id, "chef"); err != nil {
		t.Fatal(err)
	}
	wr, _ := s.GetAvatarWardrobe(ctx, id)
	if wr.Persona == nil || *wr.Persona != "chef" || wr.Tier != 2 || !wr.Chosen || len(wr.Outfits) != 2 {
		t.Fatalf("wardrobe after wearing chef = %+v", wr)
	}
	if err := s.WearAvatarOutfit(ctx, id, ""); err != nil { // back to auto
		t.Fatal(err)
	}
	if wr, _ := s.GetAvatarWardrobe(ctx, id); wr.Chosen || *wr.Persona != "chef" {
		t.Fatalf("auto keeps the current look until the weekly job: %+v", wr)
	}
}
