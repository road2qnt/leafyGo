package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"leafygo/internal/domain"
	"leafygo/internal/telemetry"
)

type SubmitProposalInput struct {
	ID                uuid.UUID
	DBID              uuid.UUID
	MigrationID       uuid.UUID
	ActionType        string
	TargetExecutionID *uuid.UUID
	Resolution        *string
	Reason            string
	ProposedBy        uuid.UUID
}

type ApproveProposalInput struct {
	DBID          uuid.UUID
	ProposalID    uuid.UUID
	Actor         domain.User
	Comment       string
	TraceParent   string
	TraceState    string
	CorrelationID string
}

type ApproveProposalResult struct {
	Proposal      domain.MigrationProposal
	RequiredCount int
	ApprovedCount int
	Enqueued      bool
	Execution     *domain.MigrationExecution
	Job           *domain.MigrationJob
}

type proposalRow struct {
	ID                 uuid.UUID
	DBID               uuid.UUID
	MigrationID        uuid.UUID
	MigrationRevision  int
	ActionType         string
	TargetExecutionID  pgtype.UUID
	Resolution         pgtype.Text
	Reason             string
	Status             string
	ProposedBy         pgtype.UUID
	ProposedByUsername string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (r proposalRow) toDomain() domain.MigrationProposal {
	p := domain.MigrationProposal{
		ID:                 r.ID,
		DBID:               r.DBID,
		MigrationID:        r.MigrationID,
		MigrationRevision:  r.MigrationRevision,
		ActionType:         r.ActionType,
		Reason:             r.Reason,
		Status:             r.Status,
		ProposedByUsername: r.ProposedByUsername,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
	}
	if r.TargetExecutionID.Valid {
		id := uuid.UUID(r.TargetExecutionID.Bytes)
		p.TargetExecutionID = &id
	}
	if r.Resolution.Valid {
		res := r.Resolution.String
		p.Resolution = &res
	}
	if r.ProposedBy.Valid {
		id := uuid.UUID(r.ProposedBy.Bytes)
		p.ProposedBy = &id
	}
	return p
}

func validActionType(actionType string) bool {
	switch actionType {
	case domain.ActionApply, domain.ActionRollback, domain.ActionForward,
		domain.ActionBatchRetry, domain.ActionBatchRevert, domain.ActionResolveUncertain:
		return true
	default:
		return false
	}
}

func (s *Store) checkConflictingDirectionProposalTx(ctx context.Context, tx pgx.Tx, dbID uuid.UUID, effectiveSeq int64, actionType string) error {
	var conflict bool
	var err error
	switch actionType {
	case domain.ActionApply, domain.ActionForward:
		err = tx.QueryRow(ctx, `
			select exists (
				select 1
				from migration_proposals p
				join migration_definitions d on d.id = p.migration_id
				where p.db_id = $1
				  and p.status in ('pending_review', 'approved', 'queued', 'running')
				  and p.action_type in ('rollback', 'batch_revert')
				  and d.effective_seq <= $2
			)`, dbID, effectiveSeq).Scan(&conflict)
		if err != nil {
			return fmt.Errorf("check conflicting rollback proposal: %w", err)
		}
		if conflict {
			return fmt.Errorf("%w: an active rollback or batch revert proposal exists for an earlier or same migration; finish or withdraw it first", ErrConflict)
		}
	case domain.ActionRollback:
		err = tx.QueryRow(ctx, `
			select exists (
				select 1
				from migration_proposals p
				join migration_definitions d on d.id = p.migration_id
				where p.db_id = $1
				  and p.status in ('pending_review', 'approved', 'queued', 'running')
				  and p.action_type in ('apply', 'forward', 'batch_retry')
				  and d.effective_seq >= $2
			)`, dbID, effectiveSeq).Scan(&conflict)
		if err != nil {
			return fmt.Errorf("check conflicting apply/forward proposal: %w", err)
		}
		if conflict {
			return fmt.Errorf("%w: an active apply or forward proposal exists for a later or same migration; finish or withdraw it first", ErrConflict)
		}
	}
	return nil
}

func scanProposalRow(row pgx.Row) (domain.MigrationProposal, error) {
	var r proposalRow
	err := row.Scan(
		&r.ID, &r.DBID, &r.MigrationID, &r.MigrationRevision,
		&r.ActionType, &r.TargetExecutionID, &r.Resolution, &r.Reason,
		&r.Status, &r.ProposedBy, &r.ProposedByUsername,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MigrationProposal{}, ErrNotFound
	}
	if err != nil {
		return domain.MigrationProposal{}, fmt.Errorf("scan migration_proposal: %w", err)
	}
	return r.toDomain(), nil
}

func (s *Store) loadProposalApproversAndApprovalsTx(ctx context.Context, q interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, proposalID uuid.UUID) ([]domain.ProposalApprover, []domain.MigrationApproval, error) {
	arows, err := q.Query(ctx, `
		select mpa.proposal_id, mpa.group_id, coalesce(g.name, ''), mpa.leader_id,
		       coalesce(u.username, ''), mpa.access_level
		from migration_proposal_approvers mpa
		left join groups g on g.id = mpa.group_id
		left join users u on u.id = mpa.leader_id
		where mpa.proposal_id = $1
		order by coalesce(g.name, '')`, proposalID)
	if err != nil {
		return nil, nil, fmt.Errorf("query proposal approvers: %w", err)
	}
	var approvers []domain.ProposalApprover
	for arows.Next() {
		var pa domain.ProposalApprover
		var levelStr string
		if err := arows.Scan(&pa.ProposalID, &pa.GroupID, &pa.GroupName, &pa.LeaderID, &pa.LeaderUsername, &levelStr); err != nil {
			arows.Close()
			return nil, nil, fmt.Errorf("scan proposal approver: %w", err)
		}
		pa.AccessLevel = domain.ParseLevel(levelStr)
		approvers = append(approvers, pa)
	}
	arows.Close()
	if err := arows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate proposal approvers: %w", err)
	}

	drows, err := q.Query(ctx, `
		select ma.proposal_id, ma.group_id, coalesce(g.name, ''), ma.leader_id,
		       coalesce(u.username, ''), ma.decision, ma.comment, ma.decided_at
		from migration_approvals ma
		left join groups g on g.id = ma.group_id
		left join users u on u.id = ma.leader_id
		where ma.proposal_id = $1
		order by ma.decided_at asc`, proposalID)
	if err != nil {
		return nil, nil, fmt.Errorf("query proposal approvals: %w", err)
	}
	var approvals []domain.MigrationApproval
	for drows.Next() {
		var ma domain.MigrationApproval
		if err := drows.Scan(&ma.ProposalID, &ma.GroupID, &ma.GroupName, &ma.LeaderID, &ma.LeaderUsername, &ma.Decision, &ma.Comment, &ma.DecidedAt); err != nil {
			drows.Close()
			return nil, nil, fmt.Errorf("scan proposal approval: %w", err)
		}
		approvals = append(approvals, ma)
	}
	drows.Close()
	if err := drows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate proposal approvals: %w", err)
	}
	return approvers, approvals, nil
}

// SubmitProposalToReview freezes the migration_proposal_approvers snapshot and moves a proposal to 'pending_review'.
func (s *Store) SubmitProposalToReview(ctx context.Context, in SubmitProposalInput) (domain.MigrationProposal, error) {
	if !validActionType(in.ActionType) {
		return domain.MigrationProposal{}, fmt.Errorf("%w: invalid action_type %q", ErrInvalidProposalAction, in.ActionType)
	}
	if in.ActionType == domain.ActionResolveUncertain {
		if in.TargetExecutionID == nil || in.Resolution == nil ||
			(*in.Resolution != domain.ResolutionCommitted && *in.Resolution != domain.ResolutionNotCommitted) ||
			strings.TrimSpace(in.Reason) == "" {
			return domain.MigrationProposal{}, fmt.Errorf("%w: resolve_uncertain requires target_execution_id, resolution, and reason", ErrInvalidProposalAction)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.MigrationProposal{}, fmt.Errorf("submit proposal begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Ensure DB connection exists and is not soft-deleted
	var lockedDBID uuid.UUID
	if err := tx.QueryRow(ctx, `select id from db_connections where id = $1 and deleted_at is null for update`, in.DBID).
		Scan(&lockedDBID); errors.Is(err, pgx.ErrNoRows) {
		return domain.MigrationProposal{}, ErrNotFound
	} else if err != nil {
		return domain.MigrationProposal{}, fmt.Errorf("lock db_connections: %w", err)
	}

	// 2. Lock migration_definitions row (SELECT ... FOR UPDATE)
	def, err := s.LockMigrationDefinitionTx(ctx, tx, in.DBID, in.MigrationID)
	if err != nil {
		return domain.MigrationProposal{}, err
	}
	if def.IsSuperseded {
		return domain.MigrationProposal{}, ErrMigrationImmutable
	}

	// 3. Check for duplicate active proposal on the same migration_id
	hasActive, err := s.HasActiveProposalTx(ctx, tx, def.ID)
	if err != nil {
		return domain.MigrationProposal{}, err
	}
	if hasActive {
		return domain.MigrationProposal{}, ErrDuplicateActiveProposal
	}

	// 3b. Block conflicting opposite-direction active proposals on the same database
	if err := s.checkConflictingDirectionProposalTx(ctx, tx, in.DBID, def.EffectiveSeq, in.ActionType); err != nil {
		return domain.MigrationProposal{}, err
	}

	// 4. Re-validate supersede rules if definition replaces another migration
	if def.ReplacesMigrationID != nil && in.ActionType == domain.ActionApply {
		if _, err := s.ResolveAndValidateSupersedeTx(ctx, tx, in.DBID, *def.ReplacesMigrationID, def.ID); err != nil {
			return domain.MigrationProposal{}, err
		}
	}

	// 5. Validate action-specific constraints
	if (in.ActionType == domain.ActionRollback || in.ActionType == domain.ActionBatchRevert) && def.IsIrreversible {
		return domain.MigrationProposal{}, fmt.Errorf("%w: cannot rollback or revert an irreversible migration", ErrInvalidProposalAction)
	}

	var targetExecArg any
	if in.TargetExecutionID != nil {
		targetExecArg = *in.TargetExecutionID
	} else if in.ActionType == domain.ActionRollback {
		// Automatically resolve target_execution_id to the current active execution of this migration if omitted
		activeMap, err := s.loadActiveMigrationMapTx(ctx, tx, in.DBID)
		if err != nil {
			return domain.MigrationProposal{}, err
		}
		if activeExecID, ok := activeMap[def.ID]; ok {
			in.TargetExecutionID = &activeExecID
			targetExecArg = activeExecID
		}
	}

	// 6. Query all active groups with access to db_id for the approver snapshot
	grows, err := tx.Query(ctx, `
		select g.id, g.name, g.leader_id, coalesce(u.username, '') as leader_name,
		       coalesce(u.is_active, false) as is_active, a.level
		from db_access a
		join groups g on g.id = a.group_id
		join db_connections d on d.id = a.db_id
		left join users u on u.id = g.leader_id
		where a.db_id = $1
		  and g.deleted_at is null
		  and d.deleted_at is null
		order by g.name`, in.DBID)
	if err != nil {
		return domain.MigrationProposal{}, fmt.Errorf("query approver groups: %w", err)
	}
	type snapshotGroup struct {
		GroupID      uuid.UUID
		GroupName    string
		LeaderID     pgtype.UUID
		LeaderName   string
		LeaderActive bool
		Level        string
	}
	var snapGroups []snapshotGroup
	for grows.Next() {
		var sg snapshotGroup
		if err := grows.Scan(&sg.GroupID, &sg.GroupName, &sg.LeaderID, &sg.LeaderName, &sg.LeaderActive, &sg.Level); err != nil {
			grows.Close()
			return domain.MigrationProposal{}, fmt.Errorf("scan approver group: %w", err)
		}
		snapGroups = append(snapGroups, sg)
	}
	grows.Close()
	if err := grows.Err(); err != nil {
		return domain.MigrationProposal{}, fmt.Errorf("iterate approver groups: %w", err)
	}

	if len(snapGroups) == 0 {
		return domain.MigrationProposal{}, ErrNoAccessGroups
	}
	for _, sg := range snapGroups {
		if !sg.LeaderID.Valid || !sg.LeaderActive {
			return domain.MigrationProposal{}, fmt.Errorf("%w: group %q does not have an active leader", ErrMissingGroupLeader, sg.GroupName)
		}
	}

	proposalID := in.ID
	if proposalID == uuid.Nil {
		proposalID = uuid.New()
	}
	var proposedByArg any
	if in.ProposedBy != uuid.Nil {
		proposedByArg = in.ProposedBy
	}
	var resolutionArg any
	if in.Resolution != nil {
		resolutionArg = *in.Resolution
	}

	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `
		insert into migration_proposals (
			id, db_id, migration_id, migration_revision, action_type,
			target_execution_id, resolution, reason, status,
			proposed_by, created_at, updated_at
		) values (
			$1, $2, $3, $4, $5,
			$6, $7, $8, 'pending_review',
			$9, $10, $10
		)`,
		proposalID, in.DBID, def.ID, def.Revision, in.ActionType,
		targetExecArg, resolutionArg, strings.TrimSpace(in.Reason),
		proposedByArg, now,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.MigrationProposal{}, ErrDuplicateActiveProposal
		}
		return domain.MigrationProposal{}, fmt.Errorf("insert migration_proposal: %w", err)
	}

	for _, sg := range snapGroups {
		leaderID := uuid.UUID(sg.LeaderID.Bytes)
		if _, err := tx.Exec(ctx, `
			insert into migration_proposal_approvers (proposal_id, group_id, leader_id, access_level)
			values ($1, $2, $3, $4)`,
			proposalID, sg.GroupID, leaderID, sg.Level); err != nil {
			return domain.MigrationProposal{}, fmt.Errorf("insert migration_proposal_approvers: %w", err)
		}
	}

	prop, err := s.getProposalTx(ctx, tx, in.DBID, proposalID)
	if err != nil {
		return domain.MigrationProposal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MigrationProposal{}, fmt.Errorf("submit proposal commit: %w", err)
	}
	return prop, nil
}

func (s *Store) getProposalTx(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, dbID, proposalID uuid.UUID) (domain.MigrationProposal, error) {
	var r proposalRow
	var jobID pgtype.UUID
	err := q.QueryRow(ctx, `
		select p.id, p.db_id, p.migration_id, p.migration_revision,
		       p.action_type, p.target_execution_id, p.resolution, p.reason,
		       p.status, p.proposed_by, coalesce(u.username, ''),
		       p.created_at, p.updated_at, j.job_id
		from migration_proposals p
		left join users u on u.id = p.proposed_by
		left join migration_jobs j on j.proposal_id = p.id
		where p.id = $1 and p.db_id = $2`, proposalID, dbID).Scan(
		&r.ID, &r.DBID, &r.MigrationID, &r.MigrationRevision,
		&r.ActionType, &r.TargetExecutionID, &r.Resolution, &r.Reason,
		&r.Status, &r.ProposedBy, &r.ProposedByUsername,
		&r.CreatedAt, &r.UpdatedAt, &jobID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MigrationProposal{}, ErrNotFound
	}
	if err != nil {
		return domain.MigrationProposal{}, fmt.Errorf("scan migration_proposal: %w", err)
	}
	prop := r.toDomain()
	if jobID.Valid {
		id := uuid.UUID(jobID.Bytes)
		prop.JobID = &id
	}
	approvers, approvals, err := s.loadProposalApproversAndApprovalsTx(ctx, q, proposalID)
	if err != nil {
		return domain.MigrationProposal{}, err
	}
	prop.Approvers = approvers
	prop.Approvals = approvals
	return prop, nil
}

func (s *Store) GetMigrationProposal(ctx context.Context, dbID, proposalID uuid.UUID) (domain.MigrationProposal, error) {
	return s.getProposalTx(ctx, s.pool, dbID, proposalID)
}

func (s *Store) ListMigrationProposals(ctx context.Context, dbID uuid.UUID) ([]domain.MigrationProposal, error) {
	rows, err := s.pool.Query(ctx, `
		select p.id, p.db_id, p.migration_id, p.migration_revision,
		       p.action_type, p.target_execution_id, p.resolution, p.reason,
		       p.status, p.proposed_by, coalesce(u.username, ''),
		       p.created_at, p.updated_at, j.job_id
		from migration_proposals p
		left join users u on u.id = p.proposed_by
		left join migration_jobs j on j.proposal_id = p.id
		where p.db_id = $1
		order by p.created_at desc`, dbID)
	if err != nil {
		return nil, fmt.Errorf("list migration_proposals: %w", err)
	}
	var proposals []domain.MigrationProposal
	for rows.Next() {
		var r proposalRow
		var jobID pgtype.UUID
		if err := rows.Scan(
			&r.ID, &r.DBID, &r.MigrationID, &r.MigrationRevision,
			&r.ActionType, &r.TargetExecutionID, &r.Resolution, &r.Reason,
			&r.Status, &r.ProposedBy, &r.ProposedByUsername,
			&r.CreatedAt, &r.UpdatedAt, &jobID,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan migration_proposal: %w", err)
		}
		p := r.toDomain()
		if jobID.Valid {
			id := uuid.UUID(jobID.Bytes)
			p.JobID = &id
		}
		proposals = append(proposals, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate migration_proposals: %w", err)
	}

	for i := range proposals {
		approvers, approvals, err := s.loadProposalApproversAndApprovalsTx(ctx, s.pool, proposals[i].ID)
		if err != nil {
			return nil, err
		}
		proposals[i].Approvers = approvers
		proposals[i].Approvals = approvals
	}
	return proposals, nil
}

// WithdrawProposal transitions a pending_review proposal to 'withdrawn', invalidates its existing approvals,
// and increments migration_definitions.revision by 1 so the definition can be edited again.
func (s *Store) WithdrawProposal(ctx context.Context, dbID, proposalID uuid.UUID) (domain.MigrationProposal, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.MigrationProposal{}, fmt.Errorf("withdraw proposal begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	prop, err := scanProposalRow(tx.QueryRow(ctx, `
		select p.id, p.db_id, p.migration_id, p.migration_revision,
		       p.action_type, p.target_execution_id, p.resolution, p.reason,
		       p.status, p.proposed_by, coalesce((select u.username from users u where u.id = p.proposed_by), ''),
		       p.created_at, p.updated_at
		from migration_proposals p
		where p.id = $1 and p.db_id = $2
		for update of p`, proposalID, dbID))
	if err != nil {
		return domain.MigrationProposal{}, err
	}
	if prop.Status != domain.ProposalPendingReview {
		return domain.MigrationProposal{}, ErrProposalNotPendingReview
	}

	if _, err := tx.Exec(ctx, `delete from migration_approvals where proposal_id = $1`, proposalID); err != nil {
		return domain.MigrationProposal{}, fmt.Errorf("clear approvals on withdraw: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update migration_proposals
		set status = 'withdrawn', updated_at = now()
		where id = $1`, proposalID); err != nil {
		return domain.MigrationProposal{}, fmt.Errorf("update proposal withdrawn: %w", err)
	}
	if _, err := s.BumpMigrationDefinitionRevisionTx(ctx, tx, dbID, prop.MigrationID); err != nil {
		return domain.MigrationProposal{}, err
	}

	updated, err := s.getProposalTx(ctx, tx, dbID, proposalID)
	if err != nil {
		return domain.MigrationProposal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MigrationProposal{}, fmt.Errorf("withdraw proposal commit: %w", err)
	}
	return updated, nil
}

// DeleteMigrationProposal deletes an unexecuted, non-active proposal ('draft' or 'withdrawn')
// along with its approver snapshot and approval decisions.
func (s *Store) DeleteMigrationProposal(ctx context.Context, dbID, proposalID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("delete proposal begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	prop, err := scanProposalRow(tx.QueryRow(ctx, `
		select p.id, p.db_id, p.migration_id, p.migration_revision,
		       p.action_type, p.target_execution_id, p.resolution, p.reason,
		       p.status, p.proposed_by, coalesce((select u.username from users u where u.id = p.proposed_by), ''),
		       p.created_at, p.updated_at
		from migration_proposals p
		where p.id = $1 and p.db_id = $2
		for update of p`, proposalID, dbID))
	if err != nil {
		return err
	}

	switch prop.Status {
	case domain.ProposalDraft, domain.ProposalWithdrawn:
		// deletable
	case domain.ProposalPendingReview:
		return fmt.Errorf("%w: withdraw a pending proposal before deleting it", ErrConflict)
	default:
		return fmt.Errorf("%w: rejected, approved, or executed proposals cannot be deleted", ErrConflict)
	}

	var hasHistory bool
	if err := tx.QueryRow(ctx, `
		select exists (
			select 1 from migration_jobs where proposal_id = $1
			union all
			select 1 from migration_executions where proposal_id = $1
		)`, proposalID).Scan(&hasHistory); err != nil {
		return fmt.Errorf("check proposal history: %w", err)
	}
	if hasHistory {
		return fmt.Errorf("%w: proposal has execution history and cannot be deleted", ErrConflict)
	}

	if _, err := tx.Exec(ctx, `delete from migration_approvals where proposal_id = $1`, proposalID); err != nil {
		return fmt.Errorf("delete proposal approvals: %w", err)
	}
	if _, err := tx.Exec(ctx, `delete from migration_proposal_approvers where proposal_id = $1`, proposalID); err != nil {
		return fmt.Errorf("delete proposal approvers: %w", err)
	}
	if _, err := tx.Exec(ctx, `delete from migration_proposals where id = $1 and db_id = $2`, proposalID, dbID); err != nil {
		return fmt.Errorf("delete proposal: %w", mapWriteError(err))
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("delete proposal commit: %w", err)
	}
	return nil
}

type requiredQuorumGroupRow struct {
	GroupID          uuid.UUID
	GroupName        string
	SnapshotLeaderID uuid.UUID
	CurrentLeaderID  pgtype.UUID
}

func (s *Store) loadRequiredQuorumIntersectionTx(ctx context.Context, tx pgx.Tx, proposalID, dbID uuid.UUID) ([]requiredQuorumGroupRow, error) {
	rows, err := tx.Query(ctx, `
		select mpa.group_id, g.name, mpa.leader_id, g.leader_id
		from migration_proposal_approvers mpa
		join db_access a on a.db_id = $2 and a.group_id = mpa.group_id
		join groups g on g.id = mpa.group_id and g.deleted_at is null
		where mpa.proposal_id = $1
		order by g.name`, proposalID, dbID)
	if err != nil {
		return nil, fmt.Errorf("query required quorum intersection: %w", err)
	}
	defer rows.Close()

	var out []requiredQuorumGroupRow
	for rows.Next() {
		var r requiredQuorumGroupRow
		if err := rows.Scan(&r.GroupID, &r.GroupName, &r.SnapshotLeaderID, &r.CurrentLeaderID); err != nil {
			return nil, fmt.Errorf("scan required quorum group: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func resolveActorLeaderGroups(actor domain.User, quorumGroups []requiredQuorumGroupRow) []uuid.UUID {
	if actor.IsPlatformAdmin || !actor.IsActive {
		return nil
	}
	var groupIDs []uuid.UUID
	for _, qg := range quorumGroups {
		effectiveLeader := qg.SnapshotLeaderID
		if qg.CurrentLeaderID.Valid {
			effectiveLeader = uuid.UUID(qg.CurrentLeaderID.Bytes)
		}
		if effectiveLeader == actor.ID || qg.SnapshotLeaderID == actor.ID {
			groupIDs = append(groupIDs, qg.GroupID)
		}
	}
	return groupIDs
}

// RejectProposal records a 'rejected' decision for the group(s) led by in.Actor and moves the proposal to 'rejected'.
func (s *Store) RejectProposal(ctx context.Context, in ApproveProposalInput) (domain.MigrationProposal, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.MigrationProposal{}, fmt.Errorf("reject proposal begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var dbName string
	if err := tx.QueryRow(ctx, `select name from db_connections where id = $1 and deleted_at is null for update`, in.DBID).
		Scan(&dbName); errors.Is(err, pgx.ErrNoRows) {
		return domain.MigrationProposal{}, ErrNotFound
	} else if err != nil {
		return domain.MigrationProposal{}, fmt.Errorf("lock db connection: %w", err)
	}

	prop, err := scanProposalRow(tx.QueryRow(ctx, `
		select p.id, p.db_id, p.migration_id, p.migration_revision,
		       p.action_type, p.target_execution_id, p.resolution, p.reason,
		       p.status, p.proposed_by, coalesce((select u.username from users u where u.id = p.proposed_by), ''),
		       p.created_at, p.updated_at
		from migration_proposals p
		where p.id = $1 and p.db_id = $2
		for update of p`, in.ProposalID, in.DBID))
	if err != nil {
		return domain.MigrationProposal{}, err
	}
	if prop.Status != domain.ProposalPendingReview {
		return domain.MigrationProposal{}, ErrProposalNotPendingReview
	}

	quorumGroups, err := s.loadRequiredQuorumIntersectionTx(ctx, tx, in.ProposalID, in.DBID)
	if err != nil {
		return domain.MigrationProposal{}, err
	}
	if len(quorumGroups) == 0 {
		return domain.MigrationProposal{}, ErrNoAccessGroups
	}

	leaderGroupIDs := resolveActorLeaderGroups(in.Actor, quorumGroups)
	if len(leaderGroupIDs) == 0 {
		return domain.MigrationProposal{}, ErrNotGroupLeader
	}

	comment := strings.TrimSpace(in.Comment)
	for _, gid := range leaderGroupIDs {
		if _, err := tx.Exec(ctx, `
			insert into migration_approvals (proposal_id, group_id, leader_id, decision, comment, decided_at)
			values ($1, $2, $3, 'rejected', $4, now())
			on conflict (proposal_id, group_id) do update
			set leader_id  = excluded.leader_id,
			    decision   = excluded.decision,
			    comment    = excluded.comment,
			    decided_at = excluded.decided_at`,
			in.ProposalID, gid, in.Actor.ID, comment); err != nil {
			return domain.MigrationProposal{}, fmt.Errorf("insert rejection: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		update migration_proposals
		set status = 'rejected', updated_at = now()
		where id = $1`, in.ProposalID); err != nil {
		return domain.MigrationProposal{}, fmt.Errorf("update proposal rejected: %w", err)
	}

	updated, err := s.getProposalTx(ctx, tx, in.DBID, in.ProposalID)
	if err != nil {
		return domain.MigrationProposal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MigrationProposal{}, fmt.Errorf("reject proposal commit: %w", err)
	}
	return updated, nil
}

// checkPredecessorApprovalGateTx enforces the Task 4 Predecessor Approval Gate (409 PREDECESSOR_NOT_READY):
//   - For 'apply' and 'forward': every predecessor slot effective_seq < def.EffectiveSeq on dbID must
//     already have an Active definition OR an in-queue/running job ('apply'/'forward' with status in ('queued', 'running')),
//     and the current def.EffectiveSeq slot itself must not already be Active or have an in-queue/running job.
//   - For 'rollback': def must currently be Active, and every currently Active migration with
//     effective_seq > def.EffectiveSeq must already have a 'rollback' job with status in ('queued', 'running').
func (s *Store) checkPredecessorApprovalGateTx(ctx context.Context, tx pgx.Tx, dbID uuid.UUID, def domain.MigrationDefinition, prop domain.MigrationProposal) (*uuid.UUID, error) {
	// Lock all non-superseded definitions on db_id
	rows, err := tx.Query(ctx, `
		select id, effective_seq
		from migration_definitions
		where db_id = $1 and is_superseded = false
		order by effective_seq asc, seq_version asc
		for update`, dbID)
	if err != nil {
		return nil, fmt.Errorf("lock definitions for predecessor gate: %w", err)
	}
	type defEntry struct {
		ID           uuid.UUID
		EffectiveSeq int64
	}
	var defs []defEntry
	for rows.Next() {
		var de defEntry
		if err := rows.Scan(&de.ID, &de.EffectiveSeq); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan definition for predecessor gate: %w", err)
		}
		defs = append(defs, de)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate definitions for predecessor gate: %w", err)
	}

	// Jobs first, so a job finishing between the two reads is still seen.
	jrows, err := tx.Query(ctx, `
		select j.migration_id, d.effective_seq, j.action_type
		from migration_jobs j
		join migration_definitions d on d.id = j.migration_id
		where j.db_id = $1 and j.status in ('queued', 'running')`, dbID)
	if err != nil {
		return nil, fmt.Errorf("query active jobs for predecessor gate: %w", err)
	}
	inFlightApplyForwardBySeq := make(map[int64]bool)
	inFlightRollbackBySeq := make(map[int64]bool)
	for jrows.Next() {
		var migID uuid.UUID
		var effSeq int64
		var act string
		if err := jrows.Scan(&migID, &effSeq, &act); err != nil {
			jrows.Close()
			return nil, fmt.Errorf("scan active job for predecessor gate: %w", err)
		}
		switch act {
		case domain.ActionApply, domain.ActionForward, domain.ActionBatchRetry:
			inFlightApplyForwardBySeq[effSeq] = true
		case domain.ActionRollback, domain.ActionBatchRevert:
			inFlightRollbackBySeq[effSeq] = true
		}
	}
	jrows.Close()
	if err := jrows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active jobs for predecessor gate: %w", err)
	}

	activeMap, err := s.loadActiveMigrationMapTx(ctx, tx, dbID)
	if err != nil {
		return nil, err
	}

	activeBySeq := make(map[int64]uuid.UUID)
	distinctSeqs := make([]int64, 0)
	seenSeq := make(map[int64]bool)
	for _, de := range defs {
		if !seenSeq[de.EffectiveSeq] {
			seenSeq[de.EffectiveSeq] = true
			distinctSeqs = append(distinctSeqs, de.EffectiveSeq)
		}
		if execID, ok := activeMap[de.ID]; ok {
			activeBySeq[de.EffectiveSeq] = execID
		}
	}

	switch prop.ActionType {
	case domain.ActionApply, domain.ActionForward:
		// Current slot must not already be active or have an in-flight apply/forward/rollback job
		if _, alreadyActive := activeBySeq[def.EffectiveSeq]; alreadyActive {
			return nil, ErrPredecessorNotReady
		}
		if inFlightApplyForwardBySeq[def.EffectiveSeq] || inFlightRollbackBySeq[def.EffectiveSeq] {
			return nil, ErrPredecessorNotReady
		}
		// Every predecessor effective_seq < def.EffectiveSeq must be Active OR have a queued/running apply/forward job,
		// and must not have a queued/running rollback job.
		for _, seq := range distinctSeqs {
			if seq >= def.EffectiveSeq {
				continue
			}
			_, isActive := activeBySeq[seq]
			if (!isActive && !inFlightApplyForwardBySeq[seq]) || inFlightRollbackBySeq[seq] {
				return nil, ErrPredecessorNotReady
			}
		}
		return nil, nil

	case domain.ActionRollback:
		activeExecID, isDefActive := activeMap[def.ID]
		if !isDefActive || inFlightRollbackBySeq[def.EffectiveSeq] {
			return nil, ErrPredecessorNotReady
		}
		if prop.TargetExecutionID != nil && *prop.TargetExecutionID != activeExecID {
			return nil, ErrPredecessorNotReady
		}
		// Every currently Active migration with effective_seq > def.EffectiveSeq must already have a queued/running rollback job,
		// and no later slot may have a queued/running apply/forward job.
		for seq := range activeBySeq {
			if seq > def.EffectiveSeq && !inFlightRollbackBySeq[seq] {
				return nil, ErrPredecessorNotReady
			}
		}
		for seq := range inFlightApplyForwardBySeq {
			if seq > def.EffectiveSeq {
				return nil, ErrPredecessorNotReady
			}
		}
		return &activeExecID, nil

	default:
		return prop.TargetExecutionID, nil
	}
}

// resolveExecutionLineageTx computes the exact (root_execution_id, rollback_of_execution_id) pair
// according to the Task 5 Item 4a table.
func (s *Store) resolveExecutionLineageTx(
	ctx context.Context,
	tx pgx.Tx,
	newExecutionID uuid.UUID,
	def domain.MigrationDefinition,
	prop domain.MigrationProposal,
	resolvedTargetExecID *uuid.UUID,
) (rootExecutionID uuid.UUID, rollbackOfExecutionID *uuid.UUID, err error) {
	switch prop.ActionType {
	case domain.ActionApply, domain.ActionForward:
		return newExecutionID, nil, nil

	case domain.ActionRollback:
		targetID := resolvedTargetExecID
		if targetID == nil {
			targetID = prop.TargetExecutionID
		}
		if targetID == nil {
			return uuid.Nil, nil, fmt.Errorf("%w: rollback requires target_execution_id", ErrInvalidProposalAction)
		}
		var targetRootID uuid.UUID
		if err := tx.QueryRow(ctx, `
			select root_execution_id
			from migration_executions
			where execution_id = $1 and db_id = $2`, *targetID, prop.DBID).Scan(&targetRootID); errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil, ErrNotFound
		} else if err != nil {
			return uuid.Nil, nil, fmt.Errorf("load rollback target execution: %w", err)
		}

		if def.ExecutionMode == domain.ExecutionModeBatch {
			// Batch-mode rollback: root_execution_id = target.root_execution_id;
			// rollback_of_execution_id = highest seq_order on target.root_execution_id with action_type in ('apply', 'forward', 'batch_retry')
			var highestForwardID uuid.UUID
			if err := tx.QueryRow(ctx, `
				select execution_id
				from migration_executions
				where root_execution_id = $1
				  and action_type in ('apply', 'forward', 'batch_retry')
				order by seq_order desc
				limit 1`, targetRootID).Scan(&highestForwardID); err != nil {
				return uuid.Nil, nil, fmt.Errorf("find highest forward execution for batch rollback: %w", err)
			}
			return targetRootID, &highestForwardID, nil
		}

		// Normal-mode rollback: root_execution_id = new execution_id itself; rollback_of_execution_id = target_execution_id
		tCopy := *targetID
		return newExecutionID, &tCopy, nil

	case domain.ActionBatchRetry:
		targetID := prop.TargetExecutionID
		var targetRootID uuid.UUID
		if targetID != nil {
			if err := tx.QueryRow(ctx, `
				select root_execution_id
				from migration_executions
				where execution_id = $1 and db_id = $2`, *targetID, prop.DBID).Scan(&targetRootID); err != nil {
				return uuid.Nil, nil, fmt.Errorf("load batch_retry target execution: %w", err)
			}
		} else {
			if err := tx.QueryRow(ctx, `
				select root_execution_id
				from migration_executions
				where migration_id = $1 and db_id = $2
				order by seq_order desc
				limit 1`, def.ID, prop.DBID).Scan(&targetRootID); err != nil {
				return uuid.Nil, nil, fmt.Errorf("load latest root_execution_id for batch_retry: %w", err)
			}
		}
		return targetRootID, nil, nil

	case domain.ActionBatchRevert:
		targetID := prop.TargetExecutionID
		var targetRootID uuid.UUID
		if targetID != nil {
			if err := tx.QueryRow(ctx, `
				select root_execution_id
				from migration_executions
				where execution_id = $1 and db_id = $2`, *targetID, prop.DBID).Scan(&targetRootID); err != nil {
				return uuid.Nil, nil, fmt.Errorf("load batch_revert target execution: %w", err)
			}
		} else {
			if err := tx.QueryRow(ctx, `
				select root_execution_id
				from migration_executions
				where migration_id = $1 and db_id = $2
				order by seq_order desc
				limit 1`, def.ID, prop.DBID).Scan(&targetRootID); err != nil {
				return uuid.Nil, nil, fmt.Errorf("load latest root_execution_id for batch_revert: %w", err)
			}
		}
		var highestForwardID uuid.UUID
		if err := tx.QueryRow(ctx, `
			select execution_id
			from migration_executions
			where root_execution_id = $1
			  and action_type in ('apply', 'forward', 'batch_retry')
			order by seq_order desc
			limit 1`, targetRootID).Scan(&highestForwardID); err != nil {
			return uuid.Nil, nil, fmt.Errorf("find highest forward execution for batch_revert: %w", err)
		}
		return targetRootID, &highestForwardID, nil

	case domain.ActionResolveUncertain:
		if prop.TargetExecutionID == nil {
			return uuid.Nil, nil, fmt.Errorf("%w: resolve_uncertain requires target_execution_id", ErrInvalidProposalAction)
		}
		var targetRootID uuid.UUID
		if err := tx.QueryRow(ctx, `
			select root_execution_id
			from migration_executions
			where execution_id = $1 and db_id = $2`, *prop.TargetExecutionID, prop.DBID).Scan(&targetRootID); err != nil {
			return uuid.Nil, nil, fmt.Errorf("load resolve_uncertain target execution: %w", err)
		}
		tCopy := *prop.TargetExecutionID
		return targetRootID, &tCopy, nil

	default:
		return uuid.Nil, nil, fmt.Errorf("%w: unsupported action_type %q", ErrInvalidProposalAction, prop.ActionType)
	}
}

// ApproveProposal records an 'approved' decision for the group(s) led by in.Actor, evaluates
// the quorum intersection (Snapshot ∩ CurrentActiveDBGroups), enforces the Predecessor Approval Gate
// (409 PREDECESSOR_NOT_READY), and atomically enqueues the migration execution & job when the final
// required Group Leader approves.
func (s *Store) ApproveProposal(ctx context.Context, in ApproveProposalInput) (ApproveProposalResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApproveProposalResult{}, fmt.Errorf("approve proposal begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Ensure DB exists and is not soft-deleted
	var dbName string
	if err := tx.QueryRow(ctx, `select name from db_connections where id = $1 and deleted_at is null for update`, in.DBID).
		Scan(&dbName); errors.Is(err, pgx.ErrNoRows) {
		return ApproveProposalResult{}, ErrNotFound
	} else if err != nil {
		return ApproveProposalResult{}, fmt.Errorf("lock db connection: %w", err)
	}

	// 2. Lock proposal row with SELECT ... FOR UPDATE
	prop, err := scanProposalRow(tx.QueryRow(ctx, `
		select p.id, p.db_id, p.migration_id, p.migration_revision,
		       p.action_type, p.target_execution_id, p.resolution, p.reason,
		       p.status, p.proposed_by, coalesce((select u.username from users u where u.id = p.proposed_by), ''),
		       p.created_at, p.updated_at
		from migration_proposals p
		where p.id = $1 and p.db_id = $2
		for update of p`, in.ProposalID, in.DBID))
	if err != nil {
		return ApproveProposalResult{}, err
	}
	if prop.Status != domain.ProposalPendingReview {
		return ApproveProposalResult{}, ErrProposalNotPendingReview
	}

	// 3. Compute quorum intersection (Snapshot ∩ CurrentActiveDBGroups)
	quorumGroups, err := s.loadRequiredQuorumIntersectionTx(ctx, tx, in.ProposalID, in.DBID)
	if err != nil {
		return ApproveProposalResult{}, err
	}
	if len(quorumGroups) == 0 {
		return ApproveProposalResult{}, ErrNoAccessGroups
	}

	leaderGroupIDs := resolveActorLeaderGroups(in.Actor, quorumGroups)
	if len(leaderGroupIDs) == 0 {
		return ApproveProposalResult{}, ErrNotGroupLeader
	}

	comment := strings.TrimSpace(in.Comment)
	for _, gid := range leaderGroupIDs {
		if _, err := tx.Exec(ctx, `
			insert into migration_approvals (proposal_id, group_id, leader_id, decision, comment, decided_at)
			values ($1, $2, $3, 'approved', $4, now())
			on conflict (proposal_id, group_id) do update
			set leader_id  = excluded.leader_id,
			    decision   = excluded.decision,
			    comment    = excluded.comment,
			    decided_at = excluded.decided_at`,
			in.ProposalID, gid, in.Actor.ID, comment); err != nil {
			return ApproveProposalResult{}, fmt.Errorf("insert approval: %w", err)
		}
	}

	// 4. Count required vs approved in the quorum intersection
	var requiredCount, approvedCount int
	err = tx.QueryRow(ctx, `
		select count(*) as required_count,
		       count(ma.group_id) filter (where ma.decision = 'approved') as approved_count
		from migration_proposal_approvers mpa
		join db_access a on a.db_id = $2 and a.group_id = mpa.group_id
		join groups g on g.id = mpa.group_id and g.deleted_at is null
		left join migration_approvals ma on ma.proposal_id = mpa.proposal_id and ma.group_id = mpa.group_id
		where mpa.proposal_id = $1`, in.ProposalID, in.DBID).
		Scan(&requiredCount, &approvedCount)
	if err != nil {
		return ApproveProposalResult{}, fmt.Errorf("calculate quorum intersection: %w", err)
	}
	if requiredCount == 0 {
		return ApproveProposalResult{}, ErrNoAccessGroups
	}

	// If quorum is not yet complete, commit partial approval and remain in pending_review
	if approvedCount < requiredCount {
		updatedProp, err := s.getProposalTx(ctx, tx, in.DBID, in.ProposalID)
		if err != nil {
			return ApproveProposalResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ApproveProposalResult{}, fmt.Errorf("approve proposal commit: %w", err)
		}
		return ApproveProposalResult{
			Proposal:      updatedProp,
			RequiredCount: requiredCount,
			ApprovedCount: approvedCount,
			Enqueued:      false,
		}, nil
	}

	// 5. Final Leader Approves (approvedCount == requiredCount) -> Predecessor Approval Gate
	def, err := s.LockMigrationDefinitionTx(ctx, tx, in.DBID, prop.MigrationID)
	if err != nil {
		return ApproveProposalResult{}, err
	}

	resolvedTargetExecID, err := s.checkPredecessorApprovalGateTx(ctx, tx, in.DBID, def, prop)
	if err != nil {
		return ApproveProposalResult{}, err
	}

	// 6. Automatic Atomic Enqueue in the same transaction
	if _, err := tx.Exec(ctx, `
		update migration_proposals
		set status = 'queued', updated_at = now()
		where id = $1`, prop.ID); err != nil {
		return ApproveProposalResult{}, fmt.Errorf("update proposal status queued: %w", err)
	}

	execID := uuid.New()
	rootExecID, rollbackOfExecID, err := s.resolveExecutionLineageTx(ctx, tx, execID, def, prop, resolvedTargetExecID)
	if err != nil {
		return ApproveProposalResult{}, err
	}

	var executedSQL string
	switch prop.ActionType {
	case domain.ActionRollback, domain.ActionBatchRevert:
		executedSQL = def.SQLDown
	case domain.ActionResolveUncertain:
		resVal := ""
		if prop.Resolution != nil {
			resVal = *prop.Resolution
		}
		executedSQL = fmt.Sprintf("-- resolve_uncertain: resolution=%s reason=%s", resVal, prop.Reason)
	default:
		executedSQL = def.SQLUp
	}

	executedByID := in.Actor.ID
	executedByUsername := in.Actor.Username
	if prop.ProposedBy != nil && prop.ProposedByUsername != "" {
		executedByID = *prop.ProposedBy
		executedByUsername = prop.ProposedByUsername
	}

	var rollbackOfArg any
	if rollbackOfExecID != nil {
		rollbackOfArg = *rollbackOfExecID
	}

	var seqOrder int64
	var createdAt time.Time
	err = tx.QueryRow(ctx, `
		insert into migration_executions (
			execution_id, root_execution_id, db_id, migration_id, proposal_id,
			migration_revision, action_type, executed_sql, executed_by,
			executed_by_username, db_name_snapshot, migration_title_snapshot,
			status, rollback_of_execution_id
		) values (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12,
			'queued', $13
		)
		returning seq_order, created_at`,
		execID, rootExecID, in.DBID, def.ID, prop.ID,
		prop.MigrationRevision, prop.ActionType, executedSQL, executedByID,
		executedByUsername, dbName, def.Title,
		rollbackOfArg,
	).Scan(&seqOrder, &createdAt)
	if err != nil {
		return ApproveProposalResult{}, fmt.Errorf("insert migration_executions: %w", err)
	}

	pubCtx, pubSpan := telemetry.StartSpan(ctx, "queue.publish",
		attribute.String("db_id", in.DBID.String()),
		attribute.String("db_name", dbName),
		attribute.String("proposal_id", prop.ID.String()),
		attribute.String("migration_id", def.ID.String()),
		attribute.String("execution_id", execID.String()),
		attribute.String("action", prop.ActionType),
		attribute.String("mode", def.ExecutionMode),
	)
	traceParent, traceState := telemetry.InjectTraceContext(pubCtx)
	if traceParent == "" {
		traceParent = in.TraceParent
		traceState = in.TraceState
	}

	job, err := s.EnqueueMigrationJobTx(pubCtx, tx, EnqueueInput{
		DBID:            in.DBID,
		ProposalID:      prop.ID,
		MigrationID:     def.ID,
		ExecutionID:     execID,
		RootExecutionID: rootExecID,
		ActionType:      prop.ActionType,
		ExecutionMode:   def.ExecutionMode,
		TraceParent:     traceParent,
		TraceState:      traceState,
		CorrelationID:   in.CorrelationID,
	})
	if err != nil {
		pubSpan.RecordError(err)
		pubSpan.SetStatus(codes.Error, err.Error())
		pubSpan.End()
		return ApproveProposalResult{}, err
	}
	pubSpan.SetAttributes(attribute.String("job_id", job.ID.String()))
	pubSpan.End()

	updatedProp, err := s.getProposalTx(ctx, tx, in.DBID, prop.ID)
	if err != nil {
		return ApproveProposalResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ApproveProposalResult{}, fmt.Errorf("approve proposal enqueue commit: %w", err)
	}

	proposalIDCopy := prop.ID
	executedByCopy := executedByID
	execRecord := &domain.MigrationExecution{
		ExecutionID:            execID,
		RootExecutionID:        rootExecID,
		DBID:                   in.DBID,
		MigrationID:            def.ID,
		ProposalID:             &proposalIDCopy,
		SeqOrder:               seqOrder,
		SeqVersion:             def.SeqVersion,
		EffectiveSeq:           def.EffectiveSeq,
		MigrationRevision:      prop.MigrationRevision,
		ActionType:             prop.ActionType,
		ExecutionMode:          def.ExecutionMode,
		ExecutedSQL:            executedSQL,
		ExecutedBy:             &executedByCopy,
		ExecutedByUsername:     executedByUsername,
		DBNameSnapshot:         dbName,
		MigrationTitleSnapshot: def.Title,
		Status:                 domain.StatusQueued,
		RollbackOfExecutionID:  rollbackOfExecID,
		TotalBatches:           job.TotalBatches,
		CreatedAt:              createdAt,
	}

	return ApproveProposalResult{
		Proposal:      updatedProp,
		RequiredCount: requiredCount,
		ApprovedCount: approvedCount,
		Enqueued:      true,
		Execution:     execRecord,
		Job:           &job,
	}, nil
}
