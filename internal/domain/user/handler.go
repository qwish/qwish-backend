package user

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// GET /api/v1/users/me
func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	profile, err := h.svc.GetProfile(r.Context(), userID)
	if err != nil {
		middleware.NotFound(w, "user")
		return
	}
	middleware.JSON(w, http.StatusOK, profile)
}

// PATCH /api/v1/users/me
func (h *Handler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	// The body is read once and unmarshalled twice: display_name goes through
	// the service, the rest are plain column writes.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	var req struct {
		DisplayName *string `json:"display_name"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	userID := middleware.GetUserID(r)
	if req.DisplayName != nil {
		if err := h.svc.UpdateDisplayName(r.Context(), userID, *req.DisplayName); err != nil {
			middleware.InternalError(w)
			return
		}
	}

	var pf personalFields
	if err := json.Unmarshal(body, &pf); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if set, args := buildUserPatch(pf); set != "" {
		args = append(args, userID)
		if err := h.svc.UpdatePersonalFields(r.Context(), set, args); err != nil {
			log.Printf("UpdateMe: personal fields: %v", err)
			middleware.InternalError(w)
			return
		}
	}
	profile, _ := h.svc.GetProfile(r.Context(), userID)
	middleware.JSON(w, http.StatusOK, profile)
}

// GET /api/v1/users/me/stats
func (h *Handler) GetMyStats(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	stats, err := h.svc.GetStats(r.Context(), userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, stats)
}

// GET /api/v1/users/me/badges
func (h *Handler) GetMyBadges(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	badges, err := h.svc.GetBadges(r.Context(), userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, badges)
}

// GET /api/v1/users/me/attempts
func (h *Handler) GetMyAttempts(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	attempts, total, err := h.svc.GetAttempts(r.Context(), userID, page, limit)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSONWithMeta(w, http.StatusOK, attempts, &middleware.Meta{
		Page: page, Limit: limit, Total: total,
	})
}

// GET /api/v1/users/:userId/profile
func (h *Handler) GetPublicProfile(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "userId")
	viewerID := middleware.GetUserID(r)
	profile, err := h.svc.GetPublicProfile(r.Context(), viewerID, targetID)
	if errors.Is(err, ErrProfilePrivate) {
		middleware.Error(w, http.StatusForbidden, "PROFILE_PRIVATE", "this profile is private")
		return
	}
	if err != nil {
		middleware.NotFound(w, "user")
		return
	}
	if viewerID != targetID {
		h.svc.RecordProfileView(r.Context(), viewerID, targetID)
	}
	middleware.JSON(w, http.StatusOK, profile)
}

// GET /api/v1/users/me/profile-views
func (h *Handler) GetMyProfileViews(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	stats, err := h.svc.GetProfileViews(r.Context(), userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, stats)
}

// GET /api/v1/users/me/rank
func (h *Handler) GetMyRank(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	instID := middleware.GetInstitutionID(r)
	rank, err := h.svc.GetRank(r.Context(), userID, instID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, rank)
}

// GET /api/v1/users/me/milestones
func (h *Handler) GetMyMilestones(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	milestones, err := h.svc.GetMilestones(r.Context(), userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, milestones)
}

// GET /api/v1/users/me/education
func (h *Handler) GetMyEducation(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	list, err := h.svc.GetEducation(r.Context(), userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, list)
}

// POST /api/v1/users/me/education
func (h *Handler) AddMyEducation(w http.ResponseWriter, r *http.Request) {
	var req Education
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if req.InstitutionName == "" {
		middleware.BadRequest(w, "institution_name is required")
		return
	}
	userID := middleware.GetUserID(r)
	out, err := h.svc.AddEducation(r.Context(), userID, req)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, out)
}

// DELETE /api/v1/users/me/education/:id
func (h *Handler) DeleteMyEducation(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	edID := chi.URLParam(r, "id")
	if err := h.svc.DeleteEducation(r.Context(), userID, edID); err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/users/me/skills
func (h *Handler) GetMySkills(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	skills, err := h.svc.GetSkills(r.Context(), userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, skills)
}

// POST /api/v1/users/me/skills
func (h *Handler) AddMySkill(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Skill string `json:"skill"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Skill == "" {
		middleware.BadRequest(w, "skill is required")
		return
	}
	userID := middleware.GetUserID(r)
	if err := h.svc.AddSkill(r.Context(), userID, req.Skill); err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/v1/users/me/skills/:skill
func (h *Handler) DeleteMySkill(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	skill := chi.URLParam(r, "skill")
	if err := h.svc.DeleteSkill(r.Context(), userID, skill); err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PATCH /api/v1/users/me/domain
func (h *Handler) UpdateMyDomain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	userID := middleware.GetUserID(r)
	if err := h.svc.UpdateDomain(r.Context(), userID, req.Domain); err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/v1/users/me
func (h *Handler) DeleteMe(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if err := h.svc.SoftDelete(r.Context(), userID); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "account deleted"})
}

// GET /api/v1/users/me/settings
func (h *Handler) GetMySettings(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.GetSettings(r.Context(), middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, st)
}

// PATCH /api/v1/users/me/settings — update theme (dark mode) and privacy flags.
func (h *Handler) UpdateMySettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Theme            *string `json:"theme"`
		ProfilePrivate   *bool   `json:"profile_private"`
		RecruiterVisible *bool   `json:"recruiter_visible"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	st, err := h.svc.UpdateSettings(r.Context(), middleware.GetUserID(r), req.Theme, req.ProfilePrivate, req.RecruiterVisible)
	if errors.Is(err, ErrInvalidTheme) {
		middleware.BadRequest(w, err.Error())
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, st)
}

// GET /api/v1/users/me/notification-preferences
func (h *Handler) GetMyNotifPrefs(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.GetNotifPrefs(r.Context(), middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, p)
}

// PATCH /api/v1/users/me/notification-preferences
func (h *Handler) UpdateMyNotifPrefs(w http.ResponseWriter, r *http.Request) {
	var raw map[string]bool
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	p, err := h.svc.UpdateNotifPrefs(r.Context(), middleware.GetUserID(r), raw)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, p)
}

// GET /api/v1/users/me/insights/weekly
func (h *Handler) GetMyWeeklyInsights(w http.ResponseWriter, r *http.Request) {
	wi, err := h.svc.GetWeeklyInsights(r.Context(), middleware.GetUserID(r), middleware.GetInstitutionID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, wi)
}

// GET /api/v1/users/me/insights/breakdown
func (h *Handler) GetMyInsightsBreakdown(w http.ResponseWriter, r *http.Request) {
	bd, err := h.svc.GetInsightsBreakdown(r.Context(), middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, bd)
}

// GET /api/v1/users/me/insights/trend?range=4w|12w|all
func (h *Handler) GetMyScoreTrend(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	trend, err := h.svc.GetScoreTrend(r.Context(), middleware.GetUserID(r), rng)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, trend)
}

// GET /api/v1/users/me/recommendations
func (h *Handler) GetMyRecommendations(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	instID := middleware.GetInstitutionID(r)
	recs, err := h.svc.GetRecommendations(r.Context(), userID, instID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, recs)
}

// GET /api/v1/users/me/quiz-pick?exclude_id=<optional-current-quiz>
func (h *Handler) PickMyQuiz(w http.ResponseWriter, r *http.Request) {
	if middleware.GetRole(r) != "student" {
		middleware.Forbidden(w)
		return
	}
	excludeID := r.URL.Query().Get("exclude_id")
	if excludeID != "" {
		if _, err := uuid.Parse(excludeID); err != nil {
			middleware.BadRequest(w, "exclude_id must be a UUID")
			return
		}
	}
	quiz, err := h.svc.PickQuiz(
		r.Context(),
		middleware.GetUserID(r),
		middleware.GetInstitutionID(r),
		excludeID,
	)
	if errors.Is(err, ErrInterestsRequired) {
		middleware.Error(w, http.StatusConflict, "INTERESTS_REQUIRED", "select at least 10 topics before requesting a quiz")
		return
	}
	if err == pgx.ErrNoRows {
		middleware.Error(w, http.StatusNotFound, "NO_QUIZ_AVAILABLE", "no unplayed assessment is available")
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, quiz)
}

// GET /api/v1/quizzes/featured returns the current public featured set. The
// same handler is also used by the super-admin console to read its selection.
func (h *Handler) GetFeaturedQuizzes(w http.ResponseWriter, r *http.Request) {
	quizzes, err := h.svc.GetFeaturedQuizzes(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, quizzes)
}

// PUT /api/v1/admin/featured-quizzes (super_admin only; route-enforced).
func (h *Handler) SetFeaturedQuizzes(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req struct {
		QuizIDs []string `json:"quiz_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if err := h.svc.SetFeaturedQuizzes(r.Context(), req.QuizIDs); err != nil {
		if errors.Is(err, ErrInvalidFeaturedQuizzes) {
			middleware.BadRequest(w, err.Error())
			return
		}
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"quiz_ids": req.QuizIDs})
}

func (h *Handler) GetMyLearningPreferences(w http.ResponseWriter, r *http.Request) {
	prefs, err := h.svc.GetLearningPreferences(r.Context(), middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, prefs)
}

func (h *Handler) UpdateMyLearningPreferences(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req LearningPreferences
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	prefs, err := h.svc.UpdateLearningPreferences(
		r.Context(), middleware.GetUserID(r), req.Language, req.Topics,
	)
	if errors.Is(err, ErrInvalidLearningLanguage) || errors.Is(err, ErrInvalidLearningTopics) {
		middleware.BadRequest(w, err.Error())
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, prefs)
}

// GET /api/v1/users/me/report-card
func (h *Handler) GetMyReportCardPDF(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	profile, err := h.svc.GetProfile(r.Context(), userID)
	if err != nil {
		middleware.NotFound(w, "user")
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="report-card.pdf"`)

	// Minimal valid PDF structure with dynamic user stats
	streamContent := fmt.Sprintf(`BT
/F1 22 Tf
50 750 Td
(Qwish Verified Skill Report Card) Tj
/F1 12 Tf
0 -40 Td
(Name: %s) Tj
0 -20 Td
(Email: %s) Tj
0 -20 Td
(Total Points: %d) Tj
0 -20 Td
(Current Streak: %d days) Tj
ET`, profile.DisplayName, profile.Email, profile.TotalPoints, profile.CurrentStreak)

	pdfData := fmt.Sprintf(`%%PDF-1.4
1 0 obj
<< /Type /Catalog /Pages 2 0 R >>
endobj
2 0 obj
<< /Type /Pages /Kids [3 0 R] /Count 1 >>
endobj
3 0 obj
<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> >> >> /Contents 4 0 R >>
endobj
4 0 obj
<< /Length %d >>
stream
%s
endstream
endobj
xref
0 5
0000000000 65535 f 
0000000009 00000 n 
0000000058 00000 n 
0000000115 00000 n 
0000000282 00000 n 
trailer
<< /Size 5 /Root 1 0 R >>
startxref
450
%%%%EOF`, len(streamContent), streamContent)

	w.Write([]byte(pdfData))
}
