package worker

import (
	"testing"

	"github.com/google/uuid"

	"leafygo/internal/domain"
	"leafygo/internal/store"
)

func baseWork(action, mode string) store.JobWork {
	return store.JobWork{
		Job:              domain.MigrationJob{ActionType: action, ExecutionMode: mode},
		Migration:        store.MigrationSpec{ID: uuid.New(), Revision: 2, EffectiveSeq: 3, ExecutionMode: mode},
		ProposalStatus:   "running",
		ProposalRevision: 2,
	}
}

func baseFacts() store.RevalidationFacts {
	g := uuid.New()
	return store.RevalidationFacts{
		Active:         map[uuid.UUID]int64{},
		CurrentGroups:  []uuid.UUID{g},
		ApprovedGroups: map[uuid.UUID]bool{g: true},
	}
}

func TestRevalidate(t *testing.T) {
	cases := []struct {
		name     string
		action   string
		mode     string
		previous string
		mutate   func(w *store.JobWork, f *store.RevalidationFacts)
		want     string
	}{
		{name: "apply with every lower slot applied", action: domain.ActionApply, mode: domain.ModeNormal,
			mutate: func(w *store.JobWork, f *store.RevalidationFacts) {
				f.LowerSlots = []int64{1, 2}
				f.Active[uuid.New()] = 1
				f.Active[uuid.New()] = 2
			}},
		{name: "database deleted", action: domain.ActionApply, mode: domain.ModeNormal,
			mutate: func(w *store.JobWork, f *store.RevalidationFacts) { w.DBDeleted = true }, want: CodeDBDeleted},
		{name: "proposal withdrawn", action: domain.ActionApply, mode: domain.ModeNormal,
			mutate: func(w *store.JobWork, f *store.RevalidationFacts) { w.ProposalStatus = "withdrawn" }, want: CodeProposalStateChanged},
		{name: "definition edited after approval", action: domain.ActionApply, mode: domain.ModeNormal,
			mutate: func(w *store.JobWork, f *store.RevalidationFacts) { w.Migration.Revision = 3 }, want: CodeRevisionChanged},
		{name: "lower slot not applied", action: domain.ActionApply, mode: domain.ModeNormal,
			mutate: func(w *store.JobWork, f *store.RevalidationFacts) {
				f.LowerSlots = []int64{1, 2}
				f.Active[uuid.New()] = 1
			}, want: CodePredecessorFailed},
		{name: "apply twice", action: domain.ActionApply, mode: domain.ModeNormal,
			mutate: func(w *store.JobWork, f *store.RevalidationFacts) { f.Active[w.Migration.ID] = 3 }, want: CodeAlreadyActive},
		{name: "slot taken by another chain member", action: domain.ActionForward, mode: domain.ModeNormal,
			mutate: func(w *store.JobWork, f *store.RevalidationFacts) { f.ChainBlocked = true }, want: CodeNotSupersedable},
		{name: "rollback of the latest migration", action: domain.ActionRollback, mode: domain.ModeNormal,
			mutate: func(w *store.JobWork, f *store.RevalidationFacts) {
				f.Active[w.Migration.ID] = 3
				f.Active[uuid.New()] = 2
				f.Target = &store.TargetFacts{MigrationID: w.Migration.ID}
			}},
		{name: "rollback out of LIFO order", action: domain.ActionRollback, mode: domain.ModeNormal,
			mutate: func(w *store.JobWork, f *store.RevalidationFacts) {
				f.Active[w.Migration.ID] = 3
				f.Active[uuid.New()] = 4
				f.Target = &store.TargetFacts{MigrationID: w.Migration.ID}
			}, want: CodePredecessorFailed},
		{name: "rollback of migration that is not applied", action: domain.ActionRollback, mode: domain.ModeNormal,
			mutate: func(w *store.JobWork, f *store.RevalidationFacts) {
				f.Target = &store.TargetFacts{MigrationID: w.Migration.ID}
			}, want: CodePredecessorFailed},
		{name: "rollback of irreversible migration", action: domain.ActionRollback, mode: domain.ModeBatch,
			mutate: func(w *store.JobWork, f *store.RevalidationFacts) {
				w.Migration.IsIrreversible = true
				f.Active[w.Migration.ID] = 3
				f.Target = &store.TargetFacts{MigrationID: w.Migration.ID}
			}, want: CodeUnsafeBatchRevert},
		{name: "rollback target of another migration", action: domain.ActionRollback, mode: domain.ModeNormal,
			mutate: func(w *store.JobWork, f *store.RevalidationFacts) {
				f.Target = &store.TargetFacts{MigrationID: uuid.New()}
			}, want: CodeInvalidTargetState},
		{name: "batch retry from partial", action: domain.ActionBatchRetry, mode: domain.ModeBatch, previous: domain.LockPartialHold},
		{name: "batch retry from partially reverted", action: domain.ActionBatchRetry, mode: domain.ModeBatch,
			previous: domain.LockPartiallyRevertedHold, want: CodeInvalidTargetState},
		{name: "batch revert from partially reverted", action: domain.ActionBatchRevert, mode: domain.ModeBatch, previous: domain.LockPartiallyRevertedHold},
		{name: "batch revert of irreversible migration", action: domain.ActionBatchRevert, mode: domain.ModeBatch, previous: domain.LockPartialHold,
			mutate: func(w *store.JobWork, f *store.RevalidationFacts) { w.Migration.IsIrreversible = true }, want: CodeUnsafeBatchRevert},
		{name: "batch revert on normal migration", action: domain.ActionBatchRevert, mode: domain.ModeNormal, previous: domain.LockPartialHold,
			want: CodeInvalidTargetState},
		{name: "resolve uncertain", action: domain.ActionResolveUncertain, mode: domain.ModeNormal, previous: domain.LockUncertainHold,
			want: CodeResolveUnsupported},
		{name: "no group has access", action: domain.ActionApply, mode: domain.ModeNormal,
			mutate: func(w *store.JobWork, f *store.RevalidationFacts) { f.CurrentGroups = nil }, want: CodeNoAccessGroups},
		{name: "group added after submit", action: domain.ActionApply, mode: domain.ModeNormal,
			mutate: func(w *store.JobWork, f *store.RevalidationFacts) {
				f.CurrentGroups = append(f.CurrentGroups, uuid.New())
			}, want: CodeApproverSetChanged},
		{name: "group removed after submit", action: domain.ActionApply, mode: domain.ModeNormal,
			mutate: func(w *store.JobWork, f *store.RevalidationFacts) { f.ApprovedGroups[uuid.New()] = true }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, f := baseWork(c.action, c.mode), baseFacts()
			if c.mutate != nil {
				c.mutate(&w, &f)
			}
			err := Revalidate(w, f, c.previous)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("err = %v; want nil", err)
			case c.want != "" && (err == nil || err.Code != c.want):
				t.Fatalf("err = %v; want %s", err, c.want)
			}
		})
	}
}

func TestJobErrorText(t *testing.T) {
	err := jobErr(CodePredecessorFailed, "migration #%d is not applied", 2)
	if got := err.Error(); got != "PREDECESSOR_FAILED: migration #2 is not applied" {
		t.Fatalf("Error() = %q", got)
	}
}
