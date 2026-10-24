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

func (s *Store) CreateGroup(ctx context.Context, name, description string) (domain.Group, error) {
	g := domain.Group{ID: uuid.New(), Name: name, Description: description, CreatedAt: time.Now().UTC()}
	_, err := s.pool.Exec(ctx, `
		insert into groups (id, name, description, created_at) values ($1,$2,$3,$4)`,
		g.ID, g.Name, g.Description, g.CreatedAt)
	if err != nil {
		return domain.Group{}, fmt.Errorf("create group: %w", mapWriteError(err))
	}
	return g, nil
}

func (s *Store) GetGroup(ctx context.Context, id uuid.UUID) (domain.Group, error) {
	var g domain.Group
	err := s.pool.QueryRow(ctx, `
		select id, name, description, created_at from groups where id=$1`, id).
		Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Group{}, ErrNotFound
	}
	if err != nil {
		return domain.Group{}, fmt.Errorf("get group: %w", err)
	}
	return g, nil
}

func (s *Store) GetGroupByName(ctx context.Context, name string) (domain.Group, error) {
	var g domain.Group
	err := s.pool.QueryRow(ctx, `
		select id, name, description, created_at from groups where lower(name)=lower($1)`, name).
		Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Group{}, ErrNotFound
	}
	if err != nil {
		return domain.Group{}, fmt.Errorf("get group by name: %w", err)
	}
	return g, nil
}

func (s *Store) ListGroups(ctx context.Context) ([]domain.Group, error) {
	rows, err := s.pool.Query(ctx, `
		select id, name, description, created_at from groups order by lower(name)`)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()

	var groups []domain.Group
	for rows.Next() {
		var g domain.Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan group: %w", err)
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (s *Store) AddGroupMember(ctx context.Context, groupID, userID uuid.UUID) error {
	u, err := s.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.IsPlatformAdmin {
		return ErrAdminGroupMember
	}
	_, err = s.pool.Exec(ctx, `
		insert into group_members (group_id, user_id) values ($1,$2)
		on conflict do nothing`, groupID, userID)
	if err != nil {
		return fmt.Errorf("add member: %w", err)
	}
	return nil
}

func (s *Store) RemoveGroupMember(ctx context.Context, groupID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `delete from group_members where group_id=$1 and user_id=$2`, groupID, userID)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	return nil
}

func (s *Store) ListGroupMembers(ctx context.Context, groupID uuid.UUID) ([]domain.User, error) {
	rows, err := s.pool.Query(ctx, `
		select u.id, u.username, u.is_platform_admin, u.is_active, u.created_at
		from group_members m join users u on u.id=m.user_id
		where m.group_id=$1 order by lower(u.username)`, groupID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.Username, &u.IsPlatformAdmin, &u.IsActive, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) ListGroupAccess(ctx context.Context, groupID uuid.UUID) ([]domain.GroupAccess, error) {
	rows, err := s.pool.Query(ctx, `
		select a.db_id, c.name, a.level
		from db_access a join db_connections c on c.id=a.db_id
		where a.group_id=$1
		order by lower(c.name)`, groupID)
	if err != nil {
		return nil, fmt.Errorf("list group access: %w", err)
	}
	defer rows.Close()

	var out []domain.GroupAccess
	for rows.Next() {
		var ga domain.GroupAccess
		var level string
		if err := rows.Scan(&ga.DBID, &ga.DBName, &level); err != nil {
			return nil, fmt.Errorf("scan group access: %w", err)
		}
		ga.Level = domain.ParseLevel(level)
		out = append(out, ga)
	}
	return out, rows.Err()
}

func (s *Store) DeleteGroup(ctx context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("delete group begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var groupID uuid.UUID
	err = tx.QueryRow(ctx, `select id from groups where id=$1 for update`, id).Scan(&groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock group: %w", err)
	}

	rows, err := tx.Query(ctx, `select user_id from group_members where group_id=$1`, id)
	if err != nil {
		return nil, fmt.Errorf("list group members: %w", err)
	}
	var members []uuid.UUID
	for rows.Next() {
		var userID uuid.UUID
		if err := rows.Scan(&userID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan group member: %w", err)
		}
		members = append(members, userID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list group members: %w", err)
	}

	if _, err := tx.Exec(ctx, `delete from groups where id=$1`, id); err != nil {
		return nil, fmt.Errorf("delete group: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("delete group commit: %w", err)
	}
	return members, nil
}

func (s *Store) RevokeGroupMembersSessionsExcept(ctx context.Context, groupID, actorID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		update sessions set revoked_at=now()
		where revoked_at is null
		  and user_id <> $2
		  and user_id in (select user_id from group_members where group_id=$1)`, groupID, actorID)
	if err != nil {
		return fmt.Errorf("revoke group member sessions: %w", err)
	}
	return nil
}

func (s *Store) SetGroupAccess(ctx context.Context, groupID, dbID uuid.UUID, level domain.AccessLevel) error {
	if level == domain.LevelNone {
		return s.RemoveGroupAccess(ctx, groupID, dbID)
	}
	_, err := s.pool.Exec(ctx, `
		insert into db_access (db_id, group_id, level) values ($1,$2,$3)
		on conflict (db_id, group_id) do update set level=excluded.level`,
		dbID, groupID, level.String())
	if err != nil {
		return fmt.Errorf("set access: %w", err)
	}
	return nil
}

func (s *Store) RemoveGroupAccess(ctx context.Context, groupID, dbID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `delete from db_access where group_id=$1 and db_id=$2`, groupID, dbID)
	if err != nil {
		return fmt.Errorf("remove access: %w", err)
	}
	return nil
}
