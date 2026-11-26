package worker

import (
	"time"

	"leafygo/internal/domain"
	"leafygo/internal/store"
	"leafygo/internal/target"
)

// Runs the whole script and its evidence row in one target transaction.
func (r *run) executeNormal() {
	job := r.work.Job
	sql := r.work.Migration.SQLUp
	if job.ActionType == domain.ActionRollback {
		sql = r.work.Migration.SQLDown
	}

	ctx, hb := r.startHeartbeat()
	start := time.Now()
	r.log.Info("sql start", "sql_source", "migration")
	err := target.RunNormal(ctx, r.conn, sql, job.ExecutionID, job.MigrationID, job.ActionType, r.lease.Epoch, r.w.cfg.ExecTimeout)
	hb.stop()
	if r.abandoned(hb) {
		return
	}
	switch {
	case err == nil:
		r.log.Info("sql commit", "sql_source", "migration", "duration_ms", time.Since(start).Milliseconds())
		r.finish(store.Outcome{Status: domain.JobSuccess})
	case isUncertain(err):
		r.markUncertain(err)
	default:
		r.log.Warn("sql rollback", "sql_source", "migration", "duration_ms", time.Since(start).Milliseconds(), "error", err)
		r.finish(store.Outcome{Status: domain.JobFailed, ErrText: failedText(err)})
	}
}
