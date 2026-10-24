package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"leafygo/internal/domain"
)

func (s *Store) CreateUser(ctx context.Context, username, passwordHash string, isAdmin bool) (domain.User, error) {
	u := domain.User{
		ID:              uuid.New(),
		Username:        username,
		PasswordHash:    passwordHash,
		IsPlatformAdmin: isAdmin,
		IsActive:        true,
		CreatedAt:       time.Now().UTC(),
	}
	_, err := s.pool.Exec(ctx, `
		insert into users (id, username, password_hash, is_platform_admin, is_active, created_at, updated_at)
		values ($1, $2, $3, $4, $5, $6, $6)`,
		u.ID, u.Username, u.PasswordHash, u.IsPlatformAdmin, u.IsActive, u.CreatedAt)
	if err != nil {
		return domain.User{}, fmt.Errorf("create user: %w", mapWriteError(err))
	}
	return u, nil
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (domain.User, error) {
	return s.scanUser(s.pool.QueryRow(ctx, `
		select id, username, password_hash, is_platform_admin, is_active, created_at
		from users where lower(username) = lower($1)`, username))
}

func (s *Store) GetUserByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	return s.scanUser(s.pool.QueryRow(ctx, `
		select id, username, password_hash, is_platform_admin, is_active, created_at
		from users where id = $1`, id))
}

func (s *Store) scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsPlatformAdmin, &u.IsActive, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("scan user: %w", err)
	}
	return u, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := s.pool.Query(ctx, `
		select id, username, is_platform_admin, is_active, created_at
		from users order by lower(username)`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.Username, &u.IsPlatformAdmin, &u.IsActive, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) SetUserAdmin(ctx context.Context, id uuid.UUID, admin bool) error {
	tag, err := s.pool.Exec(ctx, `update users set is_platform_admin=$2, updated_at=now() where id=$1`, id, admin)
	if err != nil {
		return fmt.Errorf("set admin: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func checkAdminDeactivation(targetID, actorID uuid.UUID, targetIsActiveAdmin bool, activeAdmins int, active bool) error {
	if active {
		return nil
	}
	if targetID == actorID {
		return ErrSelfMutation
	}
	if targetIsActiveAdmin && activeAdmins <= 1 {
		return ErrLastAdmin
	}
	return nil
}

func (s *Store) SetUserActiveGuarded(ctx context.Context, targetID uuid.UUID, active bool, actorID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("set user active begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		select id from users
		where is_platform_admin and is_active
		order by id
		for update`)
	if err != nil {
		return fmt.Errorf("lock active admins: %w", err)
	}
	activeAdmins := 0
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan active admin: %w", err)
		}
		activeAdmins++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("lock active admins: %w", err)
	}

	var targetIsAdmin, targetIsActive bool
	err = tx.QueryRow(ctx, `
		select is_platform_admin, is_active from users where id=$1 for update`, targetID).
		Scan(&targetIsAdmin, &targetIsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load target user: %w", err)
	}

	if err := checkAdminDeactivation(targetID, actorID, targetIsAdmin && targetIsActive, activeAdmins, active); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		update users set is_active=$2, updated_at=now() where id=$1`, targetID, active); err != nil {
		return fmt.Errorf("set user active: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("set user active commit: %w", err)
	}
	return nil
}

func (s *Store) CountActivePlatformAdmins(ctx context.Context) (int, error) {
	var count int
	if err := s.pool.QueryRow(ctx, `
		select count(*) from users where is_platform_admin and is_active`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active admins: %w", err)
	}
	return count, nil
}

func (s *Store) UpdateUserPassword(ctx context.Context, id uuid.UUID, hash string) error {
	tag, err := s.pool.Exec(ctx, `update users set password_hash=$2, updated_at=now() where id=$1`, id, hash)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
