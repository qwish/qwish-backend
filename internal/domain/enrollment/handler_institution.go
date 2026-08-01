package enrollment

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

type InstitutionHandler struct {
	svc *Service
	db  *pgxpool.Pool
}

func NewInstitutionHandler(svc *Service, db *pgxpool.Pool) *InstitutionHandler {
	return &InstitutionHandler{svc: svc, db: db}
}

type rosterRequest struct {
	FullName      string `json:"full_name"`
	Email         string `json:"email"`
	RollNumber    string `json:"roll_number"`
	Grade         string `json:"grade"`
	Section       string `json:"section"`
	AdmissionDate string `json:"admission_date"`
	Phone         string `json:"phone"`
	GuardianName  string `json:"guardian_name"`
	GuardianPhone string `json:"guardian_phone"`
	GuardianEmail string `json:"guardian_email"`
}

func (r rosterRequest) toInput() RosterInput {
	return RosterInput{
		FullName: r.FullName, Email: r.Email, RollNumber: r.RollNumber,
		Grade: r.Grade, Section: r.Section, AdmissionDate: r.AdmissionDate,
		Phone: r.Phone, GuardianName: r.GuardianName,
		GuardianPhone: r.GuardianPhone, GuardianEmail: r.GuardianEmail,
	}
}

// POST /api/v1/institution/students
func (h *InstitutionHandler) CreateStudent(w http.ResponseWriter, r *http.Request) {
	var req rosterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if req.FullName == "" {
		middleware.BadRequest(w, "full_name is required")
		return
	}

	e, err := h.svc.CreateRosterEntry(r.Context(), middleware.GetInstitutionID(r), req.toInput())
	if errors.Is(err, ErrRollNumberTaken) {
		middleware.Error(w, http.StatusConflict, "ROLL_NUMBER_TAKEN",
			"another live enrollment already uses this roll number")
		return
	}
	if err != nil {
		log.Printf("CreateStudent: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, e)
}

// PATCH /api/v1/institution/enrollments/{enrollmentId}
func (h *InstitutionHandler) UpdateStudent(w http.ResponseWriter, r *http.Request) {
	var req rosterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if req.FullName == "" {
		middleware.BadRequest(w, "full_name is required")
		return
	}

	err := h.svc.UpdateRosterEntry(r.Context(), middleware.GetInstitutionID(r),
		chi.URLParam(r, "enrollmentId"), req.toInput())
	switch {
	case errors.Is(err, ErrRollNumberTaken):
		middleware.Error(w, http.StatusConflict, "ROLL_NUMBER_TAKEN",
			"another live enrollment already uses this roll number")
		return
	case errors.Is(err, ErrNotFound):
		middleware.NotFound(w, "student")
		return
	case err != nil:
		log.Printf("UpdateStudent: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
