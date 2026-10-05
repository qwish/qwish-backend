package quiz

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"time"
)

type feedCursor struct {
	PublishedAt *time.Time `json:"published_at"`
	ID          string     `json:"id"`
}

func decodeFeedCursor(raw string) (*feedCursor, error) {
	c := &feedCursor{}
	if raw == "" {
		return c, nil
	}
	if len(raw) > 512 {
		return nil, fmt.Errorf("invalid cursor")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor")
	}
	if err = json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("invalid cursor")
	}
	if _, err = uuid.Parse(c.ID); err != nil {
		return nil, fmt.Errorf("invalid cursor")
	}
	return c, nil
}
func (s *Service) ListForStudentCursor(ctx context.Context, institutionID, scope, quizType, saved, search, domain, subdomain string, after, before *time.Time, userID string, unplayed bool, limit int, cursor string) ([]Quiz, int, string, error) {
	c, err := decodeFeedCursor(cursor)
	if err != nil {
		return nil, 0, "", err
	}
	list, total, err := s.listForStudentFilteredScope(ctx, institutionID, scope, quizType, saved, search, domain, subdomain, after, before, userID, "newest", unplayed, 1, limit+1, c)
	if err != nil {
		return nil, 0, "", err
	}
	next := ""
	if len(list) > limit {
		list = list[:limit]
		last := list[len(list)-1]
		raw, _ := json.Marshal(feedCursor{last.PublishedAt, last.ID})
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	return list, total, next, nil
}
