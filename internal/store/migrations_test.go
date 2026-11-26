package store

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"leafygo/internal/domain"
)

func TestAdvisoryLockKeyUsesFirstEightBytesBigEndian(t *testing.T) {
	id := uuid.UUID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	got := advisoryLockKey(id)
	want := int64(0x0102030405060708)
	if got != want {
		t.Fatalf("advisoryLockKey = %d; want %d", got, want)
	}
}

func TestStaleCutoff(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	got := staleCutoff(now, 30*time.Second)
	want := now.Add(-30 * time.Second)
	if !got.Equal(want) {
		t.Fatalf("staleCutoff = %v; want %v", got, want)
	}
}

func TestDurationMillisecondsClamp(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want int
	}{
		{in: -time.Second, want: 0},
		{in: time.Duration(0), want: 0},
		{in: 500 * time.Microsecond, want: 0},
		{in: 1500 * time.Millisecond, want: 1500},
	}
	for _, tc := range cases {
		if got := durationMilliseconds(tc.in); got != tc.want {
			t.Fatalf("durationMilliseconds(%v) = %d; want %d", tc.in, got, tc.want)
		}
	}
}

func TestValidateMigrationDefinitionFields(t *testing.T) {
	selfID := uuid.New()
	cases := []struct {
		name    string
		def     domain.MigrationDefinition
		wantErr error
	}{
		{
			name:    "missing title",
			def:     domain.MigrationDefinition{SQLUp: "SELECT 1;", SQLDown: "SELECT 1;"},
			wantErr: ErrInvalidMigrationDef,
		},
		{
			name:    "missing sql_up",
			def:     domain.MigrationDefinition{Title: "t", SQLDown: "SELECT 1;"},
			wantErr: ErrInvalidMigrationDef,
		},
		{
			name:    "reversible without sql_down",
			def:     domain.MigrationDefinition{Title: "t", SQLUp: "SELECT 1;", IsIrreversible: false},
			wantErr: ErrInvalidMigrationDef,
		},
		{
			name:    "irreversible without reason",
			def:     domain.MigrationDefinition{Title: "t", SQLUp: "SELECT 1;", IsIrreversible: true},
			wantErr: ErrInvalidMigrationDef,
		},
		{
			name:    "invalid execution mode",
			def:     domain.MigrationDefinition{Title: "t", SQLUp: "SELECT 1;", SQLDown: "SELECT 1;", ExecutionMode: "unknown"},
			wantErr: ErrInvalidMigrationDef,
		},
		{
			name:    "self replaces_migration_id",
			def:     domain.MigrationDefinition{ID: selfID, Title: "t", SQLUp: "SELECT 1;", SQLDown: "SELECT 1;", ReplacesMigrationID: &selfID},
			wantErr: ErrMigrationNotSupersedable,
		},
		{
			name:    "valid reversible normal default mode",
			def:     domain.MigrationDefinition{Title: "t", SQLUp: "CREATE TABLE t(id int);", SQLDown: "DROP TABLE t;"},
			wantErr: nil,
		},
		{
			name:    "valid irreversible batch mode",
			def:     domain.MigrationDefinition{Title: "t", SQLUp: "DELETE FROM t;", IsIrreversible: true, IrreversibleReason: "lossy delete", ExecutionMode: domain.ExecutionModeBatch},
			wantErr: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.def
			err := validateMigrationDefinitionFields(&d)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if d.ExecutionMode == "" {
					t.Fatal("expected ExecutionMode default to be populated")
				}
			} else if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v; want %v", err, tc.wantErr)
			}
		})
	}
}
