package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	StatusQueued            = "queued"
	StatusRunning           = "running"
	StatusSuccess           = "success"
	StatusFailed            = "failed"
	StatusPartial           = "partial"
	StatusPartiallyReverted = "partially_reverted"
	StatusUncertain         = "uncertain"
)

const (
	LockStateRunning               = "running"
	LockStatePartialHold           = "partial_hold"
	LockStatePartiallyRevertedHold = "partially_reverted_hold"
	LockStateUncertainHold         = "uncertain_hold"
)

type MigrationExecution struct {
	ExecutionID            uuid.UUID  `json:"execution_id"`
	RootExecutionID        uuid.UUID  `json:"root_execution_id"`
	DBID                   uuid.UUID  `json:"db_id"`
	MigrationID            uuid.UUID  `json:"migration_id"`
	ProposalID             *uuid.UUID `json:"proposal_id,omitempty"`
	SeqOrder               int64      `json:"seq_order"`
	SeqVersion             int64      `json:"seq_version"`
	EffectiveSeq           int64      `json:"effective_seq"`
	MigrationRevision      int        `json:"migration_revision"`
	ActionType             string     `json:"action_type"`
	ExecutionMode          string     `json:"execution_mode"`
	ExecutedSQL            string     `json:"executed_sql"`
	ExecutedBy             *uuid.UUID `json:"executed_by,omitempty"`
	ExecutedByUsername     string     `json:"executed_by_username"`
	DBNameSnapshot         string     `json:"db_name_snapshot"`
	MigrationTitleSnapshot string     `json:"migration_title_snapshot"`
	StartedAt              *time.Time `json:"started_at,omitempty"`
	FinishedAt             *time.Time `json:"finished_at,omitempty"`
	DurationMS             *int       `json:"duration_ms,omitempty"`
	Status                 string     `json:"status"`
	ErrorMessage           string     `json:"error_message"`
	RollbackOfExecutionID  *uuid.UUID `json:"rollback_of_execution_id,omitempty"`
	CommittedBatches       int        `json:"committed_batches"`
	TotalBatches           int        `json:"total_batches"`
	CommittedRows          int64      `json:"committed_rows"`
	IsSuperseded           bool       `json:"is_superseded"`
	SupersededByID         *uuid.UUID `json:"superseded_by_migration_id,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
}

type MigrationJob struct {
	JobID            uuid.UUID  `json:"job_id"`
	QueueSeq         int64      `json:"queue_seq"`
	DBID             uuid.UUID  `json:"db_id"`
	ProposalID       uuid.UUID  `json:"proposal_id"`
	MigrationID      uuid.UUID  `json:"migration_id"`
	ExecutionID      uuid.UUID  `json:"execution_id"`
	ActionType       string     `json:"action_type"`
	ExecutionMode    string     `json:"execution_mode"`
	Status           string     `json:"status"`
	RetryCount       int        `json:"retry_count"`
	MaxRetries       int        `json:"max_retries"`
	NextRunAt        time.Time  `json:"next_run_at"`
	LastCursor       *int64     `json:"last_cursor,omitempty"`
	ScopeMinCursor   *int64     `json:"scope_min_cursor,omitempty"`
	ScopeMaxCursor   *int64     `json:"scope_max_cursor,omitempty"`
	TotalBatches     int        `json:"total_batches"`
	CompletedBatches int        `json:"completed_batches"`
	CommittedRows    int64      `json:"committed_rows"`
	TraceParent      string     `json:"trace_parent"`
	TraceState       string     `json:"trace_state"`
	CorrelationID    string     `json:"correlation_id"`
	WorkerID         *string    `json:"worker_id,omitempty"`
	LastError        string     `json:"last_error"`
	QueuedAt         time.Time  `json:"queued_at"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type BatchCheckpoint struct {
	RootExecutionID       uuid.UUID  `json:"root_execution_id"`
	BatchNo               int        `json:"batch_no"`
	WrittenByExecutionID  uuid.UUID  `json:"written_by_execution_id"`
	CursorFrom            int64      `json:"cursor_from"`
	CursorTo              int64      `json:"cursor_to"`
	RowsAffected          int64      `json:"rows_affected"`
	LockEpoch             int64      `json:"lock_epoch"`
	CommittedAt           time.Time  `json:"committed_at"`
	RevertedAt            *time.Time `json:"reverted_at,omitempty"`
	RevertedByExecutionID *uuid.UUID `json:"reverted_by_execution_id,omitempty"`
}

type DBLock struct {
	DBID              uuid.UUID  `json:"db_id"`
	JobID             uuid.UUID  `json:"job_id"`
	ExecutionID       uuid.UUID  `json:"execution_id"`
	MigrationID       uuid.UUID  `json:"migration_id"`
	WorkerID          string     `json:"worker_id"`
	LockState         string     `json:"lock_state"`
	PreviousLockState *string    `json:"previous_lock_state,omitempty"`
	LockEpoch         int64      `json:"lock_epoch"`
	LeaseUntil        time.Time  `json:"lease_until"`
	NextProbeAt       *time.Time `json:"next_probe_at,omitempty"`
	AcquiredAt        time.Time  `json:"acquired_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

