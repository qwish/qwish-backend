package middleware

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"time"
)

type TimeCursor struct {
	Time time.Time `json:"time"`
	ID   string    `json:"id"`
}

func EncodeCursor(t time.Time, id string) string {
	raw, _ := json.Marshal(TimeCursor{t, id})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func DecodeCursor(raw string) (TimeCursor, error) {
	var c TimeCursor
	if len(raw) > 512 {
		return c, fmt.Errorf("invalid cursor")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return c, fmt.Errorf("invalid cursor")
	}
	if err = json.Unmarshal(b, &c); err != nil || c.Time.IsZero() {
		return c, fmt.Errorf("invalid cursor")
	}
	if _, err = uuid.Parse(c.ID); err != nil {
		return c, fmt.Errorf("invalid cursor")
	}
	return c, nil
}
