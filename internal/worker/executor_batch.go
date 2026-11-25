package worker

import (
	"context"
	"errors"

	"leafygo/internal/domain"
	"leafygo/internal/store"
	"leafygo/internal/target"
)

// Applies sql_up batch by batch from the last checkpoint of the root execution. Every batch commits
// on its own together with its checkpoint, so a failure only loses the batch in flight.
func (r *run) executeForwardBatches() {
	ctx, hb := r.startHeartbeat()
	defer hb.stop()
	job := r.work.Job

	st, err := target.ReadForwardState(ctx, r.conn, job.RootExecutionID, job.ExecutionID)
	if err != nil {
		r.stopForward(hb, err, -1)
		return
	}

	scopeMin, scopeMax, total := job.ScopeMinCursor, job.ScopeMaxCursor, job.TotalBatches
	if scopeMax == nil && st.ActiveCount == 0 {
		scope, err := target.ScanScope(ctx, r.conn, r.batch)
		if err != nil {
			r.stopForward(hb, err, 0)
			return
		}
		scopeMin, scopeMax, total = scope.Min, scope.Max, scope.TotalBatches(r.batch.BatchSize)
		r.log.Info("batch scope", "scope_min_cursor", scopeMin, "scope_max_cursor", scopeMax, "rows", scope.Count, "total_batches", total)
		if err := r.w.st.UpdateJobScope(ctx, r.lease, scopeMin, scopeMax, total); err != nil {
			if !errors.Is(err, store.ErrLeaseLost) {
				r.log.Error("record batch scope", "error", err)
			}
			return
		}
	}
	if scopeMax == nil {
		if st.ActiveCount > 0 {
			r.stopForward(hb, errors.New("batch scope is missing for a root that already has checkpoints"), st.ActiveCount)
			return
		}
		r.finish(store.Outcome{Status: domain.JobSuccess})
		return
	}

	lastCursor := *scopeMin - 1
	if st.LastCursor != nil {
		lastCursor = *st.LastCursor
	}
	p := store.BatchProgress{
		LastCursor:     &lastCursor,
		JobBatches:     st.ActiveCount,
		JobRows:        st.ActiveRows,
		TotalBatches:   max(total, st.ActiveCount),
		AttemptBatches: st.AttemptCount,
		AttemptRows:    st.AttemptRows,
	}
	batchNo := st.LastBatchNo

	for {
		if !r.beforeBatch(ctx, hb) {
			return
		}
		from, to, ok, err := target.NextRange(ctx, r.conn, r.batch, lastCursor, *scopeMax)
		if err != nil {
			r.stopForward(hb, err, p.JobBatches)
			return
		}
		if !ok {
			break
		}
		batchNo++
		rows, err := target.RunForwardBatch(ctx, r.conn, r.work.Migration.SQLUp, target.BatchRun{
			RootID: job.RootExecutionID, ExecutionID: job.ExecutionID, BatchNo: batchNo,
			From: from, To: to, Epoch: r.lease.Epoch, Timeout: r.w.cfg.ExecTimeout,
		})
		if err != nil {
			r.log.Warn("batch rollback", "batch_no", batchNo, "cursor_from", from, "cursor_to", to, "error", err)
			r.stopForward(hb, err, p.JobBatches)
			return
		}
		r.log.Info("batch commit", "batch_no", batchNo, "cursor_from", from, "cursor_to", to, "rows", rows)

		lastCursor = to
		p.JobBatches++
		p.JobRows += rows
		p.AttemptBatches++
		p.AttemptRows += rows
		p.TotalBatches = max(p.TotalBatches, p.JobBatches)
		if !r.recordProgress(ctx, p) {
			return
		}
		r.afterBatch(batchNo)
	}
	r.finish(store.Outcome{Status: domain.JobSuccess})
}

// activeBatches below zero means the checkpoints could not be read, which is treated as partial.
func (r *run) stopForward(hb *heartbeat, err error, activeBatches int) {
	if r.abandoned(hb) {
		return
	}
	switch {
	case isUncertain(err):
		r.markUncertain(err)
	case errors.Is(err, target.ErrCheckpointConflict):
		r.log.Warn("checkpoint already written by another worker, stopping", "error", err)
	case activeBatches != 0 || r.work.Job.ActionType == domain.ActionBatchRetry:
		r.finish(store.Outcome{Status: domain.JobPartial, ErrText: failedText(err), HoldState: domain.LockPartialHold})
	default:
		r.finish(store.Outcome{Status: domain.JobFailed, ErrText: failedText(err)})
	}
}

// Undoes committed batches with sql_down from the highest batch down. Used by batch_revert and by
// rollback of a batch migration.
func (r *run) executeReverseBatches() {
	ctx, hb := r.startHeartbeat()
	defer hb.stop()
	job := r.work.Job

	st, err := target.ReadReverseState(ctx, r.conn, job.RootExecutionID, job.ExecutionID)
	if err != nil {
		r.stopReverse(hb, err, true)
		return
	}
	anyReverted := st.AnyReverted
	p := store.BatchProgress{
		LastCursor:     job.LastCursor,
		JobBatches:     st.Remaining,
		JobRows:        st.RemainingRows,
		TotalBatches:   max(job.TotalBatches, st.Remaining),
		AttemptBatches: st.AttemptCount,
		AttemptRows:    st.AttemptRows,
	}

	for {
		if !r.beforeBatch(ctx, hb) {
			return
		}
		cp, err := target.NextRevertBatch(ctx, r.conn, job.RootExecutionID)
		if err != nil {
			r.stopReverse(hb, err, anyReverted)
			return
		}
		if cp == nil {
			break
		}
		rows, err := target.RunReverseBatch(ctx, r.conn, r.work.Migration.SQLDown, target.BatchRun{
			RootID: job.RootExecutionID, ExecutionID: job.ExecutionID, BatchNo: cp.BatchNo,
			From: cp.CursorFrom, To: cp.CursorTo, Epoch: r.lease.Epoch, Timeout: r.w.cfg.ExecTimeout,
		})
		if err != nil {
			r.log.Warn("batch revert rollback", "batch_no", cp.BatchNo, "cursor_from", cp.CursorFrom, "cursor_to", cp.CursorTo, "error", err)
			r.stopReverse(hb, err, anyReverted)
			return
		}
		r.log.Info("batch revert commit", "batch_no", cp.BatchNo, "cursor_from", cp.CursorFrom, "cursor_to", cp.CursorTo, "rows", rows)

		anyReverted = true
		p.JobBatches--
		p.JobRows -= cp.Rows
		p.AttemptBatches++
		p.AttemptRows += rows
		p.LastCursor = &cp.CursorFrom
		if !r.recordProgress(ctx, p) {
			return
		}
		r.afterBatch(cp.BatchNo)
	}
	r.finish(store.Outcome{Status: domain.JobSuccess})
}

func (r *run) stopReverse(hb *heartbeat, err error, anyReverted bool) {
	if r.abandoned(hb) {
		return
	}
	switch {
	case isUncertain(err):
		r.markUncertain(err)
	case errors.Is(err, target.ErrCheckpointConflict):
		r.log.Warn("checkpoint already reverted by another worker, stopping", "error", err)
	case anyReverted:
		r.finish(store.Outcome{Status: domain.JobPartiallyReverted, ErrText: failedText(err), HoldState: domain.LockPartiallyRevertedHold})
	case r.work.Job.ActionType == domain.ActionBatchRevert:
		r.finish(store.Outcome{Status: domain.JobFailed, ErrText: failedText(err), RestorePreviousHold: true})
	default:
		r.finish(store.Outcome{Status: domain.JobFailed, ErrText: failedText(err)})
	}
}

// Verifies the lease before every batch so a worker that lost it never starts another batch.
// On shutdown the lease is handed off and another worker resumes from the checkpoints.
func (r *run) beforeBatch(ctx context.Context, hb *heartbeat) bool {
	if r.abandoned(hb) {
		return false
	}
	if r.w.stopping.Load() {
		if err := r.w.st.HandoffLease(r.ctx, r.lease); err != nil && !errors.Is(err, store.ErrLeaseLost) {
			r.log.Error("hand off lease", "error", err)
		}
		r.log.Info("worker stopping, handed the job off between batches")
		return false
	}
	if err := r.w.st.RenewLease(ctx, r.lease, r.w.cfg.LeaseTTL); err != nil {
		if !errors.Is(err, store.ErrLeaseLost) {
			r.log.Error("renew lease before batch", "error", err)
		}
		return false
	}
	return true
}

func (r *run) recordProgress(ctx context.Context, p store.BatchProgress) bool {
	if err := r.w.st.RecordBatchProgress(ctx, r.lease, p); err != nil {
		if !errors.Is(err, store.ErrLeaseLost) {
			r.log.Error("record batch progress", "error", err)
		}
		return false
	}
	return true
}

func (r *run) afterBatch(batchNo int) {
	if r.w.afterBatch != nil {
		r.w.afterBatch(r.work.Job.ID, batchNo)
	}
}
