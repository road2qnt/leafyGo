package queryconsole

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"leafygo/internal/domain"
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
		s.logger.Warn("query console concurrency saturated",
			"db_id", in.DB.ID.String(),
			"actor_id", in.Actor.ID.String(),
			"status", "busy",
		)
		return Result{}, ErrBusy
	}
	defer func() { <-s.sem }()

	// 4. Target execution
	limits := Limits{
		Timeout:        s.opts.QueryTimeout,
		MaxRows:        s.opts.MaxRows,
		MaxCellBytes:   s.opts.MaxCellBytes,
		MaxResultBytes: s.opts.MaxResultBytes,
	}

	start := time.Now()
	res, execErr := s.runner.Execute(ctx, in.DB, stmt, limits)
	duration := time.Since(start)

	// 5. Safe metadata-only logging (never log SQL, rows, DSN, or passwords)
	if execErr != nil {
		sqlStateClass := ""
		var tgtErr *TargetError
		if errorsAs(execErr, &tgtErr) && len(tgtErr.Code) >= 2 {
			sqlStateClass = tgtErr.Code[:2]
		}
		s.logger.Warn("query console execution failed",
			"db_id", in.DB.ID.String(),
			"actor_id", in.Actor.ID.String(),
			"duration_ms", duration.Milliseconds(),
			"status", "error",
			"sqlstate_class", sqlStateClass,
		)
		return Result{}, execErr
	}

	s.logger.Info("query console execution succeeded",
		"db_id", in.DB.ID.String(),
		"actor_id", in.Actor.ID.String(),
		"duration_ms", res.Duration.Milliseconds(),
		"row_count", res.RowCount,
		"truncated", res.Truncated,
		"status", "success",
	)

	return res, nil
}

func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}
