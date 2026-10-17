package scheduler

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/notification"
)

func TestRefreshAvatarPersonas(t *testing.T) {
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
	newUser := func() string {
		t.Helper()
		var id string
		if err := db.QueryRow(ctx, `INSERT INTO users(supabase_uid,full_name,display_name,email,role) VALUES(gen_random_uuid(),'L','L',$1,'student') RETURNING id`,
			uuid.NewString()+"@a.test").Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			db.Exec(ctx, `DELETE FROM user_notifications WHERE user_id=$1`, id)
			db.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
		})
		return id
	}
	master := func(user, topic string, mastery float64, reviews int) {
		t.Helper()
		if _, err := db.Exec(ctx, `INSERT INTO learner_topic_mastery(user_id,topic,mastery,review_count) VALUES($1,$2,$3,$4)
			ON CONFLICT (user_id,topic) DO UPDATE SET mastery=EXCLUDED.mastery, review_count=EXCLUDED.review_count`,
			user, topic, mastery, reviews); err != nil {
			t.Fatal(err)
		}
	}
	worn := func(user string) (persona string, tier int) {
		t.Helper()
		var p *string
		if err := db.QueryRow(ctx, `SELECT avatar_persona, avatar_tier FROM users WHERE id=$1`, user).Scan(&p, &tier); err != nil {
			t.Fatal(err)
		}
		if p != nil {
			persona = *p
		}
		return
	}
	wardrobe := func(user string) map[string]int {
		rows, _ := db.Query(ctx, `SELECT persona, tier FROM user_avatar_outfits WHERE user_id=$1`, user)
		defer rows.Close()
		m := map[string]int{}
		for rows.Next() {
			var p string
			var tier int
			rows.Scan(&p, &tier)
			m[p] = tier
		}
		return m
	}
	notes := func(user string) (n int) {
		db.QueryRow(ctx, `SELECT COUNT(*) FROM user_notifications WHERE user_id=$1 AND reference='avatar_persona'`, user).Scan(&n)
		return
	}
	s := New(db, nil, nil, notification.NewService(db, "", "", ""), nil)
	run := func() {
		t.Helper()
		if err := s.RefreshAvatarPersonas(ctx); err != nil {
			t.Fatal(err)
		}
	}

	learner, novice := newUser(), newUser()
	master(learner, "general_mixed", 0.95, 9) // generic: loses to anything specific
	master(learner, "science_space", 0.80, 3) // subdomain override beats the domain's "scientist"
	master(novice, "ent_movies", 0.95, 2)     // too few attempts to count
	run()
	if p, tier := worn(learner); p != "astronaut" || tier != 1 {
		t.Fatalf("learner wears %q tier %d, want astronaut 1", p, tier)
	}
	if w := wardrobe(learner); len(w) != 2 || w["scholar"] != 2 {
		t.Fatalf("wardrobe = %v, want astronaut + scholar (tier 2)", w)
	}
	if p, _ := worn(novice); p != "" {
		t.Fatalf("novice wears %q, want nothing yet", p)
	}

	run() // unchanged week: no new notification
	if n := notes(learner); n != 1 {
		t.Fatalf("notifications = %d, want 1 after an unchanged week", n)
	}

	master(learner, "science_space", 0.92, 16) // deeper mastery: outfit upgrades to tier 3
	run()
	if p, tier := worn(learner); p != "astronaut" || tier != 3 {
		t.Fatalf("learner wears %q tier %d, want astronaut 3", p, tier)
	}
	if n := notes(learner); n != 2 {
		t.Fatalf("notifications = %d, want 2 after the upgrade", n)
	}

	// The learner picks their scholar outfit; a stronger new domain is added
	// to the wardrobe but doesn't override the choice.
	db.Exec(ctx, `UPDATE users SET avatar_persona='scholar', avatar_persona_chosen=true WHERE id=$1`, learner)
	master(learner, "quantitative", 0.99, 40)
	run()
	if p, tier := worn(learner); p != "scholar" || tier != 2 {
		t.Fatalf("learner wears %q tier %d, want their chosen scholar 2", p, tier)
	}
	if w := wardrobe(learner); w["mathematician"] != 3 {
		t.Fatalf("wardrobe = %v, want mathematician 3 added", w)
	}

	master(novice, "ent_movies", 0.95, 3)
	run()
	db.Exec(ctx, `UPDATE learner_topic_mastery SET mastery=0.3 WHERE user_id=$1`, novice)
	run() // skills fade: outfits and tiers stay earned
	if p, _ := worn(novice); p != "director" {
		t.Fatalf("novice wears %q, want director kept", p)
	}
}
