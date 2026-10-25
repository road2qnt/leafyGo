package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
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
	viewerPageSize, err := positiveInt64Env("VIEWER_PAGE_SIZE", 50)
	if err != nil {
		return Config{}, err
	}
	viewerMaxPageSize, err := positiveInt64Env("VIEWER_MAX_PAGE_SIZE", 100)
	if err != nil {
		return Config{}, err
	}
	if viewerMaxPageSize < viewerPageSize {
		return Config{}, errors.New("VIEWER_MAX_PAGE_SIZE must be greater than or equal to VIEWER_PAGE_SIZE")
	}
	cfg.ViewerPageSize = int(viewerPageSize)
	cfg.ViewerMaxPageSize = int(viewerMaxPageSize)

	if cfg.QueryTimeout, err = durationEnv("QUERY_TIMEOUT", 15*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.MaxQueryBytes, err = positiveInt64Env("MAX_QUERY_BYTES", 65536); err != nil {
		return Config{}, err
	}
	queryMaxRows, err := positiveInt64Env("QUERY_MAX_ROWS", 1000)
	if err != nil {
		return Config{}, err
	}
	cfg.QueryMaxRows = int(queryMaxRows)
	if cfg.QueryMaxCellBytes, err = positiveInt64Env("QUERY_MAX_CELL_BYTES", 4096); err != nil {
		return Config{}, err
	}
	if cfg.QueryMaxResultBytes, err = positiveInt64Env("QUERY_MAX_RESULT_BYTES", 2097152); err != nil {
		return Config{}, err
	}
	queryMaxConcurrent, err := positiveInt64Env("QUERY_MAX_CONCURRENT", 4)
	if err != nil {
		return Config{}, err
	}
	cfg.QueryMaxConcurrent = int(queryMaxConcurrent)

	if cfg.UIPreview, err = boolEnv("UI_PREVIEW", false); err != nil {
		return Config{}, err
	}
	if cfg.UIPreview && cfg.AppEnv == "production" {
		return Config{}, errors.New("UI_PREVIEW must not be enabled in production")
	}

	key, err := decodeKey(os.Getenv("ENCRYPTION_KEY"))
	if err != nil {
		return Config{}, err
	}
	cfg.EncryptionKey = key

	return cfg, nil
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
