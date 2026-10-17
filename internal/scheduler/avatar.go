package scheduler

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/qwish/backend/internal/domain/notification"
)

// A topic counts as mastered once its spaced-repetition mastery (an EWMA of
// quiz scores, see attempt.recordAdaptiveLearningTx) is high and has been
// confirmed by several attempts, so one lucky quiz can't change an avatar.
// Deeper mastery upgrades the outfit: tier 2 and 3 add costume details.
const (
	personaMinMastery = 0.75
	personaMinReviews = 3
)

// masteredTopics lists every mastered topic with the persona it earns (the
// subdomain's, else its domain's), the outfit tier it is worth, and a
// strength used to pick the learner's strongest domain.
const masteredTopics = `
	mastered AS (
	  SELECT m.user_id, m.topic,
	         COALESCE(sd.persona, d.persona, td.persona) AS persona,
	         CASE WHEN m.mastery >= 0.90 AND m.review_count >= 15 THEN 3
	              WHEN m.mastery >= 0.82 AND m.review_count >= 8  THEN 2
	              ELSE 1 END AS tier,
	         m.mastery * ln(1 + m.review_count) AS strength
	    FROM learner_topic_mastery m
	    LEFT JOIN subdomains sd ON sd.slug = m.topic
	    LEFT JOIN domains d  ON d.slug = sd.domain_slug
	    LEFT JOIN domains td ON td.slug = m.topic
	   WHERE m.mastery >= $1 AND m.review_count >= $2
	     AND COALESCE(sd.persona, d.persona, td.persona) IS NOT NULL
	)`

// RefreshAvatarPersonas grows each learner's wardrobe and dresses them:
//  1. every mastered domain's outfit is added, or upgraded to a higher tier
//     (tiers never drop, and outfits are never taken away);
//  2. learners who haven't picked an outfit wear their strongest domain's
//     ("scholar" only when nothing more specific is mastered); learners who
//     have picked one keep it, at its current tier.
//
// Each learner whose wardrobe gained or upgraded an outfit gets one
// notification. Intended to run weekly.
func (s *Scheduler) RefreshAvatarPersonas(ctx context.Context) error {
	log.Println("[cron] running avatar-personas")
	rows, err := s.db.Query(ctx, `WITH `+masteredTopics+`,
		earned AS (
		  SELECT user_id, persona, MAX(tier) AS tier FROM mastered GROUP BY user_id, persona
		)
		INSERT INTO user_avatar_outfits AS o (user_id, persona, tier)
		SELECT e.user_id, e.persona, e.tier FROM earned e
		  JOIN users u ON u.id = e.user_id AND u.deleted_at IS NULL
		ON CONFLICT (user_id, persona) DO UPDATE SET tier = EXCLUDED.tier, upgraded_at = now()
		  WHERE o.tier < EXCLUDED.tier
		RETURNING o.user_id, o.persona, o.tier, o.earned_at = o.upgraded_at`, personaMinMastery, personaMinReviews)
	if err != nil {
		return err
	}
	type change struct {
		persona string
		tier    int
		isNew   bool
	}
	changes := map[string]change{}
	for rows.Next() {
		var id string
		var c change
		if err := rows.Scan(&id, &c.persona, &c.tier, &c.isNew); err != nil {
			rows.Close()
			return err
		}
		// One notification per learner: a brand-new outfit beats an upgrade.
		if prev, seen := changes[id]; !seen || (c.isNew && !prev.isNew) {
			changes[id] = c
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	if _, err := s.db.Exec(ctx, `WITH `+masteredTopics+`,
		strongest AS (
		  SELECT DISTINCT ON (user_id) user_id, persona FROM mastered
		   ORDER BY user_id, persona = 'scholar', strength DESC, topic
		)
		UPDATE users u SET avatar_persona = o.persona, avatar_tier = o.tier, avatar_persona_at = now()
		  FROM user_avatar_outfits o
		  LEFT JOIN strongest st ON st.user_id = o.user_id
		 WHERE o.user_id = u.id AND u.deleted_at IS NULL
		   AND o.persona = CASE WHEN u.avatar_persona_chosen THEN u.avatar_persona ELSE st.persona END
		   AND (u.avatar_persona IS DISTINCT FROM o.persona OR u.avatar_tier <> o.tier)`,
		personaMinMastery, personaMinReviews); err != nil {
		return err
	}

	for id, c := range changes {
		title := fmt.Sprintf("New avatar outfit: %s ✨", personaLabel(c.persona))
		body := "You've mastered new ground. Try it on from your avatar wardrobe."
		if !c.isNew {
			title = fmt.Sprintf("Your %s outfit levelled up", personaLabel(c.persona))
			body = "Your mastery keeps growing, and so does your outfit. Take a look!"
		}
		s.notifSvc.Emit(ctx, id, "system", title, body,
			notification.WithIcon("face"), notification.WithReference("avatar_persona"))
	}
	log.Printf("[cron] avatar-personas done (%d wardrobes changed)", len(changes))
	return nil
}

func personaLabel(p string) string {
	if p == "" {
		return p
	}
	return strings.ToUpper(p[:1]) + p[1:]
}
