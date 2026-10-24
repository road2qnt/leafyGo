package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"leafygo/internal/domain"
	"leafygo/internal/store"
	"leafygo/internal/target"
)

type HistoryStore interface {
	AcquireMigrationLock(ctx context.Context, dbID uuid.UUID) (int64, func(), error)
	GetMigrationDraft(ctx context.Context, dbID, id uuid.UUID) (domain.MigrationDraft, error)
	UpdateMigrationDraft(ctx context.Context, dbID, id uuid.UUID, title, sql string, revision int, actorID uuid.UUID) (domain.MigrationDraft, error)
	InsertRunningMigration(ctx context.Context, h domain.MigrationHistory) error
	FinishMigration(ctx context.Context, historyID, dbID uuid.UUID, status, errText string, duration time.Duration, draftID *uuid.UUID, draftRevision int) error
	ListMigrationHistory(ctx context.Context, dbID uuid.UUID, limit int) ([]domain.MigrationHistory, error)
	GetMigrationHistory(ctx context.Context, dbID, id uuid.UUID) (domain.MigrationHistory, error)
	ReapStaleRunningMigrations(ctx context.Context, dbID uuid.UUID, olderThan time.Duration) (int64, error)
}

type Runner interface {
	Run(ctx context.Context, db domain.DBConnection, sql string, timeout time.Duration) (time.Duration, error)
}

const maxConcurrentMigrations = 2

type Options struct {
	AdvisoryLockTimeout time.Duration
	MigrationTimeout    time.Duration
	MaxMigrationBytes   int
}

type Service struct {
	store  HistoryStore
	runner Runner
	sem    chan struct{}
	opts   Options
}

func NewService(store HistoryStore, runner Runner, opts Options) *Service {
	if opts.AdvisoryLockTimeout <= 0 {
		opts.AdvisoryLockTimeout = 10 * time.Second
	}
	if opts.MigrationTimeout <= 0 {
		opts.MigrationTimeout = 30 * time.Second
	}
	if opts.MaxMigrationBytes <= 0 {
		opts.MaxMigrationBytes = 1048576 // 1 MiB default
	}
	return &Service{
		store:  store,
		runner: runner,
		sem:    make(chan struct{}, maxConcurrentMigrations),
		opts:   opts,
	}
}

type ExecuteInput struct {
	Actor   domain.User
	DB      domain.DBConnection
	Level   domain.AccessLevel
	DraftID uuid.UUID
	Token   int
	Title   string
	SQL     string
}

func (s *Service) ExecuteDraft(ctx context.Context, in ExecuteInput) (domain.MigrationHistory, error) {
	// 1. Level check
	if in.Level < domain.LevelEdit {
		return domain.MigrationHistory{}, ErrForbidden
	}

	// 2. Load draft
	draft, err := s.store.GetMigrationDraft(ctx, in.DB.ID, in.DraftID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return domain.MigrationHistory{}, ErrNotFound
		}
		return domain.MigrationHistory{}, err
	}

	// 3. Token check
	if in.Token != draft.Revision {
		return domain.MigrationHistory{}, ErrConflict
	}

	// 4. Lightweight validation without store/lock/history
	trimmedSQL := strings.TrimSpace(in.SQL)
	if trimmedSQL == "" || (s.opts.MaxMigrationBytes > 0 && len(in.SQL) > s.opts.MaxMigrationBytes) || strings.ContainsRune(in.SQL, 0) {
		return domain.MigrationHistory{}, ErrValidation
	}

	// 5. Update draft with token revision -> saved with revision N+1
	saved, err := s.store.UpdateMigrationDraft(ctx, in.DB.ID, in.DraftID, in.Title, in.SQL, in.Token, in.Actor.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return domain.MigrationHistory{}, ErrNotFound
		}
		if errors.Is(err, store.ErrConflict) {
			return domain.MigrationHistory{}, ErrConflict
		}
		return domain.MigrationHistory{}, err
	}

	// 6. SQL guard: statements splitter and tx control rejection
	stmts, err := Statements(saved.SQLText)
	if err != nil {
		return domain.MigrationHistory{}, fmt.Errorf("%w: %w", ErrValidation, err)
	}
	if err := RejectTxControl(stmts); err != nil {
		return domain.MigrationHistory{}, fmt.Errorf("%w: %w", ErrValidation, err)
	}

	// 7. Acquire semaphore
	semCtx, cancelSem := context.WithTimeout(context.WithoutCancel(ctx), s.opts.AdvisoryLockTimeout)
	defer cancelSem()

	select {
	case s.sem <- struct{}{}:
	case <-semCtx.Done():
		return domain.MigrationHistory{}, ErrBusy
	}
	defer func() { <-s.sem }()

	// 8. Acquire advisory lock
	lockCtx, cancelLock := context.WithTimeout(context.WithoutCancel(ctx), s.opts.AdvisoryLockTimeout)
	defer cancelLock()

	version, releaseLock, err := s.store.AcquireMigrationLock(lockCtx, in.DB.ID)
	if err != nil {
		if errors.Is(err, store.ErrLockTimeout) || lockCtx.Err() != nil {
			return domain.MigrationHistory{}, ErrLockTimeout
		}
		return domain.MigrationHistory{}, err
	}
	defer releaseLock()

	// 9. Re-load draft after acquiring lock
	lockedDraft, err := s.store.GetMigrationDraft(lockCtx, in.DB.ID, in.DraftID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return domain.MigrationHistory{}, ErrNotFound
		}
		return domain.MigrationHistory{}, err
	}
	if lockedDraft.Revision != saved.Revision {
		return domain.MigrationHistory{}, ErrConflict
	}

	// 10. Version from AcquireMigrationLock
	// 11. Insert history 'running'
	histID := uuid.New()
	now := time.Now().UTC()
	h := domain.MigrationHistory{
		ID:         histID,
		DBID:       in.DB.ID,
		Version:    version,
		SQLText:    saved.SQLText,
		ExecutedBy: &in.Actor.ID,
		StartedAt:  now,
		Status:     domain.MigrationRunning,
	}

	insertCtx, cancelInsert := context.WithTimeout(context.WithoutCancel(ctx), s.opts.AdvisoryLockTimeout)
	defer cancelInsert()

	if err := s.store.InsertRunningMigration(insertCtx, h); err != nil {
		return domain.MigrationHistory{}, err
	}

	// 12. Run target
	runCtx, cancelRun := context.WithTimeout(context.WithoutCancel(ctx), s.opts.MigrationTimeout)
	defer cancelRun()

	dur, runErr := s.runner.Run(runCtx, in.DB, saved.SQLText, s.opts.MigrationTimeout)

	// 13. Classify execution result
	var status string
	var errText string

	if runErr == nil {
		status = domain.MigrationSuccess
	} else {
		status = domain.MigrationFailed
		var runError *target.RunError
		if errors.As(runErr, &runError) && runError.Uncertain {
			errText = "hasil tidak pasti; periksa target secara manual"
		} else {
			errText = target.ErrorText(runErr)
			if strings.Contains(errText, "tidak pasti") {
				errText = strings.ReplaceAll(errText, "tidak pasti", "gagal")
			}
		}
	}

	// 14. Finish migration
	finishCtx, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), s.opts.AdvisoryLockTimeout)
	defer cancelFinish()

	var draftIDToClean *uuid.UUID
	if status == domain.MigrationSuccess {
		draftIDToClean = &in.DraftID
	}

	finishErr := s.store.FinishMigration(finishCtx, h.ID, in.DB.ID, status, errText, dur, draftIDToClean, saved.Revision)
	if finishErr != nil {
		if errors.Is(finishErr, store.ErrHistoryNotRunning) {
			return domain.MigrationHistory{}, ErrHistoryNotRunning
		}
		return domain.MigrationHistory{}, finishErr
	}

	finTime := time.Now().UTC()
	h.FinishedAt = &finTime
	h.Status = status
	h.Error = errText
	durMS := int(dur.Milliseconds())
	h.DurationMS = &durMS

	if runErr != nil {
		return h, fmt.Errorf("%w: %w", ErrTarget, runErr)
	}

	return h, nil
}
