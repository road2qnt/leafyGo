package worker

import (
	"fmt"

	"leafygo/internal/domain"
	"leafygo/internal/store"
)

const (
	CodeDBDeleted              = "DB_DELETED"
	CodeProposalStateChanged   = "PROPOSAL_STATE_CHANGED"
	CodeRevisionChanged        = "MIGRATION_REVISION_CHANGED"
	CodePredecessorFailed      = "PREDECESSOR_FAILED"
	CodeAlreadyActive          = "MIGRATION_ALREADY_ACTIVE"
	CodeNotSupersedable        = "MIGRATION_NOT_SUPERSEDABLE"
	CodeUnsafeBatchRevert      = "UNSAFE_BATCH_REVERT"
	CodeInvalidTargetState     = "INVALID_TARGET_STATE"
	CodeBatchNoCollision       = "BATCH_NO_COLLISION"
	CodeNoAccessGroups         = "NO_ACCESS_GROUPS"
	CodeApproverSetChanged     = "APPROVER_SET_CHANGED"
	CodeResolveUnsupported     = "RESOLVE_UNCERTAIN_UNSUPPORTED"
	CodeUncertain              = "UNCERTAIN_MANUAL_INTERVENTION"
	CodeTargetUnavailable      = "TARGET_UNAVAILABLE"
	CodeMigrationFailed        = "MIGRATION_FAILED"
	CodeInvalidBatchConfig     = "INVALID_BATCH_CONFIG"
	CodeCheckpointConflict     = "CHECKPOINT_CONFLICT"
	CodeExecutionLeaseRecovery = "EXECUTION_LEASE_RECOVERY"
)

// A job failure with a stable code the SPA can map to a message.
type JobError struct {
	Code    string
	Message string
}

func (e *JobError) Error() string { return e.Code + ": " + e.Message }

func jobErr(code, format string, args ...any) *JobError {
	return &JobError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Checks that the claimed job may still run. previousLock is the hold state a takeover replaced.
func Revalidate(w store.JobWork, f store.RevalidationFacts, previousLock string) *JobError {
	if w.DBDeleted {
		return jobErr(CodeDBDeleted, "the database was deleted")
	}
	if w.ProposalStatus != "running" {
		return jobErr(CodeProposalStateChanged, "proposal is %s", w.ProposalStatus)
	}
	if w.ProposalRevision != w.Migration.Revision {
		return jobErr(CodeRevisionChanged, "proposal approved revision %d but the definition is at revision %d", w.ProposalRevision, w.Migration.Revision)
	}
	if err := checkOrder(w, f, previousLock); err != nil {
		return err
	}
	return checkApprovals(f)
}

func checkOrder(w store.JobWork, f store.RevalidationFacts, previousLock string) *JobError {
	m := w.Migration
	switch w.Job.ActionType {
	case domain.ActionApply, domain.ActionForward:
		if _, ok := f.Active[m.ID]; ok {
			return jobErr(CodeAlreadyActive, "migration is already applied")
		}
		activeSlots := map[int64]bool{}
		for _, seq := range f.Active {
			activeSlots[seq] = true
		}
		for _, seq := range f.LowerSlots {
			if !activeSlots[seq] {
				return jobErr(CodePredecessorFailed, "migration #%d is not applied, finish or supersede it first", seq)
			}
		}
		if f.ChainBlocked {
			return jobErr(CodeNotSupersedable, "another migration in slot #%d is applied or still queued", m.EffectiveSeq)
		}

	case domain.ActionRollback:
		if f.Target == nil || f.Target.MigrationID != m.ID {
			return jobErr(CodeInvalidTargetState, "rollback target does not belong to this migration")
		}
		if m.IsIrreversible {
			return jobErr(CodeUnsafeBatchRevert, "migration is irreversible")
		}
		if _, ok := f.Active[m.ID]; !ok {
			return jobErr(CodePredecessorFailed, "migration is not applied")
		}
		for _, seq := range f.Active {
			if seq > m.EffectiveSeq {
				return jobErr(CodePredecessorFailed, "migration #%d is applied after this one, roll it back first", seq)
			}
		}

	case domain.ActionBatchRetry:
		if m.ExecutionMode != domain.ModeBatch {
			return jobErr(CodeInvalidTargetState, "batch retry needs a batch migration")
		}
		if previousLock != domain.LockPartialHold {
			return jobErr(CodeInvalidTargetState, "batch retry needs a partial execution, the database is %q", previousLock)
		}

	case domain.ActionBatchRevert:
		if m.ExecutionMode != domain.ModeBatch {
			return jobErr(CodeInvalidTargetState, "batch revert needs a batch migration")
		}
		if m.IsIrreversible {
			return jobErr(CodeUnsafeBatchRevert, "migration is irreversible")
		}
		if previousLock != domain.LockPartialHold && previousLock != domain.LockPartiallyRevertedHold {
			return jobErr(CodeInvalidTargetState, "batch revert needs a partial or partially reverted execution, the database is %q", previousLock)
		}

	case domain.ActionResolveUncertain:
		return jobErr(CodeResolveUnsupported, "resolving uncertain executions by proposal is not supported, the worker re-probes them automatically")
	}
	return nil
}

func checkApprovals(f store.RevalidationFacts) *JobError {
	if len(f.CurrentGroups) == 0 {
		return jobErr(CodeNoAccessGroups, "no active group has access to this database")
	}
	for _, g := range f.CurrentGroups {
		if !f.ApprovedGroups[g] {
			return jobErr(CodeApproverSetChanged, "group %s gained access after the proposal was submitted, submit it again", g)
		}
	}
	return nil
}
