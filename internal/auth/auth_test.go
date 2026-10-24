package auth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"leafygo/internal/domain"
	"leafygo/internal/store"
)

type fakeStore struct {
	mu       sync.Mutex
	sessions map[string]domain.Session
}

func newFakeStore() *fakeStore {
	return &fakeStore{sessions: make(map[string]domain.Session)}
}

func key(b []byte) string { return string(b) }

func (f *fakeStore) CreateSession(_ context.Context, sess domain.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[key(sess.IDHash)] = sess
	return nil
}

func (f *fakeStore) GetSession(_ context.Context, idHash []byte) (domain.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[key(idHash)]
	if !ok {
		return domain.Session{}, store.ErrNotFound
	}
	return s, nil
}

func (f *fakeStore) TouchSession(_ context.Context, idHash []byte, lastSeen, idleExpires time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[key(idHash)]
	s.LastSeenAt = lastSeen
	s.IdleExpiresAt = idleExpires
	f.sessions[key(idHash)] = s
	return nil
}

func (f *fakeStore) RotateSession(_ context.Context, oldHash []byte, next domain.Session, graceExpiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	old := f.sessions[key(oldHash)]
	old.RotatedTo = next.IDHash
	old.GraceExpiresAt = &graceExpiresAt
	f.sessions[key(oldHash)] = old
	f.sessions[key(next.IDHash)] = next
	return nil
}

func (f *fakeStore) RevokeSession(_ context.Context, idHash []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.sessions[key(idHash)]; ok {
		now := time.Now().UTC()
		s.RevokedAt = &now
		f.sessions[key(idHash)] = s
	}
	return nil
}

func TestHashAndVerifyRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct-horse-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, err := VerifyPassword(hash, "correct-horse-123")
	if err != nil || !ok {
		t.Fatalf("VerifyPassword = %v, %v; want true, nil", ok, err)
	}
	ok, err = VerifyPassword(hash, "wrong")
	if err != nil {
		t.Fatalf("VerifyPassword wrong: %v", err)
	}
	if ok {
		t.Fatal("wrong password verified")
	}
}

func TestHashPasswordRejectsEmpty(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Fatal("expected error for empty password")
	}
}

func TestVerifyPasswordRejectsGarbage(t *testing.T) {
	if _, err := VerifyPassword("not-a-hash", "x"); err == nil {
		t.Fatal("expected error for garbage hash")
	}
}

func TestSessionResolveAndRotateWithGrace(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStore()
	now := time.Now().UTC()
	m := NewManager(fs, 30*time.Minute, 8*time.Hour, 15*time.Minute, 10*time.Second)
	m.now = func() time.Time { return now }

	raw, _, err := m.Create(ctx, uuid.New())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, newRaw, err := m.Resolve(ctx, raw); err != nil || newRaw != "" {
		t.Fatalf("Resolve = %q, %v; want no rotation", newRaw, err)
	}

	now = now.Add(16 * time.Minute)
	m.now = func() time.Time { return now }
	_, newRaw, err := m.Resolve(ctx, raw)
	if err != nil || newRaw == "" {
		t.Fatalf("Resolve after rotate interval = %q, %v; want new identifier", newRaw, err)
	}

	if _, _, err := m.Resolve(ctx, raw); err != nil {
		t.Fatalf("old identifier inside grace: %v", err)
	}

	now = now.Add(11 * time.Second)
	m.now = func() time.Time { return now }
	if _, _, err := m.Resolve(ctx, raw); err == nil {
		t.Fatal("expected old identifier to be invalid after grace")
	}
	if _, _, err := m.Resolve(ctx, newRaw); err != nil {
		t.Fatalf("replacement identifier: %v", err)
	}
}

func TestSessionIdleAndAbsoluteExpiry(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	idle := NewManager(newFakeStore(), 30*time.Minute, 8*time.Hour, time.Hour, 10*time.Second)
	idle.now = func() time.Time { return now }
	raw, _, err := idle.Create(ctx, uuid.New())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	now = now.Add(31 * time.Minute)
	idle.now = func() time.Time { return now }
	if _, _, err := idle.Resolve(ctx, raw); err == nil {
		t.Fatal("expected idle expiry")
	}

	now2 := time.Now().UTC()
	abs := NewManager(newFakeStore(), 30*time.Minute, 8*time.Hour, time.Hour, 10*time.Second)
	abs.now = func() time.Time { return now2 }
	raw2, _, err := abs.Create(ctx, uuid.New())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i := 0; i < 23; i++ {
		now2 = now2.Add(20 * time.Minute)
		_, newRaw, err := abs.Resolve(ctx, raw2)
		if err != nil {
			t.Fatalf("touch %d/23: %v", i+1, err)
		}
		if newRaw != "" {
			raw2 = newRaw
		}
	}
	now2 = now2.Add(20 * time.Minute)
	if _, _, err := abs.Resolve(ctx, raw2); err == nil {
		t.Fatal("expected absolute expiry")
	}
}

func TestSessionRevokedIsInvalid(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStore()
	m := NewManager(fs, 30*time.Minute, 8*time.Hour, time.Hour, 10*time.Second)
	raw, _, err := m.Create(ctx, uuid.New())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Revoke(ctx, raw); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, _, err := m.Resolve(ctx, raw); err == nil {
		t.Fatal("expected revoked session to be invalid")
	}
}
