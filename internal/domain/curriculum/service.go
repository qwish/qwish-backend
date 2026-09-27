package curriculum

import (
	"context"
	"encoding/json"
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
	rows, err := s.db.Query(ctx, `SELECT y.id,y.name,y.starts_on::text,y.ends_on::text,
		(SELECT count(*) FROM class_curricula cc WHERE cc.academic_year_id=y.id AND cc.ended_at IS NULL),
		(SELECT count(DISTINCT cc.group_id) FROM class_curricula cc JOIN groups g ON g.id=cc.group_id
		  WHERE cc.academic_year_id=y.id AND cc.ended_at IS NULL AND g.archived_at IS NULL),
		(SELECT count(*) FROM groups g WHERE g.institution_id=y.institution_id AND g.archived_at IS NULL)
		FROM academic_years y WHERE y.institution_id=$1 ORDER BY y.starts_on DESC,y.id`, institutionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Year{}
	for rows.Next() {
		var y Year
		var st YearStats
		if err = rows.Scan(&y.ID, &y.Name, &y.StartsOn, &y.EndsOn, &st.Assignments, &st.ClassesCovered, &st.ActiveClasses); err != nil {
			return nil, err
		}
		y.Stats = &st
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
	settings, _ := json.Marshal(in.Settings)
	err = tx.QueryRow(ctx, `INSERT INTO curriculum_versions(curriculum_id,institution_id,label,subject,grade,settings,copied_from_version_id)
		VALUES($1,$2,$3,$4,$5,$6,(SELECT id FROM curriculum_versions WHERE id=NULLIF($7,'')::uuid AND institution_id=$2))
		RETURNING id`, curriculumID, actor.InstitutionID, in.Label, in.Subject, in.Grade, settings, in.CopiedFromVersionID).Scan(&id)
	if err != nil {
		return "", dbError(err)
	}
	if err = writeChapters(ctx, tx, id, in.Chapters); err != nil {
		return "", err
	}
	if err = audit(ctx, tx, actor, "create_curriculum_version", id); err != nil {
		return "", err
	}
	if err = recordRevision(ctx, tx, actor, id, 1, "created", in.ChangeNote, in); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func writeChapters(ctx context.Context, tx pgx.Tx, versionID string, chapters []ChapterInput) error {
	for i, chapter := range chapters {
		var chapterID string
		chDetails, _ := json.Marshal(chapter.ChapterDetails)
		if err := tx.QueryRow(ctx, `INSERT INTO curriculum_chapters(version_id,title,position,details)
			VALUES($1,$2,$3,$4) RETURNING id`, versionID, chapter.Title, i+1, chDetails).Scan(&chapterID); err != nil {
			return err
		}
		// Batch concept writes for each chapter; limit is enforced before opening
		// the transaction so large input cannot hold locks without a bound.
		batch := &pgx.Batch{}
		for j, c := range chapter.Concepts {
			coDetails, _ := json.Marshal(c.ConceptDetails)
			batch.Queue(`INSERT INTO curriculum_concepts(chapter_id,code,title,learning_outcome,position,details)
				VALUES($1,$2,$3,$4,$5,$6)`, chapterID, c.Code, c.Title, c.LearningOutcome, j+1, coDetails)
		}
		if len(chapter.Concepts) > 0 {
			if err := tx.SendBatch(ctx, batch).Close(); err != nil {
				return err
			}
		}
	}
	return nil
}

const versionColumns = `v.id,v.curriculum_id,c.name,v.label,v.subject,v.grade,v.status,v.revision,v.published_at,v.updated_at`

func scanSummary(row interface{ Scan(...any) error }, v *VersionSummary) error {
	return row.Scan(&v.ID, &v.CurriculumID, &v.Name, &v.Label, &v.Subject, &v.Grade, &v.Status, &v.Revision, &v.PublishedAt, &v.UpdatedAt)
}

// recordRevision stores a revision's content and note for history/compare.
func recordRevision(ctx context.Context, tx pgx.Tx, actor Actor, versionID string, revision int, action, note string, in VersionInput) error {
	snap, err := json.Marshal(in)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO curriculum_version_revisions(version_id,revision,action,note,actor_id,snapshot)
		VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,$6)
		ON CONFLICT (version_id,revision) DO UPDATE SET action=EXCLUDED.action,note=EXCLUDED.note`,
		versionID, revision, action, note, actor.ID, snap)
	return err
}

// ListFilter narrows the curriculum list. Empty fields don't filter.
type ListFilter struct{ Subject, Grade, Status string }

func (s *Service) ListVersions(ctx context.Context, institutionID string, page, limit int, f ListFilter) ([]VersionSummary, int, error) {
	// One statement supplies the total with the same snapshot as the page.
	rows, err := s.db.Query(ctx, `SELECT `+versionColumns+`,
		(SELECT count(*) FROM curriculum_chapters ch WHERE ch.version_id=v.id),
		(SELECT count(*) FROM curriculum_concepts co JOIN curriculum_chapters ch ON ch.id=co.chapter_id WHERE ch.version_id=v.id),
		COALESCE((SELECT json_agg(json_build_object('group_id',g.id,'name',g.name,'academic_year_name',y.name) ORDER BY g.name)
		   FROM class_curricula cc JOIN groups g ON g.id=cc.group_id JOIN academic_years y ON y.id=cc.academic_year_id
		  WHERE cc.version_id=v.id AND cc.ended_at IS NULL AND g.archived_at IS NULL
		    AND CURRENT_DATE BETWEEN y.starts_on AND y.ends_on), '[]'::json),
		count(*) OVER()
		FROM curriculum_versions v JOIN curricula c ON c.id=v.curriculum_id
		WHERE v.institution_id=$1
		  AND ($4='' OR v.subject ILIKE $4) AND ($5='' OR v.grade=$5) AND ($6='' OR v.status=$6)
		ORDER BY v.created_at DESC,v.id LIMIT $2 OFFSET $3`,
		institutionID, limit, (page-1)*limit, f.Subject, f.Grade, f.Status)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := []VersionSummary{}
	total := 0
	for rows.Next() {
		var v VersionSummary
		var chapters, concepts int
		if err = rows.Scan(&v.ID, &v.CurriculumID, &v.Name, &v.Label, &v.Subject, &v.Grade, &v.Status, &v.Revision,
			&v.PublishedAt, &v.UpdatedAt, &chapters, &concepts, &v.AssignedClasses, &total); err != nil {
			return nil, 0, err
		}
		v.ChapterCount, v.ConceptCount = &chapters, &concepts
		result = append(result, v)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	// Empty later pages still report the real total. Close before another query
	// to work with one-connection pools as well.
	rows.Close()
	if len(result) == 0 {
		err = s.db.QueryRow(ctx, `SELECT count(*) FROM curriculum_versions v WHERE v.institution_id=$1
			AND ($2='' OR v.subject ILIKE $2) AND ($3='' OR v.grade=$3) AND ($4='' OR v.status=$4)`,
			institutionID, f.Subject, f.Grade, f.Status).Scan(&total)
	}
	return result, total, err
}

// Facets returns the distinct subjects and grades an institution uses, for
// list filters that cover every page.
func (s *Service) Facets(ctx context.Context, institutionID string) (subjects, grades []string, err error) {
	err = s.db.QueryRow(ctx, `SELECT COALESCE(array_agg(DISTINCT subject ORDER BY subject),'{}'),
		COALESCE(array_agg(DISTINCT grade ORDER BY grade),'{}') FROM curriculum_versions WHERE institution_id=$1`,
		institutionID).Scan(&subjects, &grades)
	return
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
	err = tx.QueryRow(ctx, `SELECT `+versionColumns+`, v.settings, v.copied_from_version_id, src.label
		FROM curriculum_versions v LEFT JOIN curriculum_versions src ON src.id=v.copied_from_version_id
		JOIN curricula c ON c.id=v.curriculum_id WHERE v.id=$1 AND v.institution_id=$2
		AND ($3::text='' OR (v.status='published' AND EXISTS (
		  SELECT 1 FROM class_curricula cc JOIN groups g ON g.id=cc.group_id
		  JOIN group_teachers gt ON gt.group_id=g.id
		  WHERE cc.version_id=v.id AND cc.ended_at IS NULL AND g.archived_at IS NULL AND gt.user_id=NULLIF($3::text,'')::uuid
		)))`, id, institutionID, teacherID).Scan(&v.ID, &v.CurriculumID, &v.Name, &v.Label, &v.Subject, &v.Grade,
		&v.Status, &v.Revision, &v.PublishedAt, &v.UpdatedAt, &v.Settings, &v.CopiedFromVersionID, &v.CopiedFromLabel)
	if err != nil {
		return v, dbError(err)
	}
	// mapped counts questions mapped to the same code in the curriculum's other
	// editions: the mappings a code change would drop.
	rows, err := tx.Query(ctx, `SELECT ch.id,ch.title,ch.details,co.id,co.code,co.title,co.learning_outcome,co.details,
		(SELECT count(DISTINCT qc.question_id) FROM question_concepts qc
		   JOIN curriculum_concepts oc ON oc.id=qc.concept_id
		   JOIN curriculum_chapters och ON och.id=oc.chapter_id
		   JOIN curriculum_versions ov ON ov.id=och.version_id
		  WHERE ov.curriculum_id=$2 AND ov.id<>$1 AND lower(oc.code)=lower(co.code))
		FROM curriculum_chapters ch LEFT JOIN curriculum_concepts co ON co.chapter_id=ch.id
		WHERE ch.version_id=$1 ORDER BY ch.position,co.position`, id, v.CurriculumID)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var chapterID, title string
		var chDetails ChapterDetails
		var conceptID, code, conceptTitle, outcome *string
		var coDetails *ConceptDetails
		var mapped *int
		if err = rows.Scan(&chapterID, &title, &chDetails, &conceptID, &code, &conceptTitle, &outcome, &coDetails, &mapped); err != nil {
			rows.Close()
			return v, err
		}
		if len(v.Chapters) == 0 || v.Chapters[len(v.Chapters)-1].ID != chapterID {
			v.Chapters = append(v.Chapters, Chapter{ID: chapterID, Title: title, Concepts: []Concept{}, ChapterDetails: chDetails})
		}
		if conceptID != nil {
			ch := &v.Chapters[len(v.Chapters)-1]
			c := Concept{ID: *conceptID, ConceptInput: ConceptInput{Code: *code, Title: *conceptTitle, LearningOutcome: *outcome}}
			if coDetails != nil {
				c.ConceptDetails = *coDetails
			}
			if mapped != nil {
				c.MappedQuestionsPrevious = *mapped
			}
			ch.Concepts = append(ch.Concepts, c)
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
	// The version's own setting, not the incoming one, decides: turning the
	// rule off is itself a save that must carry a note.
	var needNote bool
	if err = tx.QueryRow(ctx, `SELECT COALESCE((settings->>'require_change_note')::bool,false) FROM curriculum_versions WHERE id=$1`, id).Scan(&needNote); err != nil {
		return err
	}
	if needNote && in.ChangeNote == "" {
		return ErrNoteNeeded
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
	settings, _ := json.Marshal(in.Settings)
	_, err = tx.Exec(ctx, `UPDATE curriculum_versions SET label=$2,subject=$3,grade=$4,settings=$5,revision=revision+1,updated_at=now() WHERE id=$1`,
		id, in.Label, in.Subject, in.Grade, settings)
	if err != nil {
		return dbError(err)
	}
	if err = audit(ctx, tx, actor, "update_curriculum_draft", id); err != nil {
		return err
	}
	if err = recordRevision(ctx, tx, actor, id, revision+1, "saved", in.ChangeNote, in); err != nil {
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
	// Check what the version's own rules require. The lock above holds the
	// draft, so the content read on a fresh snapshot can't move underneath.
	current, err := s.GetVersion(ctx, actor.InstitutionID, id, "")
	if err != nil {
		return err
	}
	if issues := PublishIssues(current); len(issues) > 0 {
		return &PublishBlockedError{Issues: issues}
	}
	if _, err = tx.Exec(ctx, `UPDATE curriculum_versions SET status='published',published_at=now(),revision=revision+1 WHERE id=$1`, id); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "publish_curriculum_version", id); err != nil {
		return err
	}
	if err = recordRevision(ctx, tx, actor, id, revision+1, "published", "", versionInput(current)); err != nil {
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
		if err = rows.Scan(&a.ID, &a.GroupID, &a.AcademicYearID, &a.AcademicYearName, &v.ID, &v.CurriculumID, &v.Name, &v.Label, &v.Subject, &v.Grade, &v.Status, &v.Revision, &v.PublishedAt, &v.UpdatedAt); err != nil {
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

// versionInput turns a stored version back into the input shape for snapshots.
func versionInput(v Version) VersionInput {
	in := VersionInput{Label: v.Label, Subject: v.Subject, Grade: v.Grade, Settings: v.Settings, Chapters: []ChapterInput{}}
	for _, ch := range v.Chapters {
		c := ChapterInput{Title: ch.Title, ChapterDetails: ch.ChapterDetails, Concepts: []ConceptInput{}}
		for _, co := range ch.Concepts {
			c.Concepts = append(c.Concepts, co.ConceptInput)
		}
		in.Chapters = append(in.Chapters, c)
	}
	return in
}

// History lists a version's revisions, newest first.
func (s *Service) History(ctx context.Context, institutionID, versionID string) ([]Revision, error) {
	rows, err := s.db.Query(ctx, `SELECT r.revision,r.action,r.note,COALESCE(u.display_name,'Institution admin'),r.created_at
		FROM curriculum_version_revisions r JOIN curriculum_versions v ON v.id=r.version_id
		LEFT JOIN users u ON u.id=r.actor_id
		WHERE r.version_id=$1 AND v.institution_id=$2 ORDER BY r.revision DESC`, versionID, institutionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Revision{}
	for rows.Next() {
		var r Revision
		if err := rows.Scan(&r.Revision, &r.Action, &r.Note, &r.ActorName, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RevisionSnapshot returns a revision's content, for comparing with another.
func (s *Service) RevisionSnapshot(ctx context.Context, institutionID, versionID string, revision int) (json.RawMessage, error) {
	var snap json.RawMessage
	err := s.db.QueryRow(ctx, `SELECT r.snapshot FROM curriculum_version_revisions r
		JOIN curriculum_versions v ON v.id=r.version_id
		WHERE r.version_id=$1 AND v.institution_id=$2 AND r.revision=$3`, versionID, institutionID, revision).Scan(&snap)
	return snap, dbError(err)
}

// YearStats are per-year curriculum assignment counts.
type YearStats struct {
	Assignments    int `json:"assignments"`
	ClassesCovered int `json:"classes_covered"`
	ActiveClasses  int `json:"active_classes"`
}

// UpdateYear renames or re-dates an academic year.
func (s *Service) UpdateYear(ctx context.Context, actor Actor, id string, in YearInput) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE academic_years SET name=$3,starts_on=$4::text::date,ends_on=$5::text::date
		WHERE id=$1 AND institution_id=$2`, id, actor.InstitutionID, in.Name, in.StartsOn, in.EndsOn)
	if err != nil {
		return dbError(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = audit(ctx, tx, actor, "update_academic_year", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
