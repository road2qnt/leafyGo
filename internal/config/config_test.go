package config

import (
	"strings"
	"testing"
	"time"
)

func withEnv(t *testing.T, k, v string) {
	t.Helper()
	t.Setenv(k, v)
}

func baseEnv(t *testing.T) {
	t.Helper()
	withEnv(t, "DATABASE_URL", "postgres://u:p@localhost:5432/platform?sslmode=disable")
	withEnv(t, "ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	withEnv(t, "SESSION_IDLE_TIMEOUT", "")
	withEnv(t, "SESSION_ABSOLUTE_TIMEOUT", "")
	withEnv(t, "SESSION_ROTATE_INTERVAL", "")
	withEnv(t, "SESSION_GRACE_PERIOD", "")
	withEnv(t, "COOKIE_SECURE", "")
	withEnv(t, "APP_ENV", "development")
	withEnv(t, "UI_PREVIEW", "")
	withEnv(t, "TARGET_CONNECT_TIMEOUT", "")
	withEnv(t, "VIEWER_STATEMENT_TIMEOUT", "")
	withEnv(t, "MIGRATION_ADVISORY_LOCK_TIMEOUT", "")
	withEnv(t, "MIGRATION_TIMEOUT", "")
	withEnv(t, "MAX_MIGRATION_BYTES", "")
	withEnv(t, "VIEWER_PAGE_SIZE", "")
	withEnv(t, "VIEWER_MAX_PAGE_SIZE", "")
	withEnv(t, "QUERY_TIMEOUT", "")
	withEnv(t, "MAX_QUERY_BYTES", "")
	withEnv(t, "QUERY_MAX_ROWS", "")
	withEnv(t, "QUERY_MAX_CELL_BYTES", "")
	withEnv(t, "QUERY_MAX_RESULT_BYTES", "")
	withEnv(t, "QUERY_MAX_CONCURRENT", "")
}

func TestLoadDefaults(t *testing.T) {
	baseEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Fatalf("Addr = %q", cfg.Addr)
	}
	if cfg.SessionIdle != 30*time.Minute {
		t.Fatalf("SessionIdle = %v", cfg.SessionIdle)
	}
	if cfg.SessionAbsolute != 8*time.Hour {
		t.Fatalf("SessionAbsolute = %v", cfg.SessionAbsolute)
	}
	if cfg.SessionRotate != 15*time.Minute {
		t.Fatalf("SessionRotate = %v", cfg.SessionRotate)
	}
	if cfg.SessionGrace != 10*time.Second {
		t.Fatalf("SessionGrace = %v", cfg.SessionGrace)
	}
	if cfg.CookieSecure {
		t.Fatal("CookieSecure should default to false in development")
	}
	if len(cfg.EncryptionKey) != 32 {
		t.Fatalf("key length = %d", len(cfg.EncryptionKey))
	}
}

func TestLoadTargetConfigDefaults(t *testing.T) {
	baseEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TargetConnectTimeout != 5*time.Second {
		t.Fatalf("TargetConnectTimeout = %v", cfg.TargetConnectTimeout)
	}
	if cfg.ViewerStatementTimeout != 5*time.Second {
		t.Fatalf("ViewerStatementTimeout = %v", cfg.ViewerStatementTimeout)
	}
	if cfg.MigrationAdvisoryLockTimeout != 10*time.Second {
		t.Fatalf("MigrationAdvisoryLockTimeout = %v", cfg.MigrationAdvisoryLockTimeout)
	}
	if cfg.MigrationTimeout != 30*time.Second {
		t.Fatalf("MigrationTimeout = %v", cfg.MigrationTimeout)
	}
	if cfg.MaxMigrationBytes != 1048576 {
		t.Fatalf("MaxMigrationBytes = %d", cfg.MaxMigrationBytes)
	}
	if cfg.ViewerPageSize != 50 {
		t.Fatalf("ViewerPageSize = %d", cfg.ViewerPageSize)
	}
	if cfg.ViewerMaxPageSize != 100 {
		t.Fatalf("ViewerMaxPageSize = %d", cfg.ViewerMaxPageSize)
	}
	if cfg.QueryTimeout != 15*time.Second {
		t.Fatalf("QueryTimeout = %v", cfg.QueryTimeout)
	}
	if cfg.MaxQueryBytes != 65536 {
		t.Fatalf("MaxQueryBytes = %d", cfg.MaxQueryBytes)
	}
	if cfg.QueryMaxRows != 1000 {
		t.Fatalf("QueryMaxRows = %d", cfg.QueryMaxRows)
	}
	if cfg.QueryMaxCellBytes != 4096 {
		t.Fatalf("QueryMaxCellBytes = %d", cfg.QueryMaxCellBytes)
	}
	if cfg.QueryMaxResultBytes != 2097152 {
		t.Fatalf("QueryMaxResultBytes = %d", cfg.QueryMaxResultBytes)
	}
	if cfg.QueryMaxConcurrent != 4 {
		t.Fatalf("QueryMaxConcurrent = %d", cfg.QueryMaxConcurrent)
	}
}

func TestLoadTargetConfigOverrides(t *testing.T) {
	baseEnv(t)
	withEnv(t, "TARGET_CONNECT_TIMEOUT", "2s")
	withEnv(t, "VIEWER_STATEMENT_TIMEOUT", "3s")
	withEnv(t, "MIGRATION_ADVISORY_LOCK_TIMEOUT", "4s")
	withEnv(t, "MIGRATION_TIMEOUT", "1m")
	withEnv(t, "MAX_MIGRATION_BYTES", "2048")
	withEnv(t, "VIEWER_PAGE_SIZE", "20")
	withEnv(t, "VIEWER_MAX_PAGE_SIZE", "40")
	withEnv(t, "QUERY_TIMEOUT", "10s")
	withEnv(t, "MAX_QUERY_BYTES", "32768")
	withEnv(t, "QUERY_MAX_ROWS", "500")
	withEnv(t, "QUERY_MAX_CELL_BYTES", "2048")
	withEnv(t, "QUERY_MAX_RESULT_BYTES", "1048576")
	withEnv(t, "QUERY_MAX_CONCURRENT", "8")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TargetConnectTimeout != 2*time.Second {
		t.Fatalf("TargetConnectTimeout = %v", cfg.TargetConnectTimeout)
	}
	if cfg.ViewerStatementTimeout != 3*time.Second {
		t.Fatalf("ViewerStatementTimeout = %v", cfg.ViewerStatementTimeout)
	}
	if cfg.MigrationAdvisoryLockTimeout != 4*time.Second {
		t.Fatalf("MigrationAdvisoryLockTimeout = %v", cfg.MigrationAdvisoryLockTimeout)
	}
	if cfg.MigrationTimeout != time.Minute {
		t.Fatalf("MigrationTimeout = %v", cfg.MigrationTimeout)
	}
	if cfg.MaxMigrationBytes != 2048 {
		t.Fatalf("MaxMigrationBytes = %d", cfg.MaxMigrationBytes)
	}
	if cfg.ViewerPageSize != 20 || cfg.ViewerMaxPageSize != 40 {
		t.Fatalf("page sizes = %d/%d", cfg.ViewerPageSize, cfg.ViewerMaxPageSize)
	}
	if cfg.QueryTimeout != 10*time.Second {
		t.Fatalf("QueryTimeout = %v", cfg.QueryTimeout)
	}
	if cfg.MaxQueryBytes != 32768 {
		t.Fatalf("MaxQueryBytes = %d", cfg.MaxQueryBytes)
	}
	if cfg.QueryMaxRows != 500 {
		t.Fatalf("QueryMaxRows = %d", cfg.QueryMaxRows)
	}
	if cfg.QueryMaxCellBytes != 2048 {
		t.Fatalf("QueryMaxCellBytes = %d", cfg.QueryMaxCellBytes)
	}
	if cfg.QueryMaxResultBytes != 1048576 {
		t.Fatalf("QueryMaxResultBytes = %d", cfg.QueryMaxResultBytes)
	}
	if cfg.QueryMaxConcurrent != 8 {
		t.Fatalf("QueryMaxConcurrent = %d", cfg.QueryMaxConcurrent)
	}
}

func TestLoadTargetConfigRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		key  string
		val  string
		page string
	}{
		{name: "connect zero", key: "TARGET_CONNECT_TIMEOUT", val: "0s"},
		{name: "viewer negative", key: "VIEWER_STATEMENT_TIMEOUT", val: "-1s"},
		{name: "lock bad", key: "MIGRATION_ADVISORY_LOCK_TIMEOUT", val: "nope"},
		{name: "migration zero", key: "MIGRATION_TIMEOUT", val: "0"},
		{name: "max bytes zero", key: "MAX_MIGRATION_BYTES", val: "0"},
		{name: "page zero", key: "VIEWER_PAGE_SIZE", val: "0"},
		{name: "max page zero", key: "VIEWER_MAX_PAGE_SIZE", val: "0"},
		{name: "max page below page", key: "VIEWER_MAX_PAGE_SIZE", val: "10", page: "20"},
		{name: "query timeout zero", key: "QUERY_TIMEOUT", val: "0s"},
		{name: "query timeout negative", key: "QUERY_TIMEOUT", val: "-5s"},
		{name: "max query bytes zero", key: "MAX_QUERY_BYTES", val: "0"},
		{name: "query max rows zero", key: "QUERY_MAX_ROWS", val: "0"},
		{name: "query max cell bytes zero", key: "QUERY_MAX_CELL_BYTES", val: "0"},
		{name: "query max result bytes zero", key: "QUERY_MAX_RESULT_BYTES", val: "0"},
		{name: "query max concurrent zero", key: "QUERY_MAX_CONCURRENT", val: "0"},
		{name: "query max concurrent negative", key: "QUERY_MAX_CONCURRENT", val: "-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			baseEnv(t)
			withEnv(t, tc.key, tc.val)
			if tc.page != "" {
				withEnv(t, "VIEWER_PAGE_SIZE", tc.page)
			}
			_, err := Load()
			if err == nil {
				t.Fatalf("expected error for %s=%s", tc.key, tc.val)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("error %q does not mention %s", err, tc.key)
			}
		})
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	baseEnv(t)
	withEnv(t, "DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for empty DATABASE_URL")
	}
}

func TestLoadRequiresKey(t *testing.T) {
	baseEnv(t)
	withEnv(t, "ENCRYPTION_KEY", "short")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for bad ENCRYPTION_KEY")
	}
}

func TestLoadRejectsBadDuration(t *testing.T) {
	baseEnv(t)
	withEnv(t, "SESSION_IDLE_TIMEOUT", "not-a-duration")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for bad duration")
	}
}

func TestLoadRejectsUIPreviewInProduction(t *testing.T) {
	baseEnv(t)
	withEnv(t, "APP_ENV", "production")
	withEnv(t, "UI_PREVIEW", "true")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for UI_PREVIEW in production")
	}
}
