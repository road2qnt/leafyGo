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

	"leafygo/internal/config"
	"leafygo/internal/platform"
	"leafygo/internal/secret"
	"leafygo/internal/store"
	"leafygo/internal/telemetry"
	"leafygo/internal/worker"
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
		Service: "leafygo-worker",
		Env:     cfg.AppEnv,
		Version: serviceVersion,
		Level:   slog.LevelInfo,
	})
	slog.SetDefault(logger)
	telemetry.SetDefaultLogger(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tracer, err := telemetry.NewTracerProvider(ctx, telemetry.TracerOptions{
		ServiceName:    "leafygo-worker",
		ServiceVersion: serviceVersion,
		Env:            cfg.AppEnv,
		Console:        cfg.OTELConsole,
		ConsoleWriter:  os.Stdout,
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

	box, err := secret.New(cfg.EncryptionKey)
	if err != nil {
		return err
	}
	w := worker.New(store.New(pool), box, cfg.DatabaseURL, cfg.Worker, cfg.TargetConnectTimeout, logger)

	if cfg.MetricsEnabled {
		// JobCounts is intentionally omitted on workers: jobs_current is
		// single-sourced from cmd/server so scaling worker replicas never
		// double-counts the gauge.
		metrics := telemetry.NewMetrics(telemetry.MetricsOptions{
			Logger: logger,
		})
		metrics.RegisterPool(telemetry.PgxPoolSource{Pool: pool, Role: "worker", Name: "platform"})
		w.SetMetrics(metrics)

		mux := http.NewServeMux()
		mux.Handle("/metrics", metrics.Handler())
		mux.HandleFunc("/healthz", func(rw http.ResponseWriter, _ *http.Request) {
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("ok\n"))
		})
		metricsSrv := &http.Server{
			Addr:              cfg.Worker.MetricsAddr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      15 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		go func() {
			logger.Info("worker.metrics.listening", "addr", cfg.Worker.MetricsAddr)
			if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("worker.metrics.failed", "addr", cfg.Worker.MetricsAddr, "error", err)
			}
		}()
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
				logger.Warn("worker.metrics.shutdown_failed", "error", err)
			}
		}()
	}

	return w.Run(ctx)
}
