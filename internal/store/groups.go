package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"leafygo/internal/domain"
)

func (s *Store) CreateGroup(ctx context.Context, name, description string) (domain.Group, error) {
	g := domain.Group{ID: uuid.New(), Name: name, Description: description, CreatedAt: time.Now().UTC()}
	_, err := s.pool.Exec(ctx, `
		insert into groups (id, name, description, created_at) values ($1,$2,$3,$4)`,
		g.ID, g.Name, g.Description, g.CreatedAt)
	if err != nil {
		return domain.Group{}, fmt.Errorf("create group: %w", err)
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
	_, err := s.pool.Exec(ctx, `
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
		select u.id, u.username, u.is_platform_admin, u.created_at
		from group_members m join users u on u.id=m.user_id
		where m.group_id=$1 order by lower(u.username)`, groupID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.Username, &u.IsPlatformAdmin, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// SetGroupAccess grants a group a level on a database. LevelNone removes the
// grant. Admin CRUD pages (Jibril) call this; the access policy only reads.
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
