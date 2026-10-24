package target

import (
	"net/url"
	"strings"
	"testing"

	"leafygo/internal/domain"
)

func TestBuildDSN(t *testing.T) {
	tests := []struct {
		name     string
		db       domain.DBConnection
		password string
		check    func(t *testing.T, dsn string)
	}{
		{
			name: "special characters in user password and dbname",
			db: domain.DBConnection{
				Host:     "localhost",
				Port:     5432,
				Username: "user@domain.com",
				DBName:   "my db/special#1",
				SSLMode:  "disable",
			},
			password: "p@ss:w/ord?#&=123",
			check: func(t *testing.T, dsn string) {
				u, err := url.Parse(dsn)
				if err != nil {
					t.Fatalf("parse built dsn: %v", err)
				}
				if u.User.Username() != "user@domain.com" {
					t.Errorf("expected username 'user@domain.com', got %q", u.User.Username())
				}
				p, _ := u.User.Password()
				if p != "p@ss:w/ord?#&=123" {
					t.Errorf("expected password to match unescaped, got %q", p)
				}
				if !strings.Contains(dsn, "sslmode=disable") {
					t.Errorf("expected dsn to contain sslmode=disable, got %s", dsn)
				}
			},
		},
		{
			name: "IPv4 host",
			db: domain.DBConnection{
				Host:     "127.0.0.1",
				Port:     5433,
				Username: "postgres",
				DBName:   "testdb",
				SSLMode:  "require",
			},
			password: "secret",
			check: func(t *testing.T, dsn string) {
				u, err := url.Parse(dsn)
				if err != nil {
					t.Fatalf("parse built dsn: %v", err)
				}
				if u.Host != "127.0.0.1:5433" {
					t.Errorf("expected host 127.0.0.1:5433, got %s", u.Host)
				}
			},
		},
		{
			name: "IPv6 literal with brackets",
			db: domain.DBConnection{
				Host:     "[::1]",
				Port:     5432,
				Username: "postgres",
				DBName:   "testdb",
				SSLMode:  "verify-full",
			},
			password: "secret",
			check: func(t *testing.T, dsn string) {
				u, err := url.Parse(dsn)
				if err != nil {
					t.Fatalf("parse built dsn: %v", err)
				}
				if u.Host != "[::1]:5432" {
					t.Errorf("expected host [::1]:5432, got %s", u.Host)
				}
			},
		},
		{
			name: "all valid ssl modes",
			db: domain.DBConnection{
				Host:     "db.example.com",
				Port:     5432,
				Username: "postgres",
				DBName:   "testdb",
				SSLMode:  "disable",
			},
			password: "secret",
			check: func(t *testing.T, _ string) {
				for _, mode := range []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"} {
					conn := domain.DBConnection{
						Host:     "localhost",
						Port:     5432,
						Username: "u",
						DBName:   "db",
						SSLMode:  mode,
					}
					dsn, err := buildDSN(conn, "pass")
					if err != nil {
						t.Errorf("expected sslmode %s to be valid, got err: %v", mode, err)
					}
					if !strings.Contains(dsn, "sslmode="+mode) {
						t.Errorf("expected dsn to contain sslmode=%s", mode)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dsn, err := buildDSN(tc.db, tc.password)
			if err != nil {
				t.Fatalf("buildDSN failed: %v", err)
			}
			tc.check(t, dsn)
		})
	}
}

func TestBuildDSNRejectsSSLModes(t *testing.T) {
	invalidModes := []string{
		"bogus",
		"",
		"   ",
		"Disable",
		"DISABLE",
		"verify_ca",
		"require;",
	}

	for _, mode := range invalidModes {
		t.Run("mode_"+mode, func(t *testing.T) {
			conn := domain.DBConnection{
				Host:     "localhost",
				Port:     5432,
				Username: "user",
				DBName:   "testdb",
				SSLMode:  mode,
			}
			_, err := buildDSN(conn, "pass")
			if err == nil {
				t.Errorf("expected error for invalid sslmode %q, got nil", mode)
			}
		})
	}
}
