package db

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Connect(databaseURL string) *pgxpool.Pool {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		log.Fatalf("invalid DATABASE_URL: %v", err)
	}

	// Keep a couple of connections warm. Against a remote Supabase database each
	// new connection pays a TLS + auth handshake (100–300ms); with the default
	// MinConns of 0, idle connections are dropped and every burst after a quiet
	// spell re-pays that cost — which is why endpoints "feel slow" even with a
	// handful of users. MinConns pre-warms the pool so requests reuse live conns.
	// ponytail: sized for a single 0.1-CPU / 512MB Render instance against the
	// Supabase session pooler. 0.1 CPU cannot drive 50 parallel queries, and a
	// large pool just holds pooler slots hostage. Raise MaxConns only after
	// pool.Stat().EmptyAcquireCount() is actually climbing.
	cfg.MinConns = poolSetting("DB_MIN_CONNS", 2)
	cfg.MaxConns = poolSetting("DB_MAX_CONNS", 10)
	if cfg.MaxConns < 2 {
		log.Fatal("DB_MAX_CONNS must be at least 2 (migration lock and queries)")
	}
	if cfg.MinConns > cfg.MaxConns {
		log.Fatal("DB_MIN_CONNS exceeds DB_MAX_CONNS")
	}
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute
	cfg.HealthCheckPeriod = time.Minute

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("unable to connect to database: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("unable to ping database: %v", err)
	}
	log.Printf("database connected (pool min=%d max=%d)", cfg.MinConns, cfg.MaxConns)
	return pool
}

func poolSetting(name string, fallback int32) int32 {
	if raw := os.Getenv(name); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || v < 0 {
			log.Fatalf("invalid %s", name)
		}
		return int32(v)
	}
	return fallback
}
func ConnectWorker(databaseURL string) *pgxpool.Pool {
	if url := os.Getenv("WORKER_DATABASE_URL"); url != "" {
		databaseURL = url
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	cfg.MinConns = 1
	cfg.MaxConns = poolSetting("WORKER_DB_MAX_CONNS", 8)
	if cfg.MaxConns < 6 {
		log.Fatal("WORKER_DB_MAX_CONNS must be at least 6 (workers and listeners)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	if err = pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	return pool
}
