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
)

// One execution of a claimed job on its dedicated target connection.
type run struct {
	w     *Worker
	ctx   context.Context
	log   *slog.Logger
	work  store.JobWork
	lease store.Lease
	batch target.BatchConfig
	conn  *pgx.Conn
}

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
	log := r.log.With("status", o.Status)
	if o.ErrText != "" {
		log = log.With("error", o.ErrText)
	}
	if err := r.w.st.FinishJob(r.ctx, r.lease, o); err != nil {
		if errors.Is(err, store.ErrLeaseLost) {
			log.Warn("lease lost before the outcome was recorded")
			return
		}
		log.Error("record outcome", "err", err)
		return
	}
	if o.Status == domain.JobSuccess {
		log.Info("execution finished")
	} else {
		log.Warn("execution finished")
	}
}

func (r *run) markUncertain(cause error) {
	text := (&JobError{Code: CodeUncertain, Message: "commit outcome unknown: " + target.ErrorText(cause)}).Error()
	r.log.Warn("commit outcome unknown, holding the database until a re-probe settles it", "error", cause)
	if err := r.w.st.MarkUncertain(r.ctx, r.lease, r.w.cfg.ReprobeInterval, text); err != nil {
		r.log.Error("mark uncertain", "error", err)
	}
}

func isUncertain(err error) bool {
	var re *target.RunError
	return errors.As(err, &re) && re.Uncertain
}

func failedText(err error) string {
	return (&JobError{Code: CodeMigrationFailed, Message: target.ErrorText(err)}).Error()
}
