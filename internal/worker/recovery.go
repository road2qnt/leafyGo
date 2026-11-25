package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"leafygo/internal/domain"
	"leafygo/internal/store"
	"leafygo/internal/target"
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

// Takes over every running lock whose lease expired, then re-probes every uncertain hold that is due.
func (w *Worker) recoverOnce(ctx, jobCtx context.Context) {
	for ctx.Err() == nil && !w.stopping.Load() {
		l, err := w.st.TakeoverExpiredLock(ctx, w.cfg.ID, w.cfg.LeaseTTL)
		if err != nil {
			w.log.Error("take over expired lock", "error", err)
			break
		}
		if l == nil {
			break
		}
		w.recoverLock(jobCtx, *l, false)
	}
	for ctx.Err() == nil && !w.stopping.Load() {
		l, err := w.st.ClaimDueReprobe(ctx, w.cfg.ID, w.cfg.ReprobeInterval)
		if err != nil {
			w.log.Error("claim uncertain re-probe", "error", err)
			break
		}
		if l == nil {
			break
		}
		w.recoverLock(jobCtx, *l, true)
	}
}

// Settles a job whose worker vanished. The old target session must be proven gone before the
// evidence tables are trusted, otherwise the database stays in uncertain_hold.
func (w *Worker) recoverLock(ctx context.Context, l domain.DBLock, reprobe bool) {
	lease := store.LeaseFromLock(l)
	log := w.log.With("job_id", l.JobID, "execution_id", l.ExecutionID, "db_id", l.DBID, "lock_epoch", l.LockEpoch, "reprobe", reprobe)
	log.Info("recovering job")

	work, err := w.st.LoadJobWork(ctx, l.JobID)
	if err != nil {
		log.Error("load job for recovery", "error", err)
		return
	}
	log = log.With("action", work.Job.ActionType, "mode", work.Job.ExecutionMode)
	if !reprobe && work.ExecutionStatus == domain.JobQueued {
		w.requeue(ctx, log, lease, work.Job.RetryCount,
			jobErr(CodeExecutionLeaseRecovery, "worker stopped before the execution started"))
		return
	}

	password, err := w.box.Decrypt(work.DB.PasswordEnc)
	if err != nil {
		log.Error("decrypt target password", "error", err)
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
	w.resumeBatches(ctx, log, lease, work, reprobe, password)
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
		w.requeue(ctx, log, lease, work.Job.RetryCount,
			jobErr(CodeExecutionLeaseRecovery, "worker lost its lease before committing"))
	}
}

// Continues a batch execution in place from its checkpoints, in the same direction it was going.
// Revalidation is skipped on purpose: the batches already committed were approved.
func (w *Worker) resumeBatches(ctx context.Context, log *slog.Logger, lease store.Lease, work store.JobWork, reprobe bool, password string) {
	if reprobe {
		if err := w.st.ResumeFromHold(ctx, lease, w.cfg.LeaseTTL); err != nil {
			log.Error("resume from uncertain hold", "error", err)
			return
		}
	}
	cfg, err := parseBatchConfig(work.Migration.BatchConfig)
	if err != nil {
		log.Error("parse batch config for recovery", "error", err)
		return
	}
	r := &run{w: w, ctx: ctx, log: log, work: work, lease: lease, batch: cfg}
	if err := r.open(password); err != nil {
		w.holdUncertain(ctx, log, lease, false, err)
		return
	}

	select {
	case w.sem <- struct{}{}:
	case <-ctx.Done():
		r.close()
		return
	}
	w.jobs.Add(1)
	go func() {
		defer w.jobs.Done()
		defer func() {
			<-w.sem
			w.signal()
		}()
		defer r.close()
		log.Info("resuming batches from checkpoints")
		r.execute()
	}()
}
