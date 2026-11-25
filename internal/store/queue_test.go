package store

import (
	"testing"
	"time"
)

func TestRetryBackoff(t *testing.T) {
	cases := map[int]time.Duration{
		-1: 2 * time.Second,
		0:  2 * time.Second,
		1:  4 * time.Second,
		2:  8 * time.Second,
		4:  32 * time.Second,
		5:  time.Minute,
		50: time.Minute,
	}
	for retries, want := range cases {
		if got := RetryBackoff(retries); got != want {
			t.Errorf("RetryBackoff(%d) = %v; want %v", retries, got, want)
		}
	}
}
