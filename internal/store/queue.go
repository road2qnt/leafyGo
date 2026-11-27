package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"leafygo/internal/domain"
)

const JobsChannel = "migration_jobs_channel"

const claimCandidateLimit = 10

const (
	retryBackoffBase = 2 * time.Second
	retryBackoffMax  = time.Minute
)

var (
	ErrLeaseLost      = errors.New("lease lost")
	ErrNoScopeSource  = errors.New("no earlier job on root execution to inherit batch scope from")
	ErrInvalidEnqueue = errors.New("invalid enqueue input")
)

type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type EnqueueInput struct {
	DBID            uuid.UUID
	ProposalID      uuid.UUID
	MigrationID     uuid.UUID
	ExecutionID     uuid.UUID
	RootExecutionID uuid.UUID
	ActionType      string
	ExecutionMode   string
	TraceParent     string
	TraceState      string
	CorrelationID   string
}

// Identifies a lock held by one worker. Every write guarded by a lease fails with ErrLeaseLost once
// another worker has bumped the epoch.
type Lease struct {
	DBID        uuid.UUID
	JobID       uuid.UUID
	ExecutionID uuid.UUID
	WorkerID    string
	Epoch       int64
}

type ClaimedJob struct {
	Job               domain.MigrationJob
	Lease             Lease
	PreviousLockState string
	TargetExecutionID *uuid.UUID
}

type batchScope struct {
	lastCursor       *int64
	scopeMinCursor   *int64
	scopeMaxCursor   *int64
	totalBatches     int
	completedBatches int
	committedRows    int64
}

// Must run in the transaction that inserts the execution row, so the job becomes
// visible to workers only when that transaction commits.
func (s *Store) EnqueueMigrationJobTx(ctx context.Context, tx pgx.Tx, in EnqueueInput) (domain.MigrationJob, error) {
	if !domain.ValidAction(in.ActionType) || !domain.ValidMode(in.ExecutionMode) {
		return domain.MigrationJob{}, fmt.Errorf("%w: action %q mode %q", ErrInvalidEnqueue, in.ActionType, in.ExecutionMode)
	}

	var execDB, execRoot uuid.UUID
	err := tx.QueryRow(ctx, `
		select db_id, root_execution_id from migration_executions where execution_id=$1`, in.ExecutionID).
		Scan(&execDB, &execRoot)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MigrationJob{}, fmt.Errorf("%w: execution %s does not exist", ErrInvalidEnqueue, in.ExecutionID)
	}
	if err != nil {
		return domain.MigrationJob{}, fmt.Errorf("enqueue load execution: %w", err)
	}
	if execDB != in.DBID || execRoot != in.RootExecutionID {
		return domain.MigrationJob{}, fmt.Errorf("%w: execution %s does not match db or root execution", ErrInvalidEnqueue, in.ExecutionID)
	}

	scope := batchScope{totalBatches: 1}
	if domain.InheritsBatchScope(in.ActionType, in.ExecutionMode) {
		err := tx.QueryRow(ctx, `
			select j.last_cursor, j.scope_min_cursor, j.scope_max_cursor,
			       j.total_batches, j.completed_batches, j.committed_rows
			from migration_jobs j
			join migration_executions e on e.execution_id = j.execution_id
			where e.root_execution_id = $1
			order by j.queue_seq desc
			limit 1`, in.RootExecutionID).
			Scan(&scope.lastCursor, &scope.scopeMinCursor, &scope.scopeMaxCursor,
				&scope.totalBatches, &scope.completedBatches, &scope.committedRows)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.MigrationJob{}, ErrNoScopeSource
		}
		if err != nil {
			return domain.MigrationJob{}, fmt.Errorf("enqueue load inherited scope: %w", err)
		}
	}

	jobID := uuid.New()
	_, err = tx.Exec(ctx, `
		insert into migration_jobs (
			job_id, db_id, proposal_id, migration_id, execution_id, action_type, execution_mode,
			last_cursor, scope_min_cursor, scope_max_cursor, total_batches, completed_batches, committed_rows,
			trace_parent, trace_state, correlation_id)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		jobID, in.DBID, in.ProposalID, in.MigrationID, in.ExecutionID, in.ActionType, in.ExecutionMode,
		scope.lastCursor, scope.scopeMinCursor, scope.scopeMaxCursor,
		scope.totalBatches, scope.completedBatches, scope.committedRows,
		in.TraceParent, in.TraceState, in.CorrelationID)
	if err != nil {
		return domain.MigrationJob{}, fmt.Errorf("enqueue insert job: %w", mapWriteError(err))
	}
	if err := notifyJobs(ctx, tx, in.DBID); err != nil {
		return domain.MigrationJob{}, err
	}
	return getJob(ctx, tx, jobID)
}

// Returns nil when no job is claimable. It never holds a transaction beyond the claim itself.
func (s *Store) ClaimNextJob(ctx context.Context, workerID string, leaseTTL time.Duration) (*ClaimedJob, error) {
	candidates, err := s.claimCandidates(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range candidates {
		claimed, err := s.claimCandidate(ctx, c, workerID, leaseTTL)
		if err != nil {
			return nil, err
		}
		if claimed != nil {
			return claimed, nil
		}
	}
	return nil, nil
}

type claimCandidate struct {
	jobID             uuid.UUID
	dbID              uuid.UUID
	actionType        string
	targetExecutionID *uuid.UUID
}

func (s *Store) claimCandidates(ctx context.Context) ([]claimCandidate, error) {
	rows, err := s.pool.Query(ctx, `
		with eligible as (
			select j.job_id, j.queue_seq, j.db_id, j.action_type, p.target_execution_id, j.next_run_at
			from migration_jobs j
			join db_connections d on d.id = j.db_id
			join migration_proposals p on p.id = j.proposal_id
			left join database_migration_locks l on l.db_id = j.db_id
			where j.status = 'queued'
			  and d.deleted_at is null
			  and (
			      l.db_id is null
			      or (
			          l.lock_state in ('partial_hold', 'partially_reverted_hold', 'uncertain_hold')
			          and j.action_type in ('batch_retry', 'batch_revert', 'resolve_uncertain')
			          and p.target_execution_id = l.execution_id
			      )
			  )
		),
		heads as (
			select distinct on (db_id) job_id, queue_seq, db_id, action_type, target_execution_id, next_run_at
			from eligible
			order by db_id, queue_seq asc
		)
		select job_id, db_id, action_type, target_execution_id
		from heads
		where next_run_at <= now()
		order by queue_seq asc
		limit $1`, claimCandidateLimit)
	if err != nil {
		return nil, fmt.Errorf("claim candidates: %w", err)
	}
	defer rows.Close()

	var out []claimCandidate
	for rows.Next() {
		var c claimCandidate
		if err := rows.Scan(&c.jobID, &c.dbID, &c.actionType, &c.targetExecutionID); err != nil {
			return nil, fmt.Errorf("scan claim candidate: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) claimCandidate(ctx context.Context, c claimCandidate, workerID string, leaseTTL time.Duration) (*ClaimedJob, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("claim begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var dbID, migrationID, executionID, proposalID uuid.UUID
	err = tx.QueryRow(ctx, `
		select db_id, migration_id, execution_id, proposal_id
		from migration_jobs
		where job_id = $1 and status = 'queued' and next_run_at <= now()
		for update skip locked`, c.jobID).
		Scan(&dbID, &migrationID, &executionID, &proposalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim lock job: %w", err)
	}

	var epoch int64
	var previous *string
	acquired := false
	if domain.TakesOverHold(c.actionType) && c.targetExecutionID != nil {
		err = tx.QueryRow(ctx, `
			update database_migration_locks
			set previous_lock_state = lock_state,
			    job_id       = $2,
			    execution_id = $3,
			    migration_id = $4,
			    worker_id    = $5,
			    lock_state   = 'running',
			    lock_epoch   = lock_epoch + 1,
			    lease_until  = now() + make_interval(secs => $7),
			    updated_at   = now()
			where db_id = $1
			  and execution_id = $6
			  and lock_state in ('partial_hold', 'partially_reverted_hold', 'uncertain_hold')
			returning lock_epoch, previous_lock_state`,
			dbID, c.jobID, executionID, migrationID, workerID, *c.targetExecutionID, leaseTTL.Seconds()).
			Scan(&epoch, &previous)
		switch {
		case err == nil:
			acquired = true
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("claim take over lock: %w", err)
		}
	}
	if !acquired {
		err = tx.QueryRow(ctx, `
			insert into database_migration_locks (
				db_id, job_id, execution_id, migration_id, worker_id,
				lock_state, previous_lock_state, lock_epoch, lease_until, acquired_at, updated_at)
			values ($1, $2, $3, $4, $5, 'running', null, 1, now() + make_interval(secs => $6), now(), now())
			on conflict (db_id) do nothing
			returning lock_epoch`,
			dbID, c.jobID, executionID, migrationID, workerID, leaseTTL.Seconds()).
			Scan(&epoch)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("claim acquire lock: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		update migration_jobs
		set status = 'running', worker_id = $2, started_at = coalesce(started_at, now()), updated_at = now()
		where job_id = $1`, c.jobID, workerID); err != nil {
		return nil, fmt.Errorf("claim mark job running: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update migration_proposals set status = 'running', updated_at = now() where id = $1`, proposalID); err != nil {
		return nil, fmt.Errorf("claim mark proposal running: %w", err)
	}
	job, err := getJob(ctx, tx, c.jobID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("claim commit: %w", err)
	}

	claimed := &ClaimedJob{
		Job:               job,
		Lease:             Lease{DBID: dbID, JobID: c.jobID, ExecutionID: executionID, WorkerID: workerID, Epoch: epoch},
		TargetExecutionID: c.targetExecutionID,
	}
	if previous != nil {
		claimed.PreviousLockState = *previous
	}
	return claimed, nil
}

// Requeues the job with a backoff, or fails it once retries run out. A taken over hold is restored,
// not released. Reports whether the job failed.
func (s *Store) RequeueJob(ctx context.Context, lease Lease, backoff time.Duration, errText string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("requeue begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := restoreLockTx(ctx, tx, lease); err != nil {
		return false, err
	}

	var retryCount, maxRetries int
	var executionID, proposalID uuid.UUID
	if err := tx.QueryRow(ctx, `
		select retry_count, max_retries, execution_id, proposal_id
		from migration_jobs where job_id = $1 for update`, lease.JobID).
		Scan(&retryCount, &maxRetries, &executionID, &proposalID); err != nil {
		return false, fmt.Errorf("requeue load job: %w", err)
	}

	failed := retryCount >= maxRetries
	if failed {
		err = failJobTx(ctx, tx, lease.JobID, executionID, proposalID, errText)
	} else {
		err = requeueJobTx(ctx, tx, lease.JobID, executionID, proposalID, backoff, errText)
	}
	if err != nil {
		return false, err
	}
	if err := notifyJobs(ctx, tx, lease.DBID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("requeue commit: %w", err)
	}
	return failed, nil
}

func requeueJobTx(ctx context.Context, tx pgx.Tx, jobID, executionID, proposalID uuid.UUID, backoff time.Duration, errText string) error {
	if _, err := tx.Exec(ctx, `
		update migration_jobs
		set status = 'queued', worker_id = null, retry_count = retry_count + 1,
		    next_run_at = now() + make_interval(secs => $2), last_error = $3, updated_at = now()
		where job_id = $1`, jobID, backoff.Seconds(), errText); err != nil {
		return fmt.Errorf("requeue job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update migration_executions set status = 'queued' where execution_id = $1`, executionID); err != nil {
		return fmt.Errorf("requeue execution: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update migration_proposals set status = 'queued', updated_at = now() where id = $1`, proposalID); err != nil {
		return fmt.Errorf("requeue proposal: %w", err)
	}
	return nil
}

func failJobTx(ctx context.Context, tx pgx.Tx, jobID, executionID, proposalID uuid.UUID, errText string) error {
	if _, err := tx.Exec(ctx, `
		update migration_jobs
		set status = 'failed', worker_id = null, last_error = $2, finished_at = now(), updated_at = now()
		where job_id = $1`, jobID, errText); err != nil {
		return fmt.Errorf("fail job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update migration_executions
		set status = 'failed', error_message = $2, finished_at = now(),
		    duration_ms = case when started_at is null then null
		                       else (extract(epoch from now() - started_at) * 1000)::int end
		where execution_id = $1`, executionID, errText); err != nil {
		return fmt.Errorf("fail execution: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update migration_proposals set status = 'failed', updated_at = now() where id = $1`, proposalID); err != nil {
		return fmt.Errorf("fail proposal: %w", err)
	}
	return nil
}

// Doubles from retryBackoffBase on every retry, capped at retryBackoffMax.
func RetryBackoff(retryCount int) time.Duration {
	if retryCount < 0 {
		retryCount = 0
	}
	d := retryBackoffBase
	for i := 0; i < retryCount; i++ {
		d *= 2
		if d >= retryBackoffMax {
			return retryBackoffMax
		}
	}
	return d
}

func (s *Store) UpdateJobScope(ctx context.Context, lease Lease, scopeMin, scopeMax *int64, totalBatches int) error {
	tag, err := s.pool.Exec(ctx, `
		update migration_jobs j
		set scope_min_cursor = $5, scope_max_cursor = $6, total_batches = $7, updated_at = now()
		where j.job_id = $1
		  and exists (
		      select 1 from database_migration_locks l
		      where l.db_id = $2 and l.job_id = j.job_id and l.worker_id = $3 and l.lock_epoch = $4)`,
		lease.JobID, lease.DBID, lease.WorkerID, lease.Epoch, scopeMin, scopeMax, totalBatches)
	if err != nil {
		return fmt.Errorf("update job scope: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) GetJob(ctx context.Context, jobID uuid.UUID) (domain.MigrationJob, error) {
	return getJob(ctx, s.pool, jobID)
}

func (s *Store) ListJobs(ctx context.Context, dbID uuid.UUID) ([]domain.MigrationJob, error) {
	rows, err := s.pool.Query(ctx, `
		select j.job_id, j.queue_seq, j.db_id, j.proposal_id, j.migration_id, j.execution_id, e.root_execution_id,
		       j.action_type, j.execution_mode, j.status, j.retry_count, j.max_retries, j.next_run_at,
		       j.last_cursor, j.scope_min_cursor, j.scope_max_cursor,
		       j.total_batches, j.completed_batches, j.committed_rows,
		       j.trace_parent, j.trace_state, j.correlation_id, j.worker_id, j.last_error,
		       j.queued_at, j.started_at, j.finished_at, j.updated_at
		from migration_jobs j
		join migration_executions e on e.execution_id = j.execution_id
		where j.db_id = $1
		order by j.queue_seq desc`, dbID)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	jobs := make([]domain.MigrationJob, 0)
	for rows.Next() {
		var j domain.MigrationJob
		var workerID *string
		if err := rows.Scan(
			&j.ID, &j.QueueSeq, &j.DBID, &j.ProposalID, &j.MigrationID, &j.ExecutionID, &j.RootExecutionID,
			&j.ActionType, &j.ExecutionMode, &j.Status, &j.RetryCount, &j.MaxRetries, &j.NextRunAt,
			&j.LastCursor, &j.ScopeMinCursor, &j.ScopeMaxCursor,
			&j.TotalBatches, &j.CompletedBatches, &j.CommittedRows,
			&j.TraceParent, &j.TraceState, &j.CorrelationID, &workerID, &j.LastError,
			&j.QueuedAt, &j.StartedAt, &j.FinishedAt, &j.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		if workerID != nil {
			j.WorkerID = *workerID
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list jobs rows: %w", err)
	}
	return jobs, nil
}

func getJob(ctx context.Context, q querier, jobID uuid.UUID) (domain.MigrationJob, error) {
	var j domain.MigrationJob
	var workerID *string
	err := q.QueryRow(ctx, `
		select j.job_id, j.queue_seq, j.db_id, j.proposal_id, j.migration_id, j.execution_id, e.root_execution_id,
		       j.action_type, j.execution_mode, j.status, j.retry_count, j.max_retries, j.next_run_at,
		       j.last_cursor, j.scope_min_cursor, j.scope_max_cursor,
		       j.total_batches, j.completed_batches, j.committed_rows,
		       j.trace_parent, j.trace_state, j.correlation_id, j.worker_id, j.last_error,
		       j.queued_at, j.started_at, j.finished_at, j.updated_at
		from migration_jobs j
		join migration_executions e on e.execution_id = j.execution_id
		where j.job_id = $1`, jobID).
		Scan(&j.ID, &j.QueueSeq, &j.DBID, &j.ProposalID, &j.MigrationID, &j.ExecutionID, &j.RootExecutionID,
			&j.ActionType, &j.ExecutionMode, &j.Status, &j.RetryCount, &j.MaxRetries, &j.NextRunAt,
			&j.LastCursor, &j.ScopeMinCursor, &j.ScopeMaxCursor,
			&j.TotalBatches, &j.CompletedBatches, &j.CommittedRows,
			&j.TraceParent, &j.TraceState, &j.CorrelationID, &workerID, &j.LastError,
			&j.QueuedAt, &j.StartedAt, &j.FinishedAt, &j.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MigrationJob{}, ErrNotFound
	}
	if err != nil {
		return domain.MigrationJob{}, fmt.Errorf("get job: %w", err)
	}
	if workerID != nil {
		j.WorkerID = *workerID
	}
	return j, nil
}

func notifyJobs(ctx context.Context, q querier, dbID uuid.UUID) error {
	if _, err := q.Exec(ctx, `select pg_notify($1, $2)`, JobsChannel, dbID.String()); err != nil {
		return fmt.Errorf("notify %s: %w", JobsChannel, err)
	}
	return nil
}
