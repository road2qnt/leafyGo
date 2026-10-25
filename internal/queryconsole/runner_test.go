package queryconsole_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"leafygo/internal/queryconsole"
)

func TestCalculateStatementTimeout(t *testing.T) {
	// 1. Without context deadline
	to := queryconsole.CalculateStatementTimeout(context.Background(), 10*time.Second)
	if to != 10*time.Second {
		t.Fatalf("expected 10s, got %v", to)
	}

	// 2. Default fallback if limit <= 0
	to = queryconsole.CalculateStatementTimeout(context.Background(), 0)
	if to != 15*time.Second {
		t.Fatalf("expected 15s default, got %v", to)
	}

	// 3. Context deadline shorter than limit
	ctxShort, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	to = queryconsole.CalculateStatementTimeout(ctxShort, 10*time.Second)
	if to > 2*time.Second || to < 1*time.Second {
		t.Fatalf("expected around 2s, got %v", to)
	}

	// 4. Context deadline expired / <= 0 -> clamped to 1ms
	ctxExpired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancelExpired()
	to = queryconsole.CalculateStatementTimeout(ctxExpired, 10*time.Second)
	if to != time.Millisecond {
		t.Fatalf("expected 1ms minimum, got %v", to)
	}
}

func TestClassifyTargetQueryError(t *testing.T) {
	// Nil
	if err := queryconsole.ClassifyTargetQueryError(nil); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	// DeadlineExceeded
	if err := queryconsole.ClassifyTargetQueryError(context.DeadlineExceeded); !errors.Is(err, queryconsole.ErrTimeout) {
		t.Fatalf("expected ErrTimeout for DeadlineExceeded, got %v", err)
	}

	// 57014 statement timeout
	pgTimeout := &pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout"}
	if err := queryconsole.ClassifyTargetQueryError(pgTimeout); !errors.Is(err, queryconsole.ErrTimeout) {
		t.Fatalf("expected ErrTimeout for 57014, got %v", err)
	}

	// 55P03 lock_not_available
	pgLock := &pgconn.PgError{Code: "55P03", Message: "canceling statement due to lock timeout"}
	err := queryconsole.ClassifyTargetQueryError(pgLock)
	var tgtErr *queryconsole.TargetError
	if !errors.As(err, &tgtErr) || tgtErr.Code != "55P03" {
		t.Fatalf("expected TargetError with code 55P03, got %v", err)
	}

	// 25006 read_only_sql_transaction
	pgReadOnly := &pgconn.PgError{Code: "25006", Message: "cannot execute INSERT in a read-only transaction"}
	err = queryconsole.ClassifyTargetQueryError(pgReadOnly)
	if !errors.As(err, &tgtErr) || !tgtErr.IsWriteRejection || tgtErr.Code != "25006" {
		t.Fatalf("expected write rejection for 25006, got %v", err)
	}
	if tgtErr.Message != queryconsole.MsgWriteRejection {
		t.Fatalf("expected %q, got %q", queryconsole.MsgWriteRejection, tgtErr.Message)
	}

	// Syntax error
	pgSyntax := &pgconn.PgError{Code: "42601", Message: "syntax error at or near \"FROM\"", Position: 12}
	err = queryconsole.ClassifyTargetQueryError(pgSyntax)
	if !errors.As(err, &tgtErr) || tgtErr.Code != "42601" || tgtErr.Position != 12 {
		t.Fatalf("expected syntax TargetError with code 42601 and position 12, got %v", err)
	}
}
