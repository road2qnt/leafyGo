package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr            string
	DatabaseURL     string
	TestDatabaseURL string
	EncryptionKey   []byte
	AppEnv          string
	CookieSecure    bool

	SessionIdle     time.Duration
	SessionAbsolute time.Duration
	SessionRotate   time.Duration
	SessionGrace    time.Duration

	AdminUsername string
	AdminPassword string

	UIPreview                    bool
	TargetConnectTimeout         time.Duration
	ViewerStatementTimeout       time.Duration
	MigrationAdvisoryLockTimeout time.Duration
	MigrationTimeout             time.Duration
	MaxMigrationBytes            int64
	ViewerPageSize               int
	ViewerMaxPageSize            int

	QueryTimeout        time.Duration
	MaxQueryBytes       int64
	QueryMaxRows        int
	QueryMaxCellBytes   int64
	QueryMaxResultBytes int64
	QueryMaxConcurrent  int

	OTLPEndpoint    string
	OTELConsole     bool
	OTELSampleRatio float64
	MetricsEnabled  bool
	SPARoot         bool

	Worker WorkerConfig
}

type WorkerConfig struct {
	ID                string
	MaxConcurrentJobs int
	PollInterval      time.Duration
	LeaseTTL          time.Duration
	HeartbeatInterval time.Duration
	RecoveryInterval  time.Duration
	ReprobeInterval   time.Duration
	ProbeTimeout      time.Duration
	ExecTimeout       time.Duration
	ShutdownTimeout   time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Addr:          env("ADDR", ":8080"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		AppEnv:        env("APP_ENV", "development"),
		AdminUsername: env("ADMIN_USERNAME", "admin"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
	}
	cfg.TestDatabaseURL = os.Getenv("TEST_DATABASE_URL")
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}

	var err error
	if cfg.SessionIdle, err = durationEnv("SESSION_IDLE_TIMEOUT", 30*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.SessionAbsolute, err = durationEnv("SESSION_ABSOLUTE_TIMEOUT", 8*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.SessionRotate, err = durationEnv("SESSION_ROTATE_INTERVAL", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.SessionGrace, err = durationEnv("SESSION_GRACE_PERIOD", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.CookieSecure, err = boolEnv("COOKIE_SECURE", cfg.AppEnv == "production"); err != nil {
		return Config{}, err
	}
	if cfg.TargetConnectTimeout, err = durationEnv("TARGET_CONNECT_TIMEOUT", 5*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.ViewerStatementTimeout, err = durationEnv("VIEWER_STATEMENT_TIMEOUT", 5*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.MigrationAdvisoryLockTimeout, err = durationEnv("MIGRATION_ADVISORY_LOCK_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.MigrationTimeout, err = durationEnv("MIGRATION_TIMEOUT", 30*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.MaxMigrationBytes, err = positiveInt64Env("MAX_MIGRATION_BYTES", 1048576); err != nil {
		return Config{}, err
	}
	if cfg.ViewerPageSize, err = positiveIntEnv("VIEWER_PAGE_SIZE", 50); err != nil {
		return Config{}, err
	}
	if cfg.ViewerMaxPageSize, err = positiveIntEnv("VIEWER_MAX_PAGE_SIZE", 100); err != nil {
		return Config{}, err
	}
	if cfg.ViewerMaxPageSize < cfg.ViewerPageSize {
		return Config{}, errors.New("VIEWER_MAX_PAGE_SIZE must be greater than or equal to VIEWER_PAGE_SIZE")
	}

	if cfg.QueryTimeout, err = durationEnv("QUERY_TIMEOUT", 15*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.MaxQueryBytes, err = positiveInt64Env("MAX_QUERY_BYTES", 65536); err != nil {
		return Config{}, err
	}
	if cfg.QueryMaxRows, err = positiveIntEnv("QUERY_MAX_ROWS", 1000); err != nil {
		return Config{}, err
	}
	if cfg.QueryMaxCellBytes, err = positiveInt64Env("QUERY_MAX_CELL_BYTES", 4096); err != nil {
		return Config{}, err
	}
	if cfg.QueryMaxResultBytes, err = positiveInt64Env("QUERY_MAX_RESULT_BYTES", 2097152); err != nil {
		return Config{}, err
	}
	if cfg.QueryMaxConcurrent, err = positiveIntEnv("QUERY_MAX_CONCURRENT", 4); err != nil {
		return Config{}, err
	}

	if cfg.UIPreview, err = boolEnv("UI_PREVIEW", false); err != nil {
		return Config{}, err
	}
	if cfg.UIPreview && cfg.AppEnv == "production" {
		return Config{}, errors.New("UI_PREVIEW must not be enabled in production")
	}

	cfg.OTLPEndpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if cfg.OTELConsole, err = boolEnv("OTEL_TRACES_CONSOLE", cfg.AppEnv != "production"); err != nil {
		return Config{}, err
	}
	if cfg.OTELSampleRatio, err = ratioEnv("OTEL_TRACES_SAMPLE_RATIO", 1); err != nil {
		return Config{}, err
	}
	if cfg.MetricsEnabled, err = boolEnv("METRICS_ENABLED", true); err != nil {
		return Config{}, err
	}
	if cfg.SPARoot, err = boolEnv("SPA_ROOT", false); err != nil {
		return Config{}, err
	}
	if cfg.Worker, err = loadWorkerConfig(); err != nil {
		return Config{}, err
	}

	key, err := decodeKey(os.Getenv("ENCRYPTION_KEY"))
	if err != nil {
		return Config{}, err
	}
	cfg.EncryptionKey = key

	return cfg, nil
}

func loadWorkerConfig() (WorkerConfig, error) {
	w := WorkerConfig{ID: os.Getenv("WORKER_ID")}
	var err error
	if w.MaxConcurrentJobs, err = positiveIntEnv("WORKER_MAX_CONCURRENT_JOBS", 4); err != nil {
		return WorkerConfig{}, err
	}
	durations := []struct {
		dst      *time.Duration
		key      string
		fallback time.Duration
	}{
		{&w.PollInterval, "WORKER_POLL_INTERVAL", 2 * time.Second},
		{&w.LeaseTTL, "WORKER_LEASE_TTL", 15 * time.Second},
		{&w.HeartbeatInterval, "WORKER_HEARTBEAT_INTERVAL", 5 * time.Second},
		{&w.RecoveryInterval, "WORKER_RECOVERY_INTERVAL", 5 * time.Second},
		{&w.ReprobeInterval, "WORKER_REPROBE_INTERVAL", 15 * time.Second},
		{&w.ProbeTimeout, "WORKER_PROBE_TIMEOUT", 10 * time.Second},
		{&w.ExecTimeout, "WORKER_EXEC_TIMEOUT", 30 * time.Minute},
		{&w.ShutdownTimeout, "WORKER_SHUTDOWN_TIMEOUT", 30 * time.Second},
	}
	for _, d := range durations {
		if *d.dst, err = durationEnv(d.key, d.fallback); err != nil {
			return WorkerConfig{}, err
		}
	}
	if w.HeartbeatInterval >= w.LeaseTTL {
		return WorkerConfig{}, errors.New("WORKER_HEARTBEAT_INTERVAL must be shorter than WORKER_LEASE_TTL")
	}
	return w, nil
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration (example: 30m): %w", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	return d, nil
}

func boolEnv(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false: %w", key, err)
	}
	return v, nil
}

func ratioEnv(key string, fallback float64) (float64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number between 0 and 1: %w", key, err)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
		return 0, fmt.Errorf("%s must be between 0 and 1", key)
	}
	return v, nil
}

func positiveInt64Env(key string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if v <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	return v, nil
}

func positiveIntEnv(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if v <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	return v, nil
}

func decodeKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("ENCRYPTION_KEY is required (32 bytes, base64 or hex encoded)")
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := hex.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, errors.New("ENCRYPTION_KEY must decode to exactly 32 bytes (base64 or hex)")
}
