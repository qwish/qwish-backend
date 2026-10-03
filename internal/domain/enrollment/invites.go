package enrollment

import (
	"context"
	"errors"
	"html"
	"log"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrInviteNotFound = errors.New("invite not found")

type Invite struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Status    string    `json:"status"`
	GroupID   *string   `json:"group_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type RejectedEmail struct {
	Email  string `json:"email"`
	Reason string `json:"reason"` // invalid | not_institute_domain | already_member
}

type InviteBatch struct {
	Created  []Invite        `json:"created"`
	Rejected []RejectedEmail `json:"rejected"`
}

type MyInvite struct {
	ID              string  `json:"id"`
	InstitutionName string  `json:"institution_name"`
	ClassName       *string `json:"class_name,omitempty"`
}

// CreateInvites invites institute-domain addresses to a class. Each address is
// judged on its own; one bad line never blocks the rest.
func (s *Service) CreateInvites(ctx context.Context, instID, groupID, invitedBy string, raw []string) (InviteBatch, error) {
	out := InviteBatch{Created: []Invite{}, Rejected: []RejectedEmail{}}
	var ok bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM groups WHERE id=$1 AND institution_id=$2 AND archived_at IS NULL)`, groupID, instID).Scan(&ok); err != nil {
		return out, err
	}
	if !ok {
		return out, ErrNotFound
	}
	seen := map[string]bool{}
	for _, r := range raw {
		addr := strings.ToLower(strings.TrimSpace(r))
		if addr == "" || seen[addr] {
			continue
		}
		seen[addr] = true
		if a, err := mail.ParseAddress(addr); err != nil || a.Address != addr {
			out.Rejected = append(out.Rejected, RejectedEmail{r, "invalid"})
			continue
		}
		var onDomain, member bool
		if err := s.db.QueryRow(ctx, `SELECT
			EXISTS(SELECT 1 FROM institution_domains WHERE institution_id=$1 AND domain=split_part($2,'@',2) AND verified_at IS NOT NULL),
			EXISTS(SELECT 1 FROM group_students gs JOIN users u ON u.id=gs.user_id
			        LEFT JOIN user_emails ue ON ue.user_id=u.id AND ue.verified_at IS NOT NULL
			        WHERE gs.group_id=$3 AND (lower(u.email)=$2 OR ue.email=$2))`,
			instID, addr, groupID).Scan(&onDomain, &member); err != nil {
			return out, err
		}
		if !onDomain {
			out.Rejected = append(out.Rejected, RejectedEmail{addr, "not_institute_domain"})
			continue
		}
		if member {
			out.Rejected = append(out.Rejected, RejectedEmail{addr, "already_member"})
			continue
		}
		var inv Invite
		err := s.db.QueryRow(ctx, `INSERT INTO student_invites (institution_id, group_id, email, invited_by)
			VALUES ($1,$2,$3,NULLIF($4,'')::uuid)
			ON CONFLICT (institution_id, COALESCE(group_id,'00000000-0000-0000-0000-000000000000'::uuid), email) WHERE status='pending'
			DO UPDATE SET created_at=student_invites.created_at
			RETURNING id, email, status, group_id::text, created_at`, instID, groupID, addr, invitedBy).
			Scan(&inv.ID, &inv.Email, &inv.Status, &inv.GroupID, &inv.CreatedAt)
		if err != nil {
			return out, err
		}
		out.Created = append(out.Created, inv)
	}
	return out, nil
}

func (s *Service) ListClassInvites(ctx context.Context, instID, groupID string) ([]Invite, error) {
	rows, err := s.db.Query(ctx, `SELECT id, email, status, group_id::text, created_at FROM student_invites
		WHERE institution_id=$1 AND group_id=$2 AND status='pending' ORDER BY created_at DESC`, instID, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invite{}
	for rows.Next() {
		var i Invite
		if err := rows.Scan(&i.ID, &i.Email, &i.Status, &i.GroupID, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Service) RevokeInvite(ctx context.Context, instID, inviteID string) error {
	tag, err := s.db.Exec(ctx, `UPDATE student_invites SET status='revoked' WHERE id=$1 AND institution_id=$2 AND status='pending'`, inviteID, instID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrInviteNotFound
	}
	return err
}

// InviteGroup returns the invite's class, for the teacher scope check.
func (s *Service) InviteGroup(ctx context.Context, inviteID string) (string, error) {
	var g *string
	err := s.db.QueryRow(ctx, `SELECT group_id::text FROM student_invites WHERE id=$1`, inviteID).Scan(&g)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && g == nil) {
		return "", ErrInviteNotFound
	}
	return *g, err
}

func (s *Service) MyInvites(ctx context.Context, userID string) ([]MyInvite, error) {
	rows, err := s.db.Query(ctx, `WITH `+verifiedEmailsCTE+`
		SELECT DISTINCT si.id::text, i.name, g.name FROM student_invites si
		JOIN emails e ON e.email=si.email
		JOIN institutions i ON i.id=si.institution_id AND i.status='verified'
		LEFT JOIN groups g ON g.id=si.group_id
		WHERE si.status='pending' AND (si.group_id IS NULL OR g.archived_at IS NULL)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MyInvite{}
	for rows.Next() {
		var m MyInvite
		if err := rows.Scan(&m.ID, &m.InstitutionName, &m.ClassName); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AcceptInvite joins the invited class through the normal join path, so the
// cap, locking and invite consumption rules are the same as a code join.
func (s *Service) AcceptInvite(ctx context.Context, userID, inviteID string) (JoinResult, error) {
	var instID string
	var groupID, code *string
	err := s.db.QueryRow(ctx, `WITH `+verifiedEmailsCTE+`
		SELECT si.institution_id::text, si.group_id::text, g.invite_code FROM student_invites si
		JOIN emails e ON e.email=si.email LEFT JOIN groups g ON g.id=si.group_id
		WHERE si.id=$2 AND si.status='pending'`, userID, inviteID).Scan(&instID, &groupID, &code)
	if errors.Is(err, pgx.ErrNoRows) {
		return JoinResult{}, ErrInviteNotFound
	}
	if err != nil {
		return JoinResult{}, err
	}
	if groupID != nil {
		return s.ConfirmJoin(ctx, userID, *code, *groupID)
	}
	return s.acceptInstituteInvite(ctx, userID, inviteID, instID)
}

// acceptInstituteInvite handles migration-made invites with no class: it
// creates the enrollment; the app then prompts for a class code.
func (s *Service) acceptInstituteInvite(ctx context.Context, userID, inviteID, instID string) (JoinResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return JoinResult{}, err
	}
	defer tx.Rollback(ctx)
	var role, name string
	if err = tx.QueryRow(ctx, `SELECT u.role, i.name FROM users u, institutions i WHERE u.id=$1 AND i.id=$2 FOR UPDATE OF u`, userID, instID).Scan(&role, &name); err != nil {
		return JoinResult{}, err
	}
	if role != "student" {
		return JoinResult{}, ErrJoinRole
	}
	p := JoinPreview{Kind: "class", InstitutionID: instID, InstitutionName: name, Route: "invite"}
	e, err := ensureEnrollment(ctx, tx, userID, p)
	if err != nil {
		return JoinResult{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE student_invites SET status='accepted', accepted_by=$1, accepted_at=now() WHERE id=$2`, userID, inviteID); err != nil {
		return JoinResult{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET institution_id=$1, updated_at=now() WHERE id=$2`, instID, userID); err != nil {
		return JoinResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return JoinResult{}, err
	}
	return JoinResult{Destination: p, Enrollment: &e, Status: "joined"}, nil
}

func (s *Service) notifyInvites(ctx context.Context, invites []Invite) {
	if s.sendMail == nil {
		return
	}
	for _, inv := range invites {
		var inst, class string
		if s.db.QueryRow(ctx, `SELECT i.name, COALESCE(g.name,'') FROM student_invites si JOIN institutions i ON i.id=si.institution_id
			LEFT JOIN groups g ON g.id=si.group_id WHERE si.id=$1`, inv.ID).Scan(&inst, &class) != nil {
			continue
		}
		body := "<p>" + html.EscapeString(inst) + " invited you to join " + html.EscapeString(class) +
			" on Qwish.</p><p>Open the Qwish app, add this email under Profile → Emails, and accept the invite.</p>"
		if err := s.sendMail(ctx, inv.Email, "You're invited to "+inst+" on Qwish", body); err != nil {
			log.Printf("invite mail %s: %v", inv.ID, err)
		}
	}
}
