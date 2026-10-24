package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"leafygo/internal/domain"
	"leafygo/internal/store"
)

var (
	ErrSessionInvalid = errors.New("session invalid")
	ErrSessionExpired = errors.New("session expired")
)

type SessionStore interface {
	CreateSession(ctx context.Context, sess domain.Session) error
	GetSession(ctx context.Context, idHash []byte) (domain.Session, error)
	TouchSession(ctx context.Context, idHash []byte, lastSeen, idleExpires time.Time) error
	RotateSession(ctx context.Context, oldHash []byte, next domain.Session, graceExpiresAt time.Time) error
	RevokeSession(ctx context.Context, idHash []byte) error
}

type Manager struct {
	store    SessionStore
	idle     time.Duration
	absolute time.Duration
	rotate   time.Duration
	grace    time.Duration
	now      func() time.Time
}

func NewManager(store SessionStore, idle, absolute, rotate, grace time.Duration) *Manager {
	return &Manager{store: store, idle: idle, absolute: absolute, rotate: rotate, grace: grace, now: time.Now}
}

func (m *Manager) CookieTTL() time.Duration { return m.absolute }

func (m *Manager) Create(ctx context.Context, userID uuid.UUID) (string, domain.Session, error) {
	raw, err := randomToken()
	if err != nil {
		return "", domain.Session{}, err
	}
	csrf, err := randomToken()
	if err != nil {
		return "", domain.Session{}, err
	}
	now := m.now().UTC()
	sess := domain.Session{
		IDHash:            HashToken(raw),
		UserID:            userID,
		CSRFToken:         csrf,
		CreatedAt:         now,
		LastSeenAt:        now,
		IdleExpiresAt:     minTime(now.Add(m.idle), now.Add(m.absolute)),
		AbsoluteExpiresAt: now.Add(m.absolute),
	}
	if err := m.store.CreateSession(ctx, sess); err != nil {
		return "", domain.Session{}, err
	}
	return raw, sess, nil
}

func (m *Manager) Resolve(ctx context.Context, raw string) (domain.Session, string, error) {
	if raw == "" {
		return domain.Session{}, "", ErrSessionInvalid
	}
	hash := HashToken(raw)
	now := m.now().UTC()

	for hop := 0; hop < 4; hop++ {
		sess, err := m.store.GetSession(ctx, hash)
		if errors.Is(err, store.ErrNotFound) {
			return domain.Session{}, "", ErrSessionInvalid
		}
		if err != nil {
			return domain.Session{}, "", fmt.Errorf("resolve session: %w", err)
		}
		if sess.RevokedAt != nil {
			return domain.Session{}, "", ErrSessionInvalid
		}
		if !now.Before(sess.AbsoluteExpiresAt) || !now.Before(sess.IdleExpiresAt) {
			_ = m.store.RevokeSession(ctx, hash)
			return domain.Session{}, "", ErrSessionExpired
		}
		if sess.RotatedTo != nil {
			if sess.GraceExpiresAt != nil && now.Before(*sess.GraceExpiresAt) {
				hash = sess.RotatedTo
				continue
			}
			return domain.Session{}, "", ErrSessionInvalid
		}

		if now.Sub(sess.CreatedAt) >= m.rotate {
			newRaw, err := randomToken()
			if err != nil {
				return domain.Session{}, "", err
			}
			next := domain.Session{
				IDHash:            HashToken(newRaw),
				UserID:            sess.UserID,
				CSRFToken:         sess.CSRFToken,
				CreatedAt:         now,
				LastSeenAt:        now,
				IdleExpiresAt:     minTime(now.Add(m.idle), sess.AbsoluteExpiresAt),
				AbsoluteExpiresAt: sess.AbsoluteExpiresAt,
			}
			if err := m.store.RotateSession(ctx, hash, next, now.Add(m.grace)); err != nil {
				return domain.Session{}, "", err
			}
			return next, newRaw, nil
		}

		if now.Sub(sess.LastSeenAt) >= time.Minute {
			idleExpires := minTime(now.Add(m.idle), sess.AbsoluteExpiresAt)
			if err := m.store.TouchSession(ctx, hash, now, idleExpires); err != nil {
				return domain.Session{}, "", err
			}
			sess.LastSeenAt = now
			sess.IdleExpiresAt = idleExpires
		}
		return sess, "", nil
	}
	return domain.Session{}, "", ErrSessionInvalid
}

func (m *Manager) Revoke(ctx context.Context, raw string) error {
	if raw == "" {
		return nil
	}
	return m.store.RevokeSession(ctx, HashToken(raw))
}

func HashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", fmt.Errorf("random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
