package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindMigrationsDirUsesConfiguredDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MIGRATIONS_DIR", dir)

	got, err := findMigrationsDir()
	if err != nil {
		t.Fatalf("findMigrationsDir: %v", err)
	}
	want, _ := filepath.Abs(dir)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFindMigrationsDirRejectsMissingConfiguredAndFallbacks(t *testing.T) {
	working := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(working); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	t.Setenv("MIGRATIONS_DIR", filepath.Join(working, "missing"))

	// The test executable directory has no migrations sibling, and the empty
	// temporary working directory has no local fallback.
	if _, err := findMigrationsDir(); err == nil {
		t.Fatal("expected an error when no migration directory exists")
	}
}
