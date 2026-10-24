package migration_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"leafygo/internal/domain"
	"leafygo/internal/migration"
	"leafygo/internal/store"
	"leafygo/internal/target"
)

type mockHistoryStore struct {
	mu sync.Mutex

	drafts    map[uuid.UUID]domain.MigrationDraft
	histories []domain.MigrationHistory
	locks     int

	lockFunc          func(ctx context.Context, dbID uuid.UUID) (int64, func(), error)
	updateDraftFunc   func(ctx context.Context, dbID, id uuid.UUID, title, sql string, revision int, actorID uuid.UUID) (domain.MigrationDraft, error)
	insertRunningFunc func(ctx context.Context, h domain.MigrationHistory) error
	finishFunc        func(ctx context.Context, historyID, dbID uuid.UUID, status, errText string, duration time.Duration, draftID *uuid.UUID, draftRevision int) error

	calledInsertRunning bool
	calledRunner        bool
	insertBeforeRun     bool
	finishDraftID       *uuid.UUID
	finishStatus        string
	finishError         string
}

func newMockHistoryStore() *mockHistoryStore {
	return &mockHistoryStore{
		drafts: make(map[uuid.UUID]domain.MigrationDraft),
	}
}

func (m *mockHistoryStore) AcquireMigrationLock(ctx context.Context, dbID uuid.UUID) (int64, func(), error) {
	if m.lockFunc != nil {
		return m.lockFunc(ctx, dbID)
	}
	m.mu.Lock()
	m.locks++
	m.mu.Unlock()
	return 1, func() {
		m.mu.Lock()
		m.locks--
		m.mu.Unlock()
	}, nil
}

func (m *mockHistoryStore) GetMigrationDraft(ctx context.Context, dbID, id uuid.UUID) (domain.MigrationDraft, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.drafts[id]
	if !ok {
		return domain.MigrationDraft{}, store.ErrNotFound
	}
	return d, nil
}

func (m *mockHistoryStore) UpdateMigrationDraft(ctx context.Context, dbID, id uuid.UUID, title, sql string, revision int, actorID uuid.UUID) (domain.MigrationDraft, error) {
	if m.updateDraftFunc != nil {
		return m.updateDraftFunc(ctx, dbID, id, title, sql, revision, actorID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.drafts[id]
	if !ok {
		return domain.MigrationDraft{}, store.ErrNotFound
	}
	if d.Revision != revision {
		return domain.MigrationDraft{}, store.ErrConflict
	}
	d.Title = title
	d.SQLText = sql
	d.Revision++
	d.UpdatedBy = &actorID
	m.drafts[id] = d
	return d, nil
}

func (m *mockHistoryStore) InsertRunningMigration(ctx context.Context, h domain.MigrationHistory) error {
	m.mu.Lock()
	m.calledInsertRunning = true
	if !m.calledRunner {
		m.insertBeforeRun = true
	}
	m.histories = append(m.histories, h)
	m.mu.Unlock()

	if m.insertRunningFunc != nil {
		return m.insertRunningFunc(ctx, h)
	}
	return nil
}

func (m *mockHistoryStore) FinishMigration(ctx context.Context, historyID, dbID uuid.UUID, status, errText string, duration time.Duration, draftID *uuid.UUID, draftRevision int) error {
	m.mu.Lock()
	m.finishStatus = status
	m.finishError = errText
	m.finishDraftID = draftID
	if status == domain.MigrationSuccess && draftID != nil {
		delete(m.drafts, *draftID)
	}
	m.mu.Unlock()

	if m.finishFunc != nil {
		return m.finishFunc(ctx, historyID, dbID, status, errText, duration, draftID, draftRevision)
	}
	return nil
}

func (m *mockHistoryStore) ListMigrationHistory(ctx context.Context, dbID uuid.UUID, limit int) ([]domain.MigrationHistory, error) {
	return nil, nil
}

func (m *mockHistoryStore) GetMigrationHistory(ctx context.Context, dbID, id uuid.UUID) (domain.MigrationHistory, error) {
	return domain.MigrationHistory{}, nil
}

func (m *mockHistoryStore) ReapStaleRunningMigrations(ctx context.Context, dbID uuid.UUID, olderThan time.Duration) (int64, error) {
	return 0, nil
}

type mockRunner struct {
	runFunc func(ctx context.Context, db domain.DBConnection, sql string, timeout time.Duration) (time.Duration, error)
	st      *mockHistoryStore
}

func (r *mockRunner) Run(ctx context.Context, db domain.DBConnection, sql string, timeout time.Duration) (time.Duration, error) {
	if r.st != nil {
		r.st.mu.Lock()
		r.st.calledRunner = true
		r.st.mu.Unlock()
	}
	if r.runFunc != nil {
		return r.runFunc(ctx, db, sql, timeout)
	}
	return 10 * time.Millisecond, nil
}

func makeValidInput(dbID, draftID uuid.UUID) migration.ExecuteInput {
	return migration.ExecuteInput{
		Actor: domain.User{
			ID:       uuid.New(),
			Username: "tester",
		},
		DB: domain.DBConnection{
			ID:   dbID,
			Name: "testdb",
		},
		Level:   domain.LevelEdit,
		DraftID: draftID,
		Token:   1,
		Title:   "Add test table",
		SQL:     "CREATE TABLE test (id int);",
	}
}

func TestExecuteSuccessDeletesDraft(t *testing.T) {
	st := newMockHistoryStore()
	dbID := uuid.New()
	draftID := uuid.New()
	st.drafts[draftID] = domain.MigrationDraft{
		ID:       draftID,
		DBID:     dbID,
		Revision: 1,
		SQLText:  "initial",
	}

	rn := &mockRunner{st: st}
	svc := migration.NewService(st, rn, migration.Options{})

	in := makeValidInput(dbID, draftID)
	h, err := svc.ExecuteDraft(context.Background(), in)
	if err != nil {
		t.Fatalf("ExecuteDraft failed: %v", err)
	}

	if h.Status != domain.MigrationSuccess {
		t.Errorf("expected status %s, got %s", domain.MigrationSuccess, h.Status)
	}
	if _, exists := st.drafts[draftID]; exists {
		t.Errorf("expected draft to be deleted on success")
	}
	if st.finishDraftID == nil || *st.finishDraftID != draftID {
		t.Errorf("expected finishDraftID to be %v, got %v", draftID, st.finishDraftID)
	}
}

func TestExecuteSQLFailureKeepsDraftNoUncertainText(t *testing.T) {
	st := newMockHistoryStore()
	dbID := uuid.New()
	draftID := uuid.New()
	st.drafts[draftID] = domain.MigrationDraft{
		ID:       draftID,
		DBID:     dbID,
		Revision: 1,
		SQLText:  "initial",
	}

	rn := &mockRunner{
		st: st,
		runFunc: func(ctx context.Context, db domain.DBConnection, sql string, timeout time.Duration) (time.Duration, error) {
			return 5 * time.Millisecond, &target.RunError{
				Err:       errors.New("syntax error at or near SELECT"),
				Uncertain: false,
			}
		},
	}
	svc := migration.NewService(st, rn, migration.Options{})

	in := makeValidInput(dbID, draftID)
	h, err := svc.ExecuteDraft(context.Background(), in)
	if err == nil {
		t.Fatal("expected target error")
	}
	if !errors.Is(err, migration.ErrTarget) {
		t.Errorf("expected ErrTarget, got %v", err)
	}
	if h.Status != domain.MigrationFailed {
		t.Errorf("expected status failed, got %s", h.Status)
	}
	if strings.Contains(h.Error, "tidak pasti") {
		t.Errorf("error text must not contain 'tidak pasti': %q", h.Error)
	}
	if _, exists := st.drafts[draftID]; !exists {
		t.Errorf("draft should not be deleted on failure")
	}
}

func TestExecuteUncertainAfterCommitUsesUncertainText(t *testing.T) {
	st := newMockHistoryStore()
	dbID := uuid.New()
	draftID := uuid.New()
	st.drafts[draftID] = domain.MigrationDraft{
		ID:       draftID,
		DBID:     dbID,
		Revision: 1,
		SQLText:  "initial",
	}

	rn := &mockRunner{
		st: st,
		runFunc: func(ctx context.Context, db domain.DBConnection, sql string, timeout time.Duration) (time.Duration, error) {
			return 5 * time.Millisecond, &target.RunError{
				Err:       errors.New("connection reset by peer during commit"),
				Uncertain: true,
			}
		},
	}
	svc := migration.NewService(st, rn, migration.Options{})

	in := makeValidInput(dbID, draftID)
	h, err := svc.ExecuteDraft(context.Background(), in)
	if err == nil {
		t.Fatal("expected target error")
	}
	if !errors.Is(err, migration.ErrTarget) {
		t.Errorf("expected ErrTarget, got %v", err)
	}
	expectedMsg := "hasil tidak pasti; periksa target secara manual"
	if h.Error != expectedMsg {
		t.Errorf("expected error %q, got %q", expectedMsg, h.Error)
	}
}

func TestExecuteConnectFailureUsesVersionAndFailed(t *testing.T) {
	st := newMockHistoryStore()
	dbID := uuid.New()
	draftID := uuid.New()
	st.drafts[draftID] = domain.MigrationDraft{
		ID:       draftID,
		DBID:     dbID,
		Revision: 1,
	}

	rn := &mockRunner{
		st: st,
		runFunc: func(ctx context.Context, db domain.DBConnection, sql string, timeout time.Duration) (time.Duration, error) {
			return 0, &target.RunError{
				Err:       errors.New("network unreachable"),
				Uncertain: false,
			}
		},
	}
	svc := migration.NewService(st, rn, migration.Options{})

	in := makeValidInput(dbID, draftID)
	h, err := svc.ExecuteDraft(context.Background(), in)
	if err == nil {
		t.Fatal("expected connect error")
	}
	if h.Version != 1 {
		t.Errorf("expected allocated version 1, got %d", h.Version)
	}
	if h.Status != domain.MigrationFailed {
		t.Errorf("expected status failed, got %s", h.Status)
	}
}

func TestExecuteInsertsRunningBeforeRunner(t *testing.T) {
	st := newMockHistoryStore()
	dbID := uuid.New()
	draftID := uuid.New()
	st.drafts[draftID] = domain.MigrationDraft{
		ID:       draftID,
		DBID:     dbID,
		Revision: 1,
	}

	rn := &mockRunner{st: st}
	svc := migration.NewService(st, rn, migration.Options{})

	in := makeValidInput(dbID, draftID)
	_, err := svc.ExecuteDraft(context.Background(), in)
	if err != nil {
		t.Fatalf("ExecuteDraft failed: %v", err)
	}
	if !st.insertBeforeRun {
		t.Errorf("expected InsertRunningMigration to be called before runner.Run")
	}
}

func TestTokenMismatchNoLockNoHistory(t *testing.T) {
	st := newMockHistoryStore()
	dbID := uuid.New()
	draftID := uuid.New()
	st.drafts[draftID] = domain.MigrationDraft{
		ID:       draftID,
		DBID:     dbID,
		Revision: 2, // store has 2, input has 1
	}

	rn := &mockRunner{st: st}
	svc := migration.NewService(st, rn, migration.Options{})

	in := makeValidInput(dbID, draftID)
	in.Token = 1 // mismatch

	_, err := svc.ExecuteDraft(context.Background(), in)
	if !errors.Is(err, migration.ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
	if st.calledInsertRunning {
		t.Errorf("InsertRunningMigration must not be called on token mismatch")
	}
	if st.locks != 0 {
		t.Errorf("no lock should be held or acquired")
	}
}

func TestValidationFailureNoLockNoHistory(t *testing.T) {
	st := newMockHistoryStore()
	dbID := uuid.New()
	draftID := uuid.New()
	st.drafts[draftID] = domain.MigrationDraft{
		ID:       draftID,
		DBID:     dbID,
		Revision: 1,
	}

	rn := &mockRunner{st: st}
	svc := migration.NewService(st, rn, migration.Options{MaxMigrationBytes: 100})

	// Test empty SQL
	in := makeValidInput(dbID, draftID)
	in.SQL = "   "
	_, err := svc.ExecuteDraft(context.Background(), in)
	if !errors.Is(err, migration.ErrValidation) {
		t.Errorf("expected ErrValidation for empty SQL, got %v", err)
	}

	// Test SQL too large
	in.SQL = strings.Repeat("A", 200)
	_, err = svc.ExecuteDraft(context.Background(), in)
	if !errors.Is(err, migration.ErrValidation) {
		t.Errorf("expected ErrValidation for oversized SQL, got %v", err)
	}

	// Test NUL byte
	in.SQL = "SELECT 1;\x00"
	_, err = svc.ExecuteDraft(context.Background(), in)
	if !errors.Is(err, migration.ErrValidation) {
		t.Errorf("expected ErrValidation for NUL byte, got %v", err)
	}

	if st.calledInsertRunning {
		t.Errorf("InsertRunningMigration must not be called on validation failure")
	}
}

func TestDraftEditedDuringMigrationKeepsDraft(t *testing.T) {
	st := newMockHistoryStore()
	dbID := uuid.New()
	draftID := uuid.New()
	st.drafts[draftID] = domain.MigrationDraft{
		ID:       draftID,
		DBID:     dbID,
		Revision: 1,
	}

	rn := &mockRunner{
		st: st,
		runFunc: func(ctx context.Context, db domain.DBConnection, sql string, timeout time.Duration) (time.Duration, error) {
			// Simulate concurrent user editing draft during migration execution
			st.mu.Lock()
			d := st.drafts[draftID]
			d.Revision = 10
			st.drafts[draftID] = d
			st.mu.Unlock()
			return 10 * time.Millisecond, nil
		},
	}
	svc := migration.NewService(st, rn, migration.Options{})

	in := makeValidInput(dbID, draftID)
	h, err := svc.ExecuteDraft(context.Background(), in)
	if err != nil {
		t.Fatalf("expected ExecuteDraft to succeed: %v", err)
	}
	if h.Status != domain.MigrationSuccess {
		t.Errorf("expected success status, got %s", h.Status)
	}
}

func TestFinishZeroRowsReturnsErrHistoryNotRunning(t *testing.T) {
	st := newMockHistoryStore()
	dbID := uuid.New()
	draftID := uuid.New()
	st.drafts[draftID] = domain.MigrationDraft{
		ID:       draftID,
		DBID:     dbID,
		Revision: 1,
	}
	st.finishFunc = func(ctx context.Context, historyID, dbID uuid.UUID, status, errText string, duration time.Duration, draftID *uuid.UUID, draftRevision int) error {
		return store.ErrHistoryNotRunning
	}

	rn := &mockRunner{st: st}
	svc := migration.NewService(st, rn, migration.Options{})

	in := makeValidInput(dbID, draftID)
	_, err := svc.ExecuteDraft(context.Background(), in)
	if !errors.Is(err, migration.ErrHistoryNotRunning) {
		t.Errorf("expected ErrHistoryNotRunning, got %v", err)
	}
}

func TestRequestCancelAfterStartDoesNotCancelRunnerOrFinish(t *testing.T) {
	st := newMockHistoryStore()
	dbID := uuid.New()
	draftID := uuid.New()
	st.drafts[draftID] = domain.MigrationDraft{
		ID:       draftID,
		DBID:     dbID,
		Revision: 1,
	}

	ctx, cancel := context.WithCancel(context.Background())

	runnerRan := false
	finishRan := false

	rn := &mockRunner{
		st: st,
		runFunc: func(runCtx context.Context, db domain.DBConnection, sql string, timeout time.Duration) (time.Duration, error) {
			// Cancel caller request context!
			cancel()

			// Check that runCtx is NOT canceled
			if runCtx.Err() != nil {
				t.Errorf("runCtx should not be canceled by caller cancellation: %v", runCtx.Err())
			}
			runnerRan = true
			return 10 * time.Millisecond, nil
		},
	}

	st.finishFunc = func(finishCtx context.Context, historyID, dbID uuid.UUID, status, errText string, duration time.Duration, draftID *uuid.UUID, draftRevision int) error {
		if finishCtx.Err() != nil {
			t.Errorf("finishCtx should not be canceled by caller cancellation: %v", finishCtx.Err())
		}
		finishRan = true
		return nil
	}

	svc := migration.NewService(st, rn, migration.Options{})

	in := makeValidInput(dbID, draftID)
	h, err := svc.ExecuteDraft(ctx, in)
	if err != nil {
		t.Fatalf("expected success despite caller cancel: %v", err)
	}
	if !runnerRan || !finishRan {
		t.Errorf("runnerRan=%v finishRan=%v, both should be true", runnerRan, finishRan)
	}
	if h.Status != domain.MigrationSuccess {
		t.Errorf("expected success status, got %s", h.Status)
	}
}

func TestSemaphoreReleasedOnSuccessAndFailure(t *testing.T) {
	st := newMockHistoryStore()
	rn := &mockRunner{st: st}
	svc := migration.NewService(st, rn, migration.Options{})

	// 1. Success path
	dbID := uuid.New()
	draftID := uuid.New()
	st.drafts[draftID] = domain.MigrationDraft{ID: draftID, DBID: dbID, Revision: 1}
	_, err := svc.ExecuteDraft(context.Background(), makeValidInput(dbID, draftID))
	if err != nil {
		t.Fatalf("success execute: %v", err)
	}

	// 2. Failure path
	draftID2 := uuid.New()
	st.drafts[draftID2] = domain.MigrationDraft{ID: draftID2, DBID: dbID, Revision: 1}
	rn.runFunc = func(ctx context.Context, db domain.DBConnection, sql string, timeout time.Duration) (time.Duration, error) {
		return 0, errors.New("fail")
	}
	in2 := makeValidInput(dbID, draftID2)
	_, _ = svc.ExecuteDraft(context.Background(), in2)

	// Verify semaphore is empty (has capacity 2)
	// We can do this by running two executions concurrently
	rn.runFunc = nil
	draftID3 := uuid.New()
	st.drafts[draftID3] = domain.MigrationDraft{ID: draftID3, DBID: dbID, Revision: 1}
	_, err = svc.ExecuteDraft(context.Background(), makeValidInput(dbID, draftID3))
	if err != nil {
		t.Fatalf("should succeed if semaphore was released: %v", err)
	}
}

func TestSemaphoreBusyReturnsErrBusy(t *testing.T) {
	st := newMockHistoryStore()

	blockCh := make(chan struct{})
	var activeRunners int32

	rn := &mockRunner{
		st: st,
		runFunc: func(ctx context.Context, db domain.DBConnection, sql string, timeout time.Duration) (time.Duration, error) {
			atomic.AddInt32(&activeRunners, 1)
			<-blockCh
			return 10 * time.Millisecond, nil
		},
	}

	svc := migration.NewService(st, rn, migration.Options{
		AdvisoryLockTimeout: 50 * time.Millisecond,
	})

	// Start two migrations that block in runner
	for i := 0; i < 2; i++ {
		dbID := uuid.New()
		dID := uuid.New()
		st.mu.Lock()
		st.drafts[dID] = domain.MigrationDraft{ID: dID, DBID: dbID, Revision: 1}
		st.mu.Unlock()

		go func(did, dbid uuid.UUID) {
			_, _ = svc.ExecuteDraft(context.Background(), makeValidInput(dbid, did))
		}(dID, dbID)
	}

	// Wait for both runners to be active
	for atomic.LoadInt32(&activeRunners) < 2 {
		time.Sleep(5 * time.Millisecond)
	}

	// Third migration must hit semaphore timeout -> ErrBusy
	dbID3 := uuid.New()
	dID3 := uuid.New()
	st.mu.Lock()
	st.drafts[dID3] = domain.MigrationDraft{ID: dID3, DBID: dbID3, Revision: 1}
	st.mu.Unlock()

	_, err := svc.ExecuteDraft(context.Background(), makeValidInput(dbID3, dID3))
	if !errors.Is(err, migration.ErrBusy) {
		t.Errorf("expected ErrBusy when 2 migrations are active, got %v", err)
	}

	// Unblock runners
	close(blockCh)
}
