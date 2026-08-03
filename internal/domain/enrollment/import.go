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
	Reason     string `json:"reason,omitempty"`
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
}

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
	m := rosterMatch{byRoll: map[string]string{}, byEmail: map[string]string{}}
	rollNumbers := make([]string, 0, len(rows))
	emails := make([]string, 0, len(rows))
	for _, in := range rows {
		if in.RollNumber != "" {
			rollNumbers = append(rollNumbers, in.RollNumber)
		} else if in.Email != "" {
			emails = append(emails, in.Email)
		}
	}
	if len(rollNumbers) == 0 && len(emails) == 0 {
		return m, nil
	}

	res, err := q.Query(ctx, `
		SELECT id, COALESCE(roll_number,''), COALESCE(email,'')
		  FROM enrollments
		 WHERE institution_id=$1 AND ended_at IS NULL
		   AND (roll_number = ANY($2::text[]) OR email = ANY($3::text[]))`,
		instID, rollNumbers, emails)
	if err != nil {
		return m, err
	}
	defer res.Close()
	for res.Next() {
		var id, roll, email string
		if err := res.Scan(&id, &roll, &email); err != nil {
			return m, err
		}
		if roll != "" {
			m.byRoll[roll] = id
		}
		if email != "" {
			m.byEmail[email] = id
		}
	}
	return m, res.Err()
}

// PreviewImport validates rows against the live roster and writes nothing.
func (s *Service) PreviewImport(ctx context.Context, instID string, rows []RosterInput) ([]RowVerdict, error) {
	match, err := matchRoster(ctx, s.db, instID, rows)
	if err != nil {
		return nil, err
	}
	verdicts := make([]RowVerdict, 0, len(rows))
	for i, in := range rows {
		v := RowVerdict{Row: i + 2, FullName: in.FullName, RollNumber: in.RollNumber, Action: "create"}
		if match.id(in) != "" {
			v.Action = "update"
		}
		verdicts = append(verdicts, v)
	}
	return verdicts, nil
}

// CommitImport applies every row in one transaction. Rows matching a live
// enrollment are updated in place; the rest are created with a claim code.
func (s *Service) CommitImport(ctx context.Context, instID string, rows []RosterInput) ([]Enrollment, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Matched inside the transaction so the reads see the same snapshot as the
	// writes below.
	match, err := matchRoster(ctx, tx, instID, rows)
	if err != nil {
		return nil, err
	}

	created := make([]Enrollment, 0, len(rows))
	for i, in := range rows {
		existingID := match.id(in)

		if existingID != "" {
			if _, err := tx.Exec(ctx,
				`UPDATE enrollments
				    SET full_name=$1, email=$2, grade=$3, section=$4,
				        admission_date=$5, updated_at=now()
				  WHERE id=$6`,
				in.FullName, nilIfEmpty(in.Email), nilIfEmpty(in.Grade), nilIfEmpty(in.Section),
				nilIfEmpty(in.AdmissionDate), existingID); err != nil {
				return nil, fmt.Errorf("row %d: %w", i+2, err)
			}
			continue
		}

		code, err := GenerateClaimCode()
		if err != nil {
			return nil, err
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
			return nil, fmt.Errorf("row %d: %w", i+2, err)
		}
		created = append(created, e)
	}

	return created, tx.Commit(ctx)
}
