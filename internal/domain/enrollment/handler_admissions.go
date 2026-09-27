package enrollment

import (
	"encoding/json"
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

func (h *InstitutionHandler) AdmissionPolicy(w http.ResponseWriter, r *http.Request) {
	if middleware.GetInstitutionID(r) == "" {
		middleware.Forbidden(w)
		return
	}
	p, err := policyFor(r.Context(), h.db, middleware.GetInstitutionID(r))
	if err != nil {
		joinError(w, err)
		return
	}
	middleware.JSON(w, 200, p)
}
func (h *InstitutionHandler) SaveAdmissionPolicy(w http.ResponseWriter, r *http.Request) {
	if middleware.GetInstitutionID(r) == "" {
		middleware.Forbidden(w)
		return
	}
	var p AdmissionPolicy
	if err := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024)).Decode(&p); err != nil {
		middleware.BadRequest(w, "Invalid admission settings")
		return
	}
	if err := p.Validate(); err != nil {
		joinError(w, err)
		return
	}
	raw, _ := json.Marshal(p)
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		joinError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var locked string
	err = tx.QueryRow(r.Context(), `SELECT id FROM institutions WHERE id=$1 FOR UPDATE`, middleware.GetInstitutionID(r)).Scan(&locked)
	// The previous policy goes in the log beside the new one, so the audit
	// Details show exactly which rule changed.
	var previous []byte
	if err == nil {
		tx.QueryRow(r.Context(), `SELECT policy FROM admission_policies WHERE institution_id=$1`, locked).Scan(&previous)
		_, err = tx.Exec(r.Context(), `INSERT INTO admission_policies(institution_id,policy) VALUES($1,$2) ON CONFLICT(institution_id) DO UPDATE SET policy=EXCLUDED.policy`, locked, raw)
	}
	if err == nil {
		if previous == nil {
			previous = []byte("{}")
		}
		_, err = tx.Exec(r.Context(), `INSERT INTO audit_log(admin_id,admin_name,admin_role,action_type,target_type,target_id,reason,institution_id,old_value,new_value)
			SELECT id,display_name,role,'update_admission_policy','institution',$2,$3,$2,$4,$5 FROM users WHERE id=$1`,
			middleware.GetUserID(r), locked, p.Mode, previous, raw)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		joinError(w, err)
		return
	}
	middleware.JSON(w, 200, p)
}
func (h *InstitutionHandler) AdmissionRequests(w http.ResponseWriter, r *http.Request) {
	if middleware.GetInstitutionID(r) == "" {
		middleware.Forbidden(w)
		return
	}
	rows, err := h.svc.Requests(r.Context(), "", middleware.GetInstitutionID(r), r.URL.Query().Get("filter"), admissionOffset(r))
	if err != nil {
		joinError(w, err)
		return
	}
	middleware.JSON(w, 200, rows)
}
func (h *StudentHandler) AdmissionRequests(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.Requests(r.Context(), middleware.GetUserID(r), "", "", admissionOffset(r))
	if err != nil {
		joinError(w, err)
		return
	}
	middleware.JSON(w, 200, rows)
}
func readAdmissionAction(w http.ResponseWriter, r *http.Request) (string, string, string, bool) {
	id := chi.URLParam(r, "requestId")
	if _, err := uuid.Parse(id); err != nil {
		middleware.BadRequest(w, "Invalid request ID")
		return "", "", "", false
	}
	var req struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if err := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || len(req.Reason) > 1000 {
		middleware.BadRequest(w, "Invalid request action or reason")
		return "", "", "", false
	}
	return id, req.Action, strings.TrimSpace(req.Reason), true
}
func (h *InstitutionHandler) ReviewAdmission(w http.ResponseWriter, r *http.Request) {
	if middleware.GetInstitutionID(r) == "" {
		middleware.Forbidden(w)
		return
	}
	id, action, reason, ok := readAdmissionAction(w, r)
	if !ok {
		return
	}
	if action != "approve" && action != "decline" {
		middleware.BadRequest(w, "Choose approve or decline")
		return
	}
	err := h.svc.ActOnRequest(r.Context(), id, "", middleware.GetInstitutionID(r), middleware.GetUserID(r), action, reason)
	if err != nil {
		joinError(w, err)
		return
	}
	middleware.JSON(w, 200, map[string]bool{"ok": true})
}
func (h *StudentHandler) ActOnAdmission(w http.ResponseWriter, r *http.Request) {
	id, action, _, ok := readAdmissionAction(w, r)
	if !ok {
		return
	}
	if action != "cancel" && action != "complete" {
		middleware.BadRequest(w, "Choose cancel or complete")
		return
	}
	err := h.svc.ActOnRequest(r.Context(), id, middleware.GetUserID(r), "", "", action, "")
	if err != nil {
		joinError(w, err)
		return
	}
	middleware.JSON(w, 200, map[string]bool{"ok": true})
}

func admissionOffset(r *http.Request) int {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 || offset > 1000000 {
		return 0
	}
	return offset
}

// GET /institution/admissions/requests/counts
func (h *InstitutionHandler) AdmissionRequestCounts(w http.ResponseWriter, r *http.Request) {
	open, history, err := h.svc.RequestCounts(r.Context(), middleware.GetInstitutionID(r))
	if err != nil {
		joinError(w, err)
		return
	}
	middleware.JSON(w, 200, map[string]int{"open": open, "history": history})
}

// POST /institution/admissions/policy/test {email}
// A dry run: how the current policy would treat a join from this address.
func (h *InstitutionHandler) TestAdmissionPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&req); err != nil {
		middleware.BadRequest(w, "email is required")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
		middleware.BadRequest(w, "enter a valid email address")
		return
	}
	v, err := h.svc.TestPolicy(r.Context(), middleware.GetInstitutionID(r), email)
	if err != nil {
		joinError(w, err)
		return
	}
	middleware.JSON(w, 200, v)
}
