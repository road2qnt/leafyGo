package main

import (
	"strings"
	"testing"
)

func TestMigrationSQL(t *testing.T) {
	up, down := migrationSQL(7, 0)
	if up != "insert into k6_events (migration_seq) values (7);" {
		t.Fatalf("up = %q", up)
	}
	if down != "delete from k6_events where migration_seq = 7;" {
		t.Fatalf("down = %q", down)
	}

	up, _ = migrationSQL(7, 150)
	if !strings.HasPrefix(up, "select pg_sleep(0.150);\n") || !strings.HasSuffix(up, "values (7);") {
		t.Fatalf("up with work = %q", up)
	}
}

func TestSplitAddr(t *testing.T) {
	host, port, err := splitAddr("targets-db:5432")
	if err != nil || host != "targets-db" || port != 5432 {
		t.Fatalf("got %q %d %v", host, port, err)
	}
	for _, bad := range []string{"targets-db", "targets-db:0", "targets-db:x"} {
		if _, _, err := splitAddr(bad); err == nil {
			t.Fatalf("%q: expected error", bad)
		}
	}
}

func TestDecodeKey(t *testing.T) {
	for _, raw := range []string{
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		strings.Repeat("00", 32),
	} {
		if b, err := decodeKey(raw); err != nil || len(b) != 32 {
			t.Fatalf("%q: %v", raw, err)
		}
	}
	if _, err := decodeKey("short"); err == nil {
		t.Fatal("expected error for a short key")
	}
}
