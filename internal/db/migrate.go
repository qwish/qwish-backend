package db

import (
	"context"
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

	_, err := pool.Exec(ctx, `
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

	files, err := filepath.Glob("migrations/*.sql")
	if err != nil {
		log.Fatalf("migrations: glob: %v", err)
	}
	sort.Strings(files)

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
