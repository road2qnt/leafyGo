package config

import (
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
