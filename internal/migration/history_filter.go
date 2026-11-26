package migration

import (
	"fmt"

	"github.com/google/uuid"

	"leafygo/internal/domain"
)

// FilterHistoryExecutions implements the Task 5 Item 4b "Hide rollback" filter algorithm,
// including Root-Group Refinement for multi-attempt batch root cycles.
//
// Absolute Rule: rows with status == "failed" or action_type == "resolve_uncertain" are NEVER hidden.
func FilterHistoryExecutions(execs []domain.MigrationExecution, hideRollback bool) []domain.MigrationExecution {
	if !hideRollback {
		return execs
	}
	hiddenIDs := make(map[uuid.UUID]bool)
	for _, exec := range execs {
		if (exec.ActionType == domain.ActionRollback || exec.ActionType == domain.ActionBatchRevert) &&
			exec.Status == domain.StatusSuccess &&
			exec.RollbackOfExecutionID != nil {
			// 1. Hide this successful rollback / batch_revert row
			hiddenIDs[exec.ExecutionID] = true
			// 2. Hide its direct target rollback_of_execution_id
			hiddenIDs[*exec.RollbackOfExecutionID] = true

			// 3. Root-Group Refinement (Item 4b):
			// Also hide all intermediate forward/retry attempts (e.g. partial) on the same root_execution_id
			// that occurred before this revert, EXCEPT rows with status == 'failed'.
			for _, other := range execs {
				if other.RootExecutionID == exec.RootExecutionID &&
					(other.ActionType == domain.ActionApply || other.ActionType == domain.ActionForward || other.ActionType == domain.ActionBatchRetry) &&
					other.SeqOrder < exec.SeqOrder &&
					other.Status != domain.StatusFailed {
					hiddenIDs[other.ExecutionID] = true
				}
			}
		}
	}

	result := make([]domain.MigrationExecution, 0, len(execs))
	for _, exec := range execs {
		// Absolute Rule: status == 'failed' and action_type == 'resolve_uncertain' are NEVER hidden
		if exec.Status == domain.StatusFailed || exec.ActionType == domain.ActionResolveUncertain {
			result = append(result, exec)
			continue
		}
		if !hiddenIDs[exec.ExecutionID] {
			result = append(result, exec)
		}
	}
	return result
}

// ComputeExecutionLineage computes (root_execution_id, rollback_of_execution_id) according to the
// exact Task 5 Item 4a table.
func ComputeExecutionLineage(
	newExecutionID uuid.UUID,
	actionType, executionMode string,
	targetExecution *domain.MigrationExecution,
	rootExecutions []domain.MigrationExecution,
) (rootExecutionID uuid.UUID, rollbackOfExecutionID *uuid.UUID, err error) {
	switch actionType {
	case domain.ActionApply, domain.ActionForward:
		return newExecutionID, nil, nil

	case domain.ActionRollback:
		if targetExecution == nil {
			return uuid.Nil, nil, fmt.Errorf("%w: rollback requires target_execution_id", ErrValidation)
		}
		if executionMode == domain.ExecutionModeBatch {
			highestID, ok := findHighestSeqOrderForwardExecution(targetExecution.RootExecutionID, rootExecutions)
			if !ok {
				highestID = targetExecution.ExecutionID
			}
			return targetExecution.RootExecutionID, &highestID, nil
		}
		tID := targetExecution.ExecutionID
		return newExecutionID, &tID, nil

	case domain.ActionBatchRetry:
		if targetExecution == nil {
			return uuid.Nil, nil, fmt.Errorf("%w: batch_retry requires target_execution_id", ErrValidation)
		}
		return targetExecution.RootExecutionID, nil, nil

	case domain.ActionBatchRevert:
		if targetExecution == nil {
			return uuid.Nil, nil, fmt.Errorf("%w: batch_revert requires target_execution_id", ErrValidation)
		}
		highestID, ok := findHighestSeqOrderForwardExecution(targetExecution.RootExecutionID, rootExecutions)
		if !ok {
			highestID = targetExecution.ExecutionID
		}
		return targetExecution.RootExecutionID, &highestID, nil

	case domain.ActionResolveUncertain:
		if targetExecution == nil {
			return uuid.Nil, nil, fmt.Errorf("%w: resolve_uncertain requires target_execution_id", ErrValidation)
		}
		tID := targetExecution.ExecutionID
		return targetExecution.RootExecutionID, &tID, nil

	default:
		return uuid.Nil, nil, fmt.Errorf("%w: unsupported action_type %q", ErrValidation, actionType)
	}
}

func findHighestSeqOrderForwardExecution(rootID uuid.UUID, execs []domain.MigrationExecution) (uuid.UUID, bool) {
	var bestID uuid.UUID
	var bestSeq int64 = -1
	found := false
	for _, e := range execs {
		if e.RootExecutionID != rootID {
			continue
		}
		if e.ActionType == domain.ActionApply || e.ActionType == domain.ActionForward || e.ActionType == domain.ActionBatchRetry {
			if !found || e.SeqOrder > bestSeq {
				bestSeq = e.SeqOrder
				bestID = e.ExecutionID
				found = true
			}
		}
	}
	return bestID, found
}

// ValidateLinearApplyOrForwardTarget verifies that target is not superseded and occupies
// the lowest effective_seq slot among all definitions on the database that is not currently Active.
func ValidateLinearApplyOrForwardTarget(target domain.MigrationDefinition, allDefs []domain.MigrationDefinition) error {
	if target.IsSuperseded {
		return ErrMigrationImmutable
	}
	if target.IsActive {
		return ErrPredecessorNotReady
	}

	slotActive := make(map[int64]bool)
	for _, d := range allDefs {
		if !d.IsSuperseded && d.IsActive {
			slotActive[d.EffectiveSeq] = true
		}
	}
	if slotActive[target.EffectiveSeq] {
		return ErrPredecessorNotReady
	}

	for _, d := range allDefs {
		if !d.IsSuperseded && !slotActive[d.EffectiveSeq] && d.EffectiveSeq < target.EffectiveSeq {
			return ErrPredecessorNotReady
		}
	}
	return nil
}

// ValidateLIFORollbackTarget verifies that target is currently Active, is not irreversible,
// and has the highest effective_seq among all currently Active definitions on the database.
func ValidateLIFORollbackTarget(target domain.MigrationDefinition, allDefs []domain.MigrationDefinition) error {
	if target.IsSuperseded || !target.IsActive {
		return ErrPredecessorNotReady
	}
	if target.IsIrreversible {
		return fmt.Errorf("%w: cannot rollback an irreversible migration", ErrValidation)
	}

	for _, d := range allDefs {
		if !d.IsSuperseded && d.IsActive && d.EffectiveSeq > target.EffectiveSeq {
			return ErrPredecessorNotReady
		}
	}
	return nil
}

