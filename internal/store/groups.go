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

type groupRow struct {
	ID             uuid.UUID
	Name           string
	Description    string
	LeaderID       pgtype.UUID
	LeaderUsername string
	DeletedAt      pgtype.Timestamptz
	CreatedAt      time.Time
}

func (r groupRow) toDomain() domain.Group {
	g := domain.Group{
		ID:             r.ID,
		Name:           r.Name,
		Description:    r.Description,
		LeaderUsername: r.LeaderUsername,
		CreatedAt:      r.CreatedAt,
	}
	if r.LeaderID.Valid {
		id := uuid.UUID(r.LeaderID.Bytes)
		g.LeaderID = &id
	}
	if r.DeletedAt.Valid {
		t := r.DeletedAt.Time
		g.DeletedAt = &t
	}
	return g
}

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

func (s *Store) UpdateGroup(ctx context.Context, id uuid.UUID, name, description string) (domain.Group, error) {
	var r groupRow
	err := s.pool.QueryRow(ctx, `
		update groups g set name=$2, description=$3
		where g.id=$1 and g.deleted_at is null
		returning g.id, g.name, g.description, g.leader_id,
		          coalesce((select u.username from users u where u.id = g.leader_id), ''),
		          g.deleted_at, g.created_at`, id, name, description).
		Scan(&r.ID, &r.Name, &r.Description, &r.LeaderID, &r.LeaderUsername, &r.DeletedAt, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Group{}, ErrNotFound
	}
	if err != nil {
		return domain.Group{}, fmt.Errorf("update group: %w", mapWriteError(err))
	}
	return r.toDomain(), nil
}

func (s *Store) GetGroup(ctx context.Context, id uuid.UUID) (domain.Group, error) {
	var r groupRow
	err := s.pool.QueryRow(ctx, `
		select g.id, g.name, g.description, g.leader_id, coalesce(u.username, ''), g.deleted_at, g.created_at
		from groups g
		left join users u on u.id = g.leader_id
		where g.id=$1 and g.deleted_at is null`, id).
		Scan(&r.ID, &r.Name, &r.Description, &r.LeaderID, &r.LeaderUsername, &r.DeletedAt, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Group{}, ErrNotFound
	}
	if err != nil {
		return domain.Group{}, fmt.Errorf("get group: %w", err)
	}
	return r.toDomain(), nil
}

func (s *Store) GetGroupByName(ctx context.Context, name string) (domain.Group, error) {
	var r groupRow
	err := s.pool.QueryRow(ctx, `
		select g.id, g.name, g.description, g.leader_id, coalesce(u.username, ''), g.deleted_at, g.created_at
		from groups g
		left join users u on u.id = g.leader_id
		where lower(g.name)=lower($1) and g.deleted_at is null`, name).
		Scan(&r.ID, &r.Name, &r.Description, &r.LeaderID, &r.LeaderUsername, &r.DeletedAt, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Group{}, ErrNotFound
	}
	if err != nil {
		return domain.Group{}, fmt.Errorf("get group by name: %w", err)
	}
	return r.toDomain(), nil
}

func (s *Store) ListGroups(ctx context.Context) ([]domain.Group, error) {
	rows, err := s.pool.Query(ctx, `
		select g.id, g.name, g.description, g.leader_id, coalesce(u.username, ''), g.deleted_at, g.created_at
		from groups g
		left join users u on u.id = g.leader_id
		where g.deleted_at is null
		order by lower(g.name)`)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()

	var groups []domain.Group
	for rows.Next() {
		var r groupRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.LeaderID, &r.LeaderUsername, &r.DeletedAt, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan group: %w", err)
		}
		groups = append(groups, r.toDomain())
	}
	return groups, rows.Err()
}

func (s *Store) SetGroupLeader(ctx context.Context, groupID uuid.UUID, leaderID *uuid.UUID) (domain.Group, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Group{}, fmt.Errorf("set group leader begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var lockedID uuid.UUID
	err = tx.QueryRow(ctx, `select id from groups where id=$1 and deleted_at is null for update`, groupID).Scan(&lockedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Group{}, ErrNotFound
	}
	if err != nil {
		return domain.Group{}, fmt.Errorf("lock group: %w", err)
	}

	var leaderArg any
	if leaderID != nil && *leaderID != uuid.Nil {
		var validLeader bool
		err = tx.QueryRow(ctx, `
			select exists (
				select 1 from group_members gm
				join users u on u.id = gm.user_id
				where gm.group_id = $1 and gm.user_id = $2 and u.is_active = true
			)`, groupID, *leaderID).Scan(&validLeader)
		if err != nil {
			return domain.Group{}, fmt.Errorf("validate group leader: %w", err)
		}
		if !validLeader {
			return domain.Group{}, ErrInvalidGroupLeader
		}
		leaderArg = *leaderID
	}

	var r groupRow
	err = tx.QueryRow(ctx, `
		update groups g set leader_id=$2
		where g.id=$1 and g.deleted_at is null
		returning g.id, g.name, g.description, g.leader_id,
		          coalesce((select u.username from users u where u.id = g.leader_id), ''),
		          g.deleted_at, g.created_at`, groupID, leaderArg).
		Scan(&r.ID, &r.Name, &r.Description, &r.LeaderID, &r.LeaderUsername, &r.DeletedAt, &r.CreatedAt)
	if err != nil {
		return domain.Group{}, fmt.Errorf("update group leader: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Group{}, fmt.Errorf("set group leader commit: %w", err)
	}
	return r.toDomain(), nil
}

func (s *Store) ListUserLeaderGroupIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `
		select g.id
		from groups g
		where g.leader_id = $1 and g.deleted_at is null
		order by lower(g.name)`, userID)
	if err != nil {
		return nil, fmt.Errorf("list user leader groups: %w", err)
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan leader group id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) ListRequiredApproverGroups(ctx context.Context, dbID uuid.UUID) ([]domain.RequiredApproverGroup, error) {
	rows, err := s.pool.Query(ctx, `
		select g.id, g.name, g.leader_id, coalesce(u.username, '') as leader_name,
		       coalesce(u.is_active, false) as is_active, a.level
		from db_access a
		join groups g on g.id = a.group_id
		join db_connections d on d.id = a.db_id
		left join users u on u.id = g.leader_id
		where a.db_id = $1
		  and g.deleted_at is null
		  and d.deleted_at is null
		order by g.name`, dbID)
	if err != nil {
		return nil, fmt.Errorf("list required approver groups: %w", err)
	}
	defer rows.Close()

	var out []domain.RequiredApproverGroup
	for rows.Next() {
		var item domain.RequiredApproverGroup
		var leaderID pgtype.UUID
		var levelStr string
		if err := rows.Scan(&item.GroupID, &item.GroupName, &leaderID, &item.LeaderUsername, &item.LeaderActive, &levelStr); err != nil {
			return nil, fmt.Errorf("scan required approver group: %w", err)
		}
		if leaderID.Valid {
			id := uuid.UUID(leaderID.Bytes)
			item.LeaderID = &id
		}
		item.AccessLevel = domain.ParseLevel(levelStr)
		out = append(out, item)
	}
	return out, rows.Err()
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("remove member begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `delete from group_members where group_id=$1 and user_id=$2`, groupID, userID); err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	if _, err := tx.Exec(ctx, `update groups set leader_id=null where id=$1 and leader_id=$2`, groupID, userID); err != nil {
		return fmt.Errorf("clear group leader on remove member: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("remove member commit: %w", err)
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
		where a.group_id=$1 and c.deleted_at is null
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
	err = tx.QueryRow(ctx, `select id from groups where id=$1 and deleted_at is null for update`, id).Scan(&groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock group: %w", err)
	}

	var hasHistory bool
	err = tx.QueryRow(ctx, `
		select exists (
			select 1 from migration_proposal_approvers where group_id = $1
			union all
			select 1 from migration_approvals where group_id = $1
		)`, id).Scan(&hasHistory)
	if err != nil {
		return nil, fmt.Errorf("check group history: %w", err)
	}
	if hasHistory {
		return nil, ErrGroupHasHistory
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

func (s *Store) SoftDeleteGroup(ctx context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("soft delete group begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var groupID uuid.UUID
	err = tx.QueryRow(ctx, `select id from groups where id=$1 and deleted_at is null for update`, id).Scan(&groupID)
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

	if _, err := tx.Exec(ctx, `update groups set deleted_at=now() where id=$1`, id); err != nil {
		return nil, fmt.Errorf("soft delete group: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("soft delete group commit: %w", err)
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
