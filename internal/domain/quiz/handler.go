package quiz

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// GET /api/v1/quizzes
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	userID := middleware.GetUserID(r)
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	quizzes, total, err := h.svc.ListForStudent(r.Context(), instID, q.Get("type"), q.Get("saved"), q.Get("search"), userID, page, limit)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSONWithMeta(w, http.StatusOK, quizzes, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// GET /api/v1/quizzes/:quizId
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	quiz, err := h.svc.GetByID(r.Context(), chi.URLParam(r, "quizId"))
	if err != nil {
		middleware.NotFound(w, "quiz")
		return
	}
	middleware.JSON(w, http.StatusOK, quiz)
}

// POST /api/v1/quizzes/:quizId/save
func (h *Handler) Save(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.SaveQuiz(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "quizId")); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "quiz saved"})
}

// DELETE /api/v1/quizzes/:quizId/save
func (h *Handler) Unsave(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.UnsaveQuiz(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "quizId")); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "quiz unsaved"})
}

// GET /api/v1/quizzes/:quizId/share
func (h *Handler) Share(w http.ResponseWriter, r *http.Request) {
	quizID := chi.URLParam(r, "quizId")
	middleware.JSON(w, http.StatusOK, map[string]string{
		"deep_link": "quizapp://quiz/" + quizID,
	})
}

// POST /api/v1/quizzes/:quizId/reports
func (h *Handler) ReportQuiz(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reason      string `json:"reason"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Reason == "" {
		middleware.BadRequest(w, "reason is required")
		return
	}
	quizID := chi.URLParam(r, "quizId")
	if err := h.svc.SubmitReport(r.Context(), middleware.GetUserID(r), quizID, nil, req.Reason, req.Description); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "thanks — we'll review this"})
}

// POST /api/v1/quizzes/:quizId/questions/:questionId/reports
func (h *Handler) ReportQuestion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reason      string `json:"reason"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Reason == "" {
		middleware.BadRequest(w, "reason is required")
		return
	}
	quizID := chi.URLParam(r, "quizId")
	qID := chi.URLParam(r, "questionId")
	if err := h.svc.SubmitReport(r.Context(), middleware.GetUserID(r), quizID, &qID, req.Reason, req.Description); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "thanks — we'll review this"})
}

// --- Teacher routes ---

// GET /api/v1/teacher/quizzes
func (h *Handler) TeacherList(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	quizzes, total, err := h.svc.ListForTeacher(r.Context(), userID, q.Get("status"), page, limit)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSONWithMeta(w, http.StatusOK, quizzes, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// POST /api/v1/teacher/quizzes
func (h *Handler) TeacherCreate(w http.ResponseWriter, r *http.Request) {
	var req CreateQuizReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Title == "" {
		middleware.BadRequest(w, "title is required")
		return
	}
	if req.Visibility == "" {
		req.Visibility = "institution"
	}
	quiz, err := h.svc.Create(r.Context(), req, middleware.GetUserID(r), middleware.GetInstitutionID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, quiz)
}

// PATCH /api/v1/teacher/quizzes/:quizId
func (h *Handler) TeacherUpdate(w http.ResponseWriter, r *http.Request) {
	var req CreateQuizReq
	json.NewDecoder(r.Body).Decode(&req)
	if err := h.svc.Update(r.Context(), chi.URLParam(r, "quizId"), middleware.GetUserID(r), req); err != nil {
		middleware.InternalError(w)
		return
	}
	quiz, _ := h.svc.GetByID(r.Context(), chi.URLParam(r, "quizId"))
	middleware.JSON(w, http.StatusOK, quiz)
}

// POST /api/v1/teacher/quizzes/:quizId/questions
func (h *Handler) TeacherAddQuestion(w http.ResponseWriter, r *http.Request) {
	var req AddQuestionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Prompt == "" || req.Type == "" {
		middleware.BadRequest(w, "prompt and type are required")
		return
	}
	q, err := h.svc.AddQuestion(r.Context(), chi.URLParam(r, "quizId"), middleware.GetUserID(r), req)
	if err != nil {
		middleware.Error(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	middleware.JSON(w, http.StatusCreated, q)
}

// GET /api/v1/teacher/quizzes/:quizId/questions
// Returns full questions (including correct answers) for a quiz the caller owns,
// for the authoring UI. GetByID confirms ownership before exposing answers.
func (h *Handler) TeacherGetQuestions(w http.ResponseWriter, r *http.Request) {
	quizID := chi.URLParam(r, "quizId")
	quiz, err := h.svc.GetByID(r.Context(), quizID)
	if err != nil {
		middleware.NotFound(w, "quiz")
		return
	}
	if quiz.CreatedBy != middleware.GetUserID(r) {
		middleware.Forbidden(w)
		return
	}
	questions, err := h.svc.GetQuestions(r.Context(), quizID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, questions)
}

// PATCH /api/v1/teacher/quizzes/:quizId/questions/:questionId
func (h *Handler) TeacherUpdateQuestion(w http.ResponseWriter, r *http.Request) {
	var req AddQuestionReq
	json.NewDecoder(r.Body).Decode(&req)
	if err := h.svc.UpdateQuestion(r.Context(),
		chi.URLParam(r, "quizId"), chi.URLParam(r, "questionId"), middleware.GetUserID(r), req); err != nil {
		middleware.Error(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "question updated"})
}

// DELETE /api/v1/teacher/quizzes/:quizId/questions/:questionId
func (h *Handler) TeacherDeleteQuestion(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeleteQuestion(r.Context(),
		chi.URLParam(r, "quizId"), chi.URLParam(r, "questionId"), middleware.GetUserID(r)); err != nil {
		middleware.Error(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "question deleted"})
}

// POST /api/v1/teacher/quizzes/:quizId/publish
func (h *Handler) TeacherPublish(w http.ResponseWriter, r *http.Request) {
	newStatus, err := h.svc.Publish(r.Context(), chi.URLParam(r, "quizId"), middleware.GetUserID(r))
	if err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": newStatus})
}

// DELETE /api/v1/teacher/quizzes/:quizId
func (h *Handler) TeacherDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), chi.URLParam(r, "quizId"), middleware.GetUserID(r)); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "quiz deleted"})
}

// POST /api/v1/teacher/quizzes/:quizId/unpublish
func (h *Handler) TeacherUnpublish(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Unpublish(r.Context(), chi.URLParam(r, "quizId"), middleware.GetUserID(r)); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "draft"})
}

// PATCH /api/v1/teacher/quizzes/:quizId/questions/order
func (h *Handler) TeacherReorderQuestions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Order []string `json:"order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Order) == 0 {
		middleware.BadRequest(w, "order array is required")
		return
	}
	if err := h.svc.ReorderQuestions(r.Context(), chi.URLParam(r, "quizId"), middleware.GetUserID(r), req.Order); err != nil {
		middleware.Error(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "questions reordered"})
}

// GET /api/v1/teacher/quizzes/:quizId/results
func (h *Handler) TeacherResults(w http.ResponseWriter, r *http.Request) {
	results, err := h.svc.GetTeacherResults(r.Context(), chi.URLParam(r, "quizId"), middleware.GetUserID(r))
	if err != nil {
		middleware.NotFound(w, "quiz")
		return
	}
	middleware.JSON(w, http.StatusOK, results)
}
