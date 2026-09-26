package user

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/qwish/backend/internal/jsonx"
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
	if errors.Is(err, pgx.ErrNoRows) {
		middleware.NotFound(w, "user")
		return
	}
	if err != nil {
		log.Printf("GetMe: user %s: %v", userID, err)
		middleware.InternalError(w)
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

// POST /api/v1/users/me/scorecard-shares
func (h *Handler) RecordMyScorecardShare(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if err := h.svc.RecordScorecardShare(r.Context(), userID); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]bool{"recorded": true})
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
	quizType := strings.TrimSpace(r.URL.Query().Get("type"))
	if quizType != "" && quizType != "knowledge_check" && quizType != "play_and_win" {
		middleware.BadRequest(w, "invalid attempt type")
		return
	}
	var since *time.Time
	switch r.URL.Query().Get("period") {
	case "", "all":
	case "30d":
		value := time.Now().AddDate(0, 0, -30)
		since = &value
	case "90d":
		value := time.Now().AddDate(0, 0, -90)
		since = &value
	default:
		middleware.BadRequest(w, "invalid attempt period")
		return
	}
	var attempts []AttemptSummary
	var total int
	var err error
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		raw, decErr := base64.RawURLEncoding.DecodeString(cursor)
		parts := strings.SplitN(string(raw), "|", 2)
		if decErr != nil || len(parts) != 2 {
			middleware.BadRequest(w, "invalid cursor")
			return
		}
		at, parseErr := time.Parse(time.RFC3339Nano, parts[0])
		if parseErr != nil {
			middleware.BadRequest(w, "invalid cursor")
			return
		}
		attempts, total, err = h.svc.GetAttemptsAfter(r.Context(), userID, at, parts[1], limit, quizType, since)
	} else {
		attempts, total, err = h.svc.GetAttempts(r.Context(), userID, page, limit, quizType, since)
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	next := ""
	if len(attempts) == limit {
		last := attempts[len(attempts)-1]
		if last.CompletedAt != nil {
			next = base64.RawURLEncoding.EncodeToString([]byte(last.CompletedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID))
		}
	}
	middleware.JSONWithMeta(w, http.StatusOK, attempts, &middleware.Meta{Page: page, Limit: limit, Total: total, Cursor: next})
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
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
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
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || req.Skill == "" {
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
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
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
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
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
	if err := jsonx.NewDecoder(r.Body).Decode(&raw); err != nil {
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
		log.Printf("GetMyInsightsBreakdown: user %s: %v", middleware.GetUserID(r), err)
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

// GET /api/v1/users/me/content
func (h *Handler) GetMyAppContent(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.GetAppContent(r.Context(), middleware.GetUserID(r), middleware.GetRole(r), middleware.GetInstitutionID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, items)
}

// POST /api/v1/users/me/content/{kind}/{contentId}/events
func (h *Handler) RecordMyContentEvent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Event string `json:"event"`
	}
	contentID := chi.URLParam(r, "contentId")
	if _, err := uuid.Parse(contentID); err != nil {
		middleware.BadRequest(w, "invalid content or event")
		return
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "event is required")
		return
	}
	err := h.svc.RecordContentEvent(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "kind"), contentID, req.Event)
	if errors.Is(err, ErrInvalidContentEvent) {
		middleware.BadRequest(w, "invalid content or event")
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
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
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
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
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	report, err := h.svc.GetLearningReport(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			middleware.NotFound(w, "user")
		} else {
			middleware.InternalError(w)
		}
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="qwish-learning-report.pdf"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Write(renderLearningReport(report))
}
