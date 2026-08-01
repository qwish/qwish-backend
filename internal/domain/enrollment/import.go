package enrollment

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"
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

// PreviewImport validates rows against the live roster and writes nothing.
func (s *Service) PreviewImport(ctx context.Context, instID string, rows []RosterInput) ([]RowVerdict, error) {
	verdicts := make([]RowVerdict, 0, len(rows))
	for i, in := range rows {
		v := RowVerdict{Row: i + 2, FullName: in.FullName, RollNumber: in.RollNumber, Action: "create"}

		var existing int
		switch {
		case in.RollNumber != "":
			s.db.QueryRow(ctx,
				`SELECT COUNT(*) FROM enrollments
				  WHERE institution_id=$1 AND roll_number=$2 AND ended_at IS NULL`,
				instID, in.RollNumber).Scan(&existing)
		case in.Email != "":
			s.db.QueryRow(ctx,
				`SELECT COUNT(*) FROM enrollments
				  WHERE institution_id=$1 AND email=$2 AND ended_at IS NULL`,
				instID, in.Email).Scan(&existing)
		}
		if existing > 0 {
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

	created := make([]Enrollment, 0, len(rows))
	for i, in := range rows {
		var existingID string
		if in.RollNumber != "" {
			tx.QueryRow(ctx,
				`SELECT id FROM enrollments
				  WHERE institution_id=$1 AND roll_number=$2 AND ended_at IS NULL`,
				instID, in.RollNumber).Scan(&existingID)
		} else if in.Email != "" {
			tx.QueryRow(ctx,
				`SELECT id FROM enrollments
				  WHERE institution_id=$1 AND email=$2 AND ended_at IS NULL`,
				instID, in.Email).Scan(&existingID)
		}

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
