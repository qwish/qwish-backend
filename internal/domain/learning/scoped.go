package learning

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qwish/backend/internal/middleware"
)

// InstitutionScopedSummary is learning evidence for one curriculum version,
// optionally narrowed to a class and an academic year, grouped by chapter and
// with the coverage an admin needs to read it: how many concepts have
// evidence at all, and which have none yet.
//
// GET /institution/learning-summary/scoped?version_id=&group_id=&academic_year_id=
//
// version_id may be omitted when group_id and academic_year_id are given; the
// class's live assignment for that year then decides the version.
func (h *Handler) InstitutionScopedSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	instID := middleware.GetInstitutionID(r)
	q := r.URL.Query()
	versionID := strings.TrimSpace(q.Get("version_id"))
	groupID := strings.TrimSpace(q.Get("group_id"))
	yearID := strings.TrimSpace(q.Get("academic_year_id"))
	for _, id := range []string{versionID, groupID, yearID} {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				middleware.BadRequest(w, "ids must be UUIDs")
				return
			}
		}
	}

	if versionID == "" {
		if groupID == "" || yearID == "" {
			middleware.BadRequest(w, "version_id, or group_id with academic_year_id, is required")
			return
		}
		err := h.db.QueryRow(ctx, `SELECT version_id::text FROM class_curricula
			WHERE group_id=$1 AND academic_year_id=$2 AND institution_id=$3 AND ended_at IS NULL
			ORDER BY assigned_at DESC LIMIT 1`, groupID, yearID, instID).Scan(&versionID)
		if err != nil {
			middleware.NotFound(w, "curriculum assignment for this class and year")
			return
		}
	}

	var label, subject, grade string
	var minStudents int
	err := h.db.QueryRow(ctx, `SELECT label, subject, grade,
		COALESCE(NULLIF((settings->>'min_students_reported')::int,0),10)
		FROM curriculum_versions WHERE id=$1 AND institution_id=$2`, versionID, instID).
		Scan(&label, &subject, &grade, &minStudents)
	if err != nil {
		middleware.NotFound(w, "curriculum version")
		return
	}

	// The academic year bounds evidence in time when one is given.
	var from, to *time.Time
	if yearID != "" {
		var f, t time.Time
		if err := h.db.QueryRow(ctx, `SELECT starts_on, ends_on + 1 FROM academic_years WHERE id=$1 AND institution_id=$2`,
			yearID, instID).Scan(&f, &t); err != nil {
			middleware.NotFound(w, "academic year")
			return
		}
		from, to = &f, &t
	}

	var students int
	if groupID != "" {
		if err := h.db.QueryRow(ctx, `SELECT count(*) FROM group_students gs JOIN groups g ON g.id=gs.group_id
			WHERE gs.group_id=$1 AND g.institution_id=$2`, groupID, instID).Scan(&students); err != nil {
			middleware.InternalError(w)
			return
		}
	} else {
		h.db.QueryRow(ctx, `SELECT count(*) FROM enrollments WHERE institution_id=$1 AND status='active'`, instID).Scan(&students)
	}

	rows, err := h.db.Query(ctx, `
		SELECT ch.title, co.id, co.code, co.title,
		       COUNT(DISTINCT le.user_id),
		       COUNT(le.id) FILTER (WHERE le.is_correct),
		       COUNT(le.id) FILTER (WHERE NOT le.is_correct),
		       COUNT(le.id) FILTER (WHERE le.id IS NOT NULL AND le.confidence_level IS NULL),
		       MAX(le.occurred_at)
		  FROM curriculum_chapters ch
		  JOIN curriculum_concepts co ON co.chapter_id=ch.id
		  LEFT JOIN learning_evidence le ON le.concept_id=co.id AND le.institution_id=$2 AND NOT le.timed_out
		       AND ($3::uuid IS NULL OR EXISTS (SELECT 1 FROM group_students gs WHERE gs.group_id=$3 AND gs.user_id=le.user_id))
		       AND ($4::timestamptz IS NULL OR le.occurred_at >= $4)
		       AND ($5::timestamptz IS NULL OR le.occurred_at < $5)
		 WHERE ch.version_id=$1
		 GROUP BY ch.position, ch.title, co.position, co.id, co.code, co.title
		 ORDER BY ch.position, co.position`,
		versionID, instID, nullUUID(groupID), from, to)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type conceptRow struct {
		ConceptID         string     `json:"concept_id"`
		ConceptCode       string     `json:"concept_code"`
		ConceptTitle      string     `json:"concept_title"`
		StudentsAssessed  int        `json:"students_assessed"`
		CorrectEvidence   int        `json:"correct_evidence"`
		ErrorEvidence     int        `json:"error_evidence"`
		UnknownConfidence int        `json:"unknown_confidence"`
		LatestEvidenceAt  *time.Time `json:"latest_evidence_at"`
	}
	type chapterRow struct {
		Title    string       `json:"title"`
		Concepts []conceptRow `json:"concepts"`
	}
	type notAssessed struct {
		Code    string `json:"code"`
		Title   string `json:"title"`
		Chapter string `json:"chapter"`
	}
	chapters := []chapterRow{}
	missing := []notAssessed{}
	concepts, withEvidence := 0, 0
	for rows.Next() {
		var chTitle string
		var c conceptRow
		if err := rows.Scan(&chTitle, &c.ConceptID, &c.ConceptCode, &c.ConceptTitle, &c.StudentsAssessed,
			&c.CorrectEvidence, &c.ErrorEvidence, &c.UnknownConfidence, &c.LatestEvidenceAt); err != nil {
			middleware.InternalError(w)
			return
		}
		concepts++
		if c.CorrectEvidence+c.ErrorEvidence > 0 {
			withEvidence++
		} else {
			missing = append(missing, notAssessed{Code: c.ConceptCode, Title: c.ConceptTitle, Chapter: chTitle})
		}
		if len(chapters) == 0 || chapters[len(chapters)-1].Title != chTitle {
			chapters = append(chapters, chapterRow{Title: chTitle, Concepts: []conceptRow{}})
		}
		last := &chapters[len(chapters)-1]
		last.Concepts = append(last.Concepts, c)
	}
	if rows.Err() != nil {
		middleware.InternalError(w)
		return
	}

	middleware.JSON(w, http.StatusOK, map[string]interface{}{
		"version": map[string]string{"id": versionID, "label": label, "subject": subject, "grade": grade},
		"coverage": map[string]interface{}{
			"students": students, "concepts": concepts, "with_evidence": withEvidence,
			"not_assessed": missing,
		},
		// Below this many students a concept's evidence isn't read at all.
		"min_students": minStudents,
		"chapters":     chapters,
	})
}

func nullUUID(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}
