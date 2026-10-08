package curriculum

import (
	"errors"
	"fmt"
	"testing"
)

func TestYearOverlapRequiresReviewedReason(t *testing.T) {
	s, a, _, _ := testService(t)
	ctx := t.Context()
	in := YearInput{Name: "First", StartsOn: "2026-01-01", EndsOn: "2026-12-31"}
	first, err := s.CreateYear(ctx, a, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Name = "Parallel"
	if _, err = s.CreateYear(ctx, a, in); !errors.Is(err, ErrYearReview) {
		t.Fatalf("unreviewed overlap: %v", err)
	}
	review, err := s.PreviewYear(ctx, a, "", in)
	if err != nil {
		t.Fatal(err)
	}
	if !review.Required || len(review.Overlaps) != 1 {
		t.Fatalf("bad review: %+v", review)
	}
	in.ReviewToken = review.Token
	in.ReviewReason = "Parallel school calendar"
	if _, err = s.CreateYear(ctx, a, in); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateYear(ctx, a, in); !errors.Is(err, ErrYearReview) {
		t.Fatalf("stale token accepted: %v", err)
	}
	years, err := s.ListYears(ctx, a.InstitutionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(years) != 2 || len(years[0].Overlaps) != 1 || years[0].Today == "" {
		t.Fatalf("year visibility: %+v", years)
	}
	_ = first
}

func TestYearAssignedDateReviewAndHistoricalScope(t *testing.T) {
	s, a, group, teacher := testService(t)
	ctx := t.Context()
	year, err := s.CreateYear(ctx, a, YearInput{Name: "Preparation", StartsOn: "2199-01-01", EndsOn: "2199-12-31"})
	if err != nil {
		t.Fatal(err)
	}
	version, err := s.CreateVersion(ctx, a, "", "Physics", sampleVersion())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PublishVersion(ctx, a, version, 1); err != nil {
		t.Fatal(err)
	}
	assignment, err := s.Assign(ctx, a, group, AssignmentInput{year.ID, version})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.ListAssignments(ctx, a.InstitutionID, group, teacher)
	if err != nil || len(list) != 1 || list[0].TemporalState != "upcoming" {
		t.Fatalf("preparation: %+v %v", list, err)
	}
	in := YearInput{Name: year.Name, StartsOn: "2000-01-01", EndsOn: "2000-12-31"}
	if err = s.UpdateYear(ctx, a, year.ID, in); !errors.Is(err, ErrYearReview) {
		t.Fatalf("unreviewed dates: %v", err)
	}
	review, err := s.PreviewYear(ctx, a, year.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Assignments) != 1 || review.Assignments[0].After != "past" {
		t.Fatalf("impact: %+v", review)
	}
	in.ReviewToken = review.Token
	in.ReviewReason = "Correct imported year dates"
	if err = s.UpdateYear(ctx, a, year.ID, in); err != nil {
		t.Fatal(err)
	}
	var audited bool
	if err = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM audit_log WHERE target_id=$1 AND reason LIKE '%2199-01-01%' AND reason LIKE '%2000-01-01%')`, year.ID).Scan(&audited); err != nil || !audited {
		t.Fatalf("before/after audit: %v %v", audited, err)
	}
	if err = s.EndAssignment(ctx, a, group, assignment); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(ctx, `UPDATE groups SET archived_at=now() WHERE id=$1`, group); err != nil {
		t.Fatal(err)
	}
	list, err = s.ListAssignments(ctx, a.InstitutionID, group, "")
	if err != nil || len(list) != 1 || list[0].EndedAt == nil {
		t.Fatalf("admin history: %+v %v", list, err)
	}
	if _, err = s.ListAssignments(ctx, a.InstitutionID, group, teacher); !errors.Is(err, ErrNotFound) {
		t.Fatalf("teacher historical scope: %v", err)
	}
}

func TestPublishedDiscoveryFiltersBeforePagination(t *testing.T) {
	s, a, _, _ := testService(t)
	ctx := t.Context()
	id, err := s.CreateVersion(ctx, a, "", "Discoverable Physics", sampleVersion())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PublishVersion(ctx, a, id, 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 22; i++ {
		if _, err = s.CreateVersion(ctx, a, "", fmt.Sprintf("New draft %02d", i), sampleVersion()); err != nil {
			t.Fatal(err)
		}
	}
	versions, total, err := s.ListVersions(ctx, a.InstitutionID, 1, 20, ListFilter{Status: "published", Search: "Discoverable"})
	if err != nil || total != 1 || len(versions) != 1 || versions[0].ID != id {
		t.Fatalf("published search: %d %+v %v", total, versions, err)
	}
	versions, total, err = s.ListVersions(ctx, a.InstitutionID, 2, 20, ListFilter{Status: "published", Search: "Discoverable"})
	if err != nil || total != 1 || len(versions) != 0 {
		t.Fatalf("later page total: %d %+v %v", total, versions, err)
	}
}
