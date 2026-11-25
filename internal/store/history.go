package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"leafygo/internal/domain"
)

type ActiveStackEntry struct {
	Definition        domain.MigrationDefinition `json:"definition"`
	ActiveExecutionID uuid.UUID                  `json:"active_execution_id"`
	RootExecutionID   uuid.UUID                  `json:"root_execution_id"`
	AppliedAt         time.Time                  `json:"applied_at"`
}

type migrationExecutionRow struct {
	ExecutionID            uuid.UUID
	RootExecutionID        uuid.UUID
	DBID                   uuid.UUID
	MigrationID            uuid.UUID
	ProposalID             pgtype.UUID
	SeqOrder               int64
	SeqVersion             int64
	EffectiveSeq           int64
	MigrationRevision      int
	ActionType             string
	ExecutionMode          string
	ExecutedSQL            string
	ExecutedBy             pgtype.UUID
	ExecutedByUsername     string
	DBNameSnapshot         string
	MigrationTitleSnapshot string
	StartedAt              pgtype.Timestamptz
	FinishedAt             pgtype.Timestamptz
	DurationMS             pgtype.Int4
	Status                 string
	ErrorMessage           string
	RollbackOfExecutionID  pgtype.UUID
	CommittedBatches       int
	TotalBatches           int
	CommittedRows          int64
	IsSuperseded           bool
	SupersededByID         pgtype.UUID
	CreatedAt              time.Time
}

func (r migrationExecutionRow) toDomain() domain.MigrationExecution {
	e := domain.MigrationExecution{
		ExecutionID:            r.ExecutionID,
		RootExecutionID:        r.RootExecutionID,
		DBID:                   r.DBID,
		MigrationID:            r.MigrationID,
		SeqOrder:               r.SeqOrder,
		SeqVersion:             r.SeqVersion,
		EffectiveSeq:           r.EffectiveSeq,
		MigrationRevision:      r.MigrationRevision,
		ActionType:             r.ActionType,
		ExecutionMode:          r.ExecutionMode,
		ExecutedSQL:            r.ExecutedSQL,
		ExecutedByUsername:     r.ExecutedByUsername,
		DBNameSnapshot:         r.DBNameSnapshot,
		MigrationTitleSnapshot: r.MigrationTitleSnapshot,
		Status:                 r.Status,
		ErrorMessage:           r.ErrorMessage,
		CommittedBatches:       r.CommittedBatches,
		TotalBatches:           r.TotalBatches,
		CommittedRows:          r.CommittedRows,
		IsSuperseded:           r.IsSuperseded,
		CreatedAt:              r.CreatedAt,
	}
	if r.ProposalID.Valid {
		id := uuid.UUID(r.ProposalID.Bytes)
		e.ProposalID = &id
	}
	if r.ExecutedBy.Valid {
		id := uuid.UUID(r.ExecutedBy.Bytes)
		e.ExecutedBy = &id
	}
	if r.StartedAt.Valid {
		t := r.StartedAt.Time
		e.StartedAt = &t
	}
	if r.FinishedAt.Valid {
		t := r.FinishedAt.Time
		e.FinishedAt = &t
	}
	if r.DurationMS.Valid {
		v := int(r.DurationMS.Int32)
		e.DurationMS = &v
	}
	if r.RollbackOfExecutionID.Valid {
		id := uuid.UUID(r.RollbackOfExecutionID.Bytes)
		e.RollbackOfExecutionID = &id
	}
	if r.SupersededByID.Valid {
		id := uuid.UUID(r.SupersededByID.Bytes)
		e.SupersededByID = &id
	}
	return e
}

const selectMigrationExecutionCols = `
	e.execution_id, e.root_execution_id, e.db_id, e.migration_id, e.proposal_id,
	e.seq_order, d.seq_version, d.effective_seq, e.migration_revision,
	e.action_type, d.execution_mode, e.executed_sql, e.executed_by,
	e.executed_by_username, e.db_name_snapshot, e.migration_title_snapshot,
	e.started_at, e.finished_at, e.duration_ms, e.status, e.error_message,
	e.rollback_of_execution_id, e.committed_batches, e.total_batches, e.committed_rows,
	d.is_superseded, d.superseded_by_migration_id, e.created_at`

func scanMigrationExecutionRow(row pgx.Row) (domain.MigrationExecution, error) {
	var r migrationExecutionRow
	err := row.Scan(
		&r.ExecutionID, &r.RootExecutionID, &r.DBID, &r.MigrationID, &r.ProposalID,
		&r.SeqOrder, &r.SeqVersion, &r.EffectiveSeq, &r.MigrationRevision,
		&r.ActionType, &r.ExecutionMode, &r.ExecutedSQL, &r.ExecutedBy,
		&r.ExecutedByUsername, &r.DBNameSnapshot, &r.MigrationTitleSnapshot,
		&r.StartedAt, &r.FinishedAt, &r.DurationMS, &r.Status, &r.ErrorMessage,
		&r.RollbackOfExecutionID, &r.CommittedBatches, &r.TotalBatches, &r.CommittedRows,
		&r.IsSuperseded, &r.SupersededByID, &r.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MigrationExecution{}, ErrNotFound
	}
	if err != nil {
		return domain.MigrationExecution{}, fmt.Errorf("scan migration_execution: %w", err)
	}
	return r.toDomain(), nil
}

func (s *Store) InsertMigrationExecution(ctx context.Context, exec domain.MigrationExecution) (domain.MigrationExecution, error) {
	if exec.ExecutionID == uuid.Nil {
		exec.ExecutionID = uuid.New()
	}
	if exec.RootExecutionID == uuid.Nil {
		exec.RootExecutionID = exec.ExecutionID
	}
	if exec.TotalBatches <= 0 {
		exec.TotalBatches = 1
	}
	if exec.Status == "" {
		exec.Status = domain.StatusQueued
	}

	var proposalArg, executedByArg, rollbackOfArg any
	if exec.ProposalID != nil {
		proposalArg = *exec.ProposalID
	}
	if exec.ExecutedBy != nil {
		executedByArg = *exec.ExecutedBy
	}
	if exec.RollbackOfExecutionID != nil {
		rollbackOfArg = *exec.RollbackOfExecutionID
	}

	_, err := s.pool.Exec(ctx, `
		insert into migration_executions (
			execution_id, root_execution_id, db_id, migration_id, proposal_id,
			migration_revision, action_type, executed_sql, executed_by,
			executed_by_username, db_name_snapshot, migration_title_snapshot,
			started_at, finished_at, duration_ms, status, error_message,
			rollback_of_execution_id, committed_batches, total_batches, committed_rows
		) values (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12,
			$13, $14, $15, $16, $17,
			$18, $19, $20, $21
		)`,
		exec.ExecutionID, exec.RootExecutionID, exec.DBID, exec.MigrationID, proposalArg,
		exec.MigrationRevision, exec.ActionType, exec.ExecutedSQL, executedByArg,
		exec.ExecutedByUsername, exec.DBNameSnapshot, exec.MigrationTitleSnapshot,
		exec.StartedAt, exec.FinishedAt, exec.DurationMS, exec.Status, exec.ErrorMessage,
		rollbackOfArg, exec.CommittedBatches, exec.TotalBatches, exec.CommittedRows,
	)
	if err != nil {
		return domain.MigrationExecution{}, fmt.Errorf("insert migration_execution: %w", mapWriteError(err))
	}
	return s.GetMigrationExecution(ctx, exec.DBID, exec.ExecutionID)
}

func (s *Store) GetMigrationExecution(ctx context.Context, dbID, executionID uuid.UUID) (domain.MigrationExecution, error) {
	return scanMigrationExecutionRow(s.pool.QueryRow(ctx, `
		select `+selectMigrationExecutionCols+`
		from migration_executions e
		join migration_definitions d on d.id = e.migration_id
		where e.execution_id = $1 and e.db_id = $2`, executionID, dbID))
}

// ListMigrationExecutions returns all migration_executions for dbID ordered by seq_order DESC.
func (s *Store) ListMigrationExecutions(ctx context.Context, dbID uuid.UUID) ([]domain.MigrationExecution, error) {
	rows, err := s.pool.Query(ctx, `
		select `+selectMigrationExecutionCols+`
		from migration_executions e
		join migration_definitions d on d.id = e.migration_id
		where e.db_id = $1
		order by e.seq_order desc`, dbID)
	if err != nil {
		return nil, fmt.Errorf("list migration_executions: %w", err)
	}
	defer rows.Close()

	var out []domain.MigrationExecution
	for rows.Next() {
		var r migrationExecutionRow
		if err := rows.Scan(
			&r.ExecutionID, &r.RootExecutionID, &r.DBID, &r.MigrationID, &r.ProposalID,
			&r.SeqOrder, &r.SeqVersion, &r.EffectiveSeq, &r.MigrationRevision,
			&r.ActionType, &r.ExecutionMode, &r.ExecutedSQL, &r.ExecutedBy,
			&r.ExecutedByUsername, &r.DBNameSnapshot, &r.MigrationTitleSnapshot,
			&r.StartedAt, &r.FinishedAt, &r.DurationMS, &r.Status, &r.ErrorMessage,
			&r.RollbackOfExecutionID, &r.CommittedBatches, &r.TotalBatches, &r.CommittedRows,
			&r.IsSuperseded, &r.SupersededByID, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan migration_execution: %w", err)
		}
		out = append(out, r.toDomain())
	}
	return out, rows.Err()
}

// GetActiveMigrationStack returns all currently Active (non-superseded) migration definitions on dbID
// ordered by effective_seq ASC, seq_version ASC.
// The top of the stack (last element) has the highest effective_seq and is the LIFO Rollback target.
func (s *Store) GetActiveMigrationStack(ctx context.Context, dbID uuid.UUID) ([]ActiveStackEntry, error) {
	defs, err := s.ListMigrationDefinitions(ctx, dbID)
	if err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `
		select distinct on (e.migration_id)
		       e.migration_id, e.execution_id, e.root_execution_id, e.action_type,
		       coalesce(e.finished_at, e.created_at)
		from migration_executions e
		join migration_definitions d on d.id = e.migration_id
		where e.db_id = $1
		  and d.is_superseded = false
		  and e.status = 'success'
		  and e.action_type in ('apply', 'forward', 'batch_retry', 'rollback', 'batch_revert')
		order by e.migration_id, e.seq_order desc`, dbID)
	if err != nil {
		return nil, fmt.Errorf("query active stack executions: %w", err)
	}
	defer rows.Close()

	type activeExecMeta struct {
		ExecutionID     uuid.UUID
		RootExecutionID uuid.UUID
		AppliedAt       time.Time
	}
	metaMap := make(map[uuid.UUID]activeExecMeta)
	for rows.Next() {
		var migID, execID, rootID uuid.UUID
		var act string
		var appliedAt time.Time
		if err := rows.Scan(&migID, &execID, &rootID, &act, &appliedAt); err != nil {
			return nil, fmt.Errorf("scan active stack execution: %w", err)
		}
		if act == domain.ActionApply || act == domain.ActionForward || act == domain.ActionBatchRetry {
			metaMap[migID] = activeExecMeta{
				ExecutionID:     execID,
				RootExecutionID: rootID,
				AppliedAt:       appliedAt,
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active stack executions: %w", err)
	}

	var stack []ActiveStackEntry
	for _, d := range defs {
		if d.IsSuperseded {
			continue
		}
		if meta, ok := metaMap[d.ID]; ok {
			d.IsActive = true
			execIDCopy := meta.ExecutionID
			d.ActiveExecutionID = &execIDCopy
			stack = append(stack, ActiveStackEntry{
				Definition:        d,
				ActiveExecutionID: meta.ExecutionID,
				RootExecutionID:   meta.RootExecutionID,
				AppliedAt:         meta.AppliedAt,
			})
		}
	}
	return stack, nil
}

// GetTopActiveMigrationForRollback returns the currently Active migration with the highest effective_seq
// (LIFO Rollback target), or nil if no migration is currently Active.
func (s *Store) GetTopActiveMigrationForRollback(ctx context.Context, dbID uuid.UUID) (*ActiveStackEntry, error) {
	stack, err := s.GetActiveMigrationStack(ctx, dbID)
	if err != nil {
		return nil, err
	}
	if len(stack) == 0 {
		return nil, nil
	}
	top := stack[len(stack)-1]
	return &top, nil
}

// GetNextEligibleMigrationForForward returns the non-superseded definition in the lowest effective_seq
// slot on dbID that is currently not Active (Linear Apply / Forward target), or nil if all slots are Active.
func (s *Store) GetNextEligibleMigrationForForward(ctx context.Context, dbID uuid.UUID) (*domain.MigrationDefinition, error) {
	defs, err := s.ListMigrationDefinitions(ctx, dbID)
	if err != nil {
		return nil, err
	}

	slotActive := make(map[int64]bool)
	for _, d := range defs {
		if !d.IsSuperseded && d.IsActive {
			slotActive[d.EffectiveSeq] = true
		}
	}

	var targetSlot int64 = -1
	for _, d := range defs {
		if !d.IsSuperseded && !slotActive[d.EffectiveSeq] {
			if targetSlot == -1 || d.EffectiveSeq < targetSlot {
				targetSlot = d.EffectiveSeq
			}
		}
	}
	if targetSlot == -1 {
		return nil, nil
	}

	// Pick the latest non-superseded definition in targetSlot (highest seq_version)
	var candidate *domain.MigrationDefinition
	for i := range defs {
		d := defs[i]
		if !d.IsSuperseded && d.EffectiveSeq == targetSlot {
			dCopy := d
			candidate = &dCopy
		}
	}
	return candidate, nil
}

// GetDatabaseLock returns the current database_migration_locks entry for dbID, or (nil, nil) if unlocked.
func (s *Store) GetDatabaseLock(ctx context.Context, dbID uuid.UUID) (*domain.DBLock, error) {
	var lock domain.DBLock
	var prevState pgtype.Text
	var nextProbe pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `
		select db_id, job_id, execution_id, migration_id, worker_id,
		       lock_state, previous_lock_state, lock_epoch, lease_until,
		       next_probe_at, acquired_at, updated_at
		from database_migration_locks
		where db_id = $1`, dbID).Scan(
		&lock.DBID, &lock.JobID, &lock.ExecutionID, &lock.MigrationID, &lock.WorkerID,
		&lock.LockState, &prevState, &lock.LockEpoch, &lock.LeaseUntil,
		&nextProbe, &lock.AcquiredAt, &lock.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get database_migration_locks: %w", err)
	}
	if prevState.Valid {
		s := prevState.String
		lock.PreviousLockState = &s
	}
	if nextProbe.Valid {
		t := nextProbe.Time
		lock.NextProbeAt = &t
	}
	return &lock, nil
}

