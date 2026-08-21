package db

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RunMigrations applies any unapplied SQL migration files from the migrations/ directory.
// It tracks applied versions in a schema_migrations table.
func RunMigrations(pool *pgxpool.Pool) {
	ctx := context.Background()

	// Only one API replica may inspect/apply migrations at a time. Without this,
	// simultaneous deploys can both see the same pending version and one dies on
	// the schema_migrations primary key after executing the SQL.
	const migrationLockID int64 = 7377697368
	lockConn, err := pool.Acquire(ctx)
	if err != nil {
		log.Fatalf("migrations: reserve connection for advisory lock: %v", err)
	}
	defer lockConn.Release()
	if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		log.Fatalf("migrations: acquire advisory lock: %v", err)
	}
	defer func() {
		if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, migrationLockID); err != nil {
			log.Printf("migrations: release advisory lock: %v", err)
		}
	}()

	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT        PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		log.Fatalf("migrations: create tracking table: %v", err)
	}

	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		log.Fatalf("migrations: query applied versions: %v", err)
	}
	applied := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			log.Fatalf("migrations: scan version: %v", err)
		}
		applied[v] = true
	}
	rows.Close()

	migrationsDir, err := findMigrationsDir()
	if err != nil {
		log.Fatalf("migrations: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	if err != nil {
		log.Fatalf("migrations: glob: %v", err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		log.Fatalf("migrations: no SQL files found in %s", migrationsDir)
	}
	latest := strings.TrimSuffix(filepath.Base(files[len(files)-1]), ".sql")
	log.Printf("migrations: found %d files in %s (latest: %s)", len(files), migrationsDir, latest)

	for _, f := range files {
		version := strings.TrimSuffix(filepath.Base(f), ".sql")
		if applied[version] {
			continue
		}

		sql, err := os.ReadFile(f)
		if err != nil {
			log.Fatalf("migrations: read %s: %v", f, err)
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			log.Fatalf("migrations: begin tx for %s: %v", f, err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			tx.Rollback(ctx)
			log.Fatalf("migrations: apply %s: %v", f, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			tx.Rollback(ctx)
			log.Fatalf("migrations: record %s: %v", f, err)
		}
		if err := tx.Commit(ctx); err != nil {
			log.Fatalf("migrations: commit %s: %v", f, err)
		}
		log.Printf("migrations: applied %s", version)
	}
	log.Println("migrations: up to date")
}

// findMigrationsDir supports both local development (repository working
// directory) and deployed binaries (migrations beside the executable). An
// explicit path is useful for release platforms that mount migrations into a
// separate directory. Crucially, a missing directory is an error rather than a
// false "up to date" result.
func findMigrationsDir() (string, error) {
	var candidates []string
	if configured := strings.TrimSpace(os.Getenv("MIGRATIONS_DIR")); configured != "" {
		candidates = append(candidates, configured)
	}
	candidates = append(candidates, "migrations")
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "migrations"))
	}

	seen := map[string]bool{}
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil || seen[absolute] {
			continue
		}
		seen[absolute] = true
		info, err := os.Stat(absolute)
		if err == nil && info.IsDir() {
			return absolute, nil
		}
	}
	return "", fmt.Errorf("migration directory not found (checked %s)", strings.Join(candidates, ", "))
}
