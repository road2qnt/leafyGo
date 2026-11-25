package migration

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"leafygo/internal/domain"
)

func TestFilterHistoryExecutionsNormalPair(t *testing.T) {
	e1Apply := uuid.New()
	e2Rollback := uuid.New()
	e3Active := uuid.New()

	execs := []domain.MigrationExecution{
		{
			ExecutionID:     e1Apply,
			RootExecutionID: e1Apply,
			SeqOrder:        1,
			ActionType:      domain.ActionApply,
			Status:          domain.StatusSuccess,
		},
		{
			ExecutionID:           e2Rollback,
			RootExecutionID:       e2Rollback,
			SeqOrder:              2,
			ActionType:            domain.ActionRollback,
			Status:                domain.StatusSuccess,
			RollbackOfExecutionID: &e1Apply,
		},
		{
			ExecutionID:     e3Active,
			RootExecutionID: e3Active,
			SeqOrder:        3,
			ActionType:      domain.ActionApply,
			Status:          domain.StatusSuccess,
		},
	}

	// hideRollback = false returns all rows
	all := FilterHistoryExecutions(execs, false)
	if len(all) != 3 {
		t.Fatalf("hideRollback=false len = %d; want 3", len(all))
	}

	// hideRollback = true hides the (e1Apply, e2Rollback) pair and keeps e3Active
	filtered := FilterHistoryExecutions(execs, true)
	if len(filtered) != 1 || filtered[0].ExecutionID != e3Active {
		t.Fatalf("hideRollback=true = %+v; want only [%v]", filtered, e3Active)
	}
}

func TestFilterHistoryExecutionsBatchRootCycleAndNeverHideFailedOrResolveUncertain(t *testing.T) {
	// Batch root cycle on root_execution_id = E1:
	// E1: apply -> partial (root=E1)
	// E2: batch_retry -> partial (root=E1)
	// E3: batch_revert -> success (root=E1, rollback_of_execution_id = E2)
	// E4: apply -> failed (root=E4)
	// E5: resolve_uncertain -> success (root=E1, rollback_of_execution_id = E1)
	e1 := uuid.New()
	e2 := uuid.New()
	e3 := uuid.New()
	e4Failed := uuid.New()
	e5Resolve := uuid.New()

	execs := []domain.MigrationExecution{
		{
			ExecutionID:     e1,
			RootExecutionID: e1,
			SeqOrder:        1,
			ActionType:      domain.ActionApply,
			Status:          domain.StatusPartial,
		},
		{
			ExecutionID:     e2,
			RootExecutionID: e1,
			SeqOrder:        2,
			ActionType:      domain.ActionBatchRetry,
			Status:          domain.StatusPartial,
		},
		{
			ExecutionID:           e3,
			RootExecutionID:       e1,
			SeqOrder:              3,
			ActionType:            domain.ActionBatchRevert,
			Status:                domain.StatusSuccess,
			RollbackOfExecutionID: &e2,
		},
		{
			ExecutionID:     e4Failed,
			RootExecutionID: e4Failed,
			SeqOrder:        4,
			ActionType:      domain.ActionApply,
			Status:          domain.StatusFailed,
		},
		{
			ExecutionID:           e5Resolve,
			RootExecutionID:       e1,
			SeqOrder:              5,
			ActionType:            domain.ActionResolveUncertain,
			Status:                domain.StatusSuccess,
			RollbackOfExecutionID: &e1,
		},
	}

	// hideRollback = false returns all 5 rows
	if got := FilterHistoryExecutions(execs, false); len(got) != 5 {
		t.Fatalf("hideRollback=false len = %d; want 5", len(got))
	}

	// hideRollback = true hides E1, E2, and E3, but NEVER hides E4 (status == 'failed') or E5 (action_type == 'resolve_uncertain')
	filtered := FilterHistoryExecutions(execs, true)
	if len(filtered) != 2 {
		t.Fatalf("hideRollback=true len = %d; want 2 (%+v)", len(filtered), filtered)
	}
	if filtered[0].ExecutionID != e4Failed || filtered[1].ExecutionID != e5Resolve {
		t.Fatalf("hideRollback=true kept [%v, %v]; want [%v, %v]",
			filtered[0].ExecutionID, filtered[1].ExecutionID, e4Failed, e5Resolve)
	}
}

func TestFilterHistoryExecutionsFailedRollbackDoesNotHideTarget(t *testing.T) {
	e1Apply := uuid.New()
	e2FailedRollback := uuid.New()

	execs := []domain.MigrationExecution{
		{
			ExecutionID:     e1Apply,
			RootExecutionID: e1Apply,
			SeqOrder:        1,
			ActionType:      domain.ActionApply,
			Status:          domain.StatusSuccess,
		},
		{
			ExecutionID:           e2FailedRollback,
			RootExecutionID:       e2FailedRollback,
			SeqOrder:              2,
			ActionType:            domain.ActionRollback,
			Status:                domain.StatusFailed,
			RollbackOfExecutionID: &e1Apply,
		},
	}

	filtered := FilterHistoryExecutions(execs, true)
	if len(filtered) != 2 {
		t.Fatalf("failed rollback must neither hide itself nor its target: got %d rows", len(filtered))
	}
}

func TestComputeExecutionLineageTable(t *testing.T) {
	newID := uuid.New()
	e1Root := uuid.New()
	e2Retry := uuid.New()

	rootExecs := []domain.MigrationExecution{
		{ExecutionID: e1Root, RootExecutionID: e1Root, SeqOrder: 1, ActionType: domain.ActionApply, Status: domain.StatusPartial},
		{ExecutionID: e2Retry, RootExecutionID: e1Root, SeqOrder: 2, ActionType: domain.ActionBatchRetry, Status: domain.StatusSuccess},
	}
	target := &rootExecs[0]

	// 1. apply -> root = newID, rollback_of = nil
	root, rbOf, err := ComputeExecutionLineage(newID, domain.ActionApply, domain.ExecutionModeNormal, nil, nil)
	if err != nil || root != newID || rbOf != nil {
		t.Fatalf("apply lineage = (%v, %v, %v)", root, rbOf, err)
	}

	// 2. forward -> root = newID, rollback_of = nil
	root, rbOf, err = ComputeExecutionLineage(newID, domain.ActionForward, domain.ExecutionModeBatch, nil, nil)
	if err != nil || root != newID || rbOf != nil {
		t.Fatalf("forward lineage = (%v, %v, %v)", root, rbOf, err)
	}

	// 3. normal rollback -> root = newID, rollback_of = target.ExecutionID
	root, rbOf, err = ComputeExecutionLineage(newID, domain.ActionRollback, domain.ExecutionModeNormal, target, rootExecs)
	if err != nil || root != newID || rbOf == nil || *rbOf != target.ExecutionID {
		t.Fatalf("normal rollback lineage = (%v, %v, %v)", root, rbOf, err)
	}

	// 4. batch rollback -> root = target.RootExecutionID, rollback_of = highest seq_order forward/retry (e2Retry)
	root, rbOf, err = ComputeExecutionLineage(newID, domain.ActionRollback, domain.ExecutionModeBatch, target, rootExecs)
	if err != nil || root != e1Root || rbOf == nil || *rbOf != e2Retry {
		t.Fatalf("batch rollback lineage = (%v, %v, %v); want root=%v rbOf=%v", root, rbOf, err, e1Root, e2Retry)
	}

	// 5. batch_retry -> root = target.RootExecutionID, rollback_of = nil
	root, rbOf, err = ComputeExecutionLineage(newID, domain.ActionBatchRetry, domain.ExecutionModeBatch, target, rootExecs)
	if err != nil || root != e1Root || rbOf != nil {
		t.Fatalf("batch_retry lineage = (%v, %v, %v)", root, rbOf, err)
	}

	// 6. batch_revert -> root = target.RootExecutionID, rollback_of = highest seq_order forward/retry (e2Retry)
	root, rbOf, err = ComputeExecutionLineage(newID, domain.ActionBatchRevert, domain.ExecutionModeBatch, target, rootExecs)
	if err != nil || root != e1Root || rbOf == nil || *rbOf != e2Retry {
		t.Fatalf("batch_revert lineage = (%v, %v, %v)", root, rbOf, err)
	}

	// 7. resolve_uncertain -> root = target.RootExecutionID, rollback_of = target.ExecutionID
	root, rbOf, err = ComputeExecutionLineage(newID, domain.ActionResolveUncertain, domain.ExecutionModeBatch, &rootExecs[1], rootExecs)
	if err != nil || root != e1Root || rbOf == nil || *rbOf != e2Retry {
		t.Fatalf("resolve_uncertain lineage = (%v, %v, %v)", root, rbOf, err)
	}
}

func TestValidateLIFORollbackAndLinearForward(t *testing.T) {
	// Scenario from spec: A(seq=1, eff=1), B(seq=2, eff=2, superseded), C(seq=3, eff=3), B'(seq=4, eff=2)
	migA := domain.MigrationDefinition{ID: uuid.New(), SeqVersion: 1, EffectiveSeq: 1, IsActive: true}
	migB := domain.MigrationDefinition{ID: uuid.New(), SeqVersion: 2, EffectiveSeq: 2, IsSuperseded: true}
	migC := domain.MigrationDefinition{ID: uuid.New(), SeqVersion: 3, EffectiveSeq: 3, IsActive: true}
	migBPrime := domain.MigrationDefinition{ID: uuid.New(), SeqVersion: 4, EffectiveSeq: 2, IsActive: true}
	defs := []domain.MigrationDefinition{migA, migB, migC, migBPrime}

	// When A(1), B'(eff=2, raw=4), and C(eff=3, raw=3) are all Active:
	// LIFO rollback must pick C(eff=3) first, NOT B'(raw=4, eff=2)!
	if err := ValidateLIFORollbackTarget(migC, defs); err != nil {
		t.Fatalf("expected C(eff=3) to be valid LIFO rollback target, got %v", err)
	}
	if err := ValidateLIFORollbackTarget(migBPrime, defs); !errors.Is(err, ErrPredecessorNotReady) {
		t.Fatalf("expected B'(eff=2) rollback while C(eff=3) active to fail with ErrPredecessorNotReady, got %v", err)
	}

	// After C and B' are rolled back (only A(1) is Active):
	// Linear Forward must pick B'(eff=2) first before C(eff=3)!
	migBPrime.IsActive = false
	migC.IsActive = false
	defsAfterRollback := []domain.MigrationDefinition{migA, migB, migC, migBPrime}

	if err := ValidateLinearApplyOrForwardTarget(migBPrime, defsAfterRollback); err != nil {
		t.Fatalf("expected B'(eff=2) to be valid Linear Forward target, got %v", err)
	}
	if err := ValidateLinearApplyOrForwardTarget(migC, defsAfterRollback); !errors.Is(err, ErrPredecessorNotReady) {
		t.Fatalf("expected C(eff=3) forward while B'(eff=2) inactive to fail with ErrPredecessorNotReady, got %v", err)
	}
}

