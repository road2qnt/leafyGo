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

func (s *Store) CreateSession(ctx context.Context, sess domain.Session) error {
	_, err := s.pool.Exec(ctx, `
		insert into sessions (id_hash, user_id, csrf_token, created_at, last_seen_at,
		                      idle_expires_at, absolute_expires_at, rotated_to, grace_expires_at, revoked_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		sess.IDHash, sess.UserID, sess.CSRFToken, sess.CreatedAt, sess.LastSeenAt,
		sess.IdleExpiresAt, sess.AbsoluteExpiresAt, sess.RotatedTo, sess.GraceExpiresAt, sess.RevokedAt)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Store) GetSession(ctx context.Context, idHash []byte) (domain.Session, error) {
	var sess domain.Session
	err := s.pool.QueryRow(ctx, `
		select id_hash, user_id, csrf_token, created_at, last_seen_at, idle_expires_at,
		       absolute_expires_at, rotated_to, grace_expires_at, revoked_at, force_rotate
		from sessions where id_hash=$1`, idHash).
		Scan(&sess.IDHash, &sess.UserID, &sess.CSRFToken, &sess.CreatedAt, &sess.LastSeenAt,
			&sess.IdleExpiresAt, &sess.AbsoluteExpiresAt, &sess.RotatedTo, &sess.GraceExpiresAt, &sess.RevokedAt, &sess.ForceRotate)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Session{}, ErrNotFound
	}
	if err != nil {
		return domain.Session{}, fmt.Errorf("get session: %w", err)
	}
	return sess, nil
}

func (s *Store) TouchSession(ctx context.Context, idHash []byte, lastSeen, idleExpires time.Time) error {
	_, err := s.pool.Exec(ctx, `
		update sessions set last_seen_at=$2, idle_expires_at=$3 where id_hash=$1`,
		idHash, lastSeen, idleExpires)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

func (s *Store) RotateSession(ctx context.Context, oldHash []byte, next domain.Session, graceExpiresAt time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("rotate session begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		update sessions set rotated_to=$2, grace_expires_at=$3 where id_hash=$1`,
		oldHash, next.IDHash, graceExpiresAt)
	if err != nil {
		return fmt.Errorf("rotate session link: %w", err)
	}
	_, err = tx.Exec(ctx, `
		insert into sessions (id_hash, user_id, csrf_token, created_at, last_seen_at,
		                      idle_expires_at, absolute_expires_at, rotated_to, grace_expires_at, revoked_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		next.IDHash, next.UserID, next.CSRFToken, next.CreatedAt, next.LastSeenAt,
		next.IdleExpiresAt, next.AbsoluteExpiresAt, next.RotatedTo, next.GraceExpiresAt, next.RevokedAt)
	if err != nil {
		return fmt.Errorf("rotate session insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("rotate session commit: %w", err)
	}
	return nil
}

func (s *Store) RevokeSession(ctx context.Context, idHash []byte) error {
	_, err := s.pool.Exec(ctx, `update sessions set revoked_at=now() where id_hash=$1`, idHash)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func (s *Store) RevokeUserSessions(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `update sessions set revoked_at=now() where user_id=$1 and revoked_at is null`, userID)
	if err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return nil
}

func (s *Store) MarkUserSessionsForRotation(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		update sessions set force_rotate=true
		where user_id=$1 and revoked_at is null and rotated_to is null`, userID)
	if err != nil {
		return fmt.Errorf("mark user sessions for rotation: %w", err)
	}
	return nil
}

func (s *Store) MarkGroupMembersSessionsForRotation(ctx context.Context, groupID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		update sessions set force_rotate=true
		where revoked_at is null and rotated_to is null
		  and user_id in (select user_id from group_members where group_id=$1)`, groupID)
	if err != nil {
		return fmt.Errorf("mark group member sessions for rotation: %w", err)
	}
	return nil
}

func (s *Store) RevokeGroupMembersSessions(ctx context.Context, groupID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		update sessions set revoked_at=now()
		where revoked_at is null
		  and user_id in (select user_id from group_members where group_id=$1)`, groupID)
	if err != nil {
		return fmt.Errorf("revoke group member sessions: %w", err)
	}
	return nil
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		delete from sessions
		where absolute_expires_at < $1
		   or (revoked_at is not null and revoked_at < $1 - interval '1 hour')`, now)
	if err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}
