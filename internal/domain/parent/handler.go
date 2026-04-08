package parent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	db *pgxpool.Pool
}

func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db}
}

// POST /api/v1/parent/link-invite   (student generates code)
func (h *Handler) GenerateInvite(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	role := middleware.GetRole(r)
	if role != "student" {
		middleware.Forbidden(w)
		return
	}
	code := uuid.New().String()[:8]
	_, err := h.db.Exec(r.Context(),
		`INSERT INTO parent_student_links (parent_id, student_id, invite_code, status)
		 VALUES ('00000000-0000-0000-0000-000000000000',$1,$2,'pending')
		 ON CONFLICT DO NOTHING`,
		userID, code)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"invite_code": code})
}

// POST /api/v1/parent/link   (parent links using code)
func (h *Handler) Link(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InviteCode string `json:"invite_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.InviteCode == "" {
		middleware.BadRequest(w, "invite_code is required")
		return
	}
	parentID := middleware.GetUserID(r)

	// Find the pending link
	var linkID, studentID string
	err := h.db.QueryRow(r.Context(),
		`SELECT id, student_id FROM parent_student_links WHERE invite_code=$1 AND status='pending'`,
		req.InviteCode,
	).Scan(&linkID, &studentID)
	if err != nil {
		middleware.NotFound(w, "invite code")
		return
	}

	// Update the link with parent_id
	_, err = h.db.Exec(r.Context(),
		`UPDATE parent_student_links SET parent_id=$1 WHERE id=$2`, parentID, linkID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "link request sent, waiting for student acceptance", "link_id": linkID})
}

// POST /api/v1/parent/link/:linkId/accept   (student accepts)
func (h *Handler) Accept(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	linkID := chi.URLParam(r, "linkId")

	_, err := h.db.Exec(r.Context(),
		`UPDATE parent_student_links SET status='active', linked_at=now()
		 WHERE id=$1 AND student_id=$2 AND status='pending'`,
		linkID, userID)
	if err != nil {
		middleware.BadRequest(w, "link not found or already processed")
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "parent link activated"})
}

// DELETE /api/v1/parent/link/:linkId
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	linkID := chi.URLParam(r, "linkId")

	_, err := h.db.Exec(r.Context(),
		`UPDATE parent_student_links SET status='revoked'
		 WHERE id=$1 AND (student_id=$2 OR parent_id=$2)`,
		linkID, userID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "link revoked"})
}

// GET /api/v1/parent/children
func (h *Handler) ListChildren(w http.ResponseWriter, r *http.Request) {
	parentID := middleware.GetUserID(r)
	rows, err := h.db.Query(r.Context(),
		`SELECT u.id, u.display_name, u.total_points, u.current_streak
		 FROM parent_student_links psl
		 JOIN users u ON u.id=psl.student_id
		 WHERE psl.parent_id=$1 AND psl.status='active'`, parentID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type child struct {
		ID            string `json:"id"`
		DisplayName   string `json:"display_name"`
		TotalPoints   int64  `json:"total_points"`
		CurrentStreak int    `json:"current_streak"`
	}
	var children []child
	for rows.Next() {
		var c child
		rows.Scan(&c.ID, &c.DisplayName, &c.TotalPoints, &c.CurrentStreak)
		children = append(children, c)
	}
	if children == nil {
		children = []child{}
	}
	middleware.JSON(w, http.StatusOK, children)
}

// GET /api/v1/parent/children/:studentId/overview
func (h *Handler) ChildOverview(w http.ResponseWriter, r *http.Request) {
	parentID := middleware.GetUserID(r)
	studentID := chi.URLParam(r, "studentId")

	// Verify link exists
	var check int
	h.db.QueryRow(r.Context(),
		`SELECT 1 FROM parent_student_links WHERE parent_id=$1 AND student_id=$2 AND status='active'`,
		parentID, studentID,
	).Scan(&check)
	if check == 0 {
		middleware.Forbidden(w)
		return
	}

	overview, err := getChildOverview(r.Context(), h.db, studentID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, overview)
}

func getChildOverview(ctx context.Context, db *pgxpool.Pool, studentID string) (map[string]interface{}, error) {
	var displayName string
	var points int64
	var streak int
	var avgScore float64
	var quizCount int
	db.QueryRow(ctx,
		`SELECT display_name, total_points, current_streak FROM users WHERE id=$1`, studentID,
	).Scan(&displayName, &points, &streak)
	db.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(AVG(score_pct),0) FROM quiz_attempts WHERE user_id=$1 AND status='completed'`, studentID,
	).Scan(&quizCount, &avgScore)

	// Last 5 attempts
	rows, _ := db.Query(ctx,
		`SELECT qa.id, q.title, COALESCE(qa.score_pct,0), COALESCE(qa.points_delta,0), qa.completed_at
		 FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id
		 WHERE qa.user_id=$1 AND qa.status='completed'
		 ORDER BY qa.completed_at DESC LIMIT 5`, studentID)
	defer rows.Close()

	type attempt struct {
		ID          string     `json:"id"`
		QuizTitle   string     `json:"quiz_title"`
		ScorePct    float64    `json:"score_pct"`
		PointsDelta int64      `json:"points_delta"`
		CompletedAt *time.Time `json:"completed_at"`
	}
	var attempts []attempt
	for rows.Next() {
		var a attempt
		rows.Scan(&a.ID, &a.QuizTitle, &a.ScorePct, &a.PointsDelta, &a.CompletedAt)
		attempts = append(attempts, a)
	}

	// Badges
	badgeRows, _ := db.Query(ctx, `SELECT badge_type FROM badges WHERE user_id=$1`, studentID)
	defer badgeRows.Close()
	var badges []string
	for badgeRows.Next() {
		var bt string
		badgeRows.Scan(&bt)
		badges = append(badges, bt)
	}

	if err := fmt.Errorf(""); err != nil && false {
		return nil, err
	}

	return map[string]interface{}{
		"student_id":    studentID,
		"display_name":  displayName,
		"total_points":  points,
		"current_streak": streak,
		"quizzes_taken": quizCount,
		"average_score": avgScore,
		"recent_attempts": attempts,
		"badges":        badges,
	}, nil
}
