package migration_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leafygo/internal/auth"
	"leafygo/internal/domain"
	"leafygo/internal/migration"
	"leafygo/internal/platform"
	"leafygo/internal/secret"
	"leafygo/internal/store"
	"leafygo/internal/target"
)

func setupIntegration(t *testing.T) (*store.Store, *target.Manager, domain.DBConnection, domain.User, *secret.Box) {
	t.Helper()
	metaURL := os.Getenv("TEST_DATABASE_URL")
	targetURL := os.Getenv("TEST_TARGET_DATABASE_URL")
	if metaURL == "" || targetURL == "" {
		t.Skip("TEST_DATABASE_URL or TEST_TARGET_DATABASE_URL is empty; skipping integration test")
	}

	ctx := context.Background()
	if err := platform.Migrate(ctx, metaURL); err != nil {
		t.Fatalf("migrate metadata db: %v", err)
	}

	pool, err := pgxpool.New(ctx, metaURL)
	if err != nil {
		t.Fatalf("open metadata pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	st := store.New(pool)
	if _, err := pool.Exec(ctx, `
		truncate users, groups, group_members, db_connections, db_access, sessions,
		         migration_history, migration_drafts`); err != nil {
		t.Fatalf("truncate metadata: %v", err)
	}

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	box, err := secret.New(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}

	u, err := url.Parse(targetURL)
	if err != nil {
		t.Fatalf("parse target url: %v", err)
	}
	pass, _ := u.User.Password()
	enc, err := box.Encrypt(pass)
	if err != nil {
		t.Fatalf("encrypt password: %v", err)
	}
	port := 5432
	if p := u.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	}
	sslMode := u.Query().Get("sslmode")
	if sslMode == "" {
		sslMode = "disable"
	}

	hash, err := auth.HashPassword("pass123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	user, err := st.CreateUser(ctx, "migrator", hash, false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	dbConn, err := st.CreateDBConnection(ctx, domain.DBConnection{
		Name:        "target_db",
		Host:        u.Hostname(),
		Port:        port,
		DBName:      strings.TrimPrefix(u.Path, "/"),
		Username:    u.User.Username(),
		PasswordEnc: enc,
		SSLMode:     sslMode,
	})
	if err != nil {
		t.Fatalf("create db connection: %v", err)
	}

	mgr := target.New(box, target.Options{
		ConnectTimeout:  3 * time.Second,
		ViewerTimeout:   3 * time.Second,
		MaxConns:        4,
		ApplicationName: "leafygo",
	})
	t.Cleanup(func() { mgr.Close() })

	return st, mgr, dbConn, user, box
}

func createTargetSchema(t *testing.T, mgr *target.Manager, dbConn domain.DBConnection) string {
	t.Helper()
	schemaName := fmt.Sprintf("test_mig_%s", strings.ReplaceAll(uuid.New().String(), "-", "_"))
	ctx := context.Background()

	_, err := mgr.Run(ctx, dbConn, fmt.Sprintf("CREATE SCHEMA %s;", schemaName), 5*time.Second)
	if err != nil {
		t.Fatalf("create test schema: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = mgr.Run(cleanupCtx, dbConn, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE;", schemaName), 5*time.Second)
	})

	return schemaName
}

func TestExecuteEndToEndSuccess(t *testing.T) {
	st, mgr, dbConn, user, _ := setupIntegration(t)
	schema := createTargetSchema(t, mgr, dbConn)
	ctx := context.Background()

	// 1. Create draft
	draft, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{
		DBID:      dbConn.ID,
		Title:     "Create users table",
		SQLText:   "initial sql",
		CreatedBy: &user.ID,
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}

	svc := migration.NewService(st, mgr, migration.Options{
		AdvisoryLockTimeout: 5 * time.Second,
		MigrationTimeout:    10 * time.Second,
	})

	sql := fmt.Sprintf("CREATE TABLE %s.clients (id int primary key, name text); INSERT INTO %s.clients VALUES (1, 'Alice');", schema, schema)

	in := migration.ExecuteInput{
		Actor:   user,
		DB:      dbConn,
		Level:   domain.LevelEdit,
		DraftID: draft.ID,
		Token:   draft.Revision,
		Title:   "Create clients table",
		SQL:     sql,
	}

	hist, err := svc.ExecuteDraft(ctx, in)
	if err != nil {
		t.Fatalf("ExecuteDraft failed: %v", err)
	}

	if hist.Status != domain.MigrationSuccess {
		t.Errorf("expected history status success, got %s (err: %s)", hist.Status, hist.Error)
	}
	if hist.Version != 1 {
		t.Errorf("expected version 1, got %d", hist.Version)
	}

	// Draft must be deleted
	_, err = st.GetMigrationDraft(ctx, dbConn.ID, draft.ID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected draft to be deleted, got err: %v", err)
	}

	// Target change must be visible
	pool, err := mgr.Pool(ctx, dbConn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s.clients", schema)).Scan(&count); err != nil {
		t.Fatalf("verify table: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

func TestExecuteEndToEndFailureRollsBack(t *testing.T) {
	st, mgr, dbConn, user, _ := setupIntegration(t)
	schema := createTargetSchema(t, mgr, dbConn)
	ctx := context.Background()

	draft, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{
		DBID:      dbConn.ID,
		Title:     "Failing migration",
		SQLText:   "initial",
		CreatedBy: &user.ID,
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}

	svc := migration.NewService(st, mgr, migration.Options{})

	sql := fmt.Sprintf("CREATE TABLE %s.should_rollback (id int); SELECT 1/0;", schema)

	in := migration.ExecuteInput{
		Actor:   user,
		DB:      dbConn,
		Level:   domain.LevelEdit,
		DraftID: draft.ID,
		Token:   draft.Revision,
		Title:   "Failing migration",
		SQL:     sql,
	}

	hist, err := svc.ExecuteDraft(ctx, in)
	if err == nil {
		t.Fatal("expected ExecuteDraft to fail")
	}
	if !errors.Is(err, migration.ErrTarget) {
		t.Errorf("expected ErrTarget, got %v", err)
	}
	if hist.Status != domain.MigrationFailed {
		t.Errorf("expected history status failed, got %s", hist.Status)
	}

	// Draft remains
	savedDraft, err := st.GetMigrationDraft(ctx, dbConn.ID, draft.ID)
	if err != nil {
		t.Fatalf("expected draft to remain, got err: %v", err)
	}
	if savedDraft.Revision != 2 {
		t.Errorf("expected draft revision 2 (N+1), got %d", savedDraft.Revision)
	}

	// Table must NOT exist on target
	pool, err := mgr.Pool(ctx, dbConn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	var exists bool
	checkSQL := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='%s' AND table_name='should_rollback')", schema)
	if err := pool.QueryRow(ctx, checkSQL).Scan(&exists); err != nil {
		t.Fatalf("check table: %v", err)
	}
	if exists {
		t.Errorf("table should_rollback was not rolled back!")
	}
}

func TestExecuteTokenMismatch(t *testing.T) {
	st, mgr, dbConn, user, _ := setupIntegration(t)
	ctx := context.Background()

	draft, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{
		DBID:      dbConn.ID,
		Title:     "Conflict draft",
		SQLText:   "initial",
		CreatedBy: &user.ID,
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}

	svc := migration.NewService(st, mgr, migration.Options{})

	in := migration.ExecuteInput{
		Actor:   user,
		DB:      dbConn,
		Level:   domain.LevelEdit,
		DraftID: draft.ID,
		Token:   999, // mismatch
		Title:   "Conflict draft",
		SQL:     "SELECT 1;",
	}

	_, err = svc.ExecuteDraft(ctx, in)
	if !errors.Is(err, migration.ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}

	histories, err := st.ListMigrationHistory(ctx, dbConn.ID, 10)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(histories) != 0 {
		t.Errorf("expected 0 histories, got %d", len(histories))
	}
}

func TestExecuteBusyOrQueue(t *testing.T) {
	st, mgr, dbConn, user, _ := setupIntegration(t)
	schema := createTargetSchema(t, mgr, dbConn)
	ctx := context.Background()

	draft1, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{DBID: dbConn.ID, Title: "Draft 1", CreatedBy: &user.ID})
	if err != nil {
		t.Fatalf("draft1: %v", err)
	}
	draft2, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{DBID: dbConn.ID, Title: "Draft 2", CreatedBy: &user.ID})
	if err != nil {
		t.Fatalf("draft2: %v", err)
	}

	svc := migration.NewService(st, mgr, migration.Options{
		AdvisoryLockTimeout: 100 * time.Millisecond,
		MigrationTimeout:    5 * time.Second,
	})

	var wg sync.WaitGroup
	errs := make([]error, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		in := migration.ExecuteInput{
			Actor:   user,
			DB:      dbConn,
			Level:   domain.LevelEdit,
			DraftID: draft1.ID,
			Token:   draft1.Revision,
			Title:   "Draft 1",
			SQL:     fmt.Sprintf("SELECT pg_sleep(1); CREATE TABLE %s.q1 (id int);", schema),
		}
		_, errs[0] = svc.ExecuteDraft(ctx, in)
	}()

	time.Sleep(50 * time.Millisecond) // Give first execution time to acquire advisory lock

	go func() {
		defer wg.Done()
		in := migration.ExecuteInput{
			Actor:   user,
			DB:      dbConn,
			Level:   domain.LevelEdit,
			DraftID: draft2.ID,
			Token:   draft2.Revision,
			Title:   "Draft 2",
			SQL:     fmt.Sprintf("CREATE TABLE %s.q2 (id int);", schema),
		}
		_, errs[1] = svc.ExecuteDraft(ctx, in)
	}()

	wg.Wait()

	// One should succeed, the other should fail with ErrLockTimeout or ErrBusy
	if errs[0] != nil && errs[1] != nil {
		t.Errorf("both failed: errs[0]=%v, errs[1]=%v", errs[0], errs[1])
	}
	var failedErr error
	if errs[0] != nil {
		failedErr = errs[0]
	} else if errs[1] != nil {
		failedErr = errs[1]
	}
	if failedErr != nil && !errors.Is(failedErr, migration.ErrLockTimeout) && !errors.Is(failedErr, migration.ErrBusy) {
		t.Errorf("expected ErrLockTimeout or ErrBusy for concurrent run, got %v", failedErr)
	}
}

type blockingRunner struct {
	mu            sync.Mutex
	activeRunners int32
	maxRunners    int32
	blockCh       chan struct{}
}

func (r *blockingRunner) Run(ctx context.Context, db domain.DBConnection, sql string, timeout time.Duration) (time.Duration, error) {
	curr := atomic.AddInt32(&r.activeRunners, 1)
	r.mu.Lock()
	if curr > r.maxRunners {
		r.maxRunners = curr
	}
	r.mu.Unlock()

	<-r.blockCh
	atomic.AddInt32(&r.activeRunners, -1)
	return 10 * time.Millisecond, nil
}

func TestExecuteThreeDBsMaxTwoRunning(t *testing.T) {
	st, _, dbConn, user, _ := setupIntegration(t)
	ctx := context.Background()

	// Create 3 distinct DBs in store
	db2, err := st.CreateDBConnection(ctx, domain.DBConnection{
		Name: "db2", Host: "localhost", Port: 5432, DBName: "db2", Username: "u", PasswordEnc: dbConn.PasswordEnc, SSLMode: "disable",
	})
	if err != nil {
		t.Fatalf("db2: %v", err)
	}
	db3, err := st.CreateDBConnection(ctx, domain.DBConnection{
		Name: "db3", Host: "localhost", Port: 5432, DBName: "db3", Username: "u", PasswordEnc: dbConn.PasswordEnc, SSLMode: "disable",
	})
	if err != nil {
		t.Fatalf("db3: %v", err)
	}

	d1, _ := st.CreateMigrationDraft(ctx, domain.MigrationDraft{DBID: dbConn.ID, Title: "d1", CreatedBy: &user.ID})
	d2, _ := st.CreateMigrationDraft(ctx, domain.MigrationDraft{DBID: db2.ID, Title: "d2", CreatedBy: &user.ID})
	d3, _ := st.CreateMigrationDraft(ctx, domain.MigrationDraft{DBID: db3.ID, Title: "d3", CreatedBy: &user.ID})

	br := &blockingRunner{blockCh: make(chan struct{})}
	svc := migration.NewService(st, br, migration.Options{
		AdvisoryLockTimeout: 600 * time.Millisecond,
		MigrationTimeout:    5 * time.Second,
	})

	var wg sync.WaitGroup
	errs := make([]error, 3)

	dbs := []domain.DBConnection{dbConn, db2, db3}
	drafts := []domain.MigrationDraft{d1, d2, d3}

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			in := migration.ExecuteInput{
				Actor:   user,
				DB:      dbs[idx],
				Level:   domain.LevelEdit,
				DraftID: drafts[idx].ID,
				Token:   drafts[idx].Revision,
				Title:   drafts[idx].Title,
				SQL:     "SELECT 1;",
			}
			_, errs[idx] = svc.ExecuteDraft(ctx, in)
		}(i)
	}

	// Wait until 2 runners are actively executing
	startWait := time.Now()
	for atomic.LoadInt32(&br.activeRunners) < 2 && time.Since(startWait) < 5*time.Second {
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for the 3rd execution to timeout waiting on the semaphore
	time.Sleep(700 * time.Millisecond)

	// Release blocked runners
	close(br.blockCh)
	wg.Wait()

	if br.maxRunners > 2 {
		t.Errorf("max active runners was %d, expected at most 2", br.maxRunners)
	}

	// Exactly one execution should have failed with ErrBusy
	busyCount := 0
	for _, err := range errs {
		if errors.Is(err, migration.ErrBusy) {
			busyCount++
		}
	}
	if busyCount != 1 {
		t.Errorf("expected exactly 1 ErrBusy, got %d (errs: %v)", busyCount, errs)
	}
}

func TestExecuteDraftEditedDuringRun(t *testing.T) {
	st, _, dbConn, user, _ := setupIntegration(t)
	ctx := context.Background()

	draft, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{
		DBID:      dbConn.ID,
		Title:     "Edit during run",
		CreatedBy: &user.ID,
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}

	fakeRn := &mockRunner{
		runFunc: func(ctx context.Context, db domain.DBConnection, sql string, timeout time.Duration) (time.Duration, error) {
			// Edit draft in store while runner is running
			editCtx := context.Background()
			_, editErr := st.UpdateMigrationDraft(editCtx, dbConn.ID, draft.ID, "New title", "New SQL", 2, user.ID)
			if editErr != nil {
				return 0, editErr
			}
			return 10 * time.Millisecond, nil
		},
	}

	svc := migration.NewService(st, fakeRn, migration.Options{})

	in := migration.ExecuteInput{
		Actor:   user,
		DB:      dbConn,
		Level:   domain.LevelEdit,
		DraftID: draft.ID,
		Token:   draft.Revision,
		Title:   "Edit during run",
		SQL:     "SELECT 1;",
	}

	hist, err := svc.ExecuteDraft(ctx, in)
	if err != nil {
		t.Fatalf("ExecuteDraft failed: %v", err)
	}
	if hist.Status != domain.MigrationSuccess {
		t.Errorf("expected success, got %s", hist.Status)
	}

	// Draft must NOT be deleted because revision was bumped to 3 by concurrent edit!
	savedDraft, err := st.GetMigrationDraft(ctx, dbConn.ID, draft.ID)
	if err != nil {
		t.Fatalf("expected draft to remain: %v", err)
	}
	if savedDraft.Revision != 3 {
		t.Errorf("expected revision 3, got %d", savedDraft.Revision)
	}
}

func TestExecuteDraftDeletedDuringRun(t *testing.T) {
	st, _, dbConn, user, _ := setupIntegration(t)
	ctx := context.Background()

	draft, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{
		DBID:      dbConn.ID,
		Title:     "Delete during run",
		CreatedBy: &user.ID,
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}

	fakeRn := &mockRunner{
		runFunc: func(ctx context.Context, db domain.DBConnection, sql string, timeout time.Duration) (time.Duration, error) {
			// Delete draft in store while runner is running
			_ = st.DeleteMigrationDraft(context.Background(), dbConn.ID, draft.ID)
			return 10 * time.Millisecond, nil
		},
	}

	svc := migration.NewService(st, fakeRn, migration.Options{})

	in := migration.ExecuteInput{
		Actor:   user,
		DB:      dbConn,
		Level:   domain.LevelEdit,
		DraftID: draft.ID,
		Token:   draft.Revision,
		Title:   "Delete during run",
		SQL:     "SELECT 1;",
	}

	hist, err := svc.ExecuteDraft(ctx, in)
	if err != nil {
		t.Fatalf("ExecuteDraft failed: %v", err)
	}
	if hist.Status != domain.MigrationSuccess {
		t.Errorf("expected success, got %s", hist.Status)
	}
}

func TestExecuteLockTimeout(t *testing.T) {
	st, mgr, dbConn, user, _ := setupIntegration(t)
	ctx := context.Background()

	draft, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{
		DBID:      dbConn.ID,
		Title:     "Lock timeout draft",
		CreatedBy: &user.ID,
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}

	// Manually hold advisory lock on dbConn.ID in a separate connection
	metaURL := os.Getenv("TEST_DATABASE_URL")
	lockConn, err := pgx.Connect(ctx, metaURL)
	if err != nil {
		t.Fatalf("connect lockConn: %v", err)
	}
	defer lockConn.Close(ctx)

	// Calculate advisory lock key (same as store.advisoryLockKey)
	var key int64
	key = int64(dbConn.ID[0])<<56 | int64(dbConn.ID[1])<<48 | int64(dbConn.ID[2])<<40 | int64(dbConn.ID[3])<<32 |
		int64(dbConn.ID[4])<<24 | int64(dbConn.ID[5])<<16 | int64(dbConn.ID[6])<<8 | int64(dbConn.ID[7])

	var locked bool
	if err := lockConn.QueryRow(ctx, "select pg_try_advisory_lock($1)", key).Scan(&locked); err != nil || !locked {
		t.Fatalf("failed to acquire advisory lock: %v (locked=%v)", err, locked)
	}
	defer func() {
		_, _ = lockConn.Exec(context.Background(), "select pg_advisory_unlock($1)", key)
	}()

	svc := migration.NewService(st, mgr, migration.Options{
		AdvisoryLockTimeout: 50 * time.Millisecond,
	})

	in := migration.ExecuteInput{
		Actor:   user,
		DB:      dbConn,
		Level:   domain.LevelEdit,
		DraftID: draft.ID,
		Token:   draft.Revision,
		Title:   "Lock timeout draft",
		SQL:     "SELECT 1;",
	}

	_, err = svc.ExecuteDraft(ctx, in)
	if !errors.Is(err, migration.ErrLockTimeout) {
		t.Errorf("expected ErrLockTimeout, got %v", err)
	}
}
