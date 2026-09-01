package user

import (
	"context"
	"errors"
	"time"
)

type AppContentItem struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Placement string     `json:"placement"`
	Title     string     `json:"title"`
	Body      *string    `json:"body,omitempty"`
	CTALabel  *string    `json:"cta_label,omitempty"`
	CTAURL    *string    `json:"cta_url,omitempty"`
	StartsAt  *time.Time `json:"starts_at,omitempty"`
	EndsAt    *time.Time `json:"ends_at,omitempty"`
}

var ErrInvalidContentEvent = errors.New("invalid content event")

// GetAppContent returns only content the authenticated learner is allowed to
// see. Audience, lifecycle and institution checks live here, never in the app.
func (s *Service) GetAppContent(ctx context.Context, userID, role, instID string) ([]AppContentItem, error) {
	rows, err := s.db.Query(ctx, `
		SELECT p.id, 'promo', p.type, p.title, p.body, p.cta_label, p.cta_url, p.starts_at, p.ends_at
		  FROM promotional_content p
		 WHERE p.status='active'
		   AND (p.starts_at IS NULL OR p.starts_at <= now())
		   AND (p.ends_at IS NULL OR p.ends_at > now())
		   AND (p.type<>'home_banner' OR p.id=(SELECT p2.id FROM promotional_content p2
		        WHERE p2.type='home_banner' AND p2.status='active'
		          AND (p2.starts_at IS NULL OR p2.starts_at<=now()) AND (p2.ends_at IS NULL OR p2.ends_at>now())
		        ORDER BY p2.starts_at DESC NULLS LAST, p2.created_at DESC LIMIT 1))
		   AND (p.audience='all'
		        OR (p.audience='students' AND $2='student')
		        OR (p.audience='institution' AND (p.institution_id::text=$3 OR EXISTS (
		              SELECT 1 FROM promo_institutions pi WHERE pi.promo_id=p.id AND pi.institution_id::text=$3)))
		        OR (p.audience='lapsed' AND EXISTS (
		              SELECT 1 FROM users u WHERE u.id=$1 AND u.last_active_at < now()-interval '30 days')))
		   AND NOT EXISTS (
		       SELECT 1 FROM content_delivery_events e
		        WHERE e.user_id=$1 AND e.content_kind='promo' AND e.content_id=p.id AND e.event_type='dismiss')
		   AND (p.type<>'splash_interstitial' OR NOT EXISTS (
		       SELECT 1 FROM content_delivery_events e
		        WHERE e.user_id=$1 AND e.content_kind='promo' AND e.content_id=p.id
		          AND e.event_type='impression' AND e.created_at > now()-interval '7 days'))
		UNION ALL
		SELECT a.id, 'announcement',
		       CASE channel WHEN 'in_app_banner' THEN 'announcement_banner' ELSE 'announcement_notification' END,
		       a.title, a.body,
		       a.cta_label, a.cta_url, COALESCE(a.scheduled_at,a.created_at), NULL
		  FROM announcements a
		 CROSS JOIN LATERAL unnest(a.delivery_types) AS channel
		 WHERE (a.status='sent' OR (a.status='scheduled' AND (a.scheduled_at IS NULL OR a.scheduled_at <= now())))
		   AND channel IN ('in_app_banner','in_app_notification')
		   AND (channel<>'in_app_notification' OR a.status='scheduled')
		   AND (a.audience='all'
		        OR (a.audience='students' AND $2='student')
		        OR (a.audience='teachers' AND $2='teacher')
		        OR (a.audience='institution' AND (a.institution_id::text=$3 OR EXISTS (
		              SELECT 1 FROM announcement_institutions ai WHERE ai.announcement_id=a.id AND ai.institution_id::text=$3)))
		        OR (a.audience='country' AND EXISTS (
		              SELECT 1 FROM users u JOIN institutions i ON i.id=u.institution_id
		               WHERE u.id=$1 AND lower(COALESCE(i.onboarding_country,'')) IN ('india','in'))))
		   AND NOT EXISTS (
		       SELECT 1 FROM content_delivery_events e
		        WHERE e.user_id=$1 AND e.content_kind='announcement' AND e.content_id=a.id AND e.event_type='dismiss')
		 ORDER BY starts_at DESC NULLS LAST`, userID, role, instID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AppContentItem{}
	for rows.Next() {
		var item AppContentItem
		if err := rows.Scan(&item.ID, &item.Kind, &item.Placement, &item.Title, &item.Body,
			&item.CTALabel, &item.CTAURL, &item.StartsAt, &item.EndsAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) RecordContentEvent(ctx context.Context, userID, kind, contentID, event string) error {
	if (kind != "promo" && kind != "announcement") ||
		(event != "impression" && event != "click" && event != "dismiss") {
		return ErrInvalidContentEvent
	}
	// Validate against the real content table before recording the polymorphic id.
	table := "promotional_content"
	if kind == "announcement" {
		table = "announcements"
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` WHERE id=$1)`, contentID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrInvalidContentEvent
	}
	conflict := "DO NOTHING"
	if event == "impression" {
		conflict = "DO UPDATE SET created_at=now()"
	}
	_, err := s.db.Exec(ctx, `INSERT INTO content_delivery_events(user_id,content_kind,content_id,event_type)
		VALUES($1,$2,$3,$4) ON CONFLICT (user_id,content_kind,content_id,event_type) `+conflict, userID, kind, contentID, event)
	return err
}
