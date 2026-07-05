package avatar

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	mw "github.com/qwish/backend/internal/middleware"
)

// Handler serves deterministic, procedurally-generated SVG avatars.
// Stateless — no DB, no auth.
type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

// Get renders the avatar for {seed} as image/svg+xml. Optional query params
// (skin, hairStyle, hairColor, background, expression, accessory) override the
// seeded random baseline; unknown/invalid values are ignored. See Meta for the
// valid vocabularies.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	seed := chi.URLParam(r, "seed")
	q := r.URL.Query()
	svg := GenerateAvatarCustom(seed, Options{
		Skin:       q.Get("skin"),
		HairStyle:  q.Get("hairStyle"),
		HairColor:  q.Get("hairColor"),
		Background: q.Get("background"),
		Expression: q.Get("expression"),
		Accessory:  q.Get("accessory"),
	})
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	// Deterministic + no auth: safe to cache hard. Params are part of the URL,
	// so distinct customizations cache separately.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write([]byte(svg))
}

// Meta returns the valid values for each customization option, so a frontend
// can build pickers without hardcoding them.
func (h *Handler) Meta(w http.ResponseWriter, r *http.Request) {
	mw.JSON(w, http.StatusOK, map[string]any{
		"skin":       SkinTones,
		"hairStyle":  HairStyles,
		"hairColor":  PaletteNames,
		"background": PaletteNames,
		"expression": Expressions,
		"accessory":  Accessories,
		"note":       "hairColor/background/skin also accept a #RRGGBB hex",
	})
}
