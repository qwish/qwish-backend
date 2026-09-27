package enrollment

import (
	"encoding/csv"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/jsonx"
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
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
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
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
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

// POST /api/v1/institution/students/import?dry_run=true   multipart: file=<csv>
//
// The dry run is the point: it returns a verdict per row so a bad file is
// caught before anything is written.
func (h *InstitutionHandler) ImportStudents(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)

	file, _, err := r.FormFile("file")
	if err != nil {
		middleware.BadRequest(w, "a CSV file field named 'file' is required")
		return
	}
	defer file.Close()

	rows, bad, err := ParseCSV(file)
	if err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}

	dryRun, _ := strconv.ParseBool(r.URL.Query().Get("dry_run"))
	if dryRun {
		verdicts, err := h.svc.PreviewImport(r.Context(), instID, rows)
		if err != nil {
			log.Printf("PreviewImport: %v", err)
			middleware.InternalError(w)
			return
		}
		middleware.JSON(w, http.StatusOK, map[string]interface{}{
			"verdicts": append(verdicts, bad...),
			"ok":       len(bad) == 0,
		})
		return
	}

	// skip_errors=true commits the valid rows and reports the rest; without it
	// any bad row rejects the whole file, as before.
	skipErrors, _ := strconv.ParseBool(r.URL.Query().Get("skip_errors"))
	if len(bad) > 0 && !skipErrors {
		middleware.JSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"error": map[string]interface{}{
				"code":     "IMPORT_VALIDATION_FAILED",
				"message":  "some rows could not be imported",
				"verdicts": bad,
			},
		})
		return
	}

	created, rejected, err := h.svc.CommitImport(r.Context(), instID, rows, skipErrors)
	if errors.Is(err, ErrImportRowsRejected) {
		middleware.JSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"error": map[string]interface{}{
				"code":     "IMPORT_VALIDATION_FAILED",
				"message":  "some rows could not be imported",
				"verdicts": append(bad, rejected...),
			},
		})
		return
	}
	if err != nil {
		log.Printf("CommitImport: %v", err)
		middleware.InternalError(w)
		return
	}
	skipped := append(bad, rejected...)

	// format=json returns codes and skipped rows together; the default stays
	// the printable CSV of claim codes.
	if r.URL.Query().Get("format") == "json" {
		type code struct {
			FullName   string `json:"full_name"`
			RollNumber string `json:"roll_number"`
			ClaimCode  string `json:"claim_code"`
		}
		codes := []code{}
		for _, e := range created {
			c := code{FullName: e.FullName}
			if e.RollNumber != nil {
				c.RollNumber = *e.RollNumber
			}
			if e.ClaimCode != nil {
				c.ClaimCode = *e.ClaimCode
			}
			codes = append(codes, c)
		}
		middleware.JSON(w, http.StatusOK, map[string]interface{}{
			"created": len(created), "claim_codes": codes, "skipped": skipped,
			"updated": len(rows) - len(created) - len(rejected),
		})
		return
	}
	w.Header().Set("X-Import-Skipped", strconv.Itoa(len(skipped)))

	// Claim codes come back as a CSV so the school can print and distribute them.
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="claim-codes.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"full_name", "roll_number", "claim_code"})
	for _, e := range created {
		roll, code := "", ""
		if e.RollNumber != nil {
			roll = *e.RollNumber
		}
		if e.ClaimCode != nil {
			code = *e.ClaimCode
		}
		cw.Write([]string{e.FullName, roll, code})
	}
	cw.Flush()
}

// PATCH /api/v1/institution/enrollments/{enrollmentId}/status  {status, reason}
func (h *InstitutionHandler) SetStudentStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status string `json:"status"` // active | suspended | graduated | transferred
		Reason string `json:"reason"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}

	enrollmentID := chi.URLParam(r, "enrollmentId")
	err := h.svc.SetStatus(r.Context(), middleware.GetInstitutionID(r), enrollmentID, req.Status)
	switch {
	case errors.Is(err, ErrNotFound):
		middleware.NotFound(w, "student")
		return
	case err != nil:
		log.Printf("SetStudentStatus: %v", err)
		middleware.BadRequest(w, err.Error())
		return
	}
	reason := "status → " + req.Status
	if req.Reason != "" {
		reason += ": " + req.Reason
	}
	h.logAudit(r, middleware.GetUserID(r), middleware.GetInstitutionID(r), "set_enrollment_status", "enrollment", enrollmentID, reason)
	middleware.JSON(w, http.StatusOK, map[string]string{"status": req.Status})
}

// POST /api/v1/institution/enrollments/promote
//
//	{from_grade, from_section, to_grade, to_section}
func (h *InstitutionHandler) PromoteStudents(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FromGrade   string `json:"from_grade"`
		FromSection string `json:"from_section"`
		ToGrade     string `json:"to_grade"`
		ToSection   string `json:"to_section"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}

	n, err := h.svc.Promote(r.Context(), middleware.GetInstitutionID(r), PromoteFilter{
		FromGrade: req.FromGrade, FromSection: req.FromSection,
		ToGrade: req.ToGrade, ToSection: req.ToSection,
	})
	if err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]int64{"promoted": n})
}

// POST /api/v1/institution/enrollments/bulk-status {enrollment_ids, status, reason}
// Applies one lifecycle change to many enrollments. Each is independent: one
// that can't change is reported and skipped, the rest still apply.
func (h *InstitutionHandler) BulkSetStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EnrollmentIDs []string `json:"enrollment_ids"`
		Status        string   `json:"status"`
		Reason        string   `json:"reason"`
	}
	if err := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 512*1024)).Decode(&req); err != nil || len(req.EnrollmentIDs) == 0 {
		middleware.BadRequest(w, "enrollment_ids and status are required")
		return
	}
	if len(req.EnrollmentIDs) > 5000 {
		middleware.BadRequest(w, "at most 5000 enrollments per request")
		return
	}
	instID, adminID := middleware.GetInstitutionID(r), middleware.GetUserID(r)
	type skip struct {
		EnrollmentID string `json:"enrollment_id"`
		Name         string `json:"name"`
		Reason       string `json:"reason"`
	}
	updated := 0
	skipped := []skip{}
	for _, id := range req.EnrollmentIDs {
		if err := h.svc.SetStatus(r.Context(), instID, id, req.Status); err != nil {
			var name string
			h.db.QueryRow(r.Context(), `SELECT COALESCE(NULLIF(u.display_name,''), e.full_name) FROM enrollments e
				LEFT JOIN users u ON u.id=e.user_id WHERE e.id=$1 AND e.institution_id=$2`, id, instID).Scan(&name)
			reason := err.Error()
			if errors.Is(err, ErrNotFound) {
				reason = "not on your roster"
			}
			skipped = append(skipped, skip{EnrollmentID: id, Name: name, Reason: reason})
			continue
		}
		updated++
	}
	note := fmt.Sprintf("bulk: %d → %s, %d skipped", updated, req.Status, len(skipped))
	if req.Reason != "" {
		note += ": " + req.Reason
	}
	h.logAudit(r, adminID, instID, "set_enrollment_status", "enrollment", "", note)
	middleware.JSON(w, http.StatusOK, map[string]interface{}{"updated": updated, "skipped": skipped})
}
