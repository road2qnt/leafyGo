package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"leafygo/internal/config"
	"leafygo/internal/platform"
	"leafygo/internal/secret"
	"leafygo/internal/store"
	"leafygo/internal/worker"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

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
	w := worker.New(store.New(pool), box, cfg.DatabaseURL, cfg.Worker, cfg.TargetConnectTimeout, logger)
	return w.Run(ctx)
}
