package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"leafygo/internal/domain"
)

type migrationDefRow struct {
	ID                      uuid.UUID
	DBID                    uuid.UUID
	SeqVersion              int64
	EffectiveSeq            int64
	Revision                int
	Title                   string
	Description             string
	SQLUp                   string
	SQLDown                 string
	IsIrreversible          bool
	IrreversibleReason      string
	ExecutionMode           string
	BatchConfigRaw          []byte
	IsImmutable             bool
	ReplacesMigrationID     pgtype.UUID
	IsSuperseded            bool
	SupersededByMigrationID pgtype.UUID
	SupersededAt            pgtype.Timestamptz
	CreatedBy               pgtype.UUID
	UpdatedBy               pgtype.UUID
	CreatedByUsername       string
	UpdatedByUsername       string
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

func (r migrationDefRow) toDomain() (domain.MigrationDefinition, error) {
	d := domain.MigrationDefinition{
		ID:                 r.ID,
		DBID:               r.DBID,
		SeqVersion:         r.SeqVersion,
		EffectiveSeq:       r.EffectiveSeq,
		Revision:           r.Revision,
		Title:              r.Title,
		Description:        r.Description,
		SQLUp:              r.SQLUp,
		SQLDown:            r.SQLDown,
		IsIrreversible:     r.IsIrreversible,
		IrreversibleReason: r.IrreversibleReason,
		ExecutionMode:      r.ExecutionMode,
		IsImmutable:        r.IsImmutable,
		IsSuperseded:       r.IsSuperseded,
		CreatedByUsername:  r.CreatedByUsername,
		UpdatedByUsername:  r.UpdatedByUsername,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
	}
	if len(r.BatchConfigRaw) > 0 && string(r.BatchConfigRaw) != "null" {
		var bc domain.BatchConfig
		if err := json.Unmarshal(r.BatchConfigRaw, &bc); err != nil {
			return domain.MigrationDefinition{}, fmt.Errorf("unmarshal batch_config: %w", err)
		}
		d.BatchConfig = &bc
	}
	if r.ReplacesMigrationID.Valid {
		id := uuid.UUID(r.ReplacesMigrationID.Bytes)
		d.ReplacesMigrationID = &id
	}
	if r.SupersededByMigrationID.Valid {
		id := uuid.UUID(r.SupersededByMigrationID.Bytes)
		d.SupersededByMigrationID = &id
	}
	if r.SupersededAt.Valid {
		t := r.SupersededAt.Time
		d.SupersededAt = &t
	}
	if r.CreatedBy.Valid {
		id := uuid.UUID(r.CreatedBy.Bytes)
		d.CreatedBy = &id
	}
	if r.UpdatedBy.Valid {
		id := uuid.UUID(r.UpdatedBy.Bytes)
		d.UpdatedBy = &id
	}
	return d, nil
}

func validateMigrationDefinitionFields(def *domain.MigrationDefinition) error {
	def.Title = strings.TrimSpace(def.Title)
	if def.Title == "" {
		return fmt.Errorf("%w: title is required", ErrInvalidMigrationDef)
	}
	if strings.TrimSpace(def.SQLUp) == "" {
		return fmt.Errorf("%w: sql_up is required", ErrInvalidMigrationDef)
	}
	if def.ExecutionMode == "" {
		def.ExecutionMode = domain.ExecutionModeNormal
	}
	if def.ExecutionMode != domain.ExecutionModeNormal && def.ExecutionMode != domain.ExecutionModeBatch {
		return fmt.Errorf("%w: invalid execution_mode %q", ErrInvalidMigrationDef, def.ExecutionMode)
	}
	if def.IsIrreversible {
		if strings.TrimSpace(def.IrreversibleReason) == "" {
			return fmt.Errorf("%w: irreversible_reason is required when is_irreversible is true", ErrInvalidMigrationDef)
		}
	} else {
		if strings.TrimSpace(def.SQLDown) == "" {
			return fmt.Errorf("%w: sql_down is required when is_irreversible is false", ErrInvalidMigrationDef)
		}
	}
	if def.ReplacesMigrationID != nil && def.ID != uuid.Nil && *def.ReplacesMigrationID == def.ID {
		return ErrMigrationNotSupersedable
	}
	return nil
}

func marshalBatchConfig(bc *domain.BatchConfig) ([]byte, error) {
	if bc == nil {
		return nil, nil
	}
	raw, err := json.Marshal(bc)
	if err != nil {
		return nil, fmt.Errorf("marshal batch_config: %w", err)
	}
	return raw, nil
}

const selectMigrationDefCols = `
	d.id, d.db_id, d.seq_version, d.effective_seq, d.revision,
	d.title, d.description, d.sql_up, d.sql_down,
	d.is_irreversible, d.irreversible_reason, d.execution_mode, d.batch_config,
	d.is_immutable, d.replaces_migration_id, d.is_superseded,
	d.superseded_by_migration_id, d.superseded_at,
	d.created_by, d.updated_by,
	coalesce(cu.username, ''), coalesce(uu.username, ''),
	d.created_at, d.updated_at`

func scanMigrationDefRow(row pgx.Row) (domain.MigrationDefinition, error) {
	var r migrationDefRow
	err := row.Scan(
		&r.ID, &r.DBID, &r.SeqVersion, &r.EffectiveSeq, &r.Revision,
		&r.Title, &r.Description, &r.SQLUp, &r.SQLDown,
		&r.IsIrreversible, &r.IrreversibleReason, &r.ExecutionMode, &r.BatchConfigRaw,
		&r.IsImmutable, &r.ReplacesMigrationID, &r.IsSuperseded,
		&r.SupersededByMigrationID, &r.SupersededAt,
		&r.CreatedBy, &r.UpdatedBy,
		&r.CreatedByUsername, &r.UpdatedByUsername,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MigrationDefinition{}, ErrNotFound
	}
	if err != nil {
		return domain.MigrationDefinition{}, fmt.Errorf("scan migration_definition: %w", err)
	}
	return r.toDomain()
}

// LockMigrationDefinitionTx locks a migration_definitions row with SELECT ... FOR UPDATE.
func (s *Store) LockMigrationDefinitionTx(ctx context.Context, tx pgx.Tx, dbID, migrationID uuid.UUID) (domain.MigrationDefinition, error) {
	return scanMigrationDefRow(tx.QueryRow(ctx, `
		select `+selectMigrationDefCols+`
		from migration_definitions d
		left join users cu on cu.id = d.created_by
		left join users uu on uu.id = d.updated_by
		where d.id = $1 and d.db_id = $2
		for update of d`, migrationID, dbID))
}

// HasActiveProposalTx checks whether a migration_id currently has an active proposal
// ('pending_review', 'approved', 'queued', 'running').
func (s *Store) HasActiveProposalTx(ctx context.Context, tx pgx.Tx, migrationID uuid.UUID) (bool, error) {
	var active bool
	err := tx.QueryRow(ctx, `
		select exists (
			select 1 from migration_proposals
			where migration_id = $1
			  and status in ('pending_review', 'approved', 'queued', 'running')
		)`, migrationID).Scan(&active)
	if err != nil {
		return false, fmt.Errorf("check active proposal: %w", err)
	}
	return active, nil
}

// ResolveAndValidateSupersedeTx walks the replaces_migration_id chain up to its root B_0,
// returns B_0.effective_seq, and enforces the Relaxed Supersede rules:
//  1. Target replacesID must exist on dbID and have at least one failed attempt (or be immutable).
//  2. Root B_0.effective_seq must be the lowest effective_seq on dbID that is not currently Active.
//  3. No member sharing (dbID, B_0.effective_seq) may currently be Active, have an in-flight/hold
//     execution ('queued', 'running', 'partial', 'partially_reverted', 'uncertain'), or have an active proposal.
//  4. Historic 'success' attempts that have already been rolled back ('rollback'/'batch_revert' -> 'success')
//     do NOT block supersede.
func (s *Store) ResolveAndValidateSupersedeTx(ctx context.Context, tx pgx.Tx, dbID, replacesID, excludeMigrationID uuid.UUID) (int64, error) {
	if excludeMigrationID != uuid.Nil && replacesID == excludeMigrationID {
		return 0, ErrMigrationNotSupersedable
	}

	// 1. Walk replaces_migration_id chain to root B_0
	visited := make(map[uuid.UUID]bool)
	if excludeMigrationID != uuid.Nil {
		visited[excludeMigrationID] = true
	}
	currID := replacesID
	var rootEffectiveSeq int64
	var directTargetImmutable bool

	for depth := 0; ; depth++ {
		if visited[currID] {
			return 0, ErrMigrationNotSupersedable
		}
		visited[currID] = true

		var effSeq int64
		var isImmutable bool
		var parentID pgtype.UUID
		err := tx.QueryRow(ctx, `
			select effective_seq, is_immutable, replaces_migration_id
			from migration_definitions
			where id = $1 and db_id = $2
			for update`, currID, dbID).Scan(&effSeq, &isImmutable, &parentID)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		if err != nil {
			return 0, fmt.Errorf("walk supersede chain: %w", err)
		}
		if depth == 0 {
			directTargetImmutable = isImmutable
		}
		rootEffectiveSeq = effSeq
		if !parentID.Valid {
			break
		}
		currID = uuid.UUID(parentID.Bytes)
	}

	// Verify direct target has failed at least once or is already marked immutable
	var directHasFailedExec bool
	if err := tx.QueryRow(ctx, `
		select exists (
			select 1 from migration_executions
			where migration_id = $1 and db_id = $2 and status = 'failed'
		)`, replacesID, dbID).Scan(&directHasFailedExec); err != nil {
		return 0, fmt.Errorf("check target failed execution: %w", err)
	}
	if !directTargetImmutable && !directHasFailedExec {
		return 0, ErrMigrationNotSupersedable
	}

	// 2. Lock all definitions on db_id to evaluate Active status per effective_seq slot
	rows, err := tx.Query(ctx, `
		select id, effective_seq, is_superseded
		from migration_definitions
		where db_id = $1
		order by effective_seq asc, seq_version asc
		for update`, dbID)
	if err != nil {
		return 0, fmt.Errorf("lock db definitions for supersede check: %w", err)
	}
	type defSlot struct {
		ID           uuid.UUID
		EffectiveSeq int64
		IsSuperseded bool
	}
	var allDefs []defSlot
	for rows.Next() {
		var ds defSlot
		if err := rows.Scan(&ds.ID, &ds.EffectiveSeq, &ds.IsSuperseded); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan db definition slot: %w", err)
		}
		allDefs = append(allDefs, ds)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate db definitions: %w", err)
	}

	// Query active status for all migrations on db_id:
	// A migration_id is Active iff its latest successful state-changing execution
	// has action_type in ('apply', 'forward', 'batch_retry').
	activeMigrations, err := s.loadActiveMigrationMapTx(ctx, tx, dbID)
	if err != nil {
		return 0, err
	}

	// Check which effective_seq slots are currently Active
	slotHasActive := make(map[int64]bool)
	distinctSlots := make([]int64, 0)
	seenSlot := make(map[int64]bool)
	for _, ds := range allDefs {
		if excludeMigrationID != uuid.Nil && ds.ID == excludeMigrationID {
			continue
		}
		if !seenSlot[ds.EffectiveSeq] {
			seenSlot[ds.EffectiveSeq] = true
			distinctSlots = append(distinctSlots, ds.EffectiveSeq)
		}
		if !ds.IsSuperseded {
			if _, ok := activeMigrations[ds.ID]; ok {
				slotHasActive[ds.EffectiveSeq] = true
			}
		}
	}

	// Rule 1: rootEffectiveSeq must be the lowest effective_seq on db_id that is currently NOT Active
	var lowestInactiveSeq int64 = -1
	for _, seq := range distinctSlots {
		if !slotHasActive[seq] {
			lowestInactiveSeq = seq
			break
		}
	}
	if lowestInactiveSeq == -1 || lowestInactiveSeq != rootEffectiveSeq {
		return 0, ErrMigrationNotSupersedable
	}

	// Rule 2: No chain member on (db_id, rootEffectiveSeq) may be Active or have an in-flight/hold execution or active proposal
	for _, ds := range allDefs {
		if ds.EffectiveSeq != rootEffectiveSeq {
			continue
		}
		if excludeMigrationID != uuid.Nil && ds.ID == excludeMigrationID {
			continue
		}
		if _, isActive := activeMigrations[ds.ID]; isActive {
			return 0, ErrMigrationNotSupersedable
		}

		var hasBlockingExecOrProposal bool
		if err := tx.QueryRow(ctx, `
			select exists (
				select 1 from migration_executions
				where migration_id = $1
				  and status in ('queued', 'running', 'partial', 'partially_reverted', 'uncertain')
				union all
				select 1 from migration_proposals
				where migration_id = $1
				  and status in ('pending_review', 'approved', 'queued', 'running')
			)`, ds.ID).Scan(&hasBlockingExecOrProposal); err != nil {
			return 0, fmt.Errorf("check blocking exec/proposal on chain member: %w", err)
		}
		if hasBlockingExecOrProposal {
			return 0, ErrMigrationNotSupersedable
		}
	}

	return rootEffectiveSeq, nil
}

// loadActiveMigrationMapTx returns map[migration_id]active_execution_id for all definitions on dbID
// whose latest state-changing 'success' execution is ('apply', 'forward', 'batch_retry').
func (s *Store) loadActiveMigrationMapTx(ctx context.Context, q interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, dbID uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	rows, err := q.Query(ctx, `
		select distinct on (e.migration_id) e.migration_id, e.execution_id, e.action_type
		from migration_executions e
		join migration_definitions d on d.id = e.migration_id
		where e.db_id = $1
		  and d.is_superseded = false
		  and e.status = 'success'
		  and e.action_type in ('apply', 'forward', 'batch_retry', 'rollback', 'batch_revert')
		order by e.migration_id, e.seq_order desc`, dbID)
	if err != nil {
		return nil, fmt.Errorf("query active migrations: %w", err)
	}
	defer rows.Close()

	active := make(map[uuid.UUID]uuid.UUID)
	for rows.Next() {
		var migID, execID uuid.UUID
		var actionType string
		if err := rows.Scan(&migID, &execID, &actionType); err != nil {
			return nil, fmt.Errorf("scan active migration: %w", err)
		}
		if actionType == domain.ActionApply || actionType == domain.ActionForward || actionType == domain.ActionBatchRetry {
			active[migID] = execID
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active migrations: %w", err)
	}
	return active, nil
}

func (s *Store) CreateMigrationDefinition(ctx context.Context, def domain.MigrationDefinition) (domain.MigrationDefinition, error) {
	if err := validateMigrationDefinitionFields(&def); err != nil {
		return domain.MigrationDefinition{}, err
	}
	batchRaw, err := marshalBatchConfig(def.BatchConfig)
	if err != nil {
		return domain.MigrationDefinition{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.MigrationDefinition{}, fmt.Errorf("create migration_definition begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock parent DB connection to serialize seq_version allocation per DB
	var lockedDBID uuid.UUID
	if err := tx.QueryRow(ctx, `select id from db_connections where id = $1 and deleted_at is null for update`, def.DBID).
		Scan(&lockedDBID); errors.Is(err, pgx.ErrNoRows) {
		return domain.MigrationDefinition{}, ErrNotFound
	} else if err != nil {
		return domain.MigrationDefinition{}, fmt.Errorf("lock db connection: %w", err)
	}

	var seqVersion int64
	if err := tx.QueryRow(ctx, `
		select coalesce(max(seq_version), 0) + 1
		from migration_definitions
		where db_id = $1`, def.DBID).Scan(&seqVersion); err != nil {
		return domain.MigrationDefinition{}, fmt.Errorf("allocate seq_version: %w", err)
	}

	effectiveSeq := seqVersion
	var replacesArg any
	if def.ReplacesMigrationID != nil {
		resolvedSeq, err := s.ResolveAndValidateSupersedeTx(ctx, tx, def.DBID, *def.ReplacesMigrationID, uuid.Nil)
		if err != nil {
			return domain.MigrationDefinition{}, err
		}
		effectiveSeq = resolvedSeq
		replacesArg = *def.ReplacesMigrationID
	}

	if def.ID == uuid.Nil {
		def.ID = uuid.New()
	}
	var createdByArg, updatedByArg any
	if def.CreatedBy != nil {
		createdByArg = *def.CreatedBy
	}
	if def.UpdatedBy != nil {
		updatedByArg = *def.UpdatedBy
	} else if def.CreatedBy != nil {
		updatedByArg = *def.CreatedBy
	}

	var batchArg any
	if batchRaw != nil {
		batchArg = batchRaw
	}

	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `
		insert into migration_definitions (
			id, db_id, seq_version, effective_seq, revision,
			title, description, sql_up, sql_down,
			is_irreversible, irreversible_reason, execution_mode, batch_config,
			is_immutable, replaces_migration_id, is_superseded,
			created_by, updated_by, created_at, updated_at
		) values (
			$1, $2, $3, $4, 1,
			$5, $6, $7, $8,
			$9, $10, $11, $12,
			false, $13, false,
			$14, $15, $16, $16
		)`,
		def.ID, def.DBID, seqVersion, effectiveSeq,
		def.Title, def.Description, def.SQLUp, def.SQLDown,
		def.IsIrreversible, def.IrreversibleReason, def.ExecutionMode, batchArg,
		replacesArg, createdByArg, updatedByArg, now,
	)
	if err != nil {
		return domain.MigrationDefinition{}, fmt.Errorf("insert migration_definition: %w", mapWriteError(err))
	}

	created, err := s.LockMigrationDefinitionTx(ctx, tx, def.DBID, def.ID)
	if err != nil {
		return domain.MigrationDefinition{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MigrationDefinition{}, fmt.Errorf("create migration_definition commit: %w", err)
	}
	return created, nil
}

func (s *Store) UpdateMigrationDefinition(ctx context.Context, def domain.MigrationDefinition) (domain.MigrationDefinition, error) {
	if err := validateMigrationDefinitionFields(&def); err != nil {
		return domain.MigrationDefinition{}, err
	}
	batchRaw, err := marshalBatchConfig(def.BatchConfig)
	if err != nil {
		return domain.MigrationDefinition{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.MigrationDefinition{}, fmt.Errorf("update migration_definition begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := s.LockMigrationDefinitionTx(ctx, tx, def.DBID, def.ID)
	if err != nil {
		return domain.MigrationDefinition{}, err
	}

	if current.IsImmutable || current.IsSuperseded {
		return domain.MigrationDefinition{}, ErrMigrationImmutable
	}
	if def.Revision > 0 && def.Revision != current.Revision {
		return domain.MigrationDefinition{}, ErrConflict
	}

	hasActive, err := s.HasActiveProposalTx(ctx, tx, def.ID)
	if err != nil {
		return domain.MigrationDefinition{}, err
	}
	if hasActive {
		return domain.MigrationDefinition{}, ErrActiveProposalExists
	}

	effectiveSeq := current.SeqVersion
	var replacesArg any
	if def.ReplacesMigrationID != nil {
		resolvedSeq, err := s.ResolveAndValidateSupersedeTx(ctx, tx, def.DBID, *def.ReplacesMigrationID, def.ID)
		if err != nil {
			return domain.MigrationDefinition{}, err
		}
		effectiveSeq = resolvedSeq
		replacesArg = *def.ReplacesMigrationID
	}

	var updatedByArg any
	if def.UpdatedBy != nil {
		updatedByArg = *def.UpdatedBy
	}
	var batchArg any
	if batchRaw != nil {
		batchArg = batchRaw
	}

	_, err = tx.Exec(ctx, `
		update migration_definitions
		set effective_seq = $3,
		    revision = revision + 1,
		    title = $4,
		    description = $5,
		    sql_up = $6,
		    sql_down = $7,
		    is_irreversible = $8,
		    irreversible_reason = $9,
		    execution_mode = $10,
		    batch_config = $11,
		    replaces_migration_id = $12,
		    updated_by = coalesce($13, updated_by),
		    updated_at = clock_timestamp()
		where id = $1 and db_id = $2`,
		def.ID, def.DBID, effectiveSeq,
		def.Title, def.Description, def.SQLUp, def.SQLDown,
		def.IsIrreversible, def.IrreversibleReason, def.ExecutionMode, batchArg,
		replacesArg, updatedByArg,
	)
	if err != nil {
		return domain.MigrationDefinition{}, fmt.Errorf("update migration_definition: %w", mapWriteError(err))
	}

	updated, err := s.LockMigrationDefinitionTx(ctx, tx, def.DBID, def.ID)
	if err != nil {
		return domain.MigrationDefinition{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MigrationDefinition{}, fmt.Errorf("update migration_definition commit: %w", err)
	}
	return updated, nil
}

// BumpMigrationDefinitionRevisionTx increments revision by 1 when a proposal is withdrawn.
func (s *Store) BumpMigrationDefinitionRevisionTx(ctx context.Context, tx pgx.Tx, dbID, migrationID uuid.UUID) (int, error) {
	var newRev int
	err := tx.QueryRow(ctx, `
		update migration_definitions
		set revision = revision + 1,
		    updated_at = clock_timestamp()
		where id = $1 and db_id = $2
		returning revision`, migrationID, dbID).Scan(&newRev)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("bump migration_definition revision: %w", err)
	}
	return newRev, nil
}

// MarkSupersededChainTx marks all previous chain members on (dbID, effectiveSeq) as superseded by winnerMigrationID.
func (s *Store) MarkSupersededChainTx(ctx context.Context, tx pgx.Tx, dbID uuid.UUID, effectiveSeq int64, winnerMigrationID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		update migration_definitions
		set is_superseded              = true,
		    superseded_by_migration_id = $1,
		    superseded_at              = now(),
		    updated_at                 = now()
		where db_id = $2
		  and effective_seq = $3
		  and id <> $1
		  and is_superseded = false`,
		winnerMigrationID, dbID, effectiveSeq)
	if err != nil {
		return fmt.Errorf("mark superseded chain: %w", err)
	}
	return nil
}

func (s *Store) MarkSupersededChain(ctx context.Context, dbID uuid.UUID, effectiveSeq int64, winnerMigrationID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("mark superseded chain begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.MarkSupersededChainTx(ctx, tx, dbID, effectiveSeq, winnerMigrationID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) GetMigrationDefinition(ctx context.Context, dbID, migrationID uuid.UUID) (domain.MigrationDefinition, error) {
	def, err := scanMigrationDefRow(s.pool.QueryRow(ctx, `
		select `+selectMigrationDefCols+`
		from migration_definitions d
		left join users cu on cu.id = d.created_by
		left join users uu on uu.id = d.updated_by
		where d.id = $1 and d.db_id = $2`, migrationID, dbID))
	if err != nil {
		return domain.MigrationDefinition{}, err
	}
	defs := []domain.MigrationDefinition{def}
	if err := s.enrichMigrationDefinitions(ctx, dbID, defs); err != nil {
		return domain.MigrationDefinition{}, err
	}
	return defs[0], nil
}

func (s *Store) ListMigrationDefinitions(ctx context.Context, dbID uuid.UUID) ([]domain.MigrationDefinition, error) {
	rows, err := s.pool.Query(ctx, `
		select `+selectMigrationDefCols+`
		from migration_definitions d
		left join users cu on cu.id = d.created_by
		left join users uu on uu.id = d.updated_by
		where d.db_id = $1
		order by d.effective_seq asc, d.seq_version asc`, dbID)
	if err != nil {
		return nil, fmt.Errorf("list migration_definitions: %w", err)
	}
	defer rows.Close()

	var defs []domain.MigrationDefinition
	for rows.Next() {
		var r migrationDefRow
		if err := rows.Scan(
			&r.ID, &r.DBID, &r.SeqVersion, &r.EffectiveSeq, &r.Revision,
			&r.Title, &r.Description, &r.SQLUp, &r.SQLDown,
			&r.IsIrreversible, &r.IrreversibleReason, &r.ExecutionMode, &r.BatchConfigRaw,
			&r.IsImmutable, &r.ReplacesMigrationID, &r.IsSuperseded,
			&r.SupersededByMigrationID, &r.SupersededAt,
			&r.CreatedBy, &r.UpdatedBy,
			&r.CreatedByUsername, &r.UpdatedByUsername,
			&r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan migration_definition: %w", err)
		}
		d, err := r.toDomain()
		if err != nil {
			return nil, err
		}
		defs = append(defs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate migration_definitions: %w", err)
	}

	if err := s.enrichMigrationDefinitions(ctx, dbID, defs); err != nil {
		return nil, err
	}
	return defs, nil
}

func (s *Store) enrichMigrationDefinitions(ctx context.Context, dbID uuid.UUID, defs []domain.MigrationDefinition) error {
	if len(defs) == 0 {
		return nil
	}
	activeMap, err := s.loadActiveMigrationMapTx(ctx, s.pool, dbID)
	if err != nil {
		return err
	}

	// Latest proposal per migration_id
	prows, err := s.pool.Query(ctx, `
		select distinct on (p.migration_id)
		       p.id, p.db_id, p.migration_id, p.migration_revision,
		       p.action_type, p.target_execution_id, p.resolution, p.reason,
		       p.status, p.proposed_by, coalesce(u.username, ''),
		       p.created_at, p.updated_at
		from migration_proposals p
		left join users u on u.id = p.proposed_by
		where p.db_id = $1
		order by p.migration_id, p.created_at desc`, dbID)
	if err != nil {
		return fmt.Errorf("query latest proposals for definitions: %w", err)
	}
	latestProposalMap := make(map[uuid.UUID]domain.MigrationProposal)
	for prows.Next() {
		var r proposalRow
		if err := prows.Scan(
			&r.ID, &r.DBID, &r.MigrationID, &r.MigrationRevision,
			&r.ActionType, &r.TargetExecutionID, &r.Resolution, &r.Reason,
			&r.Status, &r.ProposedBy, &r.ProposedByUsername,
			&r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			prows.Close()
			return fmt.Errorf("scan latest proposal: %w", err)
		}
		latestProposalMap[r.MigrationID] = r.toDomain()
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		return fmt.Errorf("iterate latest proposals: %w", err)
	}

	// Latest execution per migration_id
	erows, err := s.pool.Query(ctx, `
		select distinct on (e.migration_id) `+selectMigrationExecutionCols+`
		from migration_executions e
		join migration_definitions d on d.id = e.migration_id
		where e.db_id = $1
		order by e.migration_id, e.seq_order desc`, dbID)
	if err != nil {
		return fmt.Errorf("query latest executions for definitions: %w", err)
	}
	latestExecMap := make(map[uuid.UUID]domain.MigrationExecution)
	for erows.Next() {
		var r migrationExecutionRow
		if err := erows.Scan(
			&r.ExecutionID, &r.RootExecutionID, &r.DBID, &r.MigrationID, &r.ProposalID,
			&r.SeqOrder, &r.SeqVersion, &r.EffectiveSeq, &r.MigrationRevision,
			&r.ActionType, &r.ExecutionMode, &r.ExecutedSQL, &r.ExecutedBy,
			&r.ExecutedByUsername, &r.DBNameSnapshot, &r.MigrationTitleSnapshot,
			&r.StartedAt, &r.FinishedAt, &r.DurationMS, &r.Status, &r.ErrorMessage,
			&r.RollbackOfExecutionID, &r.CommittedBatches, &r.TotalBatches, &r.CommittedRows,
			&r.IsSuperseded, &r.SupersededByID, &r.CreatedAt,
		); err != nil {
			erows.Close()
			return fmt.Errorf("scan latest execution: %w", err)
		}
		latestExecMap[r.MigrationID] = r.toDomain()
	}
	erows.Close()
	if err := erows.Err(); err != nil {
		return fmt.Errorf("iterate latest executions: %w", err)
	}

	for i := range defs {
		id := defs[i].ID
		if !defs[i].IsSuperseded {
			if execID, ok := activeMap[id]; ok {
				defs[i].IsActive = true
				defs[i].ActiveExecutionID = &execID
			}
		}
		if lp, ok := latestProposalMap[id]; ok {
			lpCopy := lp
			defs[i].LatestProposal = &lpCopy
		}
		if le, ok := latestExecMap[id]; ok {
			leCopy := le
			defs[i].LatestExecution = &leCopy
		}
	}
	return nil
}

