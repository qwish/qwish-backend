package enrollment

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrAdmissionPending = errors.New("admission pending")
var ErrPendingElsewhere = errors.New("another institute has an open admission request")
var ErrRequestClosed = errors.New("admission request is no longer open")
var ErrAdmissionRules = errors.New("invalid admission rules")

type AdmissionPolicy struct {
	Mode         string   `json:"mode"`
	Match        string   `json:"match"`
	EmailDomains []string `json:"email_domains"`
	Emails       []string `json:"emails"`
	RosterMatch  bool     `json:"roster_match"`
}

func (p *AdmissionPolicy) Validate() error {
	if p.Mode != "allow_all" && p.Mode != "verify_first" && p.Mode != "custom" {
		return ErrAdmissionRules
	}
	if p.Match != "any" && p.Match != "all" {
		return ErrAdmissionRules
	}
	if len(p.EmailDomains) > 100 || len(p.Emails) > 1000 {
		return ErrAdmissionRules
	}
	for i, v := range p.EmailDomains {
		v = strings.ToLower(strings.TrimSpace(v))
		a, err := mail.ParseAddress("student@" + v)
		if err != nil || a.Address != "student@"+v || !strings.Contains(v, ".") || strings.ContainsAny(v, " @/:") {
			return ErrAdmissionRules
		}
		p.EmailDomains[i] = v
	}
	for i, v := range p.Emails {
		v = strings.ToLower(strings.TrimSpace(v))
		a, err := mail.ParseAddress(v)
		if err != nil || a.Address != v {
			return ErrAdmissionRules
		}
		p.Emails[i] = v
	}
	if p.Mode == "custom" && len(p.EmailDomains) == 0 && len(p.Emails) == 0 && !p.RosterMatch {
		return ErrAdmissionRules
	}
	if p.EmailDomains == nil {
		p.EmailDomains = []string{}
	}
	if p.Emails == nil {
		p.Emails = []string{}
	}
	return nil
}

func policyFor(ctx context.Context, q joinQuerier, inst string) (AdmissionPolicy, error) {
	var raw []byte
	var p AdmissionPolicy
	err := q.QueryRow(ctx, `SELECT COALESCE((SELECT policy FROM admission_policies WHERE institution_id=$1),'{"mode":"allow_all","match":"any","email_domains":[],"emails":[],"roster_match":false}'::jsonb)`, inst).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &p)
	}
	return p, err
}

// users.email is populated from the verified authentication response, never
// from the joining form. Roster matches use that same authenticated address.
func needsReview(ctx context.Context, q joinQuerier, user, inst string) (bool, error) {
	p, err := policyFor(ctx, q, inst)
	if err != nil {
		return false, err
	}
	if p.Mode == "allow_all" {
		return false, nil
	}
	if p.Mode != "custom" {
		return true, nil
	}
	var email string
	if err = q.QueryRow(ctx, `SELECT lower(btrim(email)) FROM users WHERE id=$1`, user).Scan(&email); err != nil {
		return false, err
	}
	checks := []bool{}
	if len(p.EmailDomains) > 0 {
		matched := false
		_, domain, ok := strings.Cut(email, "@")
		for _, v := range p.EmailDomains {
			matched = matched || (ok && v == domain)
		}
		checks = append(checks, matched)
	}
	if len(p.Emails) > 0 {
		matched := false
		for _, v := range p.Emails {
			matched = matched || v == email
		}
		checks = append(checks, matched)
	}
	if p.RosterMatch {
		var matched bool
		err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM enrollments WHERE institution_id=$1 AND lower(btrim(email))=$2 AND status='pending_claim')`, inst, email).Scan(&matched)
		if err != nil {
			return false, err
		}
		checks = append(checks, matched)
	}
	matched := p.Match == "all" && len(checks) > 0
	for _, v := range checks {
		if p.Match == "all" {
			matched = matched && v
		} else {
			matched = matched || v
		}
	}
	return !matched, nil
}

type AdmissionTarget struct {
	Kind     string `json:"kind"`
	TargetID string `json:"target_id"`
	Name     string `json:"name"`
	Outcome  string `json:"outcome"`
	Code     string `json:"-"`
}
type AdmissionRequest struct {
	ID                  string            `json:"id"`
	UserID              string            `json:"user_id"`
	Name                string            `json:"name"`
	Email               string            `json:"email"`
	InstitutionID       string            `json:"institution_id"`
	InstitutionName     string            `json:"institution_name"`
	SourceInstitutionID *string           `json:"source_institution_id"`
	Status              string            `json:"status"`
	Reason              string            `json:"reason"`
	CreatedAt           time.Time         `json:"created_at"`
	Targets             []AdmissionTarget `json:"targets"`
}

func (s *Service) Requests(ctx context.Context, user, inst, filter string, offset int) ([]AdmissionRequest, error) {
	rows, err := s.db.Query(ctx, `SELECT r.id,r.user_id,u.full_name,u.email,r.institution_id,i.name,r.source_institution_id,r.status,r.reason,r.created_at,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('kind',t.kind,'target_id',t.target_id,'name',t.name,'outcome',t.outcome) ORDER BY t.name) FROM admission_targets t WHERE t.request_id=r.id),'[]'::jsonb)
 FROM admission_requests r JOIN users u ON u.id=r.user_id JOIN institutions i ON i.id=r.institution_id
 WHERE ($1='' OR r.user_id::text=$1) AND ($2='' OR r.institution_id::text=$2)
 AND ($3='' OR ($3='open' AND r.status IN ('pending','approved')) OR ($3='history' AND r.status NOT IN ('pending','approved')))
 ORDER BY CASE WHEN r.status IN ('pending','approved') THEN 0 ELSE 1 END,r.created_at DESC,r.id LIMIT 50 OFFSET $4`, user, inst, filter, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdmissionRequest{}
	for rows.Next() {
		var r AdmissionRequest
		var raw []byte
		if err = rows.Scan(&r.ID, &r.UserID, &r.Name, &r.Email, &r.InstitutionID, &r.InstitutionName, &r.SourceInstitutionID, &r.Status, &r.Reason, &r.CreatedAt, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &r.Targets); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func queueAdmission(ctx context.Context, tx pgx.Tx, user, code string, p JoinPreview, source *string) (string, error) {
	var id, inst, status string
	err := tx.QueryRow(ctx, `SELECT id,institution_id,status FROM admission_requests WHERE user_id=$1 AND status IN ('pending','approved') FOR UPDATE`, user).Scan(&id, &inst, &status)
	if err == nil && inst != p.InstitutionID {
		return "", ErrPendingElsewhere
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO admission_requests(user_id,institution_id,source_institution_id) VALUES($1,$2,$3) RETURNING id`, user, p.InstitutionID, source).Scan(&id)
	}
	if err != nil {
		return "", err
	}
	if p.Kind == "claim" {
		var other bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admission_targets WHERE request_id=$1 AND kind='claim' AND target_id<>$2)`, id, p.TargetID).Scan(&other); err != nil {
			return "", err
		}
		if other {
			return "", ErrEnrollmentExists
		}
	}
	// Adding a destination after approval must receive a fresh review.
	tag, err := tx.Exec(ctx, `INSERT INTO admission_targets(request_id,kind,target_id,code,name) VALUES($1,$2,$3,$4,$5) ON CONFLICT(request_id,kind,target_id) DO UPDATE SET code=EXCLUDED.code,outcome='pending' WHERE admission_targets.code<>EXCLUDED.code`, id, p.Kind, p.TargetID, code, joinTargetName(p))
	if err == nil && status == "approved" && tag.RowsAffected() > 0 {
		_, err = tx.Exec(ctx, `UPDATE admission_requests SET status='pending',reviewed_by=NULL,reviewed_at=NULL,updated_at=now() WHERE id=$1`, id)
	}
	return id, err
}
func joinTargetName(p JoinPreview) string {
	if p.ClassName != "" {
		return p.ClassName
	}
	return p.InstitutionName
}

// Review and student actions lock the user before the request, matching joins.
// This prevents concurrent approvals from granting two institute memberships.
func (s *Service) ActOnRequest(ctx context.Context, id, user, inst, actor, action, reason string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var owner string
	err = tx.QueryRow(ctx, `SELECT user_id FROM admission_requests WHERE id=$1 AND ($2='' OR user_id::text=$2) AND ($3='' OR institution_id::text=$3)`, id, user, inst).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var name, role, userStatus string
	var current *string
	err = tx.QueryRow(ctx, `SELECT full_name,role,status,institution_id::text FROM users WHERE id=$1 FOR UPDATE`, owner).Scan(&name, &role, &userStatus, &current)
	if err != nil {
		return err
	}
	var dest, status string
	var source *string
	err = tx.QueryRow(ctx, `SELECT institution_id,status,source_institution_id::text FROM admission_requests WHERE id=$1 FOR UPDATE`, id).Scan(&dest, &status, &source)
	if err != nil {
		return err
	}
	if (action == "cancel" && status == "cancelled") || (action == "decline" && status == "declined") || ((action == "approve" || action == "complete") && status == "joined") || (action == "approve" && status == "approved") {
		return nil
	}
	if status != "pending" && status != "approved" {
		return ErrRequestClosed
	}
	next := ""
	switch action {
	case "cancel":
		next = "cancelled"
	case "decline":
		next = "declined"
	case "approve", "complete":
		if role != "student" || userStatus != "active" {
			return ErrJoinSuspended
		}
		var liveInst, liveStatus string
		err = tx.QueryRow(ctx, `SELECT institution_id,status FROM enrollments WHERE user_id=$1 AND status IN ('active','suspended') FOR UPDATE`, owner).Scan(&liveInst, &liveStatus)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if liveStatus == "suspended" {
			return ErrJoinSuspended
		}
		if current != nil && *current != dest && (source == nil || *current != *source) {
			return ErrEnrollmentExists
		}
		if liveInst != "" && liveInst != dest && (source == nil || liveInst != *source) {
			return ErrEnrollmentExists
		}
		var locked string
		if err = tx.QueryRow(ctx, `SELECT id FROM institutions WHERE id=$1 AND status='verified' FOR SHARE`, dest).Scan(&locked); err != nil {
			return ErrJoinChanged
		}
		if action == "complete" && status != "approved" {
			return ErrRequestClosed
		}
		if action == "approve" && source != nil {
			next = "approved"
			break
		}
		rows, err := tx.Query(ctx, `SELECT kind,target_id,code,name,outcome FROM admission_targets WHERE request_id=$1 ORDER BY kind`, id)
		if err != nil {
			return err
		}
		targets := []AdmissionTarget{}
		for rows.Next() {
			var t AdmissionTarget
			if err = rows.Scan(&t.Kind, &t.TargetID, &t.Code, &t.Name, &t.Outcome); err != nil {
				rows.Close()
				return err
			}
			targets = append(targets, t)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		valid := []AdmissionTarget{}
		for _, t := range targets {
			var locked string
			switch t.Kind {
			case "class":
				err = tx.QueryRow(ctx, `SELECT id FROM groups WHERE id=$1 AND institution_id=$2 AND archived_at IS NULL AND invite_code=$3 FOR SHARE`, t.TargetID, dest, t.Code).Scan(&locked)
			case "claim":
				err = tx.QueryRow(ctx, `SELECT id FROM enrollments WHERE id=$1 AND institution_id=$2 AND claim_code=$3 AND status='pending_claim' FOR UPDATE`, t.TargetID, dest, t.Code).Scan(&locked)
			case "institution":
				err = tx.QueryRow(ctx, `SELECT id FROM institutions WHERE id=$1 AND student_referral_code=$2`, dest, t.Code).Scan(&locked)
			}
			if errors.Is(err, pgx.ErrNoRows) {
				_, err = tx.Exec(ctx, `UPDATE admission_targets SET outcome='unavailable' WHERE request_id=$1 AND kind=$2 AND target_id=$3`, id, t.Kind, t.TargetID)
				if err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			valid = append(valid, t)
		}
		if len(valid) == 0 {
			return ErrJoinChanged
		}
		if source != nil && *source != dest {
			if _, err = tx.Exec(ctx, `UPDATE enrollments SET status='transferred',ended_at=now(),updated_at=now() WHERE user_id=$1 AND institution_id=$2 AND status='active'`, owner, *source); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `DELETE FROM group_students gs USING groups g WHERE gs.group_id=g.id AND gs.user_id=$1 AND g.institution_id=$2`, owner, *source); err != nil {
				return err
			}
		}
		// Prefer a claimed roster record; otherwise use a unique verified email match.
		p := JoinPreview{InstitutionID: dest}
		for _, t := range valid {
			if t.Kind == "claim" {
				p.Kind = t.Kind
				p.TargetID = t.TargetID
				break
			}
		}
		if _, err = activateEnrollment(ctx, tx, owner, name, p); err != nil {
			return err
		}
		for _, t := range valid {
			if t.Kind == "class" {
				if _, err = tx.Exec(ctx, `INSERT INTO group_students(group_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, t.TargetID, owner); err != nil {
					return err
				}
			}
			if _, err = tx.Exec(ctx, `UPDATE admission_targets SET outcome='joined' WHERE request_id=$1 AND kind=$2 AND target_id=$3`, id, t.Kind, t.TargetID); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE users SET institution_id=$1,updated_at=now() WHERE id=$2`, dest, owner); err != nil {
			return err
		}
		next = "joined"
	default:
		return ErrRequestClosed
	}
	_, err = tx.Exec(ctx, `UPDATE admission_requests SET status=$2,reason=CASE WHEN $3='' THEN reason ELSE $3 END,reviewed_by=COALESCE(NULLIF($4,'')::uuid,reviewed_by),reviewed_at=CASE WHEN $4='' THEN reviewed_at ELSE now() END,updated_at=now() WHERE id=$1`, id, next, reason, actor)
	if err != nil {
		return err
	}
	if actor != "" {
		if err = admissionAudit(ctx, tx, actor, dest, "admission_"+action, id, reason); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func admissionAudit(ctx context.Context, tx pgx.Tx, actor, inst, action, target, reason string) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_log(admin_id,admin_name,admin_role,action_type,target_type,target_id,reason,institution_id) SELECT id,display_name,role,$2,'admission',$3,$4,$5 FROM users WHERE id=$1`, actor, action, target, reason, inst)
	return err
}

func activateEnrollment(ctx context.Context, tx pgx.Tx, user, name string, p JoinPreview) (Enrollment, error) {
	e, err := scanEnrollment(tx.QueryRow(ctx, `SELECT `+selectCols+` FROM enrollments WHERE user_id=$1 AND status IN ('active','suspended') FOR UPDATE`, user))
	if err == nil {
		if e.InstitutionID != p.InstitutionID {
			return e, ErrEnrollmentExists
		}
		if e.Status != "active" {
			return e, ErrJoinSuspended
		}
		return e, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return e, err
	}
	rosterID := ""
	if p.Kind == "claim" {
		rosterID = p.TargetID
	} else {
		// Ambiguous roster matches stay for manual reconciliation.
		err = tx.QueryRow(ctx, `SELECT min(e.id::text) FROM enrollments e JOIN users u ON u.id=$1 WHERE e.institution_id=$2 AND e.status='pending_claim' AND lower(btrim(e.email))=lower(btrim(u.email)) HAVING count(*)=1`, user, p.InstitutionID).Scan(&rosterID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return e, err
		}
	}
	if rosterID != "" {
		e, err = scanEnrollment(tx.QueryRow(ctx, `UPDATE enrollments SET user_id=$1,status='active',joined_at=now(),updated_at=now() WHERE id=$2 AND status='pending_claim' RETURNING `+selectCols, user, rosterID))
		if errors.Is(err, pgx.ErrNoRows) {
			return e, ErrClaimCodeUsed
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE users u SET phone=COALESCE(NULLIF(u.phone,''),e.import_phone),guardian_name=COALESCE(NULLIF(u.guardian_name,''),e.import_guardian_name),guardian_phone=COALESCE(NULLIF(u.guardian_phone,''),e.import_guardian_phone),guardian_email=COALESCE(NULLIF(u.guardian_email,''),e.import_guardian_email) FROM enrollments e WHERE u.id=$1 AND e.id=$2`, user, rosterID)
		}
	} else {
		e, err = scanEnrollment(tx.QueryRow(ctx, `INSERT INTO enrollments(institution_id,user_id,full_name,email,status,joined_at) SELECT $1,id,$3,email,'active',now() FROM users WHERE id=$2 RETURNING `+selectCols, p.InstitutionID, user, name))
	}
	return e, err
}
