package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"leafygo/internal/config"
	"leafygo/internal/migration"
	"leafygo/internal/platform"
	"leafygo/internal/secret"
	"leafygo/internal/seed"
	seedtargets "leafygo/internal/seed/targets"
	"leafygo/internal/store"
	"leafygo/internal/target"
)

func main() {
	if err := run(); err != nil {
		slog.Error("seed failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.AppEnv == "production" {
		return errors.New("refusing to seed when APP_ENV=production")
	}
	if cfg.AdminPassword == "" {
		return errors.New("ADMIN_PASSWORD is required")
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := platform.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	pool, err := platform.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	box, err := secret.New(cfg.EncryptionKey)
	if err != nil {
		return err
	}
	targets := target.New(box, target.Options{ConnectTimeout: cfg.TargetConnectTimeout, ViewerTimeout: cfg.ViewerStatementTimeout})
	defer targets.Close()
	st := store.New(pool)

	return seed.Run(ctx, seed.Deps{
		Store: st,
		Box:   box,
		Migrations: migration.NewService(st, targets, migration.Options{
			AdvisoryLockTimeout: cfg.MigrationAdvisoryLockTimeout,
			MigrationTimeout:    cfg.MigrationTimeout,
			MaxMigrationBytes:   int(cfg.MaxMigrationBytes),
		}),
		Logger: logger,
	}, opts)
}

func loadOptions(cfg config.Config) (seed.Options, error) {
	targets, err := hostEnv("SEED_TARGETS_HOST", "localhost", "SEED_TARGETS_PORT", 5434)
	if err != nil {
		return seed.Options{}, err
	}
	analytics, err := hostEnv("SEED_ANALYTICS_HOST", "localhost", "SEED_ANALYTICS_PORT", 5436)
	if err != nil {
		return seed.Options{}, err
	}
	return seed.Options{
		AdminUsername: cfg.AdminUsername,
		AdminPassword: cfg.AdminPassword,
		UserPassword:  env("SEED_USER_PASSWORD", "password123"),
		Credentials: seedtargets.Credentials{
			Superuser:         env("SEED_SUPERUSER", "postgres"),
			SuperuserPassword: env("SEED_SUPERUSER_PASSWORD", "postgres"),
			AppUser:           env("SEED_APP_DB_USER", "leafygo_app"),
			AppPassword:       env("SEED_APP_DB_PASSWORD", "leafygo_app"),
		},
		Targets:   targets,
		Analytics: analytics,
		Legacy:    seedtargets.Host{Host: "legacy-erp.internal", Port: 5432},
	}, nil
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func hostEnv(hostKey, hostFallback, portKey string, portFallback int) (seedtargets.Host, error) {
	port := portFallback
	if raw := strings.TrimSpace(os.Getenv(portKey)); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil || p < 1 || p > 65535 {
			return seedtargets.Host{}, fmt.Errorf("%s must be a port number", portKey)
		}
		port = p
	}
	return seedtargets.Host{Host: env(hostKey, hostFallback), Port: port}, nil
}
