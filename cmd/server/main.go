package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"leafygo/internal/access"
	"leafygo/internal/auth"
	"leafygo/internal/config"
	"leafygo/internal/platform"
	"leafygo/internal/seed"
	"leafygo/internal/store"
	"leafygo/internal/telemetry"
	"leafygo/internal/web"
)

const serviceVersion = "m2-ega"

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
	logger := telemetry.NewLogger(telemetry.LoggerOptions{
		Service: "leafygo-api",
		Env:     cfg.AppEnv,
		Version: serviceVersion,
		Level:   slog.LevelInfo,
	})
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tracer, err := telemetry.NewTracerProvider(ctx, telemetry.TracerOptions{
		ServiceName:    "leafygo-api",
		ServiceVersion: serviceVersion,
		Env:            cfg.AppEnv,
		Console:        cfg.OTELConsole,
		OTLPEndpoint:   cfg.OTLPEndpoint,
		SampleRatio:    cfg.OTELSampleRatio,
	})
	if err != nil {
		logger.Warn("telemetry.tracing.disabled", "error", err)
	} else {
		tracer.Install()
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := tracer.Shutdown(shutdownCtx); err != nil {
				logger.Warn("telemetry.tracing.shutdown_failed", "error", err)
			}
		}()
	}

	if err := platform.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	pool, err := platform.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	st := store.New(pool)
	sessionManager := auth.NewManager(st, cfg.SessionIdle, cfg.SessionAbsolute, cfg.SessionRotate, cfg.SessionGrace)
	policy := access.New(st)

	if cfg.AdminPassword == "" {
		logger.Warn("ADMIN_PASSWORD is empty; no bootstrap admin was created")
	} else if err := seed.EnsureAdmin(ctx, st, cfg.AdminUsername, cfg.AdminPassword); err != nil {
		return err
	}

	if n, err := st.MarkRunningMigrationsFailed(ctx); err != nil {
		return err
	} else if n > 0 {
		logger.Warn("marked interrupted migrations as failed", "count", n)
	}

	go cleanupSessions(ctx, st, logger)

	srv := web.New(cfg, st, sessionManager, policy, logger)
	defer srv.Close()
	if cfg.MetricsEnabled {
		metrics := telemetry.NewMetrics(telemetry.MetricsOptions{
			JobCounts: telemetry.PlatformJobCountSource(pool),
			Logger:    logger,
		})
		metrics.RegisterPool(telemetry.PgxPoolSource{Pool: pool, Role: "platform", Name: "platform"})
		metrics.RegisterPoolGroup(func() []telemetry.PoolStatsSource {
			snapshots := srv.TargetPools()
			sources := make([]telemetry.PoolStatsSource, 0, len(snapshots))
			for _, snapshot := range snapshots {
				name := snapshot.Name
				if name == "" {
					name = snapshot.ID.String()
				}
				sources = append(sources, telemetry.PgxPoolSource{
					Pool: snapshot.Pool,
					Role: "target",
					Name: name,
				})
			}
			return sources
		})
		srv.SetMetricsHandler(metrics.Handler())
	}
	handler := srv.Handler()
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      35 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		logger.Info("server listening", "addr", cfg.Addr, "env", cfg.AppEnv)
		errc <- server.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		logger.Info("server stopped")
	}
	return nil
}

func cleanupSessions(ctx context.Context, st *store.Store, logger *slog.Logger) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := st.DeleteExpiredSessions(ctx, time.Now().UTC()); err != nil {
				logger.Error("cleanup sessions", "error", err)
			}
		}
	}
}
