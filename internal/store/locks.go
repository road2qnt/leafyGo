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

const lockColumns = `
	db_id, job_id, execution_id, migration_id, worker_id, lock_state, previous_lock_state,
	lock_epoch, lease_until, next_probe_at, acquired_at, updated_at`

func scanLock(row pgx.Row) (domain.DBLock, error) {
	var l domain.DBLock
	var previous *string
	err := row.Scan(&l.DBID, &l.JobID, &l.ExecutionID, &l.MigrationID, &l.WorkerID, &l.LockState, &previous,
		&l.LockEpoch, &l.LeaseUntil, &l.NextProbeAt, &l.AcquiredAt, &l.UpdatedAt)
	if err != nil {
		return domain.DBLock{}, err
	}
	if previous != nil {
		l.PreviousLockState = *previous
	}
	return l, nil
}

func (s *Store) GetDBLock(ctx context.Context, dbID uuid.UUID) (domain.DBLock, error) {
	l, err := scanLock(s.pool.QueryRow(ctx, `select `+lockColumns+` from database_migration_locks where db_id = $1`, dbID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DBLock{}, ErrNotFound
	}
	if err != nil {
		return domain.DBLock{}, fmt.Errorf("get db lock: %w", err)
	}
	return l, nil
}

// Extends a running lock. ErrLeaseLost means another worker took the lock over and the
// caller must stop touching the target database.
func (s *Store) RenewLease(ctx context.Context, lease Lease, ttl time.Duration) error {
	tag, err := s.pool.Exec(ctx, `
		update database_migration_locks
		set lease_until = now() + make_interval(secs => $5), updated_at = now()
		where db_id = $1 and job_id = $2 and worker_id = $3 and lock_epoch = $4 and lock_state = 'running'`,
		lease.DBID, lease.JobID, lease.WorkerID, lease.Epoch, ttl.Seconds())
	if err != nil {
		return fmt.Errorf("renew lease: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// Deletes a fresh lock, or returns a taken over hold to its previous state and owner without
// changing the epoch.
func restoreLockTx(ctx context.Context, tx pgx.Tx, lease Lease) error {
	var previous *string
	err := tx.QueryRow(ctx, `
		select previous_lock_state from database_migration_locks
		where db_id = $1 and job_id = $2 and worker_id = $3 and lock_epoch = $4
		for update`, lease.DBID, lease.JobID, lease.WorkerID, lease.Epoch).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("restore lock load: %w", err)
	}

	if previous == nil {
		if _, err := tx.Exec(ctx, `delete from database_migration_locks where db_id = $1`, lease.DBID); err != nil {
			return fmt.Errorf("restore lock delete: %w", err)
		}
		return nil
	}
	var targetExecution, targetJob, targetMigration uuid.UUID
	err = tx.QueryRow(ctx, `
		select t.execution_id, t.job_id, t.migration_id
		from migration_jobs j
		join migration_proposals p on p.id = j.proposal_id
		join migration_jobs t on t.execution_id = p.target_execution_id
		where j.job_id = $1`, lease.JobID).Scan(&targetExecution, &targetJob, &targetMigration)
	if err != nil {
		return fmt.Errorf("restore lock load target job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update database_migration_locks
		set lock_state = previous_lock_state, previous_lock_state = null,
		    job_id = $2, execution_id = $3, migration_id = $4, updated_at = now()
		where db_id = $1`, lease.DBID, targetJob, targetExecution, targetMigration); err != nil {
		return fmt.Errorf("restore lock update: %w", err)
	}
	return nil
}

// Grabs at most one running lock whose lease ran out, bumping its epoch so the
// previous owner is fenced off. It returns nil when there is nothing to recover.
func (s *Store) TakeoverExpiredLock(ctx context.Context, workerID string, ttl time.Duration) (*domain.DBLock, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("takeover begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	l, err := scanLock(tx.QueryRow(ctx, `
		update database_migration_locks l
		set worker_id = $1, lock_epoch = l.lock_epoch + 1,
		    lease_until = now() + make_interval(secs => $2), updated_at = now()
		where l.db_id = (
			select db_id from database_migration_locks
			where lock_state = 'running' and lease_until < now()
			order by lease_until asc
			for update skip locked
			limit 1)
		returning `+lockColumns, workerID, ttl.Seconds()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("takeover expired lock: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update migration_jobs set worker_id = $1, updated_at = now() where job_id = $2`, workerID, l.JobID); err != nil {
		return nil, fmt.Errorf("takeover update job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("takeover commit: %w", err)
	}
	return &l, nil
}
