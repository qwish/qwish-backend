// Package studygroup powers the social layer: private study groups (invite-only
// leagues with their own leaderboard) and batchmate follows.
package studygroup

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound  = errors.New("study group not found")
	ErrForbidden = errors.New("not permitted")
	ErrSelf      = errors.New("cannot follow yourself")
)

type Service struct {
	db *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

type Group struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description,omitempty"`
	OwnerID     string    `json:"owner_id"`
	InviteCode  string    `json:"invite_code"`
	MemberCount int       `json:"member_count"`
	Role        string    `json:"role,omitempty"` // requester's role in the group
	CreatedAt   time.Time `json:"created_at"`
}

type Member struct {
	Rank          int       `json:"rank"`
	UserID        string    `json:"user_id"`
	DisplayName   string    `json:"display_name"`
	Role          string    `json:"role"`
	TotalPoints   int64     `json:"total_points"`
	CurrentStreak int       `json:"current_streak"`
	JoinedAt      time.Time `json:"joined_at"`
}

// ── Groups ────────────────────────────────────────────────────────────────────

func (s *Service) Create(ctx context.Context, ownerID, name string, desc *string) (*Group, error) {
	code := generateCode(8)
	g := &Group{}
	err := s.db.QueryRow(ctx,
		`INSERT INTO study_groups (name, description, owner_id, invite_code)
		 VALUES ($1, NULLIF($2,''), $3, $4)
		 RETURNING id, name, description, owner_id, invite_code, created_at`,
		name, deref(desc), ownerID, code,
	).Scan(&g.ID, &g.Name, &g.Description, &g.OwnerID, &g.InviteCode, &g.CreatedAt)
	if err != nil {
		return nil, err
	}
	// Owner is the first member.
	if _, err := s.db.Exec(ctx,
		`INSERT INTO study_group_members (group_id, user_id, role) VALUES ($1,$2,'owner')`,
		g.ID, ownerID); err != nil {
		return nil, err
	}
	g.MemberCount = 1
	g.Role = "owner"
	return g, nil
}

// ListMine returns the groups the user belongs to.
func (s *Service) ListMine(ctx context.Context, userID string) ([]Group, error) {
	rows, err := s.db.Query(ctx,
		`SELECT g.id, g.name, g.description, g.owner_id, g.invite_code, g.created_at, m.role,
		        (SELECT COUNT(*) FROM study_group_members WHERE group_id=g.id)
		 FROM study_groups g
		 JOIN study_group_members m ON m.group_id = g.id AND m.user_id = $1
		 WHERE g.archived_at IS NULL
		 ORDER BY g.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Group
	for rows.Next() {
		var g Group
		rows.Scan(&g.ID, &g.Name, &g.Description, &g.OwnerID, &g.InviteCode, &g.CreatedAt, &g.Role, &g.MemberCount)
		list = append(list, g)
	}
	if list == nil {
		list = []Group{}
	}
	return list, nil
}

// Get returns a group's details; the user must be a member.
func (s *Service) Get(ctx context.Context, userID, groupID string) (*Group, error) {
	g := &Group{}
	err := s.db.QueryRow(ctx,
		`SELECT g.id, g.name, g.description, g.owner_id, g.invite_code, g.created_at, m.role,
		        (SELECT COUNT(*) FROM study_group_members WHERE group_id=g.id)
		 FROM study_groups g
		 JOIN study_group_members m ON m.group_id = g.id AND m.user_id = $1
		 WHERE g.id = $2 AND g.archived_at IS NULL`, userID, groupID,
	).Scan(&g.ID, &g.Name, &g.Description, &g.OwnerID, &g.InviteCode, &g.CreatedAt, &g.Role, &g.MemberCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return g, nil
}

// JoinByCode adds the user to the group matching the invite code.
func (s *Service) JoinByCode(ctx context.Context, userID, code string) (*Group, error) {
	var groupID string
	err := s.db.QueryRow(ctx,
		`SELECT id FROM study_groups WHERE invite_code=$1 AND archived_at IS NULL`, code,
	).Scan(&groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(ctx,
		`INSERT INTO study_group_members (group_id, user_id, role) VALUES ($1,$2,'member')
		 ON CONFLICT DO NOTHING`, groupID, userID); err != nil {
		return nil, err
	}
	return s.Get(ctx, userID, groupID)
}

// Leave removes the user from the group. The owner cannot leave; they must
// archive the group instead.
func (s *Service) Leave(ctx context.Context, userID, groupID string) error {
	var ownerID string
	err := s.db.QueryRow(ctx, `SELECT owner_id FROM study_groups WHERE id=$1`, groupID).Scan(&ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if ownerID == userID {
		return ErrForbidden
	}
	_, err = s.db.Exec(ctx, `DELETE FROM study_group_members WHERE group_id=$1 AND user_id=$2`, groupID, userID)
	return err
}

// Archive soft-deletes a group; only the owner may do it.
func (s *Service) Archive(ctx context.Context, userID, groupID string) error {
	ct, err := s.db.Exec(ctx,
		`UPDATE study_groups SET archived_at=now() WHERE id=$1 AND owner_id=$2 AND archived_at IS NULL`,
		groupID, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrForbidden
	}
	return nil
}

// Leaderboard returns the group's members ranked by total points (private league).
func (s *Service) Leaderboard(ctx context.Context, userID, groupID string) ([]Member, error) {
	// Membership check.
	var exists bool
	if err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM study_group_members WHERE group_id=$1 AND user_id=$2)`,
		groupID, userID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrForbidden
	}
	rows, err := s.db.Query(ctx,
		`SELECT RANK() OVER (ORDER BY u.total_points DESC),
		        u.id, u.display_name, m.role, u.total_points, u.current_streak, m.joined_at
		 FROM study_group_members m
		 JOIN users u ON u.id = m.user_id AND u.deleted_at IS NULL AND u.status='active'
		 WHERE m.group_id = $1
		 ORDER BY u.total_points DESC, u.current_streak DESC, u.id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.Rank, &m.UserID, &m.DisplayName, &m.Role, &m.TotalPoints, &m.CurrentStreak, &m.JoinedAt); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if list == nil {
		list = []Member{}
	}
	return list, nil
}

// ── Follows ─────────────────────────────────────────────────────────────────

func (s *Service) Follow(ctx context.Context, followerID, followeeID string) error {
	if followerID == followeeID {
		return ErrSelf
	}
	// Followee must exist and be active.
	var exists bool
	s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND deleted_at IS NULL AND status='active')`,
		followeeID).Scan(&exists)
	if !exists {
		return ErrNotFound
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO user_follows (follower_id, followee_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		followerID, followeeID)
	return err
}

func (s *Service) Unfollow(ctx context.Context, followerID, followeeID string) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM user_follows WHERE follower_id=$1 AND followee_id=$2`, followerID, followeeID)
	return err
}

type FollowUser struct {
	UserID        string `json:"user_id"`
	DisplayName   string `json:"display_name"`
	TotalPoints   int64  `json:"total_points"`
	CurrentStreak int    `json:"current_streak"`
	IsFollowing   bool   `json:"is_following"` // does the requester follow this user
}

// Following lists who userID follows.
func (s *Service) Following(ctx context.Context, userID string) ([]FollowUser, error) {
	rows, err := s.db.Query(ctx,
		`SELECT u.id, u.display_name, u.total_points, u.current_streak
		 FROM user_follows f
		 JOIN users u ON u.id = f.followee_id AND u.deleted_at IS NULL
		 WHERE f.follower_id = $1
		 ORDER BY u.total_points DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []FollowUser
	for rows.Next() {
		var u FollowUser
		rows.Scan(&u.UserID, &u.DisplayName, &u.TotalPoints, &u.CurrentStreak)
		u.IsFollowing = true
		list = append(list, u)
	}
	if list == nil {
		list = []FollowUser{}
	}
	return list, nil
}

// Followers lists who follows userID, flagging which ones userID follows back.
func (s *Service) Followers(ctx context.Context, userID string) ([]FollowUser, error) {
	rows, err := s.db.Query(ctx,
		`SELECT u.id, u.display_name, u.total_points, u.current_streak,
		        EXISTS(SELECT 1 FROM user_follows ff WHERE ff.follower_id=$1 AND ff.followee_id=u.id)
		 FROM user_follows f
		 JOIN users u ON u.id = f.follower_id AND u.deleted_at IS NULL
		 WHERE f.followee_id = $1
		 ORDER BY u.total_points DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []FollowUser
	for rows.Next() {
		var u FollowUser
		rows.Scan(&u.UserID, &u.DisplayName, &u.TotalPoints, &u.CurrentStreak, &u.IsFollowing)
		list = append(list, u)
	}
	if list == nil {
		list = []FollowUser{}
	}
	return list, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func generateCode(n int) string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	rb := make([]byte, n)
	rand.Read(rb)
	for i := range b {
		b[i] = letters[int(rb[i])%len(letters)]
	}
	return string(b)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
