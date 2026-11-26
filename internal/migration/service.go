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
	"leafygo/internal/telemetry"
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
	Explain(ctx context.Context, db domain.DBConnection, sql string, timeout time.Duration) (string, error)
}

const maxConcurrentMigrations = 2

type Options struct {
	AdvisoryLockTimeout time.Duration
	MigrationTimeout    time.Duration
	MaxMigrationBytes   int
	DryRunUserCooldown  time.Duration
}

// WorkflowStore defines the Milestone 2 store operations used by Service for definitions, proposals, approvals, and execution history.
type WorkflowStore interface {
	CreateMigrationDefinition(ctx context.Context, def domain.MigrationDefinition) (domain.MigrationDefinition, error)
	UpdateMigrationDefinition(ctx context.Context, def domain.MigrationDefinition) (domain.MigrationDefinition, error)
	GetMigrationDefinition(ctx context.Context, dbID, migrationID uuid.UUID) (domain.MigrationDefinition, error)
	ListMigrationDefinitions(ctx context.Context, dbID uuid.UUID) ([]domain.MigrationDefinition, error)
	SubmitProposalToReview(ctx context.Context, in store.SubmitProposalInput) (domain.MigrationProposal, error)
	GetMigrationProposal(ctx context.Context, dbID, proposalID uuid.UUID) (domain.MigrationProposal, error)
	ListMigrationProposals(ctx context.Context, dbID uuid.UUID) ([]domain.MigrationProposal, error)
	WithdrawProposal(ctx context.Context, dbID, proposalID uuid.UUID) (domain.MigrationProposal, error)
	ApproveProposal(ctx context.Context, in store.ApproveProposalInput) (store.ApproveProposalResult, error)
	RejectProposal(ctx context.Context, in store.ApproveProposalInput) (domain.MigrationProposal, error)
	ListMigrationExecutions(ctx context.Context, dbID uuid.UUID) ([]domain.MigrationExecution, error)
	GetMigrationExecution(ctx context.Context, dbID, executionID uuid.UUID) (domain.MigrationExecution, error)
	GetActiveMigrationStack(ctx context.Context, dbID uuid.UUID) ([]store.ActiveStackEntry, error)
	GetTopActiveMigrationForRollback(ctx context.Context, dbID uuid.UUID) (*store.ActiveStackEntry, error)
	GetNextEligibleMigrationForForward(ctx context.Context, dbID uuid.UUID) (*domain.MigrationDefinition, error)
}

type Service struct {
	store       HistoryStore
	workflow    WorkflowStore
	runner      Runner
	connOpener  DedicatedConnOpener
	dryRunGuard *DryRunGuard
	sem         chan struct{}
	opts        Options
}

func NewService(st HistoryStore, runner Runner, opts Options) *Service {
	if opts.AdvisoryLockTimeout <= 0 {
		opts.AdvisoryLockTimeout = 10 * time.Second
	}
	if opts.MigrationTimeout <= 0 {
		opts.MigrationTimeout = 30 * time.Second
	}
	if opts.MaxMigrationBytes <= 0 {
		opts.MaxMigrationBytes = 1048576 // 1 MiB default
	}
	var wf WorkflowStore
	if w, ok := st.(WorkflowStore); ok {
		wf = w
	}
	var opener DedicatedConnOpener
	if op, ok := runner.(DedicatedConnOpener); ok {
		opener = op
	}
	return &Service{
		store:       st,
		workflow:    wf,
		runner:      runner,
		connOpener:  opener,
		dryRunGuard: NewDryRunGuard(opts.DryRunUserCooldown),
		sem:         make(chan struct{}, maxConcurrentMigrations),
		opts:        opts,
	}
}

func (s *Service) DryRunGuard() *DryRunGuard {
	return s.dryRunGuard
}

func mapStoreWorkflowError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrMigrationImmutable):
		return fmt.Errorf("%w: %v", ErrMigrationImmutable, err)
	case errors.Is(err, store.ErrActiveProposalExists), errors.Is(err, store.ErrDuplicateActiveProposal):
		return fmt.Errorf("%w: %v", ErrDuplicateActiveProposal, err)
	case errors.Is(err, store.ErrMigrationNotSupersedable):
		return fmt.Errorf("%w: %v", ErrMigrationNotSupersedable, err)
	case errors.Is(err, store.ErrNoAccessGroups):
		return fmt.Errorf("%w: %v", ErrNoAccessGroups, err)
	case errors.Is(err, store.ErrMissingGroupLeader):
		return fmt.Errorf("%w: %v", ErrMissingGroupLeader, err)
	case errors.Is(err, store.ErrNotGroupLeader):
		return fmt.Errorf("%w: %v", ErrNotGroupLeader, err)
	case errors.Is(err, store.ErrPredecessorNotReady):
		return fmt.Errorf("%w: %v", ErrPredecessorNotReady, err)
	case errors.Is(err, store.ErrProposalNotPendingReview), errors.Is(err, store.ErrConflict):
		return fmt.Errorf("%w: %v", ErrConflict, err)
	case errors.Is(err, store.ErrInvalidMigrationDef), errors.Is(err, store.ErrInvalidProposalAction):
		return fmt.Errorf("%w: %v", ErrValidation, err)
	default:
		return err
	}
}

// QuorumGroupState represents a group in the snapshot or active db_access for quorum intersection evaluation.
type QuorumGroupState struct {
	GroupID          uuid.UUID
	GroupName        string
	SnapshotLeaderID uuid.UUID
	CurrentLeaderID  *uuid.UUID
	HasCurrentAccess bool
	IsGroupDeleted   bool
}

// EvaluateQuorumIntersection computes the intersection (Snapshot ∩ CurrentActiveDBGroups)
// and counts how many groups in the intersection have an 'approved' decision.
// Returns ErrNoAccessGroups if the intersection is empty.
func EvaluateQuorumIntersection(snapshot []QuorumGroupState, decisions map[uuid.UUID]string) (requiredGroups []QuorumGroupState, requiredCount, approvedCount int, err error) {
	for _, g := range snapshot {
		if !g.HasCurrentAccess || g.IsGroupDeleted {
			continue
		}
		requiredGroups = append(requiredGroups, g)
		requiredCount++
		if decisions[g.GroupID] == domain.DecisionApproved {
			approvedCount++
		}
	}
	if requiredCount == 0 {
		return nil, 0, 0, ErrNoAccessGroups
	}
	return requiredGroups, requiredCount, approvedCount, nil
}

// MatchActorLeaderGroups returns all group_ids in requiredGroups for which actor is the leader.
// Self-approval by a proposer who is also an active Group Leader is allowed.
// Platform Admins or users who do not lead any group in requiredGroups are rejected with ErrNotGroupLeader.
func MatchActorLeaderGroups(actor domain.User, requiredGroups []QuorumGroupState) ([]uuid.UUID, error) {
	if len(requiredGroups) == 0 {
		return nil, ErrNoAccessGroups
	}
	if actor.IsPlatformAdmin || !actor.IsActive {
		return nil, ErrNotGroupLeader
	}
	var matched []uuid.UUID
	for _, g := range requiredGroups {
		effectiveLeader := g.SnapshotLeaderID
		if g.CurrentLeaderID != nil && *g.CurrentLeaderID != uuid.Nil {
			effectiveLeader = *g.CurrentLeaderID
		}
		if effectiveLeader == actor.ID || g.SnapshotLeaderID == actor.ID {
			matched = append(matched, g.GroupID)
		}
	}
	if len(matched) == 0 {
		return nil, ErrNotGroupLeader
	}
	return matched, nil
}

// SlotReadinessState describes the state of an effective_seq slot on a database for Predecessor Gate evaluation.
type SlotReadinessState struct {
	EffectiveSeq             int64
	IsActive                 bool
	HasQueuedOrRunningApply  bool
	HasQueuedOrRunningRevert bool
}

// CheckPredecessorGate evaluates the Task 4 Predecessor Approval Gate (409 PREDECESSOR_NOT_READY).
func CheckPredecessorGate(actionType string, targetEffectiveSeq int64, slots []SlotReadinessState) error {
	slotMap := make(map[int64]SlotReadinessState, len(slots))
	for _, s := range slots {
		slotMap[s.EffectiveSeq] = s
	}

	switch actionType {
	case domain.ActionApply, domain.ActionForward:
		if cur, ok := slotMap[targetEffectiveSeq]; ok {
			if cur.IsActive || cur.HasQueuedOrRunningApply {
				return ErrPredecessorNotReady
			}
		}
		for _, s := range slots {
			if s.EffectiveSeq < targetEffectiveSeq {
				if !s.IsActive && !s.HasQueuedOrRunningApply {
					return ErrPredecessorNotReady
				}
			}
		}
		return nil

	case domain.ActionRollback:
		cur, ok := slotMap[targetEffectiveSeq]
		if !ok || !cur.IsActive {
			return ErrPredecessorNotReady
		}
		for _, s := range slots {
			if s.EffectiveSeq > targetEffectiveSeq && s.IsActive && !s.HasQueuedOrRunningRevert {
				return ErrPredecessorNotReady
			}
		}
		return nil

	default:
		return nil
	}
}

type SubmitProposalRequest struct {
	Actor             domain.User
	DB                domain.DBConnection
	Level             domain.AccessLevel
	MigrationID       uuid.UUID
	ActionType        string
	TargetExecutionID *uuid.UUID
	Resolution        *string
	Reason            string
}

type SubmitProposalResponse struct {
	Proposal domain.MigrationProposal `json:"proposal"`
	DryRun   DryRunResult             `json:"dry_run"`
}

// SubmitProposal validates RBAC (LevelEdit required), runs the Task 3 lexical guard and pre-review dry-run
// (BEGIN ... ROLLBACK) on Target DB, and snapshots migration_proposal_approvers in pending_review.
func (s *Service) SubmitProposal(ctx context.Context, req SubmitProposalRequest) (SubmitProposalResponse, error) {
	if req.Actor.IsPlatformAdmin || req.Level < domain.LevelEdit {
		return SubmitProposalResponse{}, ErrForbidden
	}
	if s.workflow == nil {
		return SubmitProposalResponse{}, errors.New("workflow store not configured")
	}

	def, err := s.workflow.GetMigrationDefinition(ctx, req.DB.ID, req.MigrationID)
	if err != nil {
		return SubmitProposalResponse{}, mapStoreWorkflowError(err)
	}

	proposalID := uuid.New()
	var dryRes DryRunResult
	if req.ActionType == domain.ActionResolveUncertain {
		dryRes = DryRunResult{Status: DryRunStatusSkipped}
	} else if s.connOpener != nil {
		dryRes, err = RunPreReviewDryRun(ctx, s.connOpener, s.dryRunGuard, req.DB, req.Actor.ID, proposalID, def, req.ActionType)
		if err != nil {
			return SubmitProposalResponse{}, err
		}
	} else {
		if err := ValidateDefinitionLexical(def, req.ActionType); err != nil {
			return SubmitProposalResponse{}, err
		}
		dryRes = DryRunResult{Status: DryRunStatusPassed}
	}

	prop, err := s.workflow.SubmitProposalToReview(ctx, store.SubmitProposalInput{
		ID:                proposalID,
		DBID:              req.DB.ID,
		MigrationID:       req.MigrationID,
		ActionType:        req.ActionType,
		TargetExecutionID: req.TargetExecutionID,
		Resolution:        req.Resolution,
		Reason:            req.Reason,
		ProposedBy:        req.Actor.ID,
	})
	if err != nil {
		return SubmitProposalResponse{}, mapStoreWorkflowError(err)
	}

	return SubmitProposalResponse{
		Proposal: prop,
		DryRun:   dryRes,
	}, nil
}

func (s *Service) WithdrawProposal(ctx context.Context, actor domain.User, level domain.AccessLevel, dbID, proposalID uuid.UUID) (domain.MigrationProposal, error) {
	if actor.IsPlatformAdmin || level < domain.LevelEdit {
		return domain.MigrationProposal{}, ErrForbidden
	}
	if s.workflow == nil {
		return domain.MigrationProposal{}, errors.New("workflow store not configured")
	}
	prop, err := s.workflow.WithdrawProposal(ctx, dbID, proposalID)
	if err != nil {
		return domain.MigrationProposal{}, mapStoreWorkflowError(err)
	}
	return prop, nil
}

func (s *Service) ApproveProposal(ctx context.Context, in store.ApproveProposalInput) (store.ApproveProposalResult, error) {
	if in.Actor.IsPlatformAdmin {
		return store.ApproveProposalResult{}, ErrNotGroupLeader
	}
	if s.workflow == nil {
		return store.ApproveProposalResult{}, errors.New("workflow store not configured")
	}
	res, err := s.workflow.ApproveProposal(ctx, in)
	if err != nil {
		return store.ApproveProposalResult{}, mapStoreWorkflowError(err)
	}
	return res, nil
}

func (s *Service) RejectProposal(ctx context.Context, in store.ApproveProposalInput) (domain.MigrationProposal, error) {
	if in.Actor.IsPlatformAdmin {
		return domain.MigrationProposal{}, ErrNotGroupLeader
	}
	if s.workflow == nil {
		return domain.MigrationProposal{}, errors.New("workflow store not configured")
	}
	prop, err := s.workflow.RejectProposal(ctx, in)
	if err != nil {
		return domain.MigrationProposal{}, mapStoreWorkflowError(err)
	}
	return prop, nil
}

func (s *Service) CreateDefinition(ctx context.Context, actor domain.User, level domain.AccessLevel, def domain.MigrationDefinition) (domain.MigrationDefinition, error) {
	if actor.IsPlatformAdmin || level < domain.LevelEdit {
		return domain.MigrationDefinition{}, ErrForbidden
	}
	if s.workflow == nil {
		return domain.MigrationDefinition{}, errors.New("workflow store not configured")
	}
	def.CreatedBy = &actor.ID
	def.UpdatedBy = &actor.ID
	created, err := s.workflow.CreateMigrationDefinition(ctx, def)
	if err != nil {
		return domain.MigrationDefinition{}, mapStoreWorkflowError(err)
	}
	return created, nil
}

func (s *Service) UpdateDefinition(ctx context.Context, actor domain.User, level domain.AccessLevel, def domain.MigrationDefinition) (domain.MigrationDefinition, error) {
	if actor.IsPlatformAdmin || level < domain.LevelEdit {
		return domain.MigrationDefinition{}, ErrForbidden
	}
	if s.workflow == nil {
		return domain.MigrationDefinition{}, errors.New("workflow store not configured")
	}
	def.UpdatedBy = &actor.ID
	updated, err := s.workflow.UpdateMigrationDefinition(ctx, def)
	if err != nil {
		return domain.MigrationDefinition{}, mapStoreWorkflowError(err)
	}
	return updated, nil
}

// ListHistory returns all migration execution attempts for dbID, filtered with FilterHistoryExecutions
// when hideRollback is true. Requires LevelView+ access (and rejects PlatformAdmin).
func (s *Service) ListHistory(ctx context.Context, actor domain.User, level domain.AccessLevel, dbID uuid.UUID, hideRollback bool) ([]domain.MigrationExecution, error) {
	if actor.IsPlatformAdmin || level < domain.LevelView {
		return nil, ErrForbidden
	}
	if s.workflow == nil {
		return nil, errors.New("workflow store not configured")
	}
	execs, err := s.workflow.ListMigrationExecutions(ctx, dbID)
	if err != nil {
		return nil, mapStoreWorkflowError(err)
	}
	return FilterHistoryExecutions(execs, hideRollback), nil
}

func (s *Service) GetHistoryExecution(ctx context.Context, actor domain.User, level domain.AccessLevel, dbID, executionID uuid.UUID) (domain.MigrationExecution, error) {
	if actor.IsPlatformAdmin || level < domain.LevelView {
		return domain.MigrationExecution{}, ErrForbidden
	}
	if s.workflow == nil {
		return domain.MigrationExecution{}, errors.New("workflow store not configured")
	}
	exec, err := s.workflow.GetMigrationExecution(ctx, dbID, executionID)
	if err != nil {
		return domain.MigrationExecution{}, mapStoreWorkflowError(err)
	}
	return exec, nil
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

type ExplainInput struct {
	Actor   domain.User
	DB      domain.DBConnection
	Level   domain.AccessLevel
	DraftID uuid.UUID
	Token   int
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
		// Never persist PostgreSQL row-detail values (Key (...) / Failing row) in
		// history or expose them to log aggregation.
		errText = telemetry.Sanitize(errText)
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

func (s *Service) ExplainDraft(ctx context.Context, in ExplainInput) (string, error) {
	// 1. Level check
	if in.Level < domain.LevelEdit {
		return "", ErrForbidden
	}

	// 2. Load draft
	draft, err := s.store.GetMigrationDraft(ctx, in.DB.ID, in.DraftID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", ErrNotFound
		}
		return "", err
	}

	// 3. Token check
	if in.Token != draft.Revision {
		return "", ErrConflict
	}

	// 4. Lightweight validation
	trimmedSQL := strings.TrimSpace(in.SQL)
	if trimmedSQL == "" || (s.opts.MaxMigrationBytes > 0 && len(in.SQL) > s.opts.MaxMigrationBytes) || strings.ContainsRune(in.SQL, 0) {
		return "", ErrValidation
	}

	// 5. Single statement check
	stmts, err := Statements(in.SQL)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrValidation, err)
	}
	if len(stmts) != 1 {
		return "", fmt.Errorf("%w: explain requires exactly one statement, got %d", ErrValidation, len(stmts))
	}

	// 6. Reject tx control
	if err := RejectTxControl(stmts); err != nil {
		return "", fmt.Errorf("%w: %w", ErrValidation, err)
	}

	// 7. Check if explainable (DDL not supported message)
	if !IsExplainable(stmts[0]) {
		return "EXPLAIN tidak berlaku untuk DDL.", nil
	}

	// 8. Execute target explain
	out, err := s.runner.Explain(ctx, in.DB, in.SQL, s.opts.MigrationTimeout)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrTarget, err)
	}

	return out, nil
}
