package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"

	"leafygo/internal/domain"
	"leafygo/internal/store"
	"leafygo/internal/target"
	"leafygo/internal/telemetry"
)

const probePollInterval = 500 * time.Millisecond

func (w *Worker) recoveryLoop(ctx, jobCtx context.Context) {
	ticker := time.NewTicker(w.cfg.RecoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.recoverOnce(ctx, jobCtx)
		}
	}
}

// Recovers expired leases, then due uncertain holds. Each needs a free job slot.
func (w *Worker) recoverOnce(ctx, jobCtx context.Context) {
	for w.acquireSlot(ctx) {
		l, err := w.st.TakeoverExpiredLock(ctx, w.cfg.ID, w.cfg.LeaseTTL)
		if err != nil || l == nil {
			w.releaseSlot()
			if err != nil {
				w.log.Error("take over expired lock", "error", err)
			}
			break
		}
		w.recoverLock(jobCtx, *l, false)
	}
	for w.acquireSlot(ctx) {
		l, err := w.st.ClaimDueReprobe(ctx, w.cfg.ID, w.cfg.ReprobeInterval)
		if err != nil || l == nil {
			w.releaseSlot()
			if err != nil {
				w.log.Error("claim uncertain re-probe", "error", err)
			}
			break
		}
		w.recoverLock(jobCtx, *l, true)
	}
}

func (w *Worker) acquireSlot(ctx context.Context) bool {
	if ctx.Err() != nil || w.stopping.Load() {
		return false
	}
	select {
	case w.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (w *Worker) releaseSlot() {
	<-w.sem
	w.signal()
}

// Settles a job whose worker vanished, holding the database as uncertain until the old session is
// proven gone. Releases the caller's slot unless a resumed run takes it.
func (w *Worker) recoverLock(ctx context.Context, l domain.DBLock, reprobe bool) {
	handedOff := false
	defer func() {
		if !handedOff {
			w.releaseSlot()
		}
	}()
	lease := store.LeaseFromLock(l)
	log := w.log.With("job_id", l.JobID, "execution_id", l.ExecutionID, "db_id", l.DBID, "lock_epoch", l.LockEpoch, "reprobe", reprobe)
	log.Info("recovering job")

	work, err := w.st.LoadJobWork(ctx, l.JobID)
	if err != nil {
		log.ErrorContext(ctx, "recovery.load_job_failed", "error", err)
		return
	}
	ctx = telemetry.ExtractTraceContext(ctx, work.Job.TraceParent, work.Job.TraceState)
	if work.Job.CorrelationID != "" {
		ctx = telemetry.WithCorrelationID(ctx, work.Job.CorrelationID)
	}
	ctx = telemetry.WithActor(ctx, "worker:"+w.cfg.ID)
	ctx = telemetry.WithDatabase(ctx, work.Job.DBID.String(), work.DB.Name)
	ctx, span := telemetry.StartSpan(ctx, "worker.recover_lock",
		attribute.String("job_id", l.JobID.String()),
		attribute.String("execution_id", l.ExecutionID.String()),
		attribute.String("db_id", l.DBID.String()),
		attribute.String("db_name", work.DB.Name),
		attribute.Bool("reprobe", reprobe),
	)
	defer span.End()

	log = log.With("action", work.Job.ActionType, "mode", work.Job.ExecutionMode, "correlation_id", work.Job.CorrelationID)
	if !reprobe && work.ExecutionStatus == domain.JobQueued {
		w.requeue(ctx, log, lease, work.Job.RetryCount, work.Job.ExecutionMode, work.Job.ActionType, work.DB.Name,
			jobErr(CodeExecutionLeaseRecovery, "worker stopped before the execution started"))
		return
	}

	password, err := w.box.Decrypt(work.DB.PasswordEnc)
	if err != nil {
		log.Error("decrypt target password", "error", err)
		w.holdUncertain(ctx, log, lease, reprobe, err)
		return
	}
	probeConn, err := target.OpenConn(ctx, work.DB, password, "leafygo-recovery-"+l.ExecutionID.String(), w.connectTimeout)
	if err != nil {
		w.holdUncertain(ctx, log, lease, reprobe, err)
		return
	}
	gone, err := w.waitSessionGone(ctx, log, probeConn, l.ExecutionID)
	if err != nil || !gone {
		target.CloseConn(probeConn)
		if err == nil {
			err = errors.New("the previous session is still alive on the target")
		}
		w.holdUncertain(ctx, log, lease, reprobe, err)
		return
	}

	if work.Job.ExecutionMode == domain.ModeNormal {
		w.settleNormal(ctx, log, lease, work, probeConn, reprobe)
		return
	}
	target.CloseConn(probeConn)
	handedOff = w.resumeBatches(ctx, log, lease, work, reprobe, password)
}

func (w *Worker) holdUncertain(ctx context.Context, log *slog.Logger, lease store.Lease, alreadyHeld bool, cause error) {
	if alreadyHeld {
		log.Warn("re-probe could not settle the execution, trying again later", "error", cause)
		return
	}
	text := (&JobError{Code: CodeUncertain, Message: "cannot prove the previous session ended: " + target.ErrorText(cause)}).Error()
	log.Warn("holding database as uncertain", "error", cause)
	if err := w.st.MarkUncertain(ctx, lease, w.cfg.ReprobeInterval, text); err != nil {
		log.Error("mark uncertain", "error", err)
	}
}

// Waits until no backend of the execution is left and the target advisory lock is free,
// terminating leftover backends on the way.
func (w *Worker) waitSessionGone(ctx context.Context, log *slog.Logger, conn *pgx.Conn, executionID uuid.UUID) (bool, error) {
	appName := target.ExecAppName(executionID)
	deadline := time.Now().Add(w.cfg.ProbeTimeout)
	for {
		pids, err := target.SessionPIDs(ctx, conn, appName)
		if err != nil {
			return false, err
		}
		if len(pids) == 0 {
			ok, err := target.TryTargetLock(ctx, conn)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		for _, pid := range pids {
			log.Warn("terminating previous execution session", "pid", pid)
			if err := target.TerminateSession(ctx, conn, pid); err != nil {
				log.Warn("terminate previous session", "pid", pid, "error", err)
			}
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(probePollInterval):
		}
	}
}

// A normal mode execution committed exactly when its evidence row exists.
func (w *Worker) settleNormal(ctx context.Context, log *slog.Logger, lease store.Lease, work store.JobWork, conn *pgx.Conn, reprobe bool) {
	found, err := target.NormalEvidence(ctx, conn, work.Job.ExecutionID)
	target.CloseConn(conn)
	if err != nil {
		w.holdUncertain(ctx, log, lease, reprobe, err)
		return
	}
	r := &run{w: w, ctx: ctx, log: log, work: work, lease: lease}
	switch {
	case found:
		log.Info("evidence found, the execution committed")
		r.finish(store.Outcome{Status: domain.JobSuccess})
	case reprobe:
		log.Info("no evidence, the execution did not commit")
		r.finish(store.Outcome{Status: domain.JobFailed, ErrText: (&JobError{Code: CodeUncertain, Message: "the commit did not reach the target"}).Error()})
	default:
		w.requeue(ctx, log, lease, work.Job.RetryCount, work.Job.ExecutionMode, work.Job.ActionType, work.DB.Name,
			jobErr(CodeExecutionLeaseRecovery, "worker lost its lease before committing"))
	}
}

// Resumes batches from checkpoints in the original direction, without revalidation.
// Reports whether the run started and took the job slot.
func (w *Worker) resumeBatches(ctx context.Context, log *slog.Logger, lease store.Lease, work store.JobWork, reprobe bool, password string) bool {
	cfg, err := parseBatchConfig(work.Migration.BatchConfig)
	if err != nil {
		log.Error("parse batch config for recovery", "error", err)
		w.holdUncertain(ctx, log, lease, reprobe, err)
		return false
	}
	if reprobe {
		if err := w.st.ResumeFromHold(ctx, lease, w.cfg.LeaseTTL); err != nil {
			log.Error("resume from uncertain hold", "error", err)
			return false
		}
	}
	r := &run{w: w, ctx: ctx, log: log, work: work, lease: lease, batch: cfg, startedAt: time.Now()}
	if err := r.open(password); err != nil {
		w.holdUncertain(ctx, log, lease, false, err)
		return false
	}

	w.jobs.Add(1)
	go func() {
		defer w.jobs.Done()
		defer w.releaseSlot()
		defer r.close()
		log.Info("resuming batches from checkpoints")
		r.execute()
	}()
	return true
}
