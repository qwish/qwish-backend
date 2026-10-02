package activity

import (
	"net/http"

	"github.com/qwish/backend/internal/middleware"
)

// Template is an editable preset: the client copies it into a new draft.
type Template struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Questions   []Question `json:"questions"`
}

func opts(pairs ...string) []Option {
	out := make([]Option, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, Option{ID: pairs[i], Label: pairs[i+1]})
	}
	return out
}

var templates = []Template{
	{ID: "hackathon-interest", Kind: "form", Title: "Hackathon interest", Description: "Tell us whether you'd like to take part.",
		Questions: []Question{
			{ID: "interested", Type: "single_choice", Label: "Would you take part?", Required: true, Options: opts("yes", "Yes", "maybe", "Maybe", "no", "No")},
			{ID: "tracks", Type: "multiple_choice", Label: "Tracks you're interested in", Options: opts("web", "Web", "mobile", "Mobile", "ai", "AI / ML", "hardware", "Hardware")},
			{ID: "experience", Type: "long_text", Label: "Past projects or hackathons", MaxLength: 1000},
		}},
	{ID: "event-registration", Kind: "form", Title: "Event registration", Description: "Register for this event.",
		Questions: []Question{
			{ID: "attending", Type: "acknowledgement", Label: "I will attend on the scheduled date", Required: true},
			{ID: "notes", Type: "short_text", Label: "Anything the organisers should know?"},
		}},
	{ID: "workshop-preferences", Kind: "form", Title: "Workshop preferences", Description: "Help us pick the next workshop.",
		Questions: []Question{
			{ID: "topics", Type: "multiple_choice", Label: "Topics you'd attend", Required: true, MaxSelect: 3, Options: opts("t1", "Topic 1", "t2", "Topic 2", "t3", "Topic 3", "t4", "Topic 4")},
			{ID: "date", Type: "date", Label: "Preferred date"},
		}},
	{ID: "volunteer-application", Kind: "form", Title: "Volunteer application", Description: "Apply to volunteer.",
		Questions: []Question{
			{ID: "role", Type: "single_choice", Label: "Preferred role", Required: true, Options: opts("logistics", "Logistics", "registration", "Registration desk", "tech", "Technical support", "media", "Media")},
			{ID: "hours", Type: "number", Label: "Hours you can give", Min: ptr(1), Max: ptr(40)},
			{ID: "why", Type: "long_text", Label: "Why do you want to volunteer?", MaxLength: 1000},
		}},
	{ID: "project-proposal", Kind: "form", Title: "Project proposal", Description: "Submit your project idea.",
		Questions: []Question{
			{ID: "title", Type: "short_text", Label: "Project title", Required: true, MaxLength: 120},
			{ID: "problem", Type: "long_text", Label: "Problem statement", Required: true, MaxLength: 2000},
			{ID: "approach", Type: "long_text", Label: "Proposed approach", MaxLength: 2000},
		}},
	{ID: "event-feedback", Kind: "form", Title: "Post-event feedback", Description: "Your responses are visible to the organisers with your name.",
		Questions: []Question{
			{ID: "rating", Type: "single_choice", Label: "How useful was the event?", Required: true, Options: opts("1", "1 – Not useful", "2", "2", "3", "3", "4", "4", "5", "5 – Very useful")},
			{ID: "liked", Type: "long_text", Label: "What worked well?", MaxLength: 1000},
			{ID: "improve", Type: "long_text", Label: "What should change next time?", MaxLength: 1000},
		}},
	{ID: "quick-poll", Kind: "poll", Title: "Quick poll", Description: "",
		Questions: []Question{
			{ID: "choice", Type: "single_choice", Label: "Your question", Required: true, Options: opts("a", "Option A", "b", "Option B")},
		}},
}

func ptr(f float64) *float64 { return &f }

func (h *Handler) Templates(w http.ResponseWriter, r *http.Request) {
	middleware.JSON(w, http.StatusOK, templates)
}
