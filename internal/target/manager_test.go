package target

import (
	"context"
	"strings"
	"testing"
	"time"

	"leafygo/internal/domain"
	"leafygo/internal/secret"
)

func TestOptionsDefaults(t *testing.T) {
	opts := Options{}
	opts.applyDefaults()

	if opts.MaxConns != 4 {
		t.Errorf("expected MaxConns=4, got %d", opts.MaxConns)
	}
	if opts.ViewerTimeout != 5*time.Second {
		t.Errorf("expected ViewerTimeout=5s, got %v", opts.ViewerTimeout)
	}
	if opts.ConnectTimeout != 5*time.Second {
		t.Errorf("expected ConnectTimeout=5s, got %v", opts.ConnectTimeout)
	}
	if opts.ApplicationName != "leafygo" {
		t.Errorf("expected ApplicationName='leafygo', got %q", opts.ApplicationName)
	}
}

func TestDecryptFailureDoesNotLeakCiphertext(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	box, err := secret.New(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}

	mgr := New(box, Options{})
	corrupted := []byte("definitely-not-valid-gcm-ciphertext-with-arbitrary-bytes-inside")

	db := domain.DBConnection{
		Host:        "localhost",
		Port:        5432,
		DBName:      "test",
		Username:    "user",
		PasswordEnc: corrupted,
		SSLMode:     "disable",
	}

	ctx := context.Background()

	// Test Run
	_, runErr := mgr.Run(ctx, db, "SELECT 1", 5*time.Second)
	if runErr == nil {
		t.Fatal("expected Run to fail on decryption failure")
	}
	if strings.Contains(runErr.Error(), string(corrupted)) {
		t.Errorf("Run error leaks ciphertext: %s", runErr.Error())
	}

	// Test Pool
	_, poolErr := mgr.Pool(ctx, db)
	if poolErr == nil {
		t.Fatal("expected Pool to fail on decryption failure")
	}
	if strings.Contains(poolErr.Error(), string(corrupted)) {
		t.Errorf("Pool error leaks ciphertext: %s", poolErr.Error())
	}

	// Test Test
	testErr := mgr.Test(ctx, db)
	if testErr == nil {
		t.Fatal("expected Test to fail on decryption failure")
	}
	if strings.Contains(testErr.Error(), string(corrupted)) {
		t.Errorf("Test error leaks ciphertext: %s", testErr.Error())
	}

	// Test Explain
	_, explainErr := mgr.Explain(ctx, db, "SELECT 1", 5*time.Second)
	if explainErr == nil {
		t.Fatal("expected Explain to fail on decryption failure")
	}
	if strings.Contains(explainErr.Error(), string(corrupted)) {
		t.Errorf("Explain error leaks ciphertext: %s", explainErr.Error())
	}
}

func TestStatementTimeoutBudgetCalculation(t *testing.T) {
	calculateRemaining := func(ctx context.Context, timeout time.Duration) time.Duration {
		remaining := timeout
		if deadline, ok := ctx.Deadline(); ok {
			rem := time.Until(deadline)
			if rem <= 0 {
				remaining = time.Millisecond
			} else {
				remaining = rem
			}
		}
		if remaining < time.Millisecond {
			remaining = time.Millisecond
		}
		return remaining
	}

	// Case 1: no deadline -> fallback to timeout
	ctx := context.Background()
	rem := calculateRemaining(ctx, 10*time.Second)
	if rem != 10*time.Second {
		t.Errorf("expected 10s, got %v", rem)
	}

	// Case 2: deadline in future (e.g. 7.5s)
	ctx2, cancel2 := context.WithDeadline(context.Background(), time.Now().Add(7500*time.Millisecond))
	defer cancel2()
	rem2 := calculateRemaining(ctx2, 30*time.Second)
	if rem2 < 7400*time.Millisecond || rem2 > 7600*time.Millisecond {
		t.Errorf("expected ~7500ms, got %v", rem2)
	}

	// Case 3: deadline already passed -> minimum 1ms
	ctx3, cancel3 := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel3()
	rem3 := calculateRemaining(ctx3, 30*time.Second)
	if rem3 != time.Millisecond {
		t.Errorf("expected minimum 1ms, got %v", rem3)
	}
}

func TestExplainOutputTruncatedTo64KiB(t *testing.T) {
	// 1. Smaller than 64 KiB is unchanged
	small := "Seq Scan on users (cost=0.00..1.01 rows=1 width=32)"
	if got := truncateToBytes(small, maxExplainOutputBytes); got != small {
		t.Errorf("expected %q, got %q", small, got)
	}

	// 2. Exactly 64 KiB
	exact := strings.Repeat("A", maxExplainOutputBytes)
	if got := truncateToBytes(exact, maxExplainOutputBytes); len(got) != maxExplainOutputBytes {
		t.Errorf("expected len=%d, got %d", maxExplainOutputBytes, len(got))
	}

	// 3. Larger than 64 KiB is truncated to exactly 64 KiB
	large := strings.Repeat("B", maxExplainOutputBytes+1000)
	gotLarge := truncateToBytes(large, maxExplainOutputBytes)
	if len(gotLarge) != maxExplainOutputBytes {
		t.Errorf("expected len=%d, got %d", maxExplainOutputBytes, len(gotLarge))
	}

	// 4. Truncation does not leave invalid UTF-8 (multibyte boundary)
	prefix := strings.Repeat("X", maxExplainOutputBytes-1)
	multi := prefix + "€€" // € is 3 bytes (0xE2, 0x82, 0xAC)
	gotMulti := truncateToBytes(multi, maxExplainOutputBytes)
	if len(gotMulti) > maxExplainOutputBytes {
		t.Errorf("expected len <= %d, got %d", maxExplainOutputBytes, len(gotMulti))
	}
	if gotMulti != prefix {
		t.Errorf("expected multibyte rune to not be sliced into invalid utf-8, got len %d", len(gotMulti))
	}
}
