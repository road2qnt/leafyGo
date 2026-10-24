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

type dbConnectionRow struct {
	ID          uuid.UUID
	Name        string
	Host        string
	Port        int
	DBName      string
	Username    string
	PasswordEnc []byte
	SSLMode     string
	CreatedBy   pgtype.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (r dbConnectionRow) toDomain() domain.DBConnection {
	c := domain.DBConnection{
		ID:          r.ID,
		Name:        r.Name,
		Host:        r.Host,
		Port:        r.Port,
		DBName:      r.DBName,
		Username:    r.Username,
		PasswordEnc: r.PasswordEnc,
		SSLMode:     r.SSLMode,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
	if r.CreatedBy.Valid {
		id := uuid.UUID(r.CreatedBy.Bytes)
		c.CreatedBy = &id
	}
	return c
}

func (s *Store) CreateDBConnection(ctx context.Context, c domain.DBConnection) (domain.DBConnection, error) {
	c.ID = uuid.New()
	c.CreatedAt = time.Now().UTC()
	c.UpdatedAt = c.CreatedAt
	var createdBy any
	if c.CreatedBy != nil {
		createdBy = *c.CreatedBy
	}
	_, err := s.pool.Exec(ctx, `
		insert into db_connections (id, name, host, port, dbname, username, password_enc, sslmode, created_by, created_at, updated_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`,
		c.ID, c.Name, c.Host, c.Port, c.DBName, c.Username, c.PasswordEnc, c.SSLMode, createdBy, c.CreatedAt)
	if err != nil {
		return domain.DBConnection{}, fmt.Errorf("create db connection: %w", mapWriteError(err))
	}
	return c, nil
}

func (s *Store) UpdateDBConnection(ctx context.Context, c domain.DBConnection) (domain.DBConnection, error) {
	var r dbConnectionRow
	err := s.pool.QueryRow(ctx, `
		update db_connections
		set name=$2, host=$3, port=$4, dbname=$5, username=$6, password_enc=$7, sslmode=$8,
		    updated_at=clock_timestamp()
		where id=$1
		returning id, name, host, port, dbname, username, password_enc, sslmode,
		          created_by, created_at, updated_at`,
		c.ID, c.Name, c.Host, c.Port, c.DBName, c.Username, c.PasswordEnc, c.SSLMode).
		Scan(&r.ID, &r.Name, &r.Host, &r.Port, &r.DBName, &r.Username, &r.PasswordEnc, &r.SSLMode,
			&r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DBConnection{}, ErrNotFound
	}
	if err != nil {
		return domain.DBConnection{}, fmt.Errorf("update db connection: %w", mapWriteError(err))
	}
	return r.toDomain(), nil
}

func (s *Store) DeleteDBConnection(ctx context.Context, id uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("delete db begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Row lock makes delete vs history-insert deterministic: the insert holds
	// FOR KEY SHARE on this row, so either delete waits and then sees running,
	// or delete wins and the insert fails its foreign key.
	var locked uuid.UUID
	err = tx.QueryRow(ctx, `select id from db_connections where id=$1 for update`, id).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock db connection: %w", err)
	}

	var running bool
	if err := tx.QueryRow(ctx, `
		select exists(select 1 from migration_history where db_id=$1 and status='running')`, id).
		Scan(&running); err != nil {
		return fmt.Errorf("check running migration: %w", err)
	}
	if running {
		return ErrRunningMigration
	}

	if _, err := tx.Exec(ctx, `delete from db_connections where id=$1`, id); err != nil {
		return fmt.Errorf("delete db connection: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("delete db commit: %w", err)
	}
	return nil
}

func (s *Store) GetDBConnection(ctx context.Context, id uuid.UUID) (domain.DBConnection, error) {
	var r dbConnectionRow
	err := s.pool.QueryRow(ctx, `
		select id, name, host, port, dbname, username, password_enc, sslmode, created_by, created_at, updated_at
		from db_connections where id=$1`, id).
		Scan(&r.ID, &r.Name, &r.Host, &r.Port, &r.DBName, &r.Username, &r.PasswordEnc, &r.SSLMode, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DBConnection{}, ErrNotFound
	}
	if err != nil {
		return domain.DBConnection{}, fmt.Errorf("get db connection: %w", err)
	}
	return r.toDomain(), nil
}

func (s *Store) ListDBConnections(ctx context.Context) ([]domain.DBConnection, error) {
	rows, err := s.pool.Query(ctx, `
		select id, name, host, port, dbname, username, password_enc, sslmode, created_by, created_at, updated_at
		from db_connections order by lower(name)`)
	if err != nil {
		return nil, fmt.Errorf("list db connections: %w", err)
	}
	defer rows.Close()

	var out []domain.DBConnection
	for rows.Next() {
		var r dbConnectionRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Host, &r.Port, &r.DBName, &r.Username, &r.PasswordEnc, &r.SSLMode, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan db connection: %w", err)
		}
		out = append(out, r.toDomain())
	}
	return out, rows.Err()
}

func (s *Store) ListAccessibleDBs(ctx context.Context, userID uuid.UUID) ([]domain.AccessibleDB, error) {
	rows, err := s.pool.Query(ctx, `
		select c.id, c.name, c.host, c.port, c.dbname, c.username, c.password_enc, c.sslmode,
		       c.created_by, c.created_at, c.updated_at,
		       max(case a.level when 'edit' then 2 else 1 end) as level
		from db_connections c
		join db_access a on a.db_id=c.id
		join group_members m on m.group_id=a.group_id
		where m.user_id=$1
		group by c.id
		order by lower(c.name)`, userID)
	if err != nil {
		return nil, fmt.Errorf("list accessible dbs: %w", err)
	}
	defer rows.Close()

	var out []domain.AccessibleDB
	for rows.Next() {
		var r dbConnectionRow
		var level int
		if err := rows.Scan(&r.ID, &r.Name, &r.Host, &r.Port, &r.DBName, &r.Username, &r.PasswordEnc, &r.SSLMode,
			&r.CreatedBy, &r.CreatedAt, &r.UpdatedAt, &level); err != nil {
			return nil, fmt.Errorf("scan accessible db: %w", err)
		}
		out = append(out, domain.AccessibleDB{DBConnection: r.toDomain(), Level: domain.AccessLevel(level)})
	}
	return out, rows.Err()
}

// Level tertinggi antar group menang; admin flag tidak dihitung.
func (s *Store) EffectiveLevel(ctx context.Context, userID, dbID uuid.UUID) (domain.AccessLevel, error) {
	var level int
	err := s.pool.QueryRow(ctx, `
		select coalesce(max(case a.level when 'edit' then 2 else 1 end), 0)
		from db_access a
		join group_members m on m.group_id=a.group_id
		where m.user_id=$1 and a.db_id=$2`, userID, dbID).Scan(&level)
	if err != nil {
		return domain.LevelNone, fmt.Errorf("effective level: %w", err)
	}
	return domain.AccessLevel(level), nil
}
