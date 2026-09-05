package curriculum

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct{ db *pgxpool.Pool }

func NewService(db *pgxpool.Pool) *Service { return &Service{db: db} }

// The handler derives both IDs from verified request context, never from JSON.
type Actor struct{ InstitutionID, ID string }

func dbError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}

// Auditing is part of the mutation's transaction. A successful save never loses
// its audit record, and a failed mutation never leaves a misleading success log.
func audit(ctx context.Context, tx pgx.Tx, actor Actor, action, target string) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_log
		(admin_id, admin_name, admin_role, action_type, target_type, target_id, institution_id)
		VALUES ($1, COALESCE((SELECT display_name FROM users WHERE id=$1), 'Institution admin'),
		'institution_admin', $2, 'curriculum', $3, $4)`, actor.ID, action, target, actor.InstitutionID)
	return err
}

func (s *Service) CreateYear(ctx context.Context, actor Actor, in YearInput) (Year, error) {
	result := Year{YearInput: in}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `INSERT INTO academic_years(institution_id,name,starts_on,ends_on)
		VALUES($1,$2,$3::text::date,$4::text::date) RETURNING id`, actor.InstitutionID, in.Name, in.StartsOn, in.EndsOn).Scan(&result.ID)
	if err != nil {
		return result, dbError(err)
	}
	if err = audit(ctx, tx, actor, "create_academic_year", result.ID); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (s *Service) ListYears(ctx context.Context, institutionID string) ([]Year, error) {
	rows, err := s.db.Query(ctx, `SELECT id,name,starts_on::text,ends_on::text FROM academic_years
		WHERE institution_id=$1 ORDER BY starts_on DESC,id`, institutionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Year{}
	for rows.Next() {
		var y Year
		if err = rows.Scan(&y.ID, &y.Name, &y.StartsOn, &y.EndsOn); err != nil {
			return nil, err
		}
		result = append(result, y)
	}
	return result, rows.Err()
}

// The curriculum header lock serializes concurrent version creation and gives
// a new version an independent identity; published concept IDs are never reused.
func (s *Service) CreateVersion(ctx context.Context, actor Actor, curriculumID, name string, in VersionInput) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if curriculumID == "" {
		err = tx.QueryRow(ctx, `INSERT INTO curricula(institution_id,name) VALUES($1,$2) RETURNING id`, actor.InstitutionID, name).Scan(&curriculumID)
	} else {
		err = tx.QueryRow(ctx, `SELECT id FROM curricula WHERE id=$1 AND institution_id=$2 FOR UPDATE`, curriculumID, actor.InstitutionID).Scan(&curriculumID)
	}
	if err != nil {
		return "", dbError(err)
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO curriculum_versions(curriculum_id,institution_id,label,subject,grade)
		VALUES($1,$2,$3,$4,$5) RETURNING id`, curriculumID, actor.InstitutionID, in.Label, in.Subject, in.Grade).Scan(&id)
	if err != nil {
		return "", dbError(err)
	}
	if err = writeChapters(ctx, tx, id, in.Chapters); err != nil {
		return "", err
	}
	if err = audit(ctx, tx, actor, "create_curriculum_version", id); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func writeChapters(ctx context.Context, tx pgx.Tx, versionID string, chapters []ChapterInput) error {
	for i, chapter := range chapters {
		var chapterID string
		if err := tx.QueryRow(ctx, `INSERT INTO curriculum_chapters(version_id,title,position)
			VALUES($1,$2,$3) RETURNING id`, versionID, chapter.Title, i+1).Scan(&chapterID); err != nil {
			return err
		}
		// Batch concept writes for each chapter; limit is enforced before opening
		// the transaction so large input cannot hold locks without a bound.
		batch := &pgx.Batch{}
		for j, c := range chapter.Concepts {
			batch.Queue(`INSERT INTO curriculum_concepts(chapter_id,code,title,learning_outcome,position)
				VALUES($1,$2,$3,$4,$5)`, chapterID, c.Code, c.Title, c.LearningOutcome, j+1)
		}
		if len(chapter.Concepts) > 0 {
			if err := tx.SendBatch(ctx, batch).Close(); err != nil {
				return err
			}
		}
	}
	return nil
}

const versionColumns = `v.id,v.curriculum_id,c.name,v.label,v.subject,v.grade,v.status,v.revision,v.published_at`

func scanSummary(row interface{ Scan(...any) error }, v *VersionSummary) error {
	return row.Scan(&v.ID, &v.CurriculumID, &v.Name, &v.Label, &v.Subject, &v.Grade, &v.Status, &v.Revision, &v.PublishedAt)
}

func (s *Service) ListVersions(ctx context.Context, institutionID string, page, limit int) ([]VersionSummary, int, error) {
	// One statement supplies the total with the same snapshot as the page.
	rows, err := s.db.Query(ctx, `SELECT `+versionColumns+`, count(*) OVER()
		FROM curriculum_versions v JOIN curricula c ON c.id=v.curriculum_id
		WHERE v.institution_id=$1 ORDER BY v.created_at DESC,v.id LIMIT $2 OFFSET $3`, institutionID, limit, (page-1)*limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := []VersionSummary{}
	total := 0
	for rows.Next() {
		var v VersionSummary
		if err = rows.Scan(&v.ID, &v.CurriculumID, &v.Name, &v.Label, &v.Subject, &v.Grade, &v.Status, &v.Revision, &v.PublishedAt, &total); err != nil {
			return nil, 0, err
		}
		result = append(result, v)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	// Empty later pages still report the real total. Close before another query
	// to work with one-connection pools as well.
	rows.Close()
	if len(result) == 0 {
		err = s.db.QueryRow(ctx, `SELECT count(*) FROM curriculum_versions WHERE institution_id=$1`, institutionID).Scan(&total)
	}
	return result, total, err
}

func (s *Service) GetVersion(ctx context.Context, institutionID, id, teacherID string) (Version, error) {
	// A repeatable snapshot prevents mixed header/content revisions while an
	// admin replaces a draft. Teachers can read only published versions assigned
	// to an active class they teach, not the institution-wide draft catalog.
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Version{}, err
	}
	defer tx.Rollback(ctx)
	v := Version{Chapters: []Chapter{}}
	err = scanSummary(tx.QueryRow(ctx, `SELECT `+versionColumns+` FROM curriculum_versions v
		JOIN curricula c ON c.id=v.curriculum_id WHERE v.id=$1 AND v.institution_id=$2
		AND ($3::text='' OR (v.status='published' AND EXISTS (
		  SELECT 1 FROM class_curricula cc JOIN groups g ON g.id=cc.group_id
		  JOIN group_teachers gt ON gt.group_id=g.id
		  WHERE cc.version_id=v.id AND cc.ended_at IS NULL AND g.archived_at IS NULL AND gt.user_id=NULLIF($3::text,'')::uuid
		)))`, id, institutionID, teacherID), &v.VersionSummary)
	if err != nil {
		return v, dbError(err)
	}
	rows, err := tx.Query(ctx, `SELECT ch.id,ch.title,co.id,co.code,co.title,co.learning_outcome
		FROM curriculum_chapters ch LEFT JOIN curriculum_concepts co ON co.chapter_id=ch.id
		WHERE ch.version_id=$1 ORDER BY ch.position,co.position`, id)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var chapterID, title string
		var conceptID, code, conceptTitle, outcome *string
		if err = rows.Scan(&chapterID, &title, &conceptID, &code, &conceptTitle, &outcome); err != nil {
			rows.Close()
			return v, err
		}
		if len(v.Chapters) == 0 || v.Chapters[len(v.Chapters)-1].ID != chapterID {
			v.Chapters = append(v.Chapters, Chapter{ID: chapterID, Title: title, Concepts: []Concept{}})
		}
		if conceptID != nil {
			ch := &v.Chapters[len(v.Chapters)-1]
			ch.Concepts = append(ch.Concepts, Concept{ID: *conceptID, ConceptInput: ConceptInput{Code: *code, Title: *conceptTitle, LearningOutcome: *outcome}})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return v, err
	}
	return v, tx.Commit(ctx)
}

func (s *Service) UpdateVersion(ctx context.Context, actor Actor, id string, revision int, in VersionInput) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockDraft(ctx, tx, actor.InstitutionID, id, revision); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM curriculum_concepts WHERE chapter_id IN (SELECT id FROM curriculum_chapters WHERE version_id=$1)`, id)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM curriculum_chapters WHERE version_id=$1`, id); err != nil {
		return err
	}
	if err = writeChapters(ctx, tx, id, in.Chapters); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE curriculum_versions SET label=$2,subject=$3,grade=$4,revision=revision+1 WHERE id=$1`, id, in.Label, in.Subject, in.Grade)
	if err != nil {
		return dbError(err)
	}
	if err = audit(ctx, tx, actor, "update_curriculum_draft", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockDraft(ctx context.Context, tx pgx.Tx, institutionID, id string, revision int) error {
	var status string
	var current int
	err := tx.QueryRow(ctx, `SELECT status,revision FROM curriculum_versions WHERE id=$1 AND institution_id=$2 FOR UPDATE`, id, institutionID).Scan(&status, &current)
	if err != nil {
		return dbError(err)
	}
	if status == "published" {
		return ErrPublished
	}
	if current != revision {
		return ErrRevision
	}
	return nil
}

func (s *Service) PublishVersion(ctx context.Context, actor Actor, id string, revision int) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockDraft(ctx, tx, actor.InstitutionID, id, revision); err != nil {
		return err
	}
	var complete bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM curriculum_chapters WHERE version_id=$1)
		AND NOT EXISTS(SELECT 1 FROM curriculum_chapters ch WHERE version_id=$1
		AND NOT EXISTS(SELECT 1 FROM curriculum_concepts co WHERE co.chapter_id=ch.id))`, id).Scan(&complete)
	if err != nil {
		return err
	}
	if !complete {
		return ErrIncomplete
	}
	if _, err = tx.Exec(ctx, `UPDATE curriculum_versions SET status='published',published_at=now(),revision=revision+1 WHERE id=$1`, id); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "publish_curriculum_version", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) Assign(ctx context.Context, actor Actor, groupID string, in AssignmentInput) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	// Lock the group so archiving cannot race with this new assignment.
	var found string
	err = tx.QueryRow(ctx, `SELECT id FROM groups WHERE id=$1 AND institution_id=$2 AND archived_at IS NULL FOR UPDATE`, groupID, actor.InstitutionID).Scan(&found)
	if err != nil {
		return "", dbError(err)
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO class_curricula(institution_id,group_id,academic_year_id,curriculum_id,version_id)
		SELECT $1,$2,y.id,v.curriculum_id,v.id FROM academic_years y CROSS JOIN curriculum_versions v
		WHERE y.id=$3 AND y.institution_id=$1 AND v.id=$4 AND v.institution_id=$1 AND v.status='published'
		RETURNING id`, actor.InstitutionID, groupID, in.AcademicYearID, in.VersionID).Scan(&id)
	if err != nil {
		return "", dbError(err)
	}
	if err = audit(ctx, tx, actor, "assign_class_curriculum", id); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (s *Service) ListAssignments(ctx context.Context, institutionID, groupID, teacherID string) ([]Assignment, error) {
	var found string
	err := s.db.QueryRow(ctx, `SELECT g.id FROM groups g WHERE g.id=$1 AND g.institution_id=$2 AND g.archived_at IS NULL
		AND ($3::text='' OR EXISTS(SELECT 1 FROM group_teachers gt WHERE gt.group_id=g.id AND gt.user_id=NULLIF($3::text,'')::uuid))`, groupID, institutionID, teacherID).Scan(&found)
	if err != nil {
		return nil, dbError(err)
	}
	rows, err := s.db.Query(ctx, `SELECT cc.id,cc.group_id,y.id,y.name,`+versionColumns+`
		FROM class_curricula cc JOIN academic_years y ON y.id=cc.academic_year_id
		JOIN curriculum_versions v ON v.id=cc.version_id JOIN curricula c ON c.id=v.curriculum_id
		JOIN groups g ON g.id=cc.group_id
		WHERE cc.group_id=$1 AND cc.institution_id=$2 AND cc.ended_at IS NULL AND g.archived_at IS NULL
		AND ($3::text='' OR EXISTS(SELECT 1 FROM group_teachers gt WHERE gt.group_id=g.id AND gt.user_id=NULLIF($3::text,'')::uuid))
		ORDER BY y.starts_on DESC,c.name,v.id`, groupID, institutionID, teacherID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Assignment{}
	for rows.Next() {
		var a Assignment
		v := &a.Version
		if err = rows.Scan(&a.ID, &a.GroupID, &a.AcademicYearID, &a.AcademicYearName, &v.ID, &v.CurriculumID, &v.Name, &v.Label, &v.Subject, &v.Grade, &v.Status, &v.Revision, &v.PublishedAt); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func (s *Service) EndAssignment(ctx context.Context, actor Actor, groupID, id string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var found string
	err = tx.QueryRow(ctx, `UPDATE class_curricula SET ended_at=now() WHERE id=$1 AND group_id=$2
		AND institution_id=$3 AND ended_at IS NULL RETURNING id`, id, groupID, actor.InstitutionID).Scan(&found)
	if err != nil {
		return dbError(err)
	}
	if err = audit(ctx, tx, actor, "end_class_curriculum", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
