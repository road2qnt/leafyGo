package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"leafygo/internal/domain"
)

type migrationDraftRow struct {
	ID            uuid.UUID
	DBID          uuid.UUID
	Title         string
	SQLText       string
	Revision      int
	CreatedBy     pgtype.UUID
	UpdatedBy     pgtype.UUID
	CreatedByName string
	UpdatedByName string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (r migrationDraftRow) toDomain() domain.MigrationDraft {
	d := domain.MigrationDraft{
		ID:            r.ID,
		DBID:          r.DBID,
		Title:         r.Title,
		SQLText:       r.SQLText,
		Revision:      r.Revision,
		CreatedByName: r.CreatedByName,
		UpdatedByName: r.UpdatedByName,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
	if r.CreatedBy.Valid {
		id := uuid.UUID(r.CreatedBy.Bytes)
		d.CreatedBy = &id
	}
	if r.UpdatedBy.Valid {
		id := uuid.UUID(r.UpdatedBy.Bytes)
		d.UpdatedBy = &id
	}
	return d
}

func hasRevisionConflict(current, token int) bool {
	if token <= 0 {
		return true
	}
	return current != token
}

func applyDraftUpdate(d domain.MigrationDraft, title, sql string, actorID uuid.UUID, now time.Time) domain.MigrationDraft {
	d.Title = title
	d.SQLText = sql
	d.Revision++
	d.UpdatedBy = &actorID
	d.UpdatedAt = now
	return d
}

func (s *Store) CreateMigrationDraft(ctx context.Context, draft domain.MigrationDraft) (domain.MigrationDraft, error) {
	if draft.ID == uuid.Nil {
		draft.ID = uuid.New()
	}
	draft.Revision = 1
	now := time.Now().UTC()
	draft.CreatedAt = now
	draft.UpdatedAt = now

	var createdBy, updatedBy any
	if draft.CreatedBy != nil {
		createdBy = *draft.CreatedBy
	}
	if draft.UpdatedBy != nil {
		updatedBy = *draft.UpdatedBy
	}

	_, err := s.pool.Exec(ctx, `
		insert into migration_drafts (id, db_id, title, sql_text, revision, created_by, updated_by, created_at, updated_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		draft.ID, draft.DBID, draft.Title, draft.SQLText, draft.Revision,
		createdBy, updatedBy, draft.CreatedAt, draft.UpdatedAt)
	if err != nil {
		return domain.MigrationDraft{}, fmt.Errorf("create draft: %w", mapWriteError(err))
	}
	return draft, nil
}

func (s *Store) UpdateMigrationDraft(ctx context.Context, dbID, draftID uuid.UUID, title, sql string, revision int, actorID uuid.UUID) (domain.MigrationDraft, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.MigrationDraft{}, fmt.Errorf("update draft begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var r migrationDraftRow
	err = tx.QueryRow(ctx, `
		select id, db_id, title, sql_text, revision, created_by, updated_by, created_at, updated_at
		from migration_drafts where id=$1 and db_id=$2 for update`, draftID, dbID).
		Scan(&r.ID, &r.DBID, &r.Title, &r.SQLText, &r.Revision, &r.CreatedBy, &r.UpdatedBy, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MigrationDraft{}, ErrNotFound
	}
	if err != nil {
		return domain.MigrationDraft{}, fmt.Errorf("load draft for update: %w", err)
	}
	if hasRevisionConflict(r.Revision, revision) {
		return domain.MigrationDraft{}, ErrConflict
	}

	err = tx.QueryRow(ctx, `
		update migration_drafts
		set title=$3, sql_text=$4, updated_by=$5, revision=revision+1, updated_at=clock_timestamp()
		where id=$1 and db_id=$2
		returning id, db_id, title, sql_text, revision, created_by, updated_by, created_at, updated_at`,
		draftID, dbID, title, sql, actorID).
		Scan(&r.ID, &r.DBID, &r.Title, &r.SQLText, &r.Revision, &r.CreatedBy, &r.UpdatedBy, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return domain.MigrationDraft{}, fmt.Errorf("update draft: %w", mapWriteError(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MigrationDraft{}, fmt.Errorf("update draft commit: %w", err)
	}
	return r.toDomain(), nil
}

func (s *Store) GetMigrationDraft(ctx context.Context, dbID, id uuid.UUID) (domain.MigrationDraft, error) {
	var r migrationDraftRow
	err := s.pool.QueryRow(ctx, `
		select d.id, d.db_id, d.title, d.sql_text, d.revision, d.created_by, d.updated_by,
		       coalesce(cu.username, ''), coalesce(uu.username, ''), d.created_at, d.updated_at
		from migration_drafts d
		left join users cu on cu.id=d.created_by
		left join users uu on uu.id=d.updated_by
		where d.id=$1 and d.db_id=$2`, id, dbID).
		Scan(&r.ID, &r.DBID, &r.Title, &r.SQLText, &r.Revision, &r.CreatedBy, &r.UpdatedBy,
			&r.CreatedByName, &r.UpdatedByName, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MigrationDraft{}, ErrNotFound
	}
	if err != nil {
		return domain.MigrationDraft{}, fmt.Errorf("get draft: %w", err)
	}
	return r.toDomain(), nil
}

func (s *Store) ListMigrationDrafts(ctx context.Context, dbID uuid.UUID) ([]domain.MigrationDraft, error) {
	rows, err := s.pool.Query(ctx, `
		select d.id, d.db_id, d.title, d.revision, d.created_by, d.updated_by,
		       coalesce(cu.username, ''), coalesce(uu.username, ''), d.created_at, d.updated_at
		from migration_drafts d
		left join users cu on cu.id=d.created_by
		left join users uu on uu.id=d.updated_by
		where d.db_id=$1
		order by d.updated_at desc`, dbID)
	if err != nil {
		return nil, fmt.Errorf("list drafts: %w", err)
	}
	defer rows.Close()

	var out []domain.MigrationDraft
	for rows.Next() {
		var r migrationDraftRow
		if err := rows.Scan(&r.ID, &r.DBID, &r.Title, &r.Revision, &r.CreatedBy, &r.UpdatedBy,
			&r.CreatedByName, &r.UpdatedByName, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan draft: %w", err)
		}
		out = append(out, r.toDomain())
	}
	return out, rows.Err()
}

func (s *Store) DeleteMigrationDraft(ctx context.Context, dbID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `delete from migration_drafts where id=$1 and db_id=$2`, id, dbID)
	if err != nil {
		return fmt.Errorf("delete draft: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
