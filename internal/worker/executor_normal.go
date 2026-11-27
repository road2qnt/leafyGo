package worker

import (
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"leafygo/internal/domain"
	"leafygo/internal/store"
	"leafygo/internal/target"
	"leafygo/internal/telemetry"
)

// Runs the whole script and its evidence row in one target transaction.
func (r *run) executeNormal() {
	job := r.work.Job
	sql := r.work.Migration.SQLUp
	if job.ActionType == domain.ActionRollback {
		sql = r.work.Migration.SQLDown
	}

	ctx, hb := r.startHeartbeat()
	spanCtx, span := telemetry.StartSpan(ctx, "target.db.query",
		attribute.String("sql_source", string(telemetry.SQLSourceMigration)),
		attribute.String("mode", domain.ModeNormal),
		attribute.String("action", job.ActionType),
		attribute.String("db_id", job.DBID.String()),
		attribute.String("db_name", r.work.DB.Name),
		attribute.String("migration_id", job.MigrationID.String()),
		attribute.String("job_id", job.ID.String()),
		attribute.String("execution_id", job.ExecutionID.String()),
	)
	meta := r.sqlMeta(sql, nil, 0, nil, nil)
	start := time.Now()
	telemetry.LogSQLStartWith(r.log, spanCtx, meta)

	err := target.RunNormal(spanCtx, r.conn, sql, job.ExecutionID, job.MigrationID, job.ActionType, r.lease.Epoch, r.w.cfg.ExecTimeout)
	dur := time.Since(start)
	hb.stop()

	telemetry.LogSQLResultWith(r.log, spanCtx, meta, 0, dur, err)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	if r.abandoned(hb) {
		telemetry.LogSQLTxEndWith(r.log, spanCtx, meta, "rollback", dur, err)
		span.End()
		return
	}
	switch {
	case err == nil:
		telemetry.LogSQLTxEndWith(r.log, spanCtx, meta, "commit", dur, nil)
		span.End()
		r.finish(store.Outcome{Status: domain.JobSuccess})
	case isUncertain(err):
		span.End()
		r.markUncertain(err)
	default:
		telemetry.LogSQLTxEndWith(r.log, spanCtx, meta, "rollback", dur, err)
		span.End()
		r.finish(store.Outcome{Status: domain.JobFailed, ErrText: failedText(err)})
	}
}
