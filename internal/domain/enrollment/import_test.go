package enrollment

import (
	"context"
	"strings"
	"testing"
)

const csvHeader = "full_name,email,roll_number,grade,section,admission_date,guardian_name,guardian_phone,guardian_email,phone\n"

func TestParseCSVReadsRowsAndFlagsBadOnes(t *testing.T) {
	in := csvHeader +
		"Alice,alice@example.test,R1,9,A,2024-06-01,Ann,555,ann@example.test,556\n" +
		",bob@example.test,R2,9,A,,,,,\n" + // missing full_name
		"Carol,carol@example.test,R3,9,A,not-a-date,,,,\n" // bad date

	rows, bad, err := ParseCSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(rows) != 1 || rows[0].FullName != "Alice" {
		t.Fatalf("rows = %+v, want only Alice", rows)
	}
	if len(bad) != 2 {
		t.Fatalf("bad = %+v, want two error rows", bad)
	}
	for _, v := range bad {
		if v.Action != "error" || v.Reason == "" {
			t.Fatalf("verdict %+v, want action=error with a reason", v)
		}
	}
}

func TestParseCSVRejectsDuplicateRollNumbersWithinFile(t *testing.T) {
	in := csvHeader +
		"Alice,,DUP,9,A,,,,,\n" +
		"Bob,,DUP,9,A,,,,,\n"

	rows, bad, err := ParseCSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(rows) != 1 || len(bad) != 1 {
		t.Fatalf("rows=%d bad=%d, want the second DUP rejected", len(rows), len(bad))
	}
}

func TestPreviewImportWritesNothing(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	var before int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM enrollments WHERE institution_id=$1`, f.InstitutionID).Scan(&before)

	verdicts, err := svc.PreviewImport(ctx, f.InstitutionID, []RosterInput{
		{FullName: "Preview One", RollNumber: "P-1"},
	})
	if err != nil {
		t.Fatalf("PreviewImport: %v", err)
	}
	if len(verdicts) != 1 || verdicts[0].Action != "create" {
		t.Fatalf("verdicts = %+v, want one create", verdicts)
	}

	var after int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM enrollments WHERE institution_id=$1`, f.InstitutionID).Scan(&after)
	if after != before {
		t.Fatalf("dry run wrote rows: before=%d after=%d", before, after)
	}
}

func TestPreviewImportMarksExistingRollAsUpdate(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	var roll string
	pool.QueryRow(ctx, `SELECT roll_number FROM enrollments WHERE id=$1`, f.StudentEnrollmentID).Scan(&roll)

	verdicts, err := svc.PreviewImport(ctx, f.InstitutionID, []RosterInput{
		{FullName: "Renamed", RollNumber: roll},
	})
	if err != nil {
		t.Fatalf("PreviewImport: %v", err)
	}
	if verdicts[0].Action != "update" {
		t.Fatalf("action = %q, want update", verdicts[0].Action)
	}
}

// A bad row must roll the whole commit back, not half-import the file.
func TestCommitImportIsAllOrNothing(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	var before int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM enrollments WHERE institution_id=$1`, f.InstitutionID).Scan(&before)

	// The second row collides with the first only after that first row has
	// already been inserted inside the transaction.
	_, err := svc.CommitImport(ctx, f.InstitutionID, []RosterInput{
		{FullName: "Good", RollNumber: "OK-1"},
		{FullName: "Bad", RollNumber: "OK-1"},
	})
	if err == nil {
		t.Fatal("expected the duplicate roll number to fail the commit")
	}

	var after int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM enrollments WHERE institution_id=$1`, f.InstitutionID).Scan(&after)
	if after != before {
		t.Fatalf("partial import: before=%d after=%d", before, after)
	}
}

func TestCommitImportReturnsClaimCodes(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	created, err := svc.CommitImport(context.Background(), f.InstitutionID, []RosterInput{
		{FullName: "Imported One", RollNumber: "I-1"},
		{FullName: "Imported Two", RollNumber: "I-2"},
	})
	if err != nil {
		t.Fatalf("CommitImport: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("created %d rows, want 2", len(created))
	}
	for _, e := range created {
		if e.ClaimCode == nil || *e.ClaimCode == "" {
			t.Fatalf("enrollment %s has no claim code", e.ID)
		}
	}
}
