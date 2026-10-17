package user

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

// avatarMoodSQL derives the learner's avatar mood from effort and return, not
// from scores: proud ("delighted") after finishing a demanding session in the
// last day whatever the result, "engaged" after any session today, "resting"
// after three quiet days, a soft "smile" otherwise (including brand-new
// learners). It never looks disappointed. Expects the users row as `u`.
const avatarMoodSQL = `CASE
	  WHEN EXISTS (SELECT 1 FROM quiz_attempts a LEFT JOIN quiz_read_stats rs ON rs.quiz_id = a.quiz_id
	                WHERE a.user_id = u.id AND a.status = 'completed' AND a.completed_at > now() - interval '24 hours'
	                  AND (a.total_questions >= 10 OR COALESCE(rs.difficulty, 0) >= 0.75)) THEN 'delighted'
	  WHEN EXISTS (SELECT 1 FROM quiz_attempts a
	                WHERE a.user_id = u.id AND a.status = 'completed' AND a.completed_at > now() - interval '24 hours') THEN 'engaged'
	  WHEN (SELECT max(a.completed_at) FROM quiz_attempts a
	         WHERE a.user_id = u.id AND a.status = 'completed') < now() - interval '3 days' THEN 'resting'
	  ELSE 'smile' END`

// ErrOutfitNotEarned is returned when a learner tries to wear an outfit that
// isn't in their wardrobe.
var ErrOutfitNotEarned = errors.New("outfit not earned")

type AvatarOutfit struct {
	Persona  string    `json:"persona"`
	Tier     int       `json:"tier"`
	EarnedAt time.Time `json:"earned_at"`
}

// AvatarWardrobe is what the learner wears and everything they could.
type AvatarWardrobe struct {
	Persona *string        `json:"persona"` // worn outfit; nil = plain clothes
	Tier    int            `json:"tier"`
	Mood    string         `json:"mood"`
	Chosen  bool           `json:"chosen"` // false = auto-dressed in their strongest domain
	Outfits []AvatarOutfit `json:"outfits"`
}

func (s *Service) GetAvatarWardrobe(ctx context.Context, userID string) (*AvatarWardrobe, error) {
	wr := &AvatarWardrobe{Outfits: []AvatarOutfit{}}
	if err := s.db.QueryRow(ctx, `SELECT u.avatar_persona, u.avatar_tier, u.avatar_persona_chosen, `+avatarMoodSQL+`
		FROM users u WHERE u.id = $1 AND u.deleted_at IS NULL`, userID,
	).Scan(&wr.Persona, &wr.Tier, &wr.Chosen, &wr.Mood); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT persona, tier, earned_at FROM user_avatar_outfits
		WHERE user_id = $1 ORDER BY tier DESC, earned_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var o AvatarOutfit
		if err := rows.Scan(&o.Persona, &o.Tier, &o.EarnedAt); err != nil {
			return nil, err
		}
		wr.Outfits = append(wr.Outfits, o)
	}
	return wr, rows.Err()
}

// WearAvatarOutfit dresses the learner in an earned outfit, at its current
// tier. An empty persona hands the choice back to the weekly job, which
// dresses them in their strongest domain; until then they keep what they wear.
func (s *Service) WearAvatarOutfit(ctx context.Context, userID, persona string) error {
	if persona == "" {
		_, err := s.db.Exec(ctx, `UPDATE users SET avatar_persona_chosen = false WHERE id = $1`, userID)
		return err
	}
	tag, err := s.db.Exec(ctx, `UPDATE users u SET avatar_persona = o.persona, avatar_tier = o.tier,
		       avatar_persona_chosen = true, avatar_persona_at = now()
		  FROM user_avatar_outfits o
		 WHERE u.id = $1 AND o.user_id = u.id AND o.persona = $2`, userID, persona)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrOutfitNotEarned
	}
	return nil
}

// GET /api/v1/users/me/avatar
func (h *Handler) GetMyAvatar(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	wr, err := h.svc.GetAvatarWardrobe(r.Context(), userID)
	if err != nil {
		log.Printf("GetMyAvatar: user %s: %v", userID, err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, wr)
}

// PUT /api/v1/users/me/avatar  {"persona": "scientist"}  ("" or null = auto)
func (h *Handler) WearMyAvatar(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Persona *string `json:"persona"`
	}
	if err := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	persona := ""
	if req.Persona != nil {
		persona = *req.Persona
	}
	userID := middleware.GetUserID(r)
	if err := h.svc.WearAvatarOutfit(r.Context(), userID, persona); errors.Is(err, ErrOutfitNotEarned) {
		middleware.BadRequest(w, "that outfit hasn't been earned yet")
		return
	} else if err != nil {
		log.Printf("WearMyAvatar: user %s: %v", userID, err)
		middleware.InternalError(w)
		return
	}
	h.GetMyAvatar(w, r)
}
