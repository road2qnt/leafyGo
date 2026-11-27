package queryconsole

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"leafygo/internal/domain"
	"leafygo/internal/telemetry"
)

// ServiceOptions defines operational configuration for the query console service.
type ServiceOptions struct {
	MaxConcurrent       int
	AdvisoryLockTimeout time.Duration
	QueryTimeout        time.Duration
	MaxQueryBytes       int64
	MaxRows             int
	MaxCellBytes        int64
	MaxResultBytes      int64
}

// Service provides ad-hoc read-only query execution with concurrency throttling and policy checks.
type Service struct {
	runner QueryRunner
	sem    chan struct{}
	logger *slog.Logger
	opts   ServiceOptions
}

// NewService creates a new query console Service.
func NewService(runner QueryRunner, logger *slog.Logger, opts ServiceOptions) *Service {
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = 4
	}
	if opts.AdvisoryLockTimeout <= 0 {
		opts.AdvisoryLockTimeout = 10 * time.Second
	}
	if opts.QueryTimeout <= 0 {
		opts.QueryTimeout = 15 * time.Second
	}
	if opts.MaxQueryBytes <= 0 {
		opts.MaxQueryBytes = 65536
	}
	if opts.MaxRows <= 0 {
		opts.MaxRows = 1000
	}
	if opts.MaxCellBytes <= 0 {
		opts.MaxCellBytes = 4096
	}
	if opts.MaxResultBytes <= 0 {
		opts.MaxResultBytes = 2097152
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Service{
		runner: runner,
		sem:    make(chan struct{}, opts.MaxConcurrent),
		logger: logger,
		opts:   opts,
	}
}

// Execute validates permissions, inspects SQL against guards, acquires a concurrency slot,
// executes the query on the target database, and logs safe audit metadata.
func (s *Service) Execute(ctx context.Context, in ExecuteInput) (Result, error) {
	// 1. Access level check (LevelView is minimum; LevelNone / Admin rejected)
	if in.Level < domain.LevelView {
		return Result{}, ErrForbidden
	}

	// 2. SQL guard inspection before target connection
	stmt, err := Validate(in.SQL, s.opts.MaxQueryBytes)
	if err != nil {
		return Result{}, err
	}

	// 3. Acquire global concurrency slot
	semCtx, cancelSem := context.WithTimeout(context.WithoutCancel(ctx), s.opts.AdvisoryLockTimeout)
	defer cancelSem()

	select {
	case s.sem <- struct{}{}:
	case <-semCtx.Done():
		s.logger.WarnContext(ctx, "query console concurrency saturated",
			"db_id", in.DB.ID.String(),
			"actor_id", in.Actor.ID.String(),
			"status", "busy",
		)
		return Result{}, ErrBusy
	}
	defer func() { <-s.sem }()

	// 4. Target execution with structured SQL logging and OpenTelemetry span
	limits := Limits{
		Timeout:        s.opts.QueryTimeout,
		MaxRows:        s.opts.MaxRows,
		MaxCellBytes:   s.opts.MaxCellBytes,
		MaxResultBytes: s.opts.MaxResultBytes,
	}

	spanCtx, span := telemetry.StartSpan(ctx, "target.db.query",
		attribute.String("sql_source", string(telemetry.SQLSourceQueryConsole)),
		attribute.String("db_id", in.DB.ID.String()),
		attribute.String("db_name", in.DB.Name),
	)
	defer span.End()

	meta := telemetry.SQLMeta{
		Source: telemetry.SQLSourceQueryConsole,
		Query:  stmt,
		Actor:  "user:" + in.Actor.Username,
		DBID:   in.DB.ID.String(),
		DBName: in.DB.Name,
	}
	telemetry.LogSQLStartWith(s.logger, spanCtx, meta)

	start := time.Now()
	res, execErr := s.runner.Execute(spanCtx, in.DB, stmt, limits)
	duration := time.Since(start)

	// 5. Structured SQL result + read-only transaction rollback log (telemetry scrubs credentials/PII)
	telemetry.LogSQLResultWith(s.logger, spanCtx, meta, int64(res.RowCount), duration, execErr)
	telemetry.LogSQLTxEndWith(s.logger, spanCtx, meta, "rollback", duration, execErr)

	if execErr != nil {
		span.RecordError(execErr)
		span.SetStatus(codes.Error, execErr.Error())
		return Result{}, execErr
	}
	span.SetAttributes(
		attribute.Int("row_count", res.RowCount),
		attribute.Bool("truncated", res.Truncated),
	)

	return res, nil
}

func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}
