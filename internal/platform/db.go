package platform

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("platform: parse database url: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("platform: open pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("platform: ping: %w", err)
	}
	return pool, nil
}

func Migrate(ctx context.Context, databaseURL string) error {
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("platform: parse config: %w", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	cfg.ConnectTimeout = 10 * time.Second
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("platform: connect for migration: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, `create table if not exists schema_migrations (
		version integer primary key,
		applied_at timestamptz not null default now()
	)`); err != nil {
		return fmt.Errorf("platform: create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("platform: read migrations: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("platform: migration %q has no numeric prefix", name)
		}
		var applied bool
		if err := conn.QueryRow(ctx, `select exists(select 1 from schema_migrations where version=$1)`, version).Scan(&applied); err != nil {
			return fmt.Errorf("platform: check migration %d: %w", version, err)
		}
		if applied {
			continue
		}
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("platform: read %s: %w", name, err)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("platform: begin migration %d: %w", version, err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("platform: apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `insert into schema_migrations(version) values($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("platform: record migration %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("platform: commit migration %s: %w", name, err)
		}
	}
	return nil
}
