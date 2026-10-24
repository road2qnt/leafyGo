package store

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"leafygo/internal/domain"
)

const migrationStoppedError = "server berhenti saat eksekusi; kondisi target tidak diketahui, periksa manual"

type migrationHistoryRow struct {
	ID             uuid.UUID
	DBID           uuid.UUID
	Version        int64
	SQLText        string
	ExecutedBy     pgtype.UUID
	StartedAt      time.Time
	FinishedAt     pgtype.Timestamptz
	Status         string
	Error          string
	DurationMS     pgtype.Int4
	ExecutedByName string
}

func (r migrationHistoryRow) toDomain() domain.MigrationHistory {
	h := domain.MigrationHistory{
		ID:             r.ID,
		DBID:           r.DBID,
		Version:        r.Version,
		SQLText:        r.SQLText,
		StartedAt:      r.StartedAt,
		Status:         r.Status,
		Error:          r.Error,
		ExecutedByName: r.ExecutedByName,
	}
	if r.ExecutedBy.Valid {
		id := uuid.UUID(r.ExecutedBy.Bytes)
		h.ExecutedBy = &id
	}
	if r.FinishedAt.Valid {
		t := r.FinishedAt.Time
		h.FinishedAt = &t
	}
	if r.DurationMS.Valid {
		v := int(r.DurationMS.Int32)
		h.DurationMS = &v
	}
	return h
}

func advisoryLockKey(dbID uuid.UUID) int64 {
	return int64(binary.BigEndian.Uint64(dbID[:8]))
}

func staleCutoff(now time.Time, olderThan time.Duration) time.Time {
	return now.Add(-olderThan)
}

func durationMilliseconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	ms := d.Milliseconds()
	if ms < 0 {
		return 0
	}
	return int(ms)
}

func (s *Store) AcquireMigrationLock(ctx context.Context, dbID uuid.UUID) (int64, func(), error) {
	key := advisoryLockKey(dbID)
	cfg := s.pool.Config().ConnConfig.Copy()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ErrLockTimeout
		}
		return 0, nil, fmt.Errorf("connect migration lock: %w", err)
	}

	unlockAndClose := func() {
		relCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(relCtx, `select pg_advisory_unlock($1)`, key)
		_ = conn.Close(relCtx)
	}

	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	locked := false
	for !locked {
		if err := conn.QueryRow(ctx, `select pg_try_advisory_lock($1)`, key).Scan(&locked); err != nil {
			unlockAndClose()
			if ctx.Err() != nil {
				return 0, nil, ErrLockTimeout
			}
			return 0, nil, fmt.Errorf("acquire migration lock: %w", err)
		}
		if locked {
			break
		}
		select {
		case <-ctx.Done():
			unlockAndClose()
			return 0, nil, ErrLockTimeout
		case <-ticker.C:
		}
	}

	var version int64
	if err := conn.QueryRow(ctx, `
		select coalesce(max(version),0)+1 from migration_history where db_id=$1`, dbID).
		Scan(&version); err != nil {
		unlockAndClose()
		return 0, nil, fmt.Errorf("allocate migration version: %w", err)
	}

	var once sync.Once
	release := func() {
		once.Do(unlockAndClose)
	}
	return version, release, nil
}

func (s *Store) InsertRunningMigration(ctx context.Context, h domain.MigrationHistory) error {
	if h.ID == uuid.Nil {
		h.ID = uuid.New()
	}
	if h.StartedAt.IsZero() {
		h.StartedAt = time.Now().UTC()
	}
	var executedBy any
	if h.ExecutedBy != nil {
		executedBy = *h.ExecutedBy
	}
	_, err := s.pool.Exec(ctx, `
		insert into migration_history
			(id, db_id, version, sql_text, executed_by, started_at, finished_at, status, error, duration_ms)
		values ($1,$2,$3,$4,$5,$6,NULL,'running','',NULL)`,
		h.ID, h.DBID, h.Version, h.SQLText, executedBy, h.StartedAt)
	if err != nil {
		return fmt.Errorf("insert running migration: %w", mapWriteError(err))
	}
	return nil
}

func (s *Store) FinishMigration(ctx context.Context, historyID, dbID uuid.UUID, status, errText string, duration time.Duration, draftID *uuid.UUID, draftRevision int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("finish migration begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		update migration_history
		set status=$3, error=$4, finished_at=now(), duration_ms=$5
		where id=$1 and db_id=$2 and status='running'`,
		historyID, dbID, status, errText, durationMilliseconds(duration))
	if err != nil {
		return fmt.Errorf("finish migration: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrHistoryNotRunning
	}

	if status == domain.MigrationSuccess && draftID != nil {
		if _, err := tx.Exec(ctx, `
			delete from migration_drafts where id=$1 and db_id=$2 and revision=$3`,
			*draftID, dbID, draftRevision); err != nil {
			return fmt.Errorf("delete executed draft: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("finish migration commit: %w", err)
	}
	return nil
}

func (s *Store) MarkRunningMigrationsFailed(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		update migration_history
		set status='failed', finished_at=now(), duration_ms=NULL, error=$1
		where status='running'`, migrationStoppedError)
	if err != nil {
		return 0, fmt.Errorf("mark running migrations failed: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (s *Store) ReapStaleRunningMigrations(ctx context.Context, dbID uuid.UUID, olderThan time.Duration) (int64, error) {
	cutoff := staleCutoff(time.Now().UTC(), olderThan)
	tag, err := s.pool.Exec(ctx, `
		update migration_history
		set status='failed', finished_at=now(), duration_ms=NULL, error=$3
		where db_id=$1 and status='running' and started_at < $2`,
		dbID, cutoff, migrationStoppedError)
	if err != nil {
		return 0, fmt.Errorf("reap stale migrations: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (s *Store) ListMigrationHistory(ctx context.Context, dbID uuid.UUID, limit int) ([]domain.MigrationHistory, error) {
	if limit < 1 {
		limit = 1
	}
	rows, err := s.pool.Query(ctx, `
		select h.id, h.db_id, h.version, ''::text, h.executed_by, h.started_at,
		       h.finished_at, h.status, h.error, h.duration_ms, coalesce(u.username, '')
		from migration_history h
		left join users u on u.id=h.executed_by
		where h.db_id=$1
		order by h.version desc
		limit $2`, dbID, limit)
	if err != nil {
		return nil, fmt.Errorf("list migration history: %w", err)
	}
	defer rows.Close()

	var out []domain.MigrationHistory
	for rows.Next() {
		var r migrationHistoryRow
		if err := rows.Scan(&r.ID, &r.DBID, &r.Version, &r.SQLText, &r.ExecutedBy, &r.StartedAt,
			&r.FinishedAt, &r.Status, &r.Error, &r.DurationMS, &r.ExecutedByName); err != nil {
			return nil, fmt.Errorf("scan migration history: %w", err)
		}
		out = append(out, r.toDomain())
	}
	return out, rows.Err()
}

func (s *Store) GetMigrationHistory(ctx context.Context, dbID, id uuid.UUID) (domain.MigrationHistory, error) {
	var r migrationHistoryRow
	err := s.pool.QueryRow(ctx, `
		select h.id, h.db_id, h.version, h.sql_text, h.executed_by, h.started_at,
		       h.finished_at, h.status, h.error, h.duration_ms, coalesce(u.username, '')
		from migration_history h
		left join users u on u.id=h.executed_by
		where h.id=$1 and h.db_id=$2`, id, dbID).
		Scan(&r.ID, &r.DBID, &r.Version, &r.SQLText, &r.ExecutedBy, &r.StartedAt,
			&r.FinishedAt, &r.Status, &r.Error, &r.DurationMS, &r.ExecutedByName)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MigrationHistory{}, ErrNotFound
	}
	if err != nil {
		return domain.MigrationHistory{}, fmt.Errorf("get migration history: %w", err)
	}
	return r.toDomain(), nil
}
