package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"leafygo/internal/domain"
)

type MigrationSpec struct {
	ID                  uuid.UUID
	Revision            int
	EffectiveSeq        int64
	SQLUp               string
	SQLDown             string
	IsIrreversible      bool
	ExecutionMode       string
	BatchConfig         []byte
	ReplacesMigrationID *uuid.UUID
}

// Everything a worker needs to run one job.
type JobWork struct {
	Job                domain.MigrationJob
	DB                 domain.DBConnection
	DBDeleted          bool
	Migration          MigrationSpec
	ProposalStatus     string
	ProposalRevision   int
	TargetExecutionID  *uuid.UUID
	ExecutionStatus    string
	ExecutionStartedAt *time.Time
}

func (s *Store) LoadJobWork(ctx context.Context, jobID uuid.UUID) (JobWork, error) {
	job, err := getJob(ctx, s.pool, jobID)
	if err != nil {
		return JobWork{}, err
	}
	w := JobWork{Job: job}
	var deletedAt *time.Time
	err = s.pool.QueryRow(ctx, `
		select d.id, d.name, d.host, d.port, d.dbname, d.username, d.password_enc, d.sslmode, d.created_at, d.updated_at, d.deleted_at,
		       m.id, m.revision, m.effective_seq, m.sql_up, m.sql_down, m.is_irreversible, m.execution_mode,
		       m.batch_config, m.replaces_migration_id,
		       p.status, p.migration_revision, p.target_execution_id,
		       e.status, e.started_at
		from migration_jobs j
		join db_connections d on d.id = j.db_id
		join migration_definitions m on m.id = j.migration_id
		join migration_proposals p on p.id = j.proposal_id
		join migration_executions e on e.execution_id = j.execution_id
		where j.job_id = $1`, jobID).
		Scan(&w.DB.ID, &w.DB.Name, &w.DB.Host, &w.DB.Port, &w.DB.DBName, &w.DB.Username, &w.DB.PasswordEnc, &w.DB.SSLMode,
			&w.DB.CreatedAt, &w.DB.UpdatedAt, &deletedAt,
			&w.Migration.ID, &w.Migration.Revision, &w.Migration.EffectiveSeq, &w.Migration.SQLUp, &w.Migration.SQLDown,
			&w.Migration.IsIrreversible, &w.Migration.ExecutionMode, &w.Migration.BatchConfig, &w.Migration.ReplacesMigrationID,
			&w.ProposalStatus, &w.ProposalRevision, &w.TargetExecutionID,
			&w.ExecutionStatus, &w.ExecutionStartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobWork{}, ErrNotFound
	}
	if err != nil {
		return JobWork{}, fmt.Errorf("load job work: %w", err)
	}
	w.DBDeleted = deletedAt != nil
	return w, nil
}

type TargetFacts struct {
	ExecutionID  uuid.UUID
	MigrationID  uuid.UUID
	ActionType   string
	Status       string
	EffectiveSeq int64
}

// Platform state the worker checks right before it touches the target database.
type RevalidationFacts struct {
	// Effective seq of every migration that is currently applied, keyed by migration.
	Active map[uuid.UUID]int64
	// Distinct effective seqs below this migration that still have a non superseded definition.
	LowerSlots []int64
	// Another definition sharing this effective seq is applied or has a queued or running job.
	ChainBlocked   bool
	Target         *TargetFacts
	CurrentGroups  []uuid.UUID
	ApprovedGroups map[uuid.UUID]bool
}

func (s *Store) LoadRevalidationFacts(ctx context.Context, w JobWork) (RevalidationFacts, error) {
	f := RevalidationFacts{Active: map[uuid.UUID]int64{}, ApprovedGroups: map[uuid.UUID]bool{}}
	dbID := w.Job.DBID

	rows, err := s.pool.Query(ctx, `
		select migration_id, effective_seq from (
			select distinct on (e.migration_id) e.migration_id, e.action_type, d.effective_seq
			from migration_executions e
			join migration_definitions d on d.id = e.migration_id
			where e.db_id = $1 and e.status = 'success'
			  and e.action_type in ('apply', 'forward', 'batch_retry', 'rollback', 'batch_revert')
			order by e.migration_id, e.seq_order desc
		) last
		where action_type in ('apply', 'forward', 'batch_retry')`, dbID)
	if err != nil {
		return f, fmt.Errorf("load active migrations: %w", err)
	}
	for rows.Next() {
		var id uuid.UUID
		var seq int64
		if err := rows.Scan(&id, &seq); err != nil {
			rows.Close()
			return f, fmt.Errorf("scan active migration: %w", err)
		}
		f.Active[id] = seq
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return f, fmt.Errorf("load active migrations: %w", err)
	}

	rows, err = s.pool.Query(ctx, `
		select distinct effective_seq from migration_definitions
		where db_id = $1 and effective_seq < $2 and is_superseded = false
		order by effective_seq`, dbID, w.Migration.EffectiveSeq)
	if err != nil {
		return f, fmt.Errorf("load lower slots: %w", err)
	}
	if f.LowerSlots, err = pgx.CollectRows(rows, pgx.RowTo[int64]); err != nil {
		return f, fmt.Errorf("load lower slots: %w", err)
	}

	if err := s.pool.QueryRow(ctx, `
		select exists (
			select 1 from migration_definitions d
			where d.db_id = $1 and d.effective_seq = $2 and d.id <> $3
			  and exists (
			      select 1 from migration_jobs j
			      where j.migration_id = d.id and j.job_id <> $4 and j.status in ('queued', 'running')))`,
		dbID, w.Migration.EffectiveSeq, w.Migration.ID, w.Job.ID).Scan(&f.ChainBlocked); err != nil {
		return f, fmt.Errorf("load chain state: %w", err)
	}
	if !f.ChainBlocked {
		for id, seq := range f.Active {
			if seq == w.Migration.EffectiveSeq && id != w.Migration.ID {
				f.ChainBlocked = true
				break
			}
		}
	}

	if w.TargetExecutionID != nil {
		var t TargetFacts
		err := s.pool.QueryRow(ctx, `
			select e.execution_id, e.migration_id, e.action_type, e.status, d.effective_seq
			from migration_executions e
			join migration_definitions d on d.id = e.migration_id
			where e.execution_id = $1 and e.db_id = $2`, *w.TargetExecutionID, dbID).
			Scan(&t.ExecutionID, &t.MigrationID, &t.ActionType, &t.Status, &t.EffectiveSeq)
		switch {
		case err == nil:
			f.Target = &t
		case !errors.Is(err, pgx.ErrNoRows):
			return f, fmt.Errorf("load target execution: %w", err)
		}
	}

	rows, err = s.pool.Query(ctx, `
		select g.id from db_access a
		join groups g on g.id = a.group_id
		where a.db_id = $1 and g.deleted_at is null`, dbID)
	if err != nil {
		return f, fmt.Errorf("load access groups: %w", err)
	}
	if f.CurrentGroups, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID]); err != nil {
		return f, fmt.Errorf("load access groups: %w", err)
	}

	rows, err = s.pool.Query(ctx, `
		select group_id from migration_approvals where proposal_id = $1 and decision = 'approved'`, w.Job.ProposalID)
	if err != nil {
		return f, fmt.Errorf("load approvals: %w", err)
	}
	approved, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return f, fmt.Errorf("load approvals: %w", err)
	}
	for _, g := range approved {
		f.ApprovedGroups[g] = true
	}
	return f, nil
}

// Fails a claimed job that never reached the target database and gives the lock back.
// The definition stays editable because is_immutable was never set.
func (s *Store) FailBeforeExecute(ctx context.Context, lease Lease, errText string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("fail before execute begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := restoreLockTx(ctx, tx, lease); err != nil {
		return err
	}
	var proposalID uuid.UUID
	if err := tx.QueryRow(ctx, `select proposal_id from migration_jobs where job_id = $1`, lease.JobID).Scan(&proposalID); err != nil {
		return fmt.Errorf("fail before execute load job: %w", err)
	}
	if err := failJobTx(ctx, tx, lease.JobID, lease.ExecutionID, proposalID, errText); err != nil {
		return err
	}
	if err := notifyJobs(ctx, tx, lease.DBID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("fail before execute commit: %w", err)
	}
	return nil
}

func lockFenceTx(ctx context.Context, tx pgx.Tx, lease Lease) error {
	var one int
	err := tx.QueryRow(ctx, `
		select 1 from database_migration_locks
		where db_id = $1 and job_id = $2 and worker_id = $3 and lock_epoch = $4
		for update`, lease.DBID, lease.JobID, lease.WorkerID, lease.Epoch).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("check lease: %w", err)
	}
	return nil
}

// Moves the execution to running and freezes the definition, right before the first statement
// is sent to the target database.
func (s *Store) StartExecution(ctx context.Context, lease Lease, migrationID uuid.UUID, freezeDefinition bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start execution begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockFenceTx(ctx, tx, lease); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update migration_executions set status = 'running', started_at = coalesce(started_at, now())
		where execution_id = $1`, lease.ExecutionID); err != nil {
		return fmt.Errorf("start execution: %w", err)
	}
	if freezeDefinition {
		if _, err := tx.Exec(ctx, `
			update migration_definitions set is_immutable = true, updated_at = now()
			where id = $1 and is_immutable = false`, migrationID); err != nil {
			return fmt.Errorf("freeze definition: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("start execution commit: %w", err)
	}
	return nil
}

// Job counters are cumulative for the root execution. Execution counters cover this attempt only.
type BatchProgress struct {
	LastCursor     *int64
	JobBatches     int
	JobRows        int64
	TotalBatches   int
	AttemptBatches int
	AttemptRows    int64
}

func (s *Store) RecordBatchProgress(ctx context.Context, lease Lease, p BatchProgress) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("record progress begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockFenceTx(ctx, tx, lease); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update migration_jobs
		set last_cursor = $2, completed_batches = $3, committed_rows = $4, total_batches = $5, updated_at = now()
		where job_id = $1`, lease.JobID, p.LastCursor, p.JobBatches, p.JobRows, p.TotalBatches); err != nil {
		return fmt.Errorf("record job progress: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update migration_executions
		set committed_batches = $2, committed_rows = $3, total_batches = $4
		where execution_id = $1`, lease.ExecutionID, p.AttemptBatches, p.AttemptRows, p.TotalBatches); err != nil {
		return fmt.Errorf("record execution progress: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("record progress commit: %w", err)
	}
	return nil
}

type Outcome struct {
	Status  string
	ErrText string
	// Lock state to keep after the job. Empty releases the lock.
	HoldState string
	// Gives a taken over hold back to the execution it came from.
	RestorePreviousHold bool
}

func ProposalStatusFor(jobStatus string) string {
	if jobStatus == domain.JobSuccess {
		return "completed"
	}
	return jobStatus
}

func (s *Store) FinishJob(ctx context.Context, lease Lease, o Outcome) error {
	switch o.Status {
	case domain.JobSuccess, domain.JobFailed, domain.JobPartial, domain.JobPartiallyReverted:
	default:
		return fmt.Errorf("finish job: unsupported status %q", o.Status)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("finish job begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockFenceTx(ctx, tx, lease); err != nil {
		return err
	}
	var proposalID, migrationID uuid.UUID
	var action, mode string
	if err := tx.QueryRow(ctx, `
		update migration_jobs
		set status = $2, last_error = $3, finished_at = now(), updated_at = now(),
		    completed_batches = case when execution_mode = 'normal' and $2 = 'success' then 1 else completed_batches end
		where job_id = $1
		returning proposal_id, migration_id, action_type, execution_mode`, lease.JobID, o.Status, o.ErrText).
		Scan(&proposalID, &migrationID, &action, &mode); err != nil {
		return fmt.Errorf("finish job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update migration_executions
		set status = $2, error_message = $3, finished_at = now(),
		    duration_ms = case when started_at is null then null
		                       else (extract(epoch from now() - started_at) * 1000)::int end,
		    committed_batches = case when $4::text = 'normal' and $2 = 'success' then 1 else committed_batches end
		where execution_id = $1`, lease.ExecutionID, o.Status, o.ErrText, mode); err != nil {
		return fmt.Errorf("finish execution: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update migration_proposals set status = $2, updated_at = now() where id = $1`,
		proposalID, ProposalStatusFor(o.Status)); err != nil {
		return fmt.Errorf("finish proposal: %w", err)
	}
	if o.Status == domain.JobSuccess && (action == domain.ActionApply || action == domain.ActionForward || action == domain.ActionBatchRetry) {
		if _, err := tx.Exec(ctx, `
			update migration_definitions
			set is_superseded = true, superseded_by_migration_id = $1, superseded_at = now(), updated_at = now()
			where db_id = $2 and id <> $1 and is_superseded = false
			  and effective_seq = (select effective_seq from migration_definitions where id = $1)`,
			migrationID, lease.DBID); err != nil {
			return fmt.Errorf("mark superseded: %w", err)
		}
	}

	switch {
	case o.RestorePreviousHold:
		if err := restoreLockTx(ctx, tx, lease); err != nil {
			return err
		}
	case o.HoldState != "":
		if _, err := tx.Exec(ctx, `
			update database_migration_locks
			set lock_state = $2, previous_lock_state = null, next_probe_at = null, updated_at = now()
			where db_id = $1`, lease.DBID, o.HoldState); err != nil {
			return fmt.Errorf("hold lock: %w", err)
		}
	default:
		if _, err := tx.Exec(ctx, `delete from database_migration_locks where db_id = $1`, lease.DBID); err != nil {
			return fmt.Errorf("release lock: %w", err)
		}
	}
	if err := notifyJobs(ctx, tx, lease.DBID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("finish job commit: %w", err)
	}
	return nil
}

// Parks a job whose commit outcome is unknown. The database stays held until a re-probe proves
// what happened on the target.
func (s *Store) MarkUncertain(ctx context.Context, lease Lease, reprobeAfter time.Duration, errText string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("mark uncertain begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockFenceTx(ctx, tx, lease); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update database_migration_locks
		set lock_state = 'uncertain_hold', previous_lock_state = 'running',
		    next_probe_at = now() + make_interval(secs => $2), updated_at = now()
		where db_id = $1`, lease.DBID, reprobeAfter.Seconds()); err != nil {
		return fmt.Errorf("mark lock uncertain: %w", err)
	}
	if err := setRunStatusTx(ctx, tx, lease, domain.JobUncertain, errText); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("mark uncertain commit: %w", err)
	}
	return nil
}

func setRunStatusTx(ctx context.Context, tx pgx.Tx, lease Lease, status, errText string) error {
	var proposalID uuid.UUID
	if err := tx.QueryRow(ctx, `
		update migration_jobs set status = $2, last_error = $3, updated_at = now()
		where job_id = $1 returning proposal_id`, lease.JobID, status, errText).Scan(&proposalID); err != nil {
		return fmt.Errorf("set job status: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update migration_executions set status = $2, error_message = $3 where execution_id = $1`,
		lease.ExecutionID, status, errText); err != nil {
		return fmt.Errorf("set execution status: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update migration_proposals set status = $2, updated_at = now() where id = $1`,
		proposalID, ProposalStatusFor(status)); err != nil {
		return fmt.Errorf("set proposal status: %w", err)
	}
	return nil
}

// Picks one uncertain hold that is due for a re-probe and pushes its next probe forward, so a
// re-probe that finds the old session still alive needs no further write.
func (s *Store) ClaimDueReprobe(ctx context.Context, workerID string, reprobeAfter time.Duration) (*domain.DBLock, error) {
	l, err := scanLock(s.pool.QueryRow(ctx, `
		update database_migration_locks l
		set worker_id = $1, lock_epoch = l.lock_epoch + 1,
		    next_probe_at = now() + make_interval(secs => $2), updated_at = now()
		where l.db_id = (
			select db_id from database_migration_locks
			where lock_state = 'uncertain_hold' and next_probe_at <= now()
			order by next_probe_at asc
			for update skip locked
			limit 1)
		returning `+lockColumns, workerID, reprobeAfter.Seconds()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim due reprobe: %w", err)
	}
	return &l, nil
}

// Turns an uncertain hold back into a running lock so a resumed batch can renew its lease.
func (s *Store) ResumeFromHold(ctx context.Context, lease Lease, ttl time.Duration) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("resume begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockFenceTx(ctx, tx, lease); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update database_migration_locks
		set lock_state = 'running', previous_lock_state = null, next_probe_at = null,
		    lease_until = now() + make_interval(secs => $2), updated_at = now()
		where db_id = $1`, lease.DBID, ttl.Seconds()); err != nil {
		return fmt.Errorf("resume lock: %w", err)
	}
	if err := setRunStatusTx(ctx, tx, lease, domain.JobRunning, ""); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("resume commit: %w", err)
	}
	return nil
}

// Expires the lease at once so another worker's recovery loop resumes the job. Used on shutdown.
func (s *Store) HandoffLease(ctx context.Context, lease Lease) error {
	tag, err := s.pool.Exec(ctx, `
		update database_migration_locks set lease_until = now(), updated_at = now()
		where db_id = $1 and job_id = $2 and worker_id = $3 and lock_epoch = $4 and lock_state = 'running'`,
		lease.DBID, lease.JobID, lease.WorkerID, lease.Epoch)
	if err != nil {
		return fmt.Errorf("hand off lease: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

func LeaseFromLock(l domain.DBLock) Lease {
	return Lease{DBID: l.DBID, JobID: l.JobID, ExecutionID: l.ExecutionID, WorkerID: l.WorkerID, Epoch: l.LockEpoch}
}
