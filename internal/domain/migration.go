package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	ExecutionModeNormal = "normal"
	ExecutionModeBatch  = "batch"
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
	ProposalDraft             = "draft"
	ProposalPendingReview     = "pending_review"
	ProposalWithdrawn         = "withdrawn"
	ProposalApproved          = "approved"
	ProposalRejected          = "rejected"
	ProposalQueued            = "queued"
	ProposalRunning           = "running"
	ProposalPartial           = "partial"
	ProposalPartiallyReverted = "partially_reverted"
	ProposalUncertain         = "uncertain"
	ProposalCompleted         = "completed"
	ProposalFailed            = "failed"
)

const (
	DecisionApproved = "approved"
	DecisionRejected = "rejected"
)

const (
	ResolutionCommitted    = "committed"
	ResolutionNotCommitted = "not_committed"
)

type BatchConfig struct {
	TableName    string `json:"table_name"`
	CursorCol    string `json:"cursor_col"`
	BatchSize    int    `json:"batch_size"`
	UpdatedAtCol string `json:"updated_at_col,omitempty"`
}

type MigrationDefinition struct {
	ID                      uuid.UUID           `json:"id"`
	DBID                    uuid.UUID           `json:"db_id"`
	SeqVersion              int64               `json:"seq_version"`
	EffectiveSeq            int64               `json:"effective_seq"`
	Revision                int                 `json:"revision"`
	Title                   string              `json:"title"`
	Description             string              `json:"description"`
	SQLUp                   string              `json:"sql_up"`
	SQLDown                 string              `json:"sql_down"`
	IsIrreversible          bool                `json:"is_irreversible"`
	IrreversibleReason      string              `json:"irreversible_reason"`
	ExecutionMode           string              `json:"execution_mode"`
	BatchConfig             *BatchConfig        `json:"batch_config,omitempty"`
	IsImmutable             bool                `json:"is_immutable"`
	ReplacesMigrationID     *uuid.UUID          `json:"replaces_migration_id,omitempty"`
	IsSuperseded            bool                `json:"is_superseded"`
	SupersededByMigrationID *uuid.UUID          `json:"superseded_by_migration_id,omitempty"`
	SupersededAt            *time.Time          `json:"superseded_at,omitempty"`
	CreatedBy               *uuid.UUID          `json:"created_by,omitempty"`
	UpdatedBy               *uuid.UUID          `json:"updated_by,omitempty"`
	CreatedByUsername       string              `json:"created_by_username"`
	UpdatedByUsername       string              `json:"updated_by_username"`
	CreatedAt               time.Time           `json:"created_at"`
	UpdatedAt               time.Time           `json:"updated_at"`
	IsActive                bool                `json:"is_active"`
	ActiveExecutionID       *uuid.UUID          `json:"active_execution_id,omitempty"`
	LatestProposal          *MigrationProposal  `json:"latest_proposal,omitempty"`
	LatestExecution         *MigrationExecution `json:"latest_execution,omitempty"`
}

type MigrationProposal struct {
	ID                 uuid.UUID           `json:"id"`
	DBID               uuid.UUID           `json:"db_id"`
	MigrationID        uuid.UUID           `json:"migration_id"`
	MigrationRevision  int                 `json:"migration_revision"`
	ActionType         string              `json:"action_type"`
	TargetExecutionID  *uuid.UUID          `json:"target_execution_id,omitempty"`
	Resolution         *string             `json:"resolution,omitempty"`
	Reason             string              `json:"reason"`
	Status             string              `json:"status"`
	ProposedBy         *uuid.UUID          `json:"proposed_by,omitempty"`
	ProposedByUsername string              `json:"proposed_by_username"`
	CreatedAt          time.Time           `json:"created_at"`
	UpdatedAt          time.Time           `json:"updated_at"`
	Approvers          []ProposalApprover  `json:"approvers,omitempty"`
	Approvals          []MigrationApproval `json:"approvals,omitempty"`
}

type ProposalApprover struct {
	ProposalID     uuid.UUID   `json:"proposal_id"`
	GroupID        uuid.UUID   `json:"group_id"`
	GroupName      string      `json:"group_name"`
	LeaderID       uuid.UUID   `json:"leader_id"`
	LeaderUsername string      `json:"leader_username"`
	AccessLevel    AccessLevel `json:"access_level"`
}

type MigrationApproval struct {
	ProposalID     uuid.UUID `json:"proposal_id"`
	GroupID        uuid.UUID `json:"group_id"`
	GroupName      string    `json:"group_name"`
	LeaderID       uuid.UUID `json:"leader_id"`
	LeaderUsername string    `json:"leader_username"`
	Decision       string    `json:"decision"`
	Comment        string    `json:"comment"`
	DecidedAt      time.Time `json:"decided_at"`
}

