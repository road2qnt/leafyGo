package target_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"leafygo/internal/domain"
	"leafygo/internal/secret"
	"leafygo/internal/target"
)

func setupTestTarget(t *testing.T) (*target.Manager, domain.DBConnection, *secret.Box) {
	t.Helper()
	rawURL := os.Getenv("TEST_TARGET_DATABASE_URL")
	if rawURL == "" {
		t.Skip("TEST_TARGET_DATABASE_URL is empty; skipping integration test")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse target url: %v", err)
	}

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	box, err := secret.New(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}

	password, _ := u.User.Password()
	enc, err := box.Encrypt(password)
	if err != nil {
		t.Fatalf("box.Encrypt: %v", err)
	}

	port := 5432
	if p := u.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	}

	sslMode := u.Query().Get("sslmode")
	if sslMode == "" {
		sslMode = "disable"
	}

	dbID := uuid.New()
	dbConn := domain.DBConnection{
		ID:          dbID,
		Name:        "target_test",
		Host:        u.Hostname(),
		Port:        port,
		DBName:      strings.TrimPrefix(u.Path, "/"),
		Username:    u.User.Username(),
		PasswordEnc: enc,
		SSLMode:     sslMode,
		UpdatedAt:   time.Now().UTC(),
	}

	mgr := target.New(box, target.Options{
		ConnectTimeout:  3 * time.Second,
		ViewerTimeout:   3 * time.Second,
		MaxConns:        4,
		ApplicationName: "leafygo",
	})
	t.Cleanup(func() { mgr.Close() })

	return mgr, dbConn, box
}

func createTestSchema(t *testing.T, dbConn domain.DBConnection, mgr *target.Manager) string {
	t.Helper()
	schemaName := fmt.Sprintf("test_schema_%s", strings.ReplaceAll(uuid.New().String(), "-", "_"))
	ctx := context.Background()

	_, err := mgr.Run(ctx, dbConn, fmt.Sprintf("CREATE SCHEMA %s; SET search_path TO %s, public;", schemaName, schemaName), 10*time.Second)
	if err != nil {
		t.Fatalf("create test schema: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = mgr.Run(cleanupCtx, dbConn, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE;", schemaName), 10*time.Second)
	})

	return schemaName
}

func TestPoolCachesAndInvalidates(t *testing.T) {
	mgr, dbConn, _ := setupTestTarget(t)
	ctx := context.Background()

	p1, err := mgr.Pool(ctx, dbConn)
	if err != nil {
		t.Fatalf("Pool 1: %v", err)
	}

	p2, err := mgr.Pool(ctx, dbConn)
	if err != nil {
		t.Fatalf("Pool 2: %v", err)
	}

	if p1 != p2 {
		t.Errorf("expected same pool pointer from cache, got %p and %p", p1, p2)
	}

	// Update UpdatedAt -> returns new pool
	dbConnUpdated := dbConn
	dbConnUpdated.UpdatedAt = dbConn.UpdatedAt.Add(1 * time.Minute)

	p3, err := mgr.Pool(ctx, dbConnUpdated)
	if err != nil {
		t.Fatalf("Pool 3: %v", err)
	}
	if p1 == p3 {
		t.Errorf("expected new pool after UpdatedAt changed, got same pointer")
	}

	// Invalidate closes pool
	mgr.Invalidate(dbConn.ID)
	// After invalidate, pool can be recreated
	p4, err := mgr.Pool(ctx, dbConnUpdated)
	if err != nil {
		t.Fatalf("Pool 4 after invalidate: %v", err)
	}
	if p3 == p4 {
		t.Errorf("expected new pool after Invalidate, got same pointer")
	}
}

func TestTestConnection(t *testing.T) {
	mgr, dbConn, box := setupTestTarget(t)
	ctx := context.Background()

	// Valid connection
	if err := mgr.Test(ctx, dbConn); err != nil {
		t.Fatalf("expected Test to succeed, got %v", err)
	}

	// Invalid password
	wrongEnc, err := box.Encrypt("wrong_password_12345")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	badConn := dbConn
	badConn.PasswordEnc = wrongEnc

	err = mgr.Test(ctx, badConn)
	if err == nil {
		t.Fatal("expected Test to fail with wrong password")
	}
	if strings.Contains(err.Error(), "wrong_password_12345") {
		t.Errorf("error leaked password: %v", err)
	}
}

func TestRunCommitAndRollback(t *testing.T) {
	mgr, dbConn, _ := setupTestTarget(t)
	schema := createTestSchema(t, dbConn, mgr)
	ctx := context.Background()

	// Success run
	sql1 := fmt.Sprintf(`
		CREATE TABLE %s.items (id int primary key, name text);
		INSERT INTO %s.items VALUES (1, 'item1');
	`, schema, schema)

	dur, err := mgr.Run(ctx, dbConn, sql1, 10*time.Second)
	if err != nil {
		t.Fatalf("expected Run to succeed: %v", err)
	}
	if dur <= 0 {
		t.Errorf("expected positive duration, got %v", dur)
	}

	// Verify table exists via viewer pool
	pool, err := mgr.Pool(ctx, dbConn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s.items", schema)).Scan(&count); err != nil {
		t.Fatalf("query items: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 item, got %d", count)
	}

	// Failure run: statement 2 fails -> rollback
	sql2 := fmt.Sprintf(`
		CREATE TABLE %s.must_rollback (id int);
		SELECT 1 / 0;
	`, schema)

	_, err = mgr.Run(ctx, dbConn, sql2, 10*time.Second)
	if err == nil {
		t.Fatal("expected error due to division by zero")
	}

	var runErr *target.RunError
	if errors.As(err, &runErr) && runErr.Uncertain {
		t.Errorf("expected Uncertain=false for statement error")
	}

	// Verify must_rollback does NOT exist
	var exists bool
	checkSQL := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='%s' AND table_name='must_rollback')", schema)
	if err := pool.QueryRow(ctx, checkSQL).Scan(&exists); err != nil {
		t.Fatalf("check table: %v", err)
	}
	if exists {
		t.Errorf("expected must_rollback table to be rolled back")
	}
}

func TestRunMultiStatementSimpleProtocol(t *testing.T) {
	mgr, dbConn, _ := setupTestTarget(t)
	schema := createTestSchema(t, dbConn, mgr)
	ctx := context.Background()

	sql := fmt.Sprintf(`
		CREATE TABLE %s.multi1 (id int);
		CREATE TABLE %s.multi2 (id int);
	`, schema, schema)

	_, err := mgr.Run(ctx, dbConn, sql, 10*time.Second)
	if err != nil {
		t.Fatalf("multi statement run failed: %v", err)
	}

	pool, err := mgr.Pool(ctx, dbConn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	var c1, c2 bool
	_ = pool.QueryRow(ctx, fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='%s' AND table_name='multi1')", schema)).Scan(&c1)
	_ = pool.QueryRow(ctx, fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema='%s' AND table_name='multi2')", schema)).Scan(&c2)
	if !c1 || !c2 {
		t.Errorf("expected both tables to exist: multi1=%v, multi2=%v", c1, c2)
	}
}

func TestRunNonTransactionalDDLFails(t *testing.T) {
	mgr, dbConn, _ := setupTestTarget(t)
	schema := createTestSchema(t, dbConn, mgr)
	ctx := context.Background()

	// CREATE INDEX CONCURRENTLY cannot run inside a transaction block
	sql := fmt.Sprintf(`
		CREATE TABLE %s.idx_test (id int);
		CREATE INDEX CONCURRENTLY idx_test_id ON %s.idx_test(id);
	`, schema, schema)

	_, err := mgr.Run(ctx, dbConn, sql, 10*time.Second)
	if err == nil {
		t.Fatal("expected CREATE INDEX CONCURRENTLY to fail in transaction block")
	}

	var runErr *target.RunError
	if errors.As(err, &runErr) && runErr.Uncertain {
		t.Errorf("expected Uncertain=false for non-transactional statement failure")
	}
}

func TestRunStatementTimeoutUsesRemainingBudget(t *testing.T) {
	mgr, dbConn, _ := setupTestTarget(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// pg_sleep for 5s will exceed remaining statement timeout budget
	sql := "SELECT pg_sleep(5);"
	start := time.Now()
	_, err := mgr.Run(ctx, dbConn, sql, 10*time.Second)
	dur := time.Since(start)

	if err == nil {
		t.Fatal("expected statement timeout error")
	}
	if dur > 4*time.Second {
		t.Errorf("took too long (%v), statement_timeout was not enforced by budget", dur)
	}
}

func TestSetDoesNotLeakToViewerPool(t *testing.T) {
	mgr, dbConn, _ := setupTestTarget(t)
	ctx := context.Background()

	// Run executes SET application_name='leak'
	_, err := mgr.Run(ctx, dbConn, "SET application_name='leak';", 10*time.Second)
	if err != nil {
		t.Fatalf("Run set application_name: %v", err)
	}

	// Viewer pool connection should still have 'leafygo'
	pool, err := mgr.Pool(ctx, dbConn)
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}

	var appName string
	if err := pool.QueryRow(ctx, "SHOW application_name").Scan(&appName); err != nil {
		t.Fatalf("query application_name: %v", err)
	}
	if appName != "leafygo" {
		t.Errorf("expected viewer pool application_name to be 'leafygo', got %q", appName)
	}
}

func TestRunConnectFailureIsNotUncertain(t *testing.T) {
	mgr, dbConn, _ := setupTestTarget(t)
	ctx := context.Background()

	badConn := dbConn
	badConn.Host = "127.0.0.1"
	badConn.Port = 59999 // unused port

	_, err := mgr.Run(ctx, badConn, "SELECT 1;", 1*time.Second)
	if err == nil {
		t.Fatal("expected connect error")
	}

	var runErr *target.RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("expected target.RunError, got %T: %v", err, err)
	}
	if runErr.Uncertain {
		t.Errorf("expected Uncertain=false for connect error")
	}
}

func TestInvalidateConcurrentWithPool(t *testing.T) {
	mgr, dbConn, _ := setupTestTarget(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = mgr.Pool(ctx, dbConn)
		}()
		go func() {
			defer wg.Done()
			mgr.Invalidate(dbConn.ID)
		}()
	}
	wg.Wait()
}
