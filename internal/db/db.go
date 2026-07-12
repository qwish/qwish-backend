package db

import (
	"context"
	"log"
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
	cfg.MinConns = 2
	cfg.MaxConns = 10
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute
	cfg.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		log.Fatalf("unable to connect to database: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		log.Fatalf("unable to ping database: %v", err)
	}
	log.Printf("database connected (pool min=%d max=%d)", cfg.MinConns, cfg.MaxConns)
	return pool
}
