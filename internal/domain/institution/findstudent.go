package institution

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qwish/backend/internal/middleware"
)

// Find a student: every record that decides whether someone belongs here, and
// why they're missing from a roster.
//
// Search only matches people connected to this institution — an enrollment in
// any state (including unclaimed roster rows). An
// email with no connection returns nothing, so the endpoint can't be used to
// learn whether an account exists elsewhere on Qwish.

// GET /institution/students/find?q=
func (h *Handler) FindStudents(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < 2 {
		middleware.BadRequest(w, "type at least 2 characters")
		return
	}
	like := "%" + q + "%"
	rows, err := h.db.Query(r.Context(), `
		WITH people AS (
			SELECT e.user_id, e.id AS enrollment_id,
			       COALESCE(NULLIF(u.display_name,''), u.full_name, e.full_name) AS name,
			       COALESCE(u.email, e.email, '') AS email, e.roll_number, e.status AS state, e.updated_at AS touched
			  FROM enrollments e LEFT JOIN users u ON u.id=e.user_id
			 WHERE e.institution_id=$1
		)
		SELECT DISTINCT ON (COALESCE(user_id::text, enrollment_id::text))
		       user_id, enrollment_id, name, email, roll_number, state
		  FROM people
		 WHERE name ILIKE $2 OR email ILIKE $2 OR roll_number ILIKE $2
		 ORDER BY COALESCE(user_id::text, enrollment_id::text), touched DESC
		 LIMIT 20`, instID, like)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type match struct {
		UserID       *string `json:"user_id"`
		EnrollmentID *string `json:"enrollment_id"`
		Name         string  `json:"name"`
		Email        string  `json:"email"`
		RollNumber   *string `json:"roll_number"`
		State        string  `json:"state"`
	}
	out := []match{}
	for rows.Next() {
		var m match
		rows.Scan(&m.UserID, &m.EnrollmentID, &m.Name, &m.Email, &m.RollNumber, &m.State)
		out = append(out, m)
	}
	middleware.JSON(w, http.StatusOK, out)
}

// chainStep is one link in "does this person belong here".
type chainStep struct {
	Key    string `json:"key"`   // account, enrollment, classes
	Label  string `json:"label"` // Account, Admission request, ...
	State  string `json:"state"` // short verdict, e.g. "Approved"
	Tone   string `json:"tone"`  // ok | wait | none | fail
	Detail string `json:"detail"`
}

type timelineEvent struct {
	At   time.Time `json:"at"`
	What string    `json:"what"`
	By   string    `json:"by"`
}

// GET /institution/students/explain?user_id= | ?enrollment_id=
func (h *Handler) ExplainStudent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	instID := middleware.GetInstitutionID(r)
	userID := r.URL.Query().Get("user_id")
	enrollmentID := r.URL.Query().Get("enrollment_id")
	for _, id := range []string{userID, enrollmentID} {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				middleware.BadRequest(w, "ids must be UUIDs")
				return
			}
		}
	}
	if userID == "" && enrollmentID == "" {
		middleware.BadRequest(w, "user_id or enrollment_id is required")
		return
	}
	if userID == "" {
		h.db.QueryRow(ctx, `SELECT COALESCE(user_id::text,'') FROM enrollments WHERE id=$1 AND institution_id=$2`,
			enrollmentID, instID).Scan(&userID)
	}

	// The enrollment here: the named one, else the person's latest.
	var enr struct {
		ID, Name, Status        string
		Email, Roll, Grade, Sec *string
		ClaimCode               *string
		Created                 time.Time
		JoinedAt, EndedAt       *time.Time
	}
	err := h.db.QueryRow(ctx, `SELECT id, full_name, status, email, roll_number, grade, section, claim_code, created_at, joined_at, ended_at
		FROM enrollments WHERE institution_id=$1 AND (id::text=$2 OR ($2='' AND user_id::text=$3))
		ORDER BY (status IN ('active','suspended','pending_claim')) DESC, updated_at DESC LIMIT 1`,
		instID, enrollmentID, userID).Scan(&enr.ID, &enr.Name, &enr.Status, &enr.Email, &enr.Roll, &enr.Grade, &enr.Sec,
		&enr.ClaimCode, &enr.Created, &enr.JoinedAt, &enr.EndedAt)
	hasEnrollment := err == nil

	if !hasEnrollment {
		// Nothing connects this person to the institution: say only that.
		middleware.NotFound(w, "record of this person at your institution")
		return
	}

	var acc struct {
		Name, Email, Role, Status string
		Since                     time.Time
	}
	hasAccount := userID != "" && h.db.QueryRow(ctx, `SELECT COALESCE(NULLIF(display_name,''), full_name), email, role, status, member_since
		FROM users WHERE id=$1 AND deleted_at IS NULL`, userID).Scan(&acc.Name, &acc.Email, &acc.Role, &acc.Status, &acc.Since) == nil

	var classes []string
	if userID != "" {
		h.db.QueryRow(ctx, `SELECT COALESCE(array_agg(g.name ORDER BY g.name),'{}') FROM group_students gs JOIN groups g ON g.id=gs.group_id
			WHERE gs.user_id=$1 AND g.institution_id=$2 AND g.archived_at IS NULL`, userID, instID).Scan(&classes)
	}

	chain := []chainStep{}
	name, email := enr.Name, ""
	if enr.Email != nil {
		email = *enr.Email
	}
	if hasAccount {
		name, email = acc.Name, acc.Email
		step := chainStep{Key: "account", Label: "Account", State: "Exists", Tone: "ok",
			Detail: "Verified email · " + acc.Role + " role"}
		if acc.Role != "student" {
			step.State, step.Tone, step.Detail = "Staff account", "fail", "A "+acc.Role+" account can't join as a student"
		} else if acc.Status == "suspended" {
			step.State, step.Tone, step.Detail = "Suspended on Qwish", "fail", "Suspended platform-wide"
		}
		chain = append(chain, step)
	} else {
		chain = append(chain, chainStep{Key: "account", Label: "Account", State: "None linked", Tone: "none",
			Detail: "The roster record hasn't been claimed yet"})
	}
	if hasEnrollment {
		tones := map[string]string{"active": "ok", "suspended": "fail", "pending_claim": "wait", "graduated": "none", "transferred": "none"}
		labels := map[string]string{"active": "Active", "suspended": "Suspended", "pending_claim": "Unclaimed roster record",
			"graduated": "Graduated", "transferred": "Transferred out"}
		detail := strings.TrimSpace(strings.Join(nonEmpty(deref(enr.Roll), gradeSection(enr.Grade, enr.Sec)), " · "))
		chain = append(chain, chainStep{Key: "enrollment", Label: "Institute enrollment", State: labels[enr.Status],
			Tone: tones[enr.Status], Detail: detail})
	} else {
		chain = append(chain, chainStep{Key: "enrollment", Label: "Institute enrollment", State: "None here yet", Tone: "none", Detail: "No enrollment here"})
	}
	classDetail := joinNames("", classes)
	if classDetail == "" {
		classDetail = "Not in any class"
	}
	classStep := chainStep{Key: "classes", Label: "Class membership", State: plural(len(classes), "class", "classes"), Tone: "ok", Detail: classDetail}
	if len(classes) == 0 {
		classStep.Tone = "none"
	}
	chain = append(chain, classStep)

	// Diagnosis: the one sentence that explains where they are, and what (if
	// anything) the admin can do.
	type diagnosis struct {
		Code     string   `json:"code"`
		Headline string   `json:"headline"`
		Detail   string   `json:"detail"`
		Actions  []string `json:"actions"`
	}
	d := diagnosis{Actions: []string{}}
	switch {
	case hasEnrollment && enr.Status == "active":
		d = diagnosis{"on_roster", "On your roster", "Active enrollment" + joinNames(" in ", classes) + ".", []string{"open_profile"}}
	case hasEnrollment && enr.Status == "suspended":
		d = diagnosis{"suspended", "Suspended", "They keep their account and history. Classes are paused until you reactivate them.", []string{"reactivate", "open_profile"}}
	case hasEnrollment && enr.Status == "pending_claim":
		d = diagnosis{"unclaimed", "Roster record not yet claimed", "The student hasn't entered their claim code in NumPie. Share the code again.", []string{"copy_claim_code"}}
	case hasEnrollment && (enr.Status == "graduated" || enr.Status == "transferred"):
		d = diagnosis{"ended", "Enrollment ended", "They " + map[string]string{"graduated": "graduated", "transferred": "transferred out"}[enr.Status] + ". Joining a class again starts a new enrollment.", []string{}}
	}

	// What happened, oldest first.
	events := []timelineEvent{}
	if hasEnrollment {
		events = append(events, timelineEvent{enr.Created, "Roster record created", "Institution"})
		if enr.JoinedAt != nil {
			events = append(events, timelineEvent{*enr.JoinedAt, "Joined the institute", name})
		}
		if enr.EndedAt != nil {
			events = append(events, timelineEvent{*enr.EndedAt, "Enrollment ended (" + enr.Status + ")", "Institution"})
		}
	}
	arows, err := h.db.Query(ctx, `SELECT al.timestamp, al.action_type, al.admin_name, COALESCE(al.reason,'')
		FROM audit_log al WHERE al.institution_id=$1 AND al.target_id::text IN ($2, $3)
		ORDER BY al.timestamp LIMIT 50`, instID, userID, enr.ID)
	if err == nil {
		for arows.Next() {
			var e timelineEvent
			var action, reason string
			arows.Scan(&e.At, &action, &e.By, &reason)
			e.What = strings.ReplaceAll(action, "_", " ")
			if reason != "" {
				e.By += " · “" + reason + "”"
			}
			events = append(events, e)
		}
		arows.Close()
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })

	resp := map[string]interface{}{
		"name": name, "email": email, "user_id": nilIfEmpty(userID), "enrollment_id": nilIfEmpty(enr.ID),
		"on_roster": hasEnrollment && (enr.Status == "active" || enr.Status == "suspended"),
		"chain":     chain, "diagnosis": d, "events": events,
	}
	if hasAccount {
		resp["account_since"] = acc.Since
	}
	if hasEnrollment && enr.Status == "pending_claim" {
		resp["claim_code"] = enr.ClaimCode
	}
	middleware.JSON(w, http.StatusOK, resp)
}

func joinNames(prefix string, names []string) string {
	if len(names) == 0 {
		return ""
	}
	return prefix + strings.Join(names, ", ")
}

func nonEmpty(vals ...string) []string {
	out := []string{}
	for _, v := range vals {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func gradeSection(g, s *string) string {
	return strings.Join(nonEmpty(prefixed("Grade ", g), prefixed("Section ", s)), " · ")
}

func prefixed(p string, v *string) string {
	if v == nil || *v == "" {
		return ""
	}
	return p + *v
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
