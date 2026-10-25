package queryconsole_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"leafygo/internal/domain"
	"leafygo/internal/queryconsole"
)

type mockRunner struct {
	mu      sync.Mutex
	calls   int
	lastSQL string
	blockCh chan struct{}
	result  queryconsole.Result
	err     error
}

func (m *mockRunner) Execute(ctx context.Context, db domain.DBConnection, sql string, limits queryconsole.Limits) (queryconsole.Result, error) {
	m.mu.Lock()
	m.calls++
	m.lastSQL = sql
	block := m.blockCh
	m.mu.Unlock()

	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return queryconsole.Result{}, ctx.Err()
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.result, m.err
}

func TestServiceAccessLevels(t *testing.T) {
	mock := &mockRunner{
		result: queryconsole.Result{
			Columns:  []string{"id"},
			Rows:     [][]queryconsole.Cell{{{Text: "1"}}},
			RowCount: 1,
		},
	}
	svc := queryconsole.NewService(mock, nil, queryconsole.ServiceOptions{})

	actor := domain.User{ID: uuid.New()}
	db := domain.DBConnection{ID: uuid.New()}

	// LevelNone rejected
	_, err := svc.Execute(context.Background(), queryconsole.ExecuteInput{
		Actor: actor,
		DB:    db,
		Level: domain.LevelNone,
		SQL:   "SELECT 1",
	})
	if !errors.Is(err, queryconsole.ErrForbidden) {
		t.Fatalf("expected ErrForbidden for LevelNone, got %v", err)
	}
	if mock.calls != 0 {
		t.Fatalf("runner should not have been called for LevelNone")
	}

	// LevelView allowed
	res, err := svc.Execute(context.Background(), queryconsole.ExecuteInput{
		Actor: actor,
		DB:    db,
		Level: domain.LevelView,
		SQL:   "SELECT 1",
	})
	if err != nil {
		t.Fatalf("unexpected error for LevelView: %v", err)
	}
	if res.RowCount != 1 {
		t.Fatalf("expected 1 row, got %d", res.RowCount)
	}

	// LevelEdit allowed
	_, err = svc.Execute(context.Background(), queryconsole.ExecuteInput{
		Actor: actor,
		DB:    db,
		Level: domain.LevelEdit,
		SQL:   "SELECT 1",
	})
	if err != nil {
		t.Fatalf("unexpected error for LevelEdit: %v", err)
	}
}

func TestServiceGuardRejectionBeforeRunner(t *testing.T) {
	mock := &mockRunner{}
	svc := queryconsole.NewService(mock, nil, queryconsole.ServiceOptions{})

	_, err := svc.Execute(context.Background(), queryconsole.ExecuteInput{
		Actor: domain.User{ID: uuid.New()},
		DB:    domain.DBConnection{ID: uuid.New()},
		Level: domain.LevelView,
		SQL:   "DROP TABLE users",
	})
	if err == nil {
		t.Fatal("expected guard rejection error, got nil")
	}
	var tgtErr *queryconsole.TargetError
	if !errors.As(err, &tgtErr) || !tgtErr.IsWriteRejection {
		t.Fatalf("expected write rejection TargetError, got %v", err)
	}
	if mock.calls != 0 {
		t.Fatalf("runner should not be invoked for disallowed SQL")
	}
}

func TestServiceConcurrencyLimit(t *testing.T) {
	blockCh := make(chan struct{})
	mock := &mockRunner{blockCh: blockCh}
	svc := queryconsole.NewService(mock, nil, queryconsole.ServiceOptions{
		MaxConcurrent:       1,
		AdvisoryLockTimeout: 50 * time.Millisecond,
	})

	actor := domain.User{ID: uuid.New()}
	db := domain.DBConnection{ID: uuid.New()}

	// First query starts and blocks
	go func() {
		_, _ = svc.Execute(context.Background(), queryconsole.ExecuteInput{
			Actor: actor,
			DB:    db,
			Level: domain.LevelView,
			SQL:   "SELECT 1",
		})
	}()

	// Wait for slot to be acquired
	time.Sleep(10 * time.Millisecond)

	// Second query should time out waiting for slot and return ErrBusy
	_, err := svc.Execute(context.Background(), queryconsole.ExecuteInput{
		Actor: actor,
		DB:    db,
		Level: domain.LevelView,
		SQL:   "SELECT 2",
	})
	if !errors.Is(err, queryconsole.ErrBusy) {
		t.Fatalf("expected ErrBusy, got %v", err)
	}

	// Unblock first query
	close(blockCh)
}

func TestServiceLoggingDoesNotLeakSQLOrRows(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	mock := &mockRunner{
		result: queryconsole.Result{
			Columns:  []string{"secret_col"},
			Rows:     [][]queryconsole.Cell{{{Text: "secret_value_123"}}},
			RowCount: 1,
		},
	}
	svc := queryconsole.NewService(mock, logger, queryconsole.ServiceOptions{})

	secretSQL := "SELECT 'super_secret_query_string'"
	_, err := svc.Execute(context.Background(), queryconsole.ExecuteInput{
		Actor: domain.User{ID: uuid.New()},
		DB:    domain.DBConnection{ID: uuid.New()},
		Level: domain.LevelView,
		SQL:   secretSQL,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logOutput := buf.String()
	if strings.Contains(logOutput, "super_secret_query_string") {
		t.Fatalf("log output leaked SQL query: %s", logOutput)
	}
	if strings.Contains(logOutput, "secret_value_123") {
		t.Fatalf("log output leaked row data: %s", logOutput)
	}
	if !strings.Contains(logOutput, "status=success") {
		t.Fatalf("expected status=success in log output: %s", logOutput)
	}
}
