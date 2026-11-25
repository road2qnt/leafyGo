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

	"leafygo/internal/config"
	"leafygo/internal/domain"
	"leafygo/internal/secret"
	"leafygo/internal/store"
)

type Worker struct {
	st             *store.Store
	box            *secret.Box
	databaseURL    string
	cfg            config.WorkerConfig
	connectTimeout time.Duration
	log            *slog.Logger

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
			defer func() {
				<-w.sem
				w.signal()
			}()
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
	job := claimed.Job
	log := w.log.With("job_id", job.ID, "execution_id", job.ExecutionID, "root_execution_id", job.RootExecutionID,
		"db_id", job.DBID, "action", job.ActionType, "mode", job.ExecutionMode, "attempt", job.RetryCount+1,
		"correlation_id", job.CorrelationID)
	log.Info("job claimed", "lock_epoch", claimed.Lease.Epoch, "previous_lock_state", claimed.PreviousLockState)

	work, err := w.st.LoadJobWork(ctx, job.ID)
	if err != nil {
		w.requeue(ctx, log, claimed.Lease, job.RetryCount, fmt.Errorf("load job: %w", err))
		return
	}
	facts, err := w.st.LoadRevalidationFacts(ctx, work)
	if err != nil {
		w.requeue(ctx, log, claimed.Lease, job.RetryCount, fmt.Errorf("load revalidation state: %w", err))
		return
	}
	if jerr := Revalidate(work, facts, claimed.PreviousLockState); jerr != nil {
		w.failBeforeExecute(ctx, log, claimed.Lease, jerr)
		return
	}

	r, jerr, err := w.prepare(ctx, log, work, claimed.Lease)
	if jerr != nil {
		w.failBeforeExecute(ctx, log, claimed.Lease, jerr)
		return
	}
	if err != nil {
		w.requeue(ctx, log, claimed.Lease, job.RetryCount, err)
		return
	}
	defer r.close()

	if err := w.st.StartExecution(ctx, claimed.Lease, work.Migration.ID, true); err != nil {
		log.Error("start execution", "error", err)
		return
	}
	log.Info("execution started")
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

func (w *Worker) failBeforeExecute(ctx context.Context, log *slog.Logger, lease store.Lease, jerr *JobError) {
	log.Warn("job failed before execution", "code", jerr.Code, "error", jerr.Message)
	if err := w.st.FailBeforeExecute(ctx, lease, jerr.Error()); err != nil {
		log.Error("record failure", "error", err)
	}
}

func (w *Worker) requeue(ctx context.Context, log *slog.Logger, lease store.Lease, retryCount int, cause error) {
	backoff := store.RetryBackoff(retryCount)
	failed, err := w.st.RequeueJob(ctx, lease, backoff, cause.Error())
	switch {
	case errors.Is(err, store.ErrLeaseLost):
		log.Warn("lease lost before requeue", "cause", cause)
	case err != nil:
		log.Error("requeue job", "error", err, "cause", cause)
	case failed:
		log.Warn("job failed after exhausting retries", "error", cause)
	default:
		log.Warn("job requeued", "error", cause, "backoff", backoff)
	}
}
