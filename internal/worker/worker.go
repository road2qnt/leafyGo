package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"leafygo/internal/config"
	"leafygo/internal/domain"
	"leafygo/internal/secret"
	"leafygo/internal/store"
	"leafygo/internal/telemetry"
)

type Worker struct {
	st             *store.Store
	box            *secret.Box
	databaseURL    string
	cfg            config.WorkerConfig
	connectTimeout time.Duration
	log            *slog.Logger
	metrics        *telemetry.Metrics

	sem      chan struct{}
	wake     chan struct{}
	jobs     sync.WaitGroup
	stopping atomic.Bool

	// Called after every committed batch. Tests use it to stop a job half way.
	afterBatch func(jobID uuid.UUID, batchNo int)
}

func New(st *store.Store, box *secret.Box, databaseURL string, cfg config.WorkerConfig, connectTimeout time.Duration, log *slog.Logger) *Worker {
	if cfg.ID == "" {
		host, _ := os.Hostname()
		cfg.ID = fmt.Sprintf("%s-%d-%s", host, os.Getpid(), uuid.NewString()[:8])
	}
	if cfg.MaxConcurrentJobs <= 0 {
		cfg.MaxConcurrentJobs = 1
	}
	return &Worker{
		st:             st,
		box:            box,
		databaseURL:    databaseURL,
		cfg:            cfg,
		connectTimeout: connectTimeout,
		log:            log.With("worker_id", cfg.ID),
		sem:            make(chan struct{}, cfg.MaxConcurrentJobs),
		wake:           make(chan struct{}, 1),
	}
}

func (w *Worker) ID() string { return w.cfg.ID }

// SetMetrics attaches the Prometheus metrics collector to the worker.
func (w *Worker) SetMetrics(m *telemetry.Metrics) { w.metrics = m }

// Runs until ctx is cancelled, then stops claiming and gives running jobs ShutdownTimeout to finish.
func (w *Worker) Run(ctx context.Context) error {
	jobCtx, cancelJobs := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelJobs()

	go w.listen(ctx)
	go w.recoveryLoop(ctx, jobCtx)

	w.log.Info("worker started", "max_concurrent_jobs", w.cfg.MaxConcurrentJobs)
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()
	w.signal()
	for {
		select {
		case <-ctx.Done():
			w.shutdown(cancelJobs)
			return nil
		case <-w.wake:
		case <-ticker.C:
		}
		w.drain(ctx, jobCtx)
	}
}

func (w *Worker) shutdown(cancelJobs context.CancelFunc) {
	w.stopping.Store(true)
	w.log.Info("worker stopping, waiting for running jobs", "timeout", w.cfg.ShutdownTimeout)
	done := make(chan struct{})
	go func() {
		w.jobs.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(w.cfg.ShutdownTimeout):
		w.log.Warn("shutdown timeout reached, abandoning running jobs to lease recovery")
		cancelJobs()
		<-done
	}
	w.log.Info("worker stopped")
}

func (w *Worker) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Claims and starts jobs until the queue is empty or every slot is busy.
func (w *Worker) drain(ctx, jobCtx context.Context) {
	for ctx.Err() == nil && !w.stopping.Load() {
		select {
		case w.sem <- struct{}{}:
		default:
			return
		}
		claimed, err := w.st.ClaimNextJob(ctx, w.cfg.ID, w.cfg.LeaseTTL)
		if err != nil || claimed == nil {
			<-w.sem
			if err != nil && ctx.Err() == nil {
				w.log.Error("claim job", "error", err)
			}
			return
		}
		w.jobs.Add(1)
		go func() {
			defer w.jobs.Done()
			defer w.releaseSlot()
			w.process(jobCtx, claimed)
		}()
	}
}

// Wakes the drain loop on every enqueue and lock release. Polling covers missed notifications.
func (w *Worker) listen(ctx context.Context) {
	for ctx.Err() == nil {
		if err := w.listenOnce(ctx); err != nil && ctx.Err() == nil {
			w.log.Warn("listen connection lost, retrying", "error", err)
			select {
			case <-ctx.Done():
			case <-time.After(w.cfg.PollInterval):
			}
		}
	}
}

func (w *Worker) listenOnce(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, w.databaseURL)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	if _, err := conn.Exec(ctx, "listen "+store.JobsChannel); err != nil {
		return err
	}
	for {
		if _, err := conn.WaitForNotification(ctx); err != nil {
			return err
		}
		w.signal()
	}
}

func (w *Worker) process(ctx context.Context, claimed *store.ClaimedJob) {
	claimedAt := time.Now()
	job := claimed.Job

	ctx = telemetry.ExtractTraceContext(ctx, job.TraceParent, job.TraceState)
	if job.CorrelationID != "" {
		ctx = telemetry.WithCorrelationID(ctx, job.CorrelationID)
	}
	ctx = telemetry.WithActor(ctx, "worker:"+w.cfg.ID)
	ctx = telemetry.WithDatabase(ctx, job.DBID.String(), "")

	ctx, span := telemetry.StartSpan(ctx, "worker.process_job",
		attribute.String("worker_id", w.cfg.ID),
		attribute.String("job_id", job.ID.String()),
		attribute.String("execution_id", job.ExecutionID.String()),
		attribute.String("root_execution_id", job.RootExecutionID.String()),
		attribute.String("migration_id", job.MigrationID.String()),
		attribute.String("db_id", job.DBID.String()),
		attribute.String("action", job.ActionType),
		attribute.String("mode", job.ExecutionMode),
		attribute.Int("attempt", job.RetryCount+1),
	)
	defer span.End()

	log := w.log.With("job_id", job.ID, "execution_id", job.ExecutionID, "root_execution_id", job.RootExecutionID,
		"migration_id", job.MigrationID, "db_id", job.DBID, "action", job.ActionType, "mode", job.ExecutionMode,
		"attempt", job.RetryCount+1, "correlation_id", job.CorrelationID)
	log.InfoContext(ctx, "job.claimed", "status", "running", "lock_epoch", claimed.Lease.Epoch, "previous_lock_state", claimed.PreviousLockState)

	work, err := w.st.LoadJobWork(ctx, job.ID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		w.requeue(ctx, log, claimed.Lease, job.RetryCount, job.ExecutionMode, job.ActionType, "", fmt.Errorf("load job: %w", err))
		return
	}
	ctx = telemetry.WithDatabase(ctx, job.DBID.String(), work.DB.Name)
	span.SetAttributes(attribute.String("db_name", work.DB.Name))
	if w.metrics != nil && !job.QueuedAt.IsZero() {
		wait := claimedAt.Sub(job.QueuedAt)
		if wait < 0 {
			wait = 0
		}
		w.metrics.ObserveQueueWait(job.ExecutionMode, work.DB.Name, wait)
	}

	facts, err := w.st.LoadRevalidationFacts(ctx, work)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		w.requeue(ctx, log, claimed.Lease, job.RetryCount, job.ExecutionMode, job.ActionType, work.DB.Name, fmt.Errorf("load revalidation state: %w", err))
		return
	}
	_, revalSpan := telemetry.StartSpan(ctx, "worker.revalidate_migration",
		attribute.String("migration_id", job.MigrationID.String()),
		attribute.String("action", job.ActionType),
		attribute.String("db_name", work.DB.Name),
	)
	if jerr := Revalidate(work, facts, claimed.PreviousLockState); jerr != nil {
		revalSpan.RecordError(jerr)
		revalSpan.SetStatus(codes.Error, jerr.Error())
		revalSpan.End()
		span.RecordError(jerr)
		span.SetStatus(codes.Error, jerr.Error())
		w.failBeforeExecute(ctx, log, claimed.Lease, work, jerr)
		return
	}
	revalSpan.End()

	lockCtx, lockSpan := telemetry.StartSpan(ctx, "worker.acquire_db_lock",
		attribute.String("db_id", job.DBID.String()),
		attribute.String("db_name", work.DB.Name),
	)
	lockStart := time.Now()
	r, jerr, err := w.prepare(lockCtx, log, work, claimed.Lease)
	if w.metrics != nil {
		w.metrics.ObserveLockWait(work.DB.Name, time.Since(lockStart))
	}
	if jerr != nil {
		lockSpan.RecordError(jerr)
		lockSpan.SetStatus(codes.Error, jerr.Error())
		lockSpan.End()
		span.RecordError(jerr)
		span.SetStatus(codes.Error, jerr.Error())
		w.failBeforeExecute(ctx, log, claimed.Lease, work, jerr)
		return
	}
	if err != nil {
		lockSpan.RecordError(err)
		lockSpan.SetStatus(codes.Error, err.Error())
		lockSpan.End()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		w.requeue(ctx, log, claimed.Lease, job.RetryCount, job.ExecutionMode, job.ActionType, work.DB.Name, err)
		return
	}
	lockSpan.End()
	r.ctx = ctx
	r.startedAt = time.Now()
	defer r.close()

	if err := w.st.StartExecution(ctx, claimed.Lease, work.Migration.ID, true); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		log.ErrorContext(ctx, "execution.start_failed", "status", "error", "error", err)
		return
	}
	log.InfoContext(ctx, "execution.started", "status", "running")
	r.execute()
}

// Opens the target connection and checks the batch state. A JobError fails the job for good,
// a plain error is worth a retry.
func (w *Worker) prepare(ctx context.Context, log *slog.Logger, work store.JobWork, lease store.Lease) (*run, *JobError, error) {
	r := &run{w: w, ctx: ctx, log: log, work: work, lease: lease}
	if work.Job.ExecutionMode == domain.ModeBatch {
		cfg, err := parseBatchConfig(work.Migration.BatchConfig)
		if err != nil {
			return nil, jobErr(CodeInvalidBatchConfig, "%v", err), nil
		}
		r.batch = cfg
	}
	password, err := w.box.Decrypt(work.DB.PasswordEnc)
	if err != nil {
		return nil, jobErr(CodeTargetUnavailable, "cannot decrypt the database password"), nil
	}
	if err := r.open(password); err != nil {
		return nil, nil, err
	}
	if work.Job.ExecutionMode == domain.ModeBatch && isForward(work.Job.ActionType) {
		st, err := r.forwardState()
		if err != nil {
			r.close()
			return nil, nil, err
		}
		if st.HasReverted {
			r.close()
			return nil, jobErr(CodeBatchNoCollision, "batches of this execution were already reverted, apply the migration again instead of retrying"), nil
		}
	}
	return r, nil, nil
}

func (w *Worker) failBeforeExecute(ctx context.Context, log *slog.Logger, lease store.Lease, work store.JobWork, jerr *JobError) {
	log.WarnContext(ctx, "job.failed_before_execute", "status", "failed", "code", jerr.Code, "error", jerr.Message)
	if w.metrics != nil {
		w.metrics.JobCompleted(domain.JobFailed, work.Job.ExecutionMode, work.Job.ActionType, work.DB.Name, 0)
	}
	if err := w.st.FailBeforeExecute(ctx, lease, jerr.Error()); err != nil {
		log.ErrorContext(ctx, "job.record_failure_failed", "status", "error", "error", err)
	}
}

func (w *Worker) requeue(ctx context.Context, log *slog.Logger, lease store.Lease, retryCount int, mode, action, dbName string, cause error) {
	backoff := store.RetryBackoff(retryCount)
	failed, err := w.st.RequeueJob(ctx, lease, backoff, cause.Error())
	switch {
	case errors.Is(err, store.ErrLeaseLost):
		log.WarnContext(ctx, "job.lease_lost_before_requeue", "status", "warn", "cause", cause)
	case err != nil:
		log.ErrorContext(ctx, "job.requeue_failed", "status", "error", "error", err, "cause", cause)
	case failed:
		if w.metrics != nil {
			w.metrics.JobCompleted(domain.JobFailed, mode, action, dbName, 0)
		}
		log.WarnContext(ctx, "job.failed_retries_exhausted", "status", "failed", "error", cause)
	default:
		if w.metrics != nil {
			w.metrics.JobRetried(mode, dbName)
		}
		log.WarnContext(ctx, "job.requeued", "status", "queued", "error", cause, "backoff_ms", backoff.Milliseconds())
	}
}
