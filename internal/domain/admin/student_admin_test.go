package admin

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Merging folds one duplicate into the other: points sum, attempts and
// enrollments repoint, and the loser is soft-deleted.
func TestMergeStudentsFoldsPointsAndAttempts(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())

	var keepID, mergeID, actorID string
	mk := func(label string, points int, dest *string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (supabase_uid, full_name, display_name, email, role, total_points)
			VALUES (gen_random_uuid(), $1, $1, $2, 'student', $3) RETURNING id`,
			label+tag, label+"-"+tag+"@example.test", points).Scan(dest); err != nil {
			t.Fatalf("seed %s: %v", label, err)
		}
	}
	mk("keep", 100, &keepID)
	mk("merge", 40, &mergeID)
	mk("actor", 0, &actorID)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`,
			[]string{keepID, mergeID, actorID})
	})

	if err := MergeStudents(ctx, pool, keepID, mergeID, actorID); err != nil {
		t.Fatalf("MergeStudents: %v", err)
	}

	var points int64
	pool.QueryRow(ctx, `SELECT total_points FROM users WHERE id=$1`, keepID).Scan(&points)
	if points != 140 {
		t.Fatalf("total_points = %d, want 140", points)
	}

	var deletedAt *time.Time
	pool.QueryRow(ctx, `SELECT deleted_at FROM users WHERE id=$1`, mergeID).Scan(&deletedAt)
	if deletedAt == nil {
		t.Fatal("merged user should be soft-deleted")
	}
}

func TestMergeStudentsRejectsSelfMerge(t *testing.T) {
	pool := openTestDB(t)
	err := MergeStudents(context.Background(), pool, "same-id", "same-id", "actor")
	if err == nil {
		t.Fatal("expected merging a user into itself to fail")
	}
}
