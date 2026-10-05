// Package notice sends teacher and leadership notifications to classes,
// departments or the whole institute. Audience rules follow leadership grants,
// the same as forms and polls.
package notice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/leadership"
)

var (
	ErrInvalid   = errors.New("invalid notice")
	ErrForbidden = errors.New("audience not allowed")
)

var categories = map[string]bool{"event": true, "test": true, "general": true}

type Actor struct {
	UserID, InstitutionID string
	Admin                 bool
}

type Draft struct {
	Title           string   `json:"title"`
	Body            string   `json:"body"`
	Category        string   `json:"category"`
	GroupIDs        []string `json:"group_ids"`
	DepartmentIDs   []string `json:"department_ids"`
	InstitutionWide bool     `json:"institution_wide"`
}

type Notice struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Body            string    `json:"body"`
	Category        string    `json:"category"`
	CreatedByName   string    `json:"created_by_name"`
	InstitutionWide bool      `json:"institution_wide"`
	GroupIDs        []string  `json:"group_ids"`
	DepartmentIDs   []string  `json:"department_ids"`
	RecipientCount  int       `json:"recipient_count"`
	CreatedAt       time.Time `json:"created_at"`
}

type Option struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Audiences struct {
	Classes         []Option `json:"classes"`
	Departments     []Option `json:"departments"`
	InstitutionWide bool     `json:"institution_wide"`
}

type Service struct {
	db       *pgxpool.Pool
	emit     func(ctx context.Context, userID, title, body, reference string)
	inflight sync.WaitGroup // deliveries still running after Send returned
}

// wait blocks until every started delivery has finished.
func (s *Service) wait() { s.inflight.Wait() }

func NewService(pool *pgxpool.Pool, emit func(ctx context.Context, userID, title, body, reference string)) *Service {
	return &Service{db: pool, emit: emit}
}

type reach struct {
	all   bool     // whole institute
	depts []string // departments the actor leads
}

func (s *Service) reach(ctx context.Context, q pgx.Tx, a Actor) (reach, error) {
	g, err := leadership.Load(ctx, q, a.UserID, a.InstitutionID)
	if err != nil {
		return reach{}, err
	}
	all, depts := g.Scope(leadership.PermActivitiesPublish)
	if depts == nil {
		depts = []string{}
	}
	return reach{all: all || a.Admin, depts: depts}, nil
}

// allowedClassesSQL: active classes in $1 the actor may target. $2 actor,
// $3 whole-institute reach, $4 departments led.
const allowedClassesSQL = `SELECT g.id::text, g.name FROM groups g
	WHERE g.institution_id=$1 AND g.archived_at IS NULL
	  AND ($3 OR g.department_id::text = ANY($4)
	       OR EXISTS(SELECT 1 FROM group_teachers gt WHERE gt.group_id=g.id AND gt.user_id=$2))`

// allowedDepartmentsSQL: departments led, or departments of classes taught.
const allowedDepartmentsSQL = `SELECT d.id::text, d.name FROM departments d
	WHERE d.institution_id=$1 AND d.archived_at IS NULL
	  AND ($3 OR d.id::text = ANY($4)
	       OR EXISTS(SELECT 1 FROM groups g JOIN group_teachers gt ON gt.group_id=g.id
	                  WHERE g.department_id=d.id AND g.archived_at IS NULL AND gt.user_id=$2))`

func options(ctx context.Context, q pgx.Tx, sql string, a Actor, r reach) ([]Option, error) {
	rows, err := q.Query(ctx, sql+` ORDER BY 2`, a.InstitutionID, a.UserID, r.all, r.depts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Option{}
	for rows.Next() {
		var o Option
		if err := rows.Scan(&o.ID, &o.Name); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Service) Audiences(ctx context.Context, a Actor) (Audiences, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Audiences{}, err
	}
	defer tx.Rollback(ctx)
	r, err := s.reach(ctx, tx, a)
	if err != nil {
		return Audiences{}, err
	}
	classes, err := options(ctx, tx, allowedClassesSQL, a, r)
	if err != nil {
		return Audiences{}, err
	}
	depts, err := options(ctx, tx, allowedDepartmentsSQL, a, r)
	if err != nil {
		return Audiences{}, err
	}
	return Audiences{Classes: classes, Departments: depts, InstitutionWide: r.all}, nil
}

func unique(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// allIn reports whether every id is in the allowed set the query returns.
func allIn(ctx context.Context, q pgx.Tx, sql string, a Actor, r reach, ids []string) (bool, error) {
	if len(ids) == 0 {
		return true, nil
	}
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM (`+sql+`) x WHERE x.id = ANY($5)`, a.InstitutionID, a.UserID, r.all, r.depts, ids).Scan(&n)
	return n == len(ids), err
}

func (s *Service) Send(ctx context.Context, a Actor, d Draft) (Notice, error) {
	d.Title, d.Body = strings.TrimSpace(d.Title), strings.TrimSpace(d.Body)
	d.GroupIDs, d.DepartmentIDs = unique(d.GroupIDs), unique(d.DepartmentIDs)
	switch {
	case d.Title == "" || len([]rune(d.Title)) > 120:
		return Notice{}, fmt.Errorf("%w: title must be 1 to 120 characters", ErrInvalid)
	case d.Body == "" || len([]rune(d.Body)) > 2000:
		return Notice{}, fmt.Errorf("%w: message must be 1 to 2000 characters", ErrInvalid)
	case !categories[d.Category]:
		return Notice{}, fmt.Errorf("%w: category must be event, test or general", ErrInvalid)
	case !d.InstitutionWide && len(d.GroupIDs)+len(d.DepartmentIDs) == 0:
		return Notice{}, fmt.Errorf("%w: choose at least one class or department", ErrInvalid)
	case len(d.GroupIDs)+len(d.DepartmentIDs) > 50:
		return Notice{}, fmt.Errorf("%w: choose at most 50 classes and departments", ErrInvalid)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Notice{}, err
	}
	defer tx.Rollback(ctx)
	r, err := s.reach(ctx, tx, a)
	if err != nil {
		return Notice{}, err
	}
	if d.InstitutionWide && !r.all {
		return Notice{}, ErrForbidden
	}
	okClasses, err := allIn(ctx, tx, allowedClassesSQL, a, r, d.GroupIDs)
	if err != nil {
		return Notice{}, err
	}
	okDepts, err := allIn(ctx, tx, allowedDepartmentsSQL, a, r, d.DepartmentIDs)
	if err != nil {
		return Notice{}, err
	}
	if !okClasses || !okDepts {
		return Notice{}, ErrForbidden
	}

	n := Notice{Title: d.Title, Body: d.Body, Category: d.Category, InstitutionWide: d.InstitutionWide,
		GroupIDs: d.GroupIDs, DepartmentIDs: d.DepartmentIDs}
	if err = tx.QueryRow(ctx, `INSERT INTO notices (institution_id, created_by, title, body, category, institution_wide)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, created_at`, a.InstitutionID, a.UserID, d.Title, d.Body, d.Category, d.InstitutionWide).
		Scan(&n.ID, &n.CreatedAt); err != nil {
		return Notice{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO notice_audience (notice_id, group_id) SELECT $1, unnest($2::uuid[])`, n.ID, d.GroupIDs); err != nil {
		return Notice{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO notice_audience (notice_id, department_id) SELECT $1, unnest($2::uuid[])`, n.ID, d.DepartmentIDs); err != nil {
		return Notice{}, err
	}

	rows, err := tx.Query(ctx, `SELECT DISTINCT u.id::text FROM users u, notices n
		WHERE n.id=$1 AND u.role='student' AND u.deleted_at IS NULL AND COALESCE(u.status,'active')='active'
		  AND EXISTS (SELECT 1 FROM enrollments m WHERE m.user_id=u.id AND m.institution_id=n.institution_id AND m.status='active')
		  AND (n.institution_wide OR EXISTS (
		        SELECT 1 FROM notice_audience na
		        JOIN groups g ON (g.id=na.group_id OR g.department_id=na.department_id)
		                     AND g.institution_id=n.institution_id AND g.archived_at IS NULL
		        JOIN group_students gs ON gs.group_id=g.id AND gs.user_id=u.id
		        WHERE na.notice_id=n.id))`, n.ID)
	if err != nil {
		return Notice{}, err
	}
	var recipients []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return Notice{}, err
		}
		recipients = append(recipients, id)
	}
	rows.Close()
	n.RecipientCount = len(recipients)
	if _, err = tx.Exec(ctx, `UPDATE notices SET recipient_count=$2 WHERE id=$1`, n.ID, n.RecipientCount); err != nil {
		return Notice{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Notice{}, err
	}
	// The notice is committed; deliver it even after the request ends, so a
	// timeout can't leave it half-sent (and a retry can't double-send).
	// ponytail: one goroutine per notice, lost on process restart; move to a job queue if that matters.
	bg := context.WithoutCancel(ctx)
	s.inflight.Add(1)
	go func() {
		defer s.inflight.Done()
		for _, id := range recipients {
			s.emit(bg, id, n.Title, n.Body, "notice:"+n.ID)
		}
	}()
	return n, nil
}

func (s *Service) List(ctx context.Context, a Actor, limit, offset int) ([]Notice, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	r, err := s.reach(ctx, tx, a)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT n.id, n.title, n.body, n.category, COALESCE(NULLIF(u.display_name,''), u.full_name, ''),
		n.institution_wide, n.recipient_count, n.created_at,
		COALESCE((SELECT array_agg(group_id::text) FROM notice_audience WHERE notice_id=n.id AND group_id IS NOT NULL), '{}'),
		COALESCE((SELECT array_agg(department_id::text) FROM notice_audience WHERE notice_id=n.id AND department_id IS NOT NULL), '{}')
		FROM notices n JOIN users u ON u.id=n.created_by
		WHERE n.institution_id=$1 AND ($3 OR n.created_by=$2)
		ORDER BY n.created_at DESC LIMIT $4 OFFSET $5`, a.InstitutionID, a.UserID, r.all, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notice{}
	for rows.Next() {
		var n Notice
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.Category, &n.CreatedByName, &n.InstitutionWide,
			&n.RecipientCount, &n.CreatedAt, &n.GroupIDs, &n.DepartmentIDs); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ReceivedNotice is a notice as its recipient sees it. Read and
// NotificationID come from the notification that delivered it, so marking that
// notification read marks the notice read.
type ReceivedNotice struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Body            string    `json:"body"`
	Category        string    `json:"category"`
	FromName        string    `json:"from_name"`
	InstitutionName string    `json:"institution_name"`
	CreatedAt       time.Time `json:"created_at"`
	Read            bool      `json:"read"`
	NotificationID  string    `json:"notification_id"`
}

// Received lists the notices delivered to userID, newest first. Delivery is
// the record: a notice appears only for the people it was sent to, never for
// someone who joined the class afterwards.
func (s *Service) Received(ctx context.Context, userID string, limit, offset int) ([]ReceivedNotice, int, error) {
	const from = `FROM user_notifications un
		JOIN notices n ON un.reference = 'notice:' || n.id::text`
	const where = ` WHERE un.user_id=$1 AND un.kind='notice'`
	var total int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) `+from+where, userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(ctx, `SELECT n.id, n.title, n.body, n.category,
		COALESCE(NULLIF(u.display_name,''), u.full_name, ''), i.name, n.created_at, un.read_at IS NOT NULL, un.id
		`+from+`
		JOIN users u ON u.id=n.created_by JOIN institutions i ON i.id=n.institution_id`+where+`
		ORDER BY n.created_at DESC, n.id LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []ReceivedNotice{}
	for rows.Next() {
		var n ReceivedNotice
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.Category, &n.FromName, &n.InstitutionName, &n.CreatedAt, &n.Read, &n.NotificationID); err != nil {
			return nil, 0, err
		}
		out = append(out, n)
	}
	return out, total, rows.Err()
}
