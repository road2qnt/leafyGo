package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"leafygo/internal/domain"
	"leafygo/internal/store"
)

const testLeaseTTL = 15 * time.Second

type execFixture struct {
	dbID        uuid.UUID
	migrationID uuid.UUID
	proposalID  uuid.UUID
	executionID uuid.UUID
	rootID      uuid.UUID
	action      string
	mode        string
}

func newMigration(t *testing.T, st *store.Store, dbID uuid.UUID, mode string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := st.Pool().Exec(context.Background(), `
		insert into migration_definitions (id, db_id, seq_version, effective_seq, title, sql_up, sql_down, execution_mode)
		select $1, $2, coalesce(max(seq_version), 0) + 1, coalesce(max(seq_version), 0) + 1, 'm', 'select 1', 'select 1', $3
		from migration_definitions where db_id = $2`, id, dbID, mode)
	if err != nil {
		t.Fatalf("insert migration definition: %v", err)
	}
	return id
}

// Inserts a queued proposal and execution. root nil makes the execution its own root.
func newExecution(t *testing.T, st *store.Store, dbID, migrationID uuid.UUID, action, mode string, root, target *uuid.UUID) execFixture {
	t.Helper()
	ctx := context.Background()
	f := execFixture{dbID: dbID, migrationID: migrationID, proposalID: uuid.New(), executionID: uuid.New(), action: action, mode: mode}
	f.rootID = f.executionID
	if root != nil {
		f.rootID = *root
	}
	if _, err := st.Pool().Exec(ctx, `
		insert into migration_proposals (id, db_id, migration_id, migration_revision, action_type, target_execution_id, status)
		values ($1, $2, $3, 1, $4, $5, 'queued')`, f.proposalID, dbID, migrationID, action, target); err != nil {
		t.Fatalf("insert proposal: %v", err)
	}
	if _, err := st.Pool().Exec(ctx, `
		insert into migration_executions (
			execution_id, root_execution_id, db_id, migration_id, proposal_id, migration_revision, action_type,
			executed_sql, executed_by_username, db_name_snapshot, migration_title_snapshot, status)
		values ($1, $2, $3, $4, $5, 1, $6, 'select 1', 'tester', 'db', 'm', 'queued')`,
		f.executionID, f.rootID, dbID, migrationID, f.proposalID, action); err != nil {
		t.Fatalf("insert execution: %v", err)
	}
	return f
}

func (f execFixture) input() store.EnqueueInput {
	return store.EnqueueInput{
		DBID: f.dbID, ProposalID: f.proposalID, MigrationID: f.migrationID, ExecutionID: f.executionID,
		RootExecutionID: f.rootID, ActionType: f.action, ExecutionMode: f.mode, CorrelationID: "corr-test",
	}
}

func enqueue(t *testing.T, st *store.Store, f execFixture) domain.MigrationJob {
	t.Helper()
	ctx := context.Background()
	tx, err := st.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	job, err := st.EnqueueMigrationJobTx(ctx, tx, f.input())
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return job
}

func newApplyJob(t *testing.T, st *store.Store, dbID uuid.UUID) (execFixture, domain.MigrationJob) {
	t.Helper()
	f := newExecution(t, st, dbID, newMigration(t, st, dbID, domain.ModeNormal), domain.ActionApply, domain.ModeNormal, nil, nil)
	return f, enqueue(t, st, f)
}

func claim(t *testing.T, st *store.Store, workerID string) *store.ClaimedJob {
	t.Helper()
	c, err := st.ClaimNextJob(context.Background(), workerID, testLeaseTTL)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	return c
}

func statusOf(t *testing.T, st *store.Store, query string, id uuid.UUID) string {
	t.Helper()
	var s string
	if err := st.Pool().QueryRow(context.Background(), query, id).Scan(&s); err != nil {
		t.Fatalf("status query: %v", err)
	}
	return s
}

func proposalStatus(t *testing.T, st *store.Store, id uuid.UUID) string {
	return statusOf(t, st, `select status from migration_proposals where id = $1`, id)
}

func executionStatus(t *testing.T, st *store.Store, id uuid.UUID) string {
	return statusOf(t, st, `select status from migration_executions where execution_id = $1`, id)
}

func TestEnqueueCreatesQueuedJobAndNotifies(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	db := createDB(t, st, "orders")

	conn, err := st.Pool().Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "listen "+store.JobsChannel); err != nil {
		t.Fatalf("listen: %v", err)
	}

	f, job := newApplyJob(t, st, db.ID)
	if job.Status != domain.JobQueued || job.ExecutionID != f.executionID || job.RootExecutionID != f.executionID {
		t.Fatalf("job = %+v", job)
	}
	if job.TotalBatches != 1 || job.CompletedBatches != 0 || job.ScopeMaxCursor != nil || job.CorrelationID != "corr-test" {
		t.Fatalf("unexpected defaults: %+v", job)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	n, err := conn.Conn().WaitForNotification(waitCtx)
	if err != nil {
		t.Fatalf("wait notification: %v", err)
	}
	if n.Payload != db.ID.String() {
		t.Fatalf("notification payload = %q", n.Payload)
	}
}

func TestEnqueueRejectsInvalidInput(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	db := createDB(t, st, "orders")
	other := createDB(t, st, "billing")
	f := newExecution(t, st, db.ID, newMigration(t, st, db.ID, domain.ModeNormal), domain.ActionApply, domain.ModeNormal, nil, nil)

	cases := map[string]func(in *store.EnqueueInput){
		"unknown action":    func(in *store.EnqueueInput) { in.ActionType = "drop_everything" },
		"unknown mode":      func(in *store.EnqueueInput) { in.ExecutionMode = "turbo" },
		"wrong root":        func(in *store.EnqueueInput) { in.RootExecutionID = uuid.New() },
		"wrong database":    func(in *store.EnqueueInput) { in.DBID = other.ID },
		"missing execution": func(in *store.EnqueueInput) { in.ExecutionID = uuid.New() },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := f.input()
			mutate(&in)
			tx, err := st.Pool().Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := st.EnqueueMigrationJobTx(ctx, tx, in); !errors.Is(err, store.ErrInvalidEnqueue) {
				t.Fatalf("err = %v; want ErrInvalidEnqueue", err)
			}
		})
	}
}

func TestEnqueueSameProposalTwiceConflicts(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	db := createDB(t, st, "orders")
	f, _ := newApplyJob(t, st, db.ID)

	second := newExecution(t, st, db.ID, newMigration(t, st, db.ID, domain.ModeNormal), domain.ActionApply, domain.ModeNormal, nil, nil)
	in := second.input()
	in.ProposalID = f.proposalID
	tx, err := st.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := st.EnqueueMigrationJobTx(ctx, tx, in); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("err = %v; want ErrConflict", err)
	}
}

func TestEnqueueInheritsBatchScopeFromRoot(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	db := createDB(t, st, "orders")
	mig := newMigration(t, st, db.ID, domain.ModeBatch)

	apply := newExecution(t, st, db.ID, mig, domain.ActionApply, domain.ModeBatch, nil, nil)
	applyJob := enqueue(t, st, apply)
	if _, err := st.Pool().Exec(ctx, `
		update migration_jobs
		set status = 'partial', last_cursor = 3000, scope_min_cursor = 1, scope_max_cursor = 10000,
		    total_batches = 10, completed_batches = 3, committed_rows = 3000
		where job_id = $1`, applyJob.ID); err != nil {
		t.Fatalf("set scope: %v", err)
	}
	if _, err := st.Pool().Exec(ctx, `update migration_proposals set status = 'partial' where id = $1`, apply.proposalID); err != nil {
		t.Fatalf("set proposal partial: %v", err)
	}

	retry := newExecution(t, st, db.ID, mig, domain.ActionBatchRetry, domain.ModeBatch, &apply.executionID, &apply.executionID)
	job := enqueue(t, st, retry)
	if job.RootExecutionID != apply.executionID {
		t.Fatalf("root = %s; want %s", job.RootExecutionID, apply.executionID)
	}
	if job.LastCursor == nil || *job.LastCursor != 3000 || job.ScopeMinCursor == nil || *job.ScopeMinCursor != 1 ||
		job.ScopeMaxCursor == nil || *job.ScopeMaxCursor != 10000 || job.TotalBatches != 10 ||
		job.CompletedBatches != 3 || job.CommittedRows != 3000 {
		t.Fatalf("scope not inherited: %+v", job)
	}
}

func TestEnqueueBatchRetryWithoutEarlierJobFails(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	db := createDB(t, st, "orders")
	mig := newMigration(t, st, db.ID, domain.ModeBatch)
	apply := newExecution(t, st, db.ID, mig, domain.ActionApply, domain.ModeBatch, nil, nil)
	if _, err := st.Pool().Exec(ctx, `update migration_proposals set status = 'partial' where id = $1`, apply.proposalID); err != nil {
		t.Fatalf("set proposal partial: %v", err)
	}
	retry := newExecution(t, st, db.ID, mig, domain.ActionBatchRetry, domain.ModeBatch, &apply.executionID, &apply.executionID)

	tx, err := st.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := st.EnqueueMigrationJobTx(ctx, tx, retry.input()); !errors.Is(err, store.ErrNoScopeSource) {
		t.Fatalf("err = %v; want ErrNoScopeSource", err)
	}
}

func TestClaimIsFIFOPerDatabaseAndParallelAcrossDatabases(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	dbA := createDB(t, st, "orders")
	dbB := createDB(t, st, "billing")

	a1, _ := newApplyJob(t, st, dbA.ID)
	a2, _ := newApplyJob(t, st, dbA.ID)
	b1, _ := newApplyJob(t, st, dbB.ID)

	first := claim(t, st, "w1")
	if first == nil || first.Job.ExecutionID != a1.executionID {
		t.Fatalf("first claim = %+v; want a1", first)
	}
	if first.Job.Status != domain.JobRunning || first.Lease.Epoch != 1 || first.PreviousLockState != "" {
		t.Fatalf("first claim state = %+v", first)
	}
	if got := proposalStatus(t, st, a1.proposalID); got != "running" {
		t.Fatalf("proposal status = %q; want running", got)
	}
	if got := executionStatus(t, st, a1.executionID); got != "queued" {
		t.Fatalf("execution status = %q; claim must leave it for revalidation", got)
	}

	second := claim(t, st, "w2")
	if second == nil || second.Job.ExecutionID != b1.executionID {
		t.Fatalf("second claim = %+v; want b1 while a2 waits for the lock", second)
	}
	if c := claim(t, st, "w3"); c != nil {
		t.Fatalf("third claim = %+v; want nil", c)
	}

	if err := st.FinishJob(ctx, first.Lease, store.Outcome{Status: domain.JobSuccess}); err != nil {
		t.Fatalf("finish: %v", err)
	}
	next := claim(t, st, "w3")
	if next == nil || next.Job.ExecutionID != a2.executionID {
		t.Fatalf("claim after release = %+v; want a2", next)
	}
}

func TestClaimFollowsQueueSeqWhenApprovalsInterleave(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	db := createDB(t, st, "orders")
	slotK := newExecution(t, st, db.ID, newMigration(t, st, db.ID, domain.ModeNormal), domain.ActionApply, domain.ModeNormal, nil, nil)
	slotNext := newExecution(t, st, db.ID, newMigration(t, st, db.ID, domain.ModeNormal), domain.ActionApply, domain.ModeNormal, nil, nil)

	txNext, err := st.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = txNext.Rollback(ctx) }()
	if _, err := txNext.Exec(ctx, `select 1`); err != nil {
		t.Fatalf("start tx: %v", err)
	}

	enqueue(t, st, slotK)
	if _, err := st.EnqueueMigrationJobTx(ctx, txNext, slotNext.input()); err != nil {
		t.Fatalf("enqueue next: %v", err)
	}
	if err := txNext.Commit(ctx); err != nil {
		t.Fatalf("commit next: %v", err)
	}

	c := claim(t, st, "w1")
	if c == nil || c.Job.ExecutionID != slotK.executionID {
		t.Fatalf("claim = %+v; want slot k first", c)
	}
}

func TestRequeueReleasesLockAndBackoffHeadBlocksDatabase(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	db := createDB(t, st, "orders")
	a1, _ := newApplyJob(t, st, db.ID)
	newApplyJob(t, st, db.ID)

	c := claim(t, st, "w1")
	failed, err := st.RequeueJob(ctx, c.Lease, time.Minute, "worker lost")
	if err != nil || failed {
		t.Fatalf("requeue = %v, %v", failed, err)
	}
	if _, err := st.GetDBLock(ctx, db.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("lock still present: %v", err)
	}
	job, err := st.GetJob(ctx, c.Job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.Status != domain.JobQueued || job.RetryCount != 1 || job.WorkerID != "" || job.LastError != "worker lost" ||
		job.StartedAt == nil || !job.NextRunAt.After(time.Now().Add(30*time.Second)) {
		t.Fatalf("requeued job = %+v", job)
	}
	if got := proposalStatus(t, st, a1.proposalID); got != "queued" {
		t.Fatalf("proposal status = %q", got)
	}
	if got := executionStatus(t, st, a1.executionID); got != "queued" {
		t.Fatalf("execution status = %q", got)
	}

	if c := claim(t, st, "w2"); c != nil {
		t.Fatalf("claim = %+v; the job behind a backing off head must wait", c)
	}

	if _, err := st.RequeueJob(ctx, c.Lease, time.Minute, "again"); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("second requeue err = %v; want ErrLeaseLost", err)
	}
}

func TestRequeueFailsJobAfterMaxRetries(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	db := createDB(t, st, "orders")
	f, job := newApplyJob(t, st, db.ID)
	if _, err := st.Pool().Exec(ctx, `update migration_jobs set max_retries = 0 where job_id = $1`, job.ID); err != nil {
		t.Fatalf("set max retries: %v", err)
	}

	c := claim(t, st, "w1")
	failed, err := st.RequeueJob(ctx, c.Lease, time.Second, "gave up")
	if err != nil || !failed {
		t.Fatalf("requeue = %v, %v; want failed", failed, err)
	}
	got, err := st.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.Status != domain.JobFailed || got.FinishedAt == nil || got.LastError != "gave up" {
		t.Fatalf("job = %+v", got)
	}
	if s := proposalStatus(t, st, f.proposalID); s != "failed" {
		t.Fatalf("proposal status = %q", s)
	}
	if s := executionStatus(t, st, f.executionID); s != "failed" {
		t.Fatalf("execution status = %q", s)
	}
	if _, err := st.GetDBLock(ctx, db.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("lock still present: %v", err)
	}
}

func TestClaimTakesOverHoldAndRestoreLockPutsItBack(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	db := createDB(t, st, "orders")
	mig := newMigration(t, st, db.ID, domain.ModeBatch)
	apply := newExecution(t, st, db.ID, mig, domain.ActionApply, domain.ModeBatch, nil, nil)
	applyJob := enqueue(t, st, apply)

	c := claim(t, st, "w1")
	if err := st.FinishJob(ctx, c.Lease, store.Outcome{Status: domain.JobPartial, HoldState: domain.LockPartialHold}); err != nil {
		t.Fatalf("finish partial: %v", err)
	}

	newApplyJob(t, st, db.ID)
	if got := claim(t, st, "w2"); got != nil {
		t.Fatalf("claim = %+v; a held database must block regular jobs", got)
	}

	retry := newExecution(t, st, db.ID, mig, domain.ActionBatchRetry, domain.ModeBatch, &apply.executionID, &apply.executionID)
	enqueue(t, st, retry)
	taken := claim(t, st, "w2")
	if taken == nil || taken.Job.ExecutionID != retry.executionID {
		t.Fatalf("takeover claim = %+v; want batch_retry", taken)
	}
	if taken.Lease.Epoch != 2 || taken.PreviousLockState != domain.LockPartialHold {
		t.Fatalf("takeover lease = %+v previous = %q", taken.Lease, taken.PreviousLockState)
	}
	lock, err := st.GetDBLock(ctx, db.ID)
	if err != nil {
		t.Fatalf("get lock: %v", err)
	}
	if lock.LockState != domain.LockRunning || lock.JobID != taken.Job.ID || lock.ExecutionID != retry.executionID {
		t.Fatalf("lock after takeover = %+v", lock)
	}

	if err := st.FailBeforeExecute(ctx, taken.Lease, "revalidation failed"); err != nil {
		t.Fatalf("fail before execute: %v", err)
	}
	lock, err = st.GetDBLock(ctx, db.ID)
	if err != nil {
		t.Fatalf("get lock: %v", err)
	}
	if lock.LockState != domain.LockPartialHold || lock.PreviousLockState != "" || lock.JobID != applyJob.ID ||
		lock.ExecutionID != apply.executionID || lock.LockEpoch != 2 {
		t.Fatalf("lock after restore = %+v", lock)
	}
}

func TestRequeueOfTakeoverKeepsDatabaseHeld(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	db := createDB(t, st, "orders")
	mig := newMigration(t, st, db.ID, domain.ModeBatch)
	apply := newExecution(t, st, db.ID, mig, domain.ActionApply, domain.ModeBatch, nil, nil)
	applyJob := enqueue(t, st, apply)

	c := claim(t, st, "w1")
	if err := st.FinishJob(ctx, c.Lease, store.Outcome{Status: domain.JobPartial, HoldState: domain.LockPartialHold}); err != nil {
		t.Fatalf("finish partial: %v", err)
	}

	retry := newExecution(t, st, db.ID, mig, domain.ActionBatchRetry, domain.ModeBatch, &apply.executionID, &apply.executionID)
	retryJob := enqueue(t, st, retry)
	_, other := newApplyJob(t, st, db.ID)
	taken := claim(t, st, "w2")
	if taken == nil || taken.Job.ID != retryJob.ID {
		t.Fatalf("takeover claim = %+v; want batch_retry", taken)
	}

	failed, err := st.RequeueJob(ctx, taken.Lease, 0, "target unreachable")
	if err != nil || failed {
		t.Fatalf("requeue = %v, %v", failed, err)
	}
	lock, err := st.GetDBLock(ctx, db.ID)
	if err != nil {
		t.Fatalf("get lock: %v", err)
	}
	if lock.LockState != domain.LockPartialHold || lock.JobID != applyJob.ID || lock.ExecutionID != apply.executionID {
		t.Fatalf("lock after requeue = %+v; the partial hold must survive", lock)
	}
	job, err := st.GetJob(ctx, retryJob.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.Status != domain.JobQueued || job.RetryCount != 1 {
		t.Fatalf("requeued job = %+v", job)
	}

	again := claim(t, st, "w3")
	if again == nil || again.Job.ID != retryJob.ID || again.PreviousLockState != domain.LockPartialHold {
		t.Fatalf("second claim = %+v; want the batch_retry to take over the hold again, not job %s", again, other.ID)
	}
}

func TestBatchRevertTakesOverHoldAfterFailedRetry(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	db := createDB(t, st, "orders")
	mig := newMigration(t, st, db.ID, domain.ModeBatch)
	apply := newExecution(t, st, db.ID, mig, domain.ActionApply, domain.ModeBatch, nil, nil)
	enqueue(t, st, apply)

	c1 := claim(t, st, "w1")
	if err := st.FinishJob(ctx, c1.Lease, store.Outcome{Status: domain.JobPartial, HoldState: domain.LockPartialHold}); err != nil {
		t.Fatalf("finish apply partial: %v", err)
	}

	// Retry takes over and fails partial again, moving lock.execution_id to retry.executionID.
	retry := newExecution(t, st, db.ID, mig, domain.ActionBatchRetry, domain.ModeBatch, &apply.executionID, &apply.executionID)
	enqueue(t, st, retry)
	c2 := claim(t, st, "w2")
	if c2 == nil || c2.Job.ExecutionID != retry.executionID {
		t.Fatalf("retry claim = %+v; want retry", c2)
	}
	if err := st.FinishJob(ctx, c2.Lease, store.Outcome{Status: domain.JobPartial, HoldState: domain.LockPartialHold}); err != nil {
		t.Fatalf("finish retry partial: %v", err)
	}

	// Revert targeting the original apply execution (same root_execution_id) must still take over the hold.
	revert := newExecution(t, st, db.ID, mig, domain.ActionBatchRevert, domain.ModeBatch, &apply.executionID, &apply.executionID)
	revertJob := enqueue(t, st, revert)
	c3 := claim(t, st, "w3")
	if c3 == nil || c3.Job.ID != revertJob.ID || c3.PreviousLockState != domain.LockPartialHold {
		t.Fatalf("revert claim = %+v; want batch_revert to take over partial_hold via root_execution_id", c3)
	}
}

func TestFailBeforeExecuteDeletesFreshLock(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	db := createDB(t, st, "orders")
	newApplyJob(t, st, db.ID)

	c := claim(t, st, "w1")
	if err := st.FailBeforeExecute(ctx, c.Lease, "revalidation failed"); err != nil {
		t.Fatalf("fail before execute: %v", err)
	}
	if _, err := st.GetDBLock(ctx, db.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("lock still present: %v", err)
	}
}

func TestConcurrentClaimsHaveOneWinner(t *testing.T) {
	st := testPool(t)
	db := createDB(t, st, "orders")
	newApplyJob(t, st, db.ID)

	const workers = 8
	var wg sync.WaitGroup
	results := make(chan *store.ClaimedJob, workers)
	errs := make(chan error, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := st.ClaimNextJob(context.Background(), "w"+string(rune('a'+i)), testLeaseTTL)
			if err != nil {
				errs <- err
				return
			}
			results <- c
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("claim: %v", err)
	}
	winners := 0
	for c := range results {
		if c != nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d; want 1", winners)
	}
}

func TestExpiredLeaseTakeoverFencesOldWorker(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	db := createDB(t, st, "orders")
	newApplyJob(t, st, db.ID)

	c := claim(t, st, "w1")
	if err := st.RenewLease(ctx, c.Lease, testLeaseTTL); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if l, err := st.TakeoverExpiredLock(ctx, "w2", testLeaseTTL); err != nil || l != nil {
		t.Fatalf("takeover of live lease = %+v, %v; want nil", l, err)
	}

	if _, err := st.Pool().Exec(ctx, `
		update database_migration_locks set lease_until = now() - interval '1 second' where db_id = $1`, db.ID); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	l, err := st.TakeoverExpiredLock(ctx, "w2", testLeaseTTL)
	if err != nil || l == nil {
		t.Fatalf("takeover = %+v, %v", l, err)
	}
	if l.LockEpoch != 2 || l.WorkerID != "w2" || l.JobID != c.Job.ID {
		t.Fatalf("taken over lock = %+v", l)
	}
	job, err := st.GetJob(ctx, c.Job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.WorkerID != "w2" {
		t.Fatalf("job worker = %q; want w2", job.WorkerID)
	}

	cursor := int64(10)
	if err := st.RenewLease(ctx, c.Lease, testLeaseTTL); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("old renew err = %v; want ErrLeaseLost", err)
	}
	progress := store.BatchProgress{LastCursor: &cursor, JobBatches: 1, JobRows: 10, TotalBatches: 10}
	if err := st.RecordBatchProgress(ctx, c.Lease, progress); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("old progress err = %v; want ErrLeaseLost", err)
	}
	if err := st.FinishJob(ctx, c.Lease, store.Outcome{Status: domain.JobPartial, HoldState: domain.LockPartialHold}); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("old finish err = %v; want ErrLeaseLost", err)
	}
	if _, err := st.RequeueJob(ctx, c.Lease, 0, "old"); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("old requeue err = %v; want ErrLeaseLost", err)
	}

	newLease := c.Lease
	newLease.WorkerID, newLease.Epoch = l.WorkerID, l.LockEpoch
	if err := st.RecordBatchProgress(ctx, newLease, progress); err != nil {
		t.Fatalf("new progress: %v", err)
	}
	minC, maxC := int64(1), int64(100)
	if err := st.UpdateJobScope(ctx, newLease, &minC, &maxC, 10); err != nil {
		t.Fatalf("new scope: %v", err)
	}
	job, err = st.GetJob(ctx, c.Job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if *job.LastCursor != 10 || job.CompletedBatches != 1 || job.CommittedRows != 10 || *job.ScopeMaxCursor != 100 || job.TotalBatches != 10 {
		t.Fatalf("progress = %+v", job)
	}
	if err := st.FinishJob(ctx, newLease, store.Outcome{Status: domain.JobSuccess}); err != nil {
		t.Fatalf("new finish: %v", err)
	}
}

func TestClaimSkipsSoftDeletedDatabase(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	db := createDB(t, st, "orders")
	newApplyJob(t, st, db.ID)
	if _, err := st.Pool().Exec(ctx, `update db_connections set deleted_at = now() where id = $1`, db.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if c := claim(t, st, "w1"); c != nil {
		t.Fatalf("claim = %+v; want nil for a soft deleted database", c)
	}
}

func TestGetJobNotFound(t *testing.T) {
	st := testPool(t)
	if _, err := st.GetJob(context.Background(), uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v; want ErrNotFound", err)
	}
}
