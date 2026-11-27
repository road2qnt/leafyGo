package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"leafygo/internal/domain"
	"leafygo/internal/store"
	"leafygo/internal/target"
	"leafygo/internal/telemetry"
)

// One execution of a claimed job on its dedicated target connection.
type run struct {
	w         *Worker
	ctx       context.Context
	log       *slog.Logger
	work      store.JobWork
	lease     store.Lease
	batch     target.BatchConfig
	conn      *pgx.Conn
	startedAt time.Time

	progressAt time.Time
	pending    *store.BatchProgress
}

func (r *run) sqlMeta(query string, params []any, batchNo int, from, to *int64) telemetry.SQLMeta {
	meta := telemetry.SQLMeta{
		Source:      telemetry.SQLSourceMigration,
		Query:       query,
		Params:      params,
		Actor:       "worker:" + r.w.cfg.ID,
		DBID:        r.work.Job.DBID.String(),
		DBName:      r.work.DB.Name,
		MigrationID: r.work.Job.MigrationID.String(),
		JobID:       r.work.Job.ID.String(),
		ExecutionID: r.work.Job.ExecutionID.String(),
		Attempt:     r.work.Job.RetryCount + 1,
		BatchNo:     batchNo,
		CursorFrom:  from,
		CursorTo:    to,
	}
	if r.batch.BatchSize > 0 {
		bs := int(r.batch.BatchSize)
		meta.BatchSize = &bs
	}
	return meta
}

const progressInterval = 250 * time.Millisecond

func (r *run) open(password string) error {
	conn, err := target.OpenExecConn(r.ctx, r.work.DB, password, r.work.Job.ExecutionID, r.w.connectTimeout)
	if err != nil {
		return err
	}
	r.conn = conn
	return nil
}

func (r *run) close() {
	if r.conn != nil {
		target.CloseConn(r.conn)
		r.conn = nil
	}
}

func (r *run) execute() {
	switch {
	case r.work.Job.ExecutionMode == domain.ModeNormal:
		r.executeNormal()
	case isForward(r.work.Job.ActionType):
		r.executeForwardBatches()
	default:
		r.executeReverseBatches()
	}
}

func isForward(action string) bool {
	return action == domain.ActionApply || action == domain.ActionForward || action == domain.ActionBatchRetry
}

func parseBatchConfig(raw []byte) (target.BatchConfig, error) {
	return target.ParseBatchConfig(raw)
}

func (r *run) forwardState() (target.ForwardState, error) {
	return target.ReadForwardState(r.ctx, r.conn, r.work.Job.RootExecutionID, r.work.Job.ExecutionID)
}

// Renews the lease in the background. Once the lease is lost it cancels the returned context
// and severs the target connection, which aborts the open transaction on the target.
type heartbeat struct {
	cancel context.CancelFunc
	lost   atomic.Bool
	done   chan struct{}
	once   sync.Once
}

func (r *run) startHeartbeat() (context.Context, *heartbeat) {
	ctx, cancel := context.WithCancel(r.ctx)
	hb := &heartbeat{cancel: cancel, done: make(chan struct{})}
	go func() {
		ticker := time.NewTicker(r.w.cfg.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-hb.done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := r.w.st.RenewLease(r.ctx, r.lease, r.w.cfg.LeaseTTL)
				if errors.Is(err, store.ErrLeaseLost) {
					r.log.Warn("lease lost, aborting execution and leaving it to recovery")
					hb.lost.Store(true)
					cancel()
					target.AbortConn(r.conn)
					return
				}
				if err != nil {
					r.log.Error("renew lease", "error", err)
				}
			}
		}
	}()
	return ctx, hb
}

func (hb *heartbeat) stop() {
	hb.once.Do(func() { close(hb.done) })
	hb.cancel()
}

// Reports whether the job was taken away from this worker by a lost lease or by shutdown.
func (r *run) abandoned(hb *heartbeat) bool {
	return hb.lost.Load() || r.ctx.Err() != nil
}

func (r *run) finish(o store.Outcome) {
	r.flushProgress()
	var dur time.Duration
	if !r.startedAt.IsZero() {
		dur = time.Since(r.startedAt)
	}
	log := r.log.With("status", o.Status, "duration_ms", dur.Milliseconds())
	if o.ErrText != "" {
		log = log.With("error", o.ErrText)
	}
	if err := r.w.st.FinishJob(r.ctx, r.lease, o); err != nil {
		if errors.Is(err, store.ErrLeaseLost) {
			log.WarnContext(r.ctx, "execution.lease_lost_before_outcome")
			return
		}
		log.ErrorContext(r.ctx, "execution.record_outcome_failed", "err", err)
		return
	}
	if r.w.metrics != nil {
		r.w.metrics.JobCompleted(o.Status, r.work.Job.ExecutionMode, r.work.Job.ActionType, r.work.DB.Name, dur)
	}
	if o.Status == domain.JobSuccess {
		log.InfoContext(r.ctx, "execution.finished")
	} else {
		log.WarnContext(r.ctx, "execution.finished")
	}
}

func (r *run) markUncertain(cause error) {
	r.flushProgress()
	var dur time.Duration
	if !r.startedAt.IsZero() {
		dur = time.Since(r.startedAt)
	}
	text := (&JobError{Code: CodeUncertain, Message: "commit outcome unknown: " + target.ErrorText(cause)}).Error()
	r.log.WarnContext(r.ctx, "execution.uncertain", "status", domain.JobUncertain, "duration_ms", dur.Milliseconds(), "error", cause)
	if r.w.metrics != nil {
		r.w.metrics.JobCompleted(domain.JobUncertain, r.work.Job.ExecutionMode, r.work.Job.ActionType, r.work.DB.Name, dur)
	}
	if err := r.w.st.MarkUncertain(r.ctx, r.lease, r.w.cfg.ReprobeInterval, text); err != nil {
		r.log.ErrorContext(r.ctx, "execution.mark_uncertain_failed", "status", "error", "error", err)
	}
}

func isUncertain(err error) bool {
	var re *target.RunError
	return errors.As(err, &re) && re.Uncertain
}

func failedText(err error) string {
	return (&JobError{Code: CodeMigrationFailed, Message: target.ErrorText(err)}).Error()
}
