package enrollment

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// RowVerdict is one row's outcome. In a dry run this is the whole response;
// on commit it is the summary.
type RowVerdict struct {
	Row        int    `json:"row"`    // 1-based, counting the header as row 1
	Action     string `json:"action"` // create | update | error
	FullName   string `json:"full_name"`
	RollNumber string `json:"roll_number,omitempty"`
	Email      string `json:"email,omitempty"`
	Grade      string `json:"grade,omitempty"`
	Section    string `json:"section,omitempty"`
	Reason     string `json:"reason,omitempty"`
	// Changes describe what an update would change, in words.
	Changes []string `json:"changes,omitempty"`
}

// ParseCSV reads the roster file, returning the usable rows and a verdict for
// each unusable one. Header order is taken from the file, not assumed, so a
// column the school reordered still lands in the right field.
func ParseCSV(r io.Reader) ([]RosterInput, []RowVerdict, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("read header: %w", err)
	}
	index := map[string]int{}
	for i, h := range header {
		index[strings.ToLower(strings.TrimSpace(h))] = i
	}
	if _, ok := index["full_name"]; !ok {
		return nil, nil, fmt.Errorf("csv must have a full_name column")
	}

	get := func(rec []string, col string) string {
		i, ok := index[col]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	var rows []RosterInput
	var bad []RowVerdict
	seenRolls := map[string]int{}

	for line := 2; ; line++ {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			bad = append(bad, RowVerdict{Row: line, Action: "error", Reason: err.Error()})
			continue
		}

		in := RosterInput{
			SourceRow:     line,
			FullName:      get(rec, "full_name"),
			Email:         get(rec, "email"),
			RollNumber:    get(rec, "roll_number"),
			Grade:         get(rec, "grade"),
			Section:       get(rec, "section"),
			AdmissionDate: get(rec, "admission_date"),
			GuardianName:  get(rec, "guardian_name"),
			GuardianPhone: get(rec, "guardian_phone"),
			GuardianEmail: get(rec, "guardian_email"),
			Phone:         get(rec, "phone"),
		}

		if in.FullName == "" {
			bad = append(bad, RowVerdict{Row: line, Action: "error", Reason: "full_name is required"})
			continue
		}
		if in.AdmissionDate != "" {
			if _, err := time.Parse("2006-01-02", in.AdmissionDate); err != nil {
				bad = append(bad, RowVerdict{Row: line, Action: "error", FullName: in.FullName,
					Reason: "admission_date must be YYYY-MM-DD"})
				continue
			}
		}
		if in.RollNumber != "" {
			if first, dup := seenRolls[in.RollNumber]; dup {
				bad = append(bad, RowVerdict{Row: line, Action: "error", FullName: in.FullName,
					RollNumber: in.RollNumber,
					Reason:     fmt.Sprintf("roll_number duplicates row %d in this file", first)})
				continue
			}
			seenRolls[in.RollNumber] = line
		}

		rows = append(rows, in)
	}

	return rows, bad, nil
}

// ErrImportRowsRejected is returned when rows fail the roster checks and the
// caller didn't ask to skip them.
var ErrImportRowsRejected = fmt.Errorf("some rows can't be imported")

// querier is the subset of pgxpool.Pool and pgx.Tx that matchRoster needs, so
// preview (pool) and commit (transaction) can share one lookup.
type querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// rosterMatch indexes the institution's live enrollments by roll number and by
// email, in a single query, so importing N rows costs one round trip instead of
// N. A 500-row spreadsheet used to mean 500 sequential queries.
type rosterMatch struct {
	byRoll  map[string]string
	byEmail map[string]string
	// current holds each matched enrollment's values, for the change summary.
	current map[string]rosterCurrent
	// elsewhere holds emails that belong to a student active at another
	// institution: joining them here is an admission transfer, not an import.
	elsewhere map[string]bool
}

type rosterCurrent struct{ FullName, Email, Grade, Section, AdmissionDate string }

// id returns the live enrollment this row updates, or "" to create. Roll number
// wins over email when both are present, matching the original per-row order.
func (m rosterMatch) id(in RosterInput) string {
	if in.RollNumber != "" {
		return m.byRoll[in.RollNumber]
	}
	if in.Email != "" {
		return m.byEmail[in.Email]
	}
	return ""
}

func matchRoster(ctx context.Context, q querier, instID string, rows []RosterInput) (rosterMatch, error) {
	m := rosterMatch{byRoll: map[string]string{}, byEmail: map[string]string{},
		current: map[string]rosterCurrent{}, elsewhere: map[string]bool{}}
	rollNumbers := make([]string, 0, len(rows))
	emails := make([]string, 0, len(rows))
	allEmails := make([]string, 0, len(rows))
	for _, in := range rows {
		if in.RollNumber != "" {
			rollNumbers = append(rollNumbers, in.RollNumber)
		} else if in.Email != "" {
			emails = append(emails, in.Email)
		}
		if in.Email != "" {
			allEmails = append(allEmails, strings.ToLower(in.Email))
		}
	}

	if len(rollNumbers) > 0 || len(emails) > 0 {
		res, err := q.Query(ctx, `
			SELECT id, COALESCE(roll_number,''), COALESCE(email,''), full_name,
			       COALESCE(grade,''), COALESCE(section,''), COALESCE(admission_date::text,'')
			  FROM enrollments
			 WHERE institution_id=$1 AND ended_at IS NULL
			   AND (roll_number = ANY($2::text[]) OR email = ANY($3::text[]))`,
			instID, rollNumbers, emails)
		if err != nil {
			return m, err
		}
		for res.Next() {
			var id, roll, email string
			var cur rosterCurrent
			if err := res.Scan(&id, &roll, &email, &cur.FullName, &cur.Grade, &cur.Section, &cur.AdmissionDate); err != nil {
				res.Close()
				return m, err
			}
			cur.Email = email
			m.current[id] = cur
			if roll != "" {
				m.byRoll[roll] = id
			}
			if email != "" {
				m.byEmail[email] = id
			}
		}
		res.Close()
		if err := res.Err(); err != nil {
			return m, err
		}
	}

	if len(allEmails) > 0 {
		res, err := q.Query(ctx, `
			SELECT DISTINCT lower(u.email) FROM users u
			  JOIN enrollments e ON e.user_id=u.id
			 WHERE lower(u.email) = ANY($2::text[]) AND u.deleted_at IS NULL
			   AND e.institution_id<>$1 AND e.status IN ('active','suspended')`, instID, allEmails)
		if err != nil {
			return m, err
		}
		for res.Next() {
			var email string
			if err := res.Scan(&email); err != nil {
				res.Close()
				return m, err
			}
			m.elsewhere[email] = true
		}
		res.Close()
		if err := res.Err(); err != nil {
			return m, err
		}
	}
	return m, nil
}

// rowNumber is the verdict row for an input: its CSV line when known.
func rowNumber(in RosterInput, i int) int {
	if in.SourceRow > 0 {
		return in.SourceRow
	}
	return i + 2
}

// verdictFor decides one row against the live roster.
func verdictFor(in RosterInput, i int, match rosterMatch) RowVerdict {
	v := RowVerdict{Row: rowNumber(in, i), FullName: in.FullName, RollNumber: in.RollNumber,
		Email: in.Email, Grade: in.Grade, Section: in.Section, Action: "create"}
	if id := match.id(in); id != "" {
		v.Action = "update"
		v.Changes = rosterChanges(match.current[id], in)
		return v
	}
	if in.Email != "" && match.elsewhere[strings.ToLower(in.Email)] {
		v.Action = "error"
		v.Reason = "This email belongs to an active student at another institute. Use an admission transfer instead."
	}
	return v
}

// rosterChanges describes what an update would change. Blank import values
// that would clear a field are named too, since the update writes them.
func rosterChanges(cur rosterCurrent, in RosterInput) []string {
	out := []string{}
	diff := func(label, before, after string) {
		if before == after {
			return
		}
		switch {
		case before == "":
			out = append(out, label+" added")
		case after == "":
			out = append(out, label+" cleared")
		default:
			out = append(out, label+" "+before+" → "+after)
		}
	}
	if cur.FullName != in.FullName {
		out = append(out, "Name corrected")
	}
	diff("Email", cur.Email, in.Email)
	diff("Grade", cur.Grade, in.Grade)
	diff("Section", cur.Section, in.Section)
	diff("Admission date", cur.AdmissionDate, in.AdmissionDate)
	if len(out) == 0 {
		out = append(out, "No changes")
	}
	return out
}

// PreviewImport validates rows against the live roster and writes nothing.
func (s *Service) PreviewImport(ctx context.Context, instID string, rows []RosterInput) ([]RowVerdict, error) {
	match, err := matchRoster(ctx, s.db, instID, rows)
	if err != nil {
		return nil, err
	}
	verdicts := make([]RowVerdict, 0, len(rows))
	for i, in := range rows {
		verdicts = append(verdicts, verdictFor(in, i, match))
	}
	return verdicts, nil
}

// CommitImport applies every row in one transaction. Rows matching a live
// enrollment are updated in place; the rest are created with a claim code.
//
// The transaction is the resume story: an interrupted commit saves nothing, so
// running it again is always safe. With skipErrors, rows the preview would
// reject (e.g. an email active at another institute) are left out and returned
// as verdicts instead of failing the whole file.
func (s *Service) CommitImport(ctx context.Context, instID string, rows []RosterInput, skipErrors bool) ([]Enrollment, []RowVerdict, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	// Matched inside the transaction so the reads see the same snapshot as the
	// writes below.
	match, err := matchRoster(ctx, tx, instID, rows)
	if err != nil {
		return nil, nil, err
	}

	skipped := []RowVerdict{}
	for i, in := range rows {
		if v := verdictFor(in, i, match); v.Action == "error" {
			skipped = append(skipped, v)
		}
	}
	if len(skipped) > 0 && !skipErrors {
		return nil, skipped, ErrImportRowsRejected
	}

	created := make([]Enrollment, 0, len(rows))
	for i, in := range rows {
		if verdictFor(in, i, match).Action == "error" {
			continue
		}
		existingID := match.id(in)

		if existingID != "" {
			if _, err := tx.Exec(ctx,
				`UPDATE enrollments
				    SET full_name=$1, email=$2, grade=$3, section=$4,
				        admission_date=$5, updated_at=now()
				  WHERE id=$6`,
				in.FullName, nilIfEmpty(in.Email), nilIfEmpty(in.Grade), nilIfEmpty(in.Section),
				nilIfEmpty(in.AdmissionDate), existingID); err != nil {
				return nil, nil, fmt.Errorf("row %d: %w", rowNumber(in, i), err)
			}
			continue
		}

		code, err := GenerateClaimCode()
		if err != nil {
			return nil, nil, err
		}
		e, err := scanEnrollment(tx.QueryRow(ctx,
			`INSERT INTO enrollments
				(institution_id, full_name, email, roll_number, grade, section, admission_date,
				 import_phone, import_guardian_name, import_guardian_phone, import_guardian_email,
				 claim_code, status)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'pending_claim')
			 RETURNING `+selectCols,
			instID, in.FullName, nilIfEmpty(in.Email), nilIfEmpty(in.RollNumber),
			nilIfEmpty(in.Grade), nilIfEmpty(in.Section), nilIfEmpty(in.AdmissionDate),
			nilIfEmpty(in.Phone), nilIfEmpty(in.GuardianName), nilIfEmpty(in.GuardianPhone),
			nilIfEmpty(in.GuardianEmail), code))
		if err != nil {
			return nil, nil, fmt.Errorf("row %d: %w", rowNumber(in, i), err)
		}
		created = append(created, e)
	}

	return created, skipped, tx.Commit(ctx)
}
