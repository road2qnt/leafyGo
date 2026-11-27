package worker

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"leafygo/internal/domain"
	"leafygo/internal/store"
	"leafygo/internal/target"
	"leafygo/internal/telemetry"
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
		batchCtx, batchSpan := telemetry.StartSpan(ctx, "worker.execute_batch",
			attribute.Int("batch_no", batchNo),
			attribute.Int64("cursor_from", from),
			attribute.Int64("cursor_to", to),
			attribute.Int64("batch_size", r.batch.BatchSize),
			attribute.String("action", job.ActionType),
			attribute.String("db_name", r.work.DB.Name),
		)
		boundSQL, boundArgs := target.BindCursorRange(r.work.Migration.SQLUp, from, to)
		meta := r.sqlMeta(boundSQL, boundArgs, batchNo, &from, &to)
		telemetry.LogSQLStartWith(r.log, batchCtx, meta)
		batchStart := time.Now()
		rows, err := target.RunForwardBatch(batchCtx, r.conn, r.work.Migration.SQLUp, target.BatchRun{
			RootID: job.RootExecutionID, ExecutionID: job.ExecutionID, BatchNo: batchNo,
			From: from, To: to, Epoch: r.lease.Epoch, Timeout: r.w.cfg.ExecTimeout,
		})
		batchDur := time.Since(batchStart)
		telemetry.LogSQLResultWith(r.log, batchCtx, meta, rows, batchDur, err)
		if err != nil {
			batchSpan.RecordError(err)
			batchSpan.SetStatus(codes.Error, err.Error())
			if !isUncertain(err) {
				telemetry.LogSQLTxEndWith(r.log, batchCtx, meta, "rollback", batchDur, err)
			}
			if r.w.metrics != nil {
				r.w.metrics.BatchProcessed("failed", r.work.DB.Name, 1)
			}
			batchSpan.End()
			r.stopForward(hb, err, p.JobBatches)
			return
		}
		telemetry.LogSQLTxEndWith(r.log, batchCtx, meta, "commit", batchDur, nil)
		batchSpan.SetAttributes(attribute.Int64("rows_affected", rows))
		batchSpan.End()
		if r.w.metrics != nil {
			r.w.metrics.BatchProcessed("success", r.work.DB.Name, 1)
			r.w.metrics.RowsProcessed(job.ActionType, r.work.DB.Name, rows)
		}

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
		batchCtx, batchSpan := telemetry.StartSpan(ctx, "worker.execute_batch",
			attribute.Int("batch_no", cp.BatchNo),
			attribute.Int64("cursor_from", cp.CursorFrom),
			attribute.Int64("cursor_to", cp.CursorTo),
			attribute.Int64("batch_size", r.batch.BatchSize),
			attribute.String("action", job.ActionType),
			attribute.String("db_name", r.work.DB.Name),
		)
		boundSQL, boundArgs := target.BindCursorRange(r.work.Migration.SQLDown, cp.CursorFrom, cp.CursorTo)
		meta := r.sqlMeta(boundSQL, boundArgs, cp.BatchNo, &cp.CursorFrom, &cp.CursorTo)
		telemetry.LogSQLStartWith(r.log, batchCtx, meta)
		batchStart := time.Now()
		rows, err := target.RunReverseBatch(batchCtx, r.conn, r.work.Migration.SQLDown, target.BatchRun{
			RootID: job.RootExecutionID, ExecutionID: job.ExecutionID, BatchNo: cp.BatchNo,
			From: cp.CursorFrom, To: cp.CursorTo, Epoch: r.lease.Epoch, Timeout: r.w.cfg.ExecTimeout,
		})
		batchDur := time.Since(batchStart)
		telemetry.LogSQLResultWith(r.log, batchCtx, meta, rows, batchDur, err)
		if err != nil {
			batchSpan.RecordError(err)
			batchSpan.SetStatus(codes.Error, err.Error())
			if !isUncertain(err) {
				telemetry.LogSQLTxEndWith(r.log, batchCtx, meta, "rollback", batchDur, err)
			}
			if r.w.metrics != nil {
				r.w.metrics.BatchProcessed("failed", r.work.DB.Name, 1)
			}
			batchSpan.End()
			r.stopReverse(hb, err, anyReverted)
			return
		}
		telemetry.LogSQLTxEndWith(r.log, batchCtx, meta, "commit", batchDur, nil)
		batchSpan.SetAttributes(attribute.Int64("rows_affected", rows))
		batchSpan.End()
		if r.w.metrics != nil {
			r.w.metrics.BatchProcessed("success", r.work.DB.Name, 1)
			r.w.metrics.RowsProcessed(job.ActionType, r.work.DB.Name, rows)
		}

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
		r.flushProgress()
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

// Writes progress at most every progressInterval. Skipped writes are flushed before the outcome.
func (r *run) recordProgress(ctx context.Context, p store.BatchProgress) bool {
	if time.Since(r.progressAt) < progressInterval {
		r.pending = &p
		return true
	}
	return r.writeProgress(ctx, p)
}

func (r *run) writeProgress(ctx context.Context, p store.BatchProgress) bool {
	if err := r.w.st.RecordBatchProgress(ctx, r.lease, p); err != nil {
		if !errors.Is(err, store.ErrLeaseLost) {
			r.log.Error("record batch progress", "error", err)
		}
		return false
	}
	r.progressAt = time.Now()
	r.pending = nil
	return true
}

func (r *run) flushProgress() {
	if r.pending != nil {
		r.writeProgress(r.ctx, *r.pending)
	}
}

func (r *run) afterBatch(batchNo int) {
	if r.w.afterBatch != nil {
		r.w.afterBatch(r.work.Job.ID, batchNo)
	}
}
