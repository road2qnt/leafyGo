package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	ActionApply            = "apply"
	ActionRollback         = "rollback"
	ActionForward          = "forward"
	ActionBatchRetry       = "batch_retry"
	ActionBatchRevert      = "batch_revert"
	ActionResolveUncertain = "resolve_uncertain"
)

const (
	ModeNormal = "normal"
	ModeBatch  = "batch"
)

const (
	JobQueued            = "queued"
	JobRunning           = "running"
	JobPartial           = "partial"
	JobPartiallyReverted = "partially_reverted"
	JobSuccess           = "success"
	JobFailed            = "failed"
	JobUncertain         = "uncertain"
)

const (
	LockRunning               = "running"
	LockPartialHold           = "partial_hold"
	LockPartiallyRevertedHold = "partially_reverted_hold"
	LockUncertainHold         = "uncertain_hold"
)

func ValidAction(a string) bool {
	switch a {
	case ActionApply, ActionRollback, ActionForward, ActionBatchRetry, ActionBatchRevert, ActionResolveUncertain:
		return true
	}
	return false
}

func ValidMode(m string) bool { return m == ModeNormal || m == ModeBatch }

// Reports if the action may claim a database whose lock is held by its target execution.
func TakesOverHold(action string) bool {
	return action == ActionBatchRetry || action == ActionBatchRevert || action == ActionResolveUncertain
}

// Reports if a job continues the batch scope of an earlier job on the same root execution.
func InheritsBatchScope(action, mode string) bool {
	if action == ActionBatchRetry || action == ActionBatchRevert {
		return true
	}
	return action == ActionRollback && mode == ModeBatch
}

type MigrationJob struct {
	ID               uuid.UUID
	QueueSeq         int64
	DBID             uuid.UUID
	ProposalID       uuid.UUID
	MigrationID      uuid.UUID
	ExecutionID      uuid.UUID
	RootExecutionID  uuid.UUID
	ActionType       string
	ExecutionMode    string
	Status           string
	RetryCount       int
	MaxRetries       int
	NextRunAt        time.Time
	LastCursor       *int64
	ScopeMinCursor   *int64
	ScopeMaxCursor   *int64
	TotalBatches     int
	CompletedBatches int
	CommittedRows    int64
	TraceParent      string
	TraceState       string
	CorrelationID    string
	WorkerID         string
	LastError        string
	QueuedAt         time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time
	UpdatedAt        time.Time
}

type DBLock struct {
	DBID              uuid.UUID
	JobID             uuid.UUID
	ExecutionID       uuid.UUID
	MigrationID       uuid.UUID
	WorkerID          string
	LockState         string
	PreviousLockState string
	LockEpoch         int64
	LeaseUntil        time.Time
	NextProbeAt       *time.Time
	AcquiredAt        time.Time
	UpdatedAt         time.Time
}
