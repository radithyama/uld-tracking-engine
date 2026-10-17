// Command migrate applies pending SQL migrations (backend/migrations/*.sql)
// and records them in schema_migrations. It runs as a one-shot compose
// service before the processor and realtime services start.
//
// Databases created by v1 had 001-003 applied by Postgres' initdb scripts,
// which keep no record. On first run the runner baselines such a database by
// probing for an object each of those migrations created, then applies only
// what is missing. Running it repeatedly is a no-op.
package main

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/radithya/uld-engine/internal/config"
	"github.com/radithya/uld-engine/internal/logx"
	"github.com/radithya/uld-engine/migrations"
)

// Probes used to detect v1 migrations applied without tracking: the table
// must exist and, if a query is given, it must return true. The table check
// runs first because Postgres resolves relations at parse time, so a query
// against a missing table errors instead of short-circuiting.
var baselineProbes = map[string]struct{ table, query string }{
	"001_schema.sql":   {table: "ulds"},
	"002_seed.sql":     {table: "aircrafts", query: `SELECT EXISTS (SELECT 1 FROM aircrafts)`},
	"003_security.sql": {table: "airports"},
}

func probe(ctx context.Context, conn *pgx.Conn, table, query string) (bool, error) {
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists || query == "" {
		return exists, err
	}
	var ok bool
	err := conn.QueryRow(ctx, query).Scan(&ok)
	return ok, err
}

func main() {
	cfg := config.LoadCommon()
	log := logx.New("migrate", cfg.LogLevel)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	conn, err := connectWithRetry(ctx, cfg.DatabaseURL)
	if err != nil {
		logx.Fatal(log, "connect", err)
	}
	defer conn.Close(context.Background())

	// Serialise concurrent runners (e.g. two deploys) on one advisory lock.
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(7140001)`); err != nil {
		logx.Fatal(log, "lock", err)
	}

	var tracked bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&tracked); err != nil {
		logx.Fatal(log, "inspect", err)
	}
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now(), baselined BOOLEAN NOT NULL DEFAULT false)`); err != nil {
		logx.Fatal(log, "create schema_migrations", err)
	}

	files, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		logx.Fatal(log, "list migrations", err)
	}
	slices.Sort(files)

	if !tracked {
		for _, f := range files {
			p, ok := baselineProbes[f]
			if !ok {
				continue
			}
			present, err := probe(ctx, conn, p.table, p.query)
			if err != nil {
				logx.Fatal(log, "baseline probe "+f, err)
			}
			if present {
				if _, err := conn.Exec(ctx, `INSERT INTO schema_migrations (version, baselined) VALUES ($1, true) ON CONFLICT DO NOTHING`, f); err != nil {
					logx.Fatal(log, "baseline "+f, err)
				}
				log.Info("baselined pre-existing migration", "version", f)
			}
		}
	}

	applied := map[string]bool{}
	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		logx.Fatal(log, "read schema_migrations", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			logx.Fatal(log, "scan", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		logx.Fatal(log, "read schema_migrations", err)
	}

	n := 0
	for _, f := range files {
		if applied[f] {
			continue
		}
		sql, err := fs.ReadFile(migrations.FS, f)
		if err != nil {
			logx.Fatal(log, "read "+f, err)
		}
		start := time.Now()
		// Simple protocol: files contain several statements and manage their
		// own transactions (some DDL, like ALTER TYPE ... ADD VALUE, must run
		// outside one). Statements are written to be idempotent.
		if _, err := conn.Exec(ctx, string(sql), pgx.QueryExecModeSimpleProtocol); err != nil {
			logx.Fatal(log, "apply "+f, fmt.Errorf("%w (migration left partially applied; fix and re-run)", err))
		}
		if _, err := conn.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, f); err != nil {
			logx.Fatal(log, "record "+f, err)
		}
		n++
		log.Info("applied migration", "version", f, "ms", time.Since(start).Milliseconds())
	}
	log.Info("migrations complete", "applied", n, "total", len(files), "files", strings.Join(files, ","))
}

func connectWithRetry(ctx context.Context, url string) (*pgx.Conn, error) {
	var lastErr error
	for {
		conn, err := pgx.Connect(ctx, url)
		if err == nil {
			if err = conn.Ping(ctx); err == nil {
				return conn, nil
			}
			conn.Close(ctx)
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w (last error: %v)", ctx.Err(), lastErr)
		case <-time.After(2 * time.Second):
		}
	}
}
