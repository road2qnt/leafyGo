package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
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
