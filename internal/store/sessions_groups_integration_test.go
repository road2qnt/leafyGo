package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"leafygo/internal/auth"
	"leafygo/internal/domain"
	"leafygo/internal/store"
)

func TestSessionTouchExtendsIdleOnly(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	u := createUser(t, st, "u", false)
	hash := createSession(t, st, u.ID)
	before, _ := st.GetSession(ctx, hash)

	seen := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Microsecond)
	idle := seen.Add(time.Hour)
	if err := st.TouchSession(ctx, hash, seen, idle); err != nil {
		t.Fatalf("touch: %v", err)
	}
	after, _ := st.GetSession(ctx, hash)
	if !after.IdleExpiresAt.Equal(idle) || !after.LastSeenAt.Equal(seen) {
		t.Fatalf("touch not applied: %+v", after)
	}
	if !after.AbsoluteExpiresAt.Equal(before.AbsoluteExpiresAt) {
		t.Fatal("touch must never extend the absolute expiration")
	}
}

func TestRotateSessionLinksOldToNewAndKeepsAbsolute(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	u := createUser(t, st, "u", false)
	oldHash := createSession(t, st, u.ID)
	old, _ := st.GetSession(ctx, oldHash)

	next := domain.Session{
		IDHash: auth.HashToken("next-" + uuid.NewString()), UserID: u.ID, CSRFToken: "c2",
		CreatedAt: old.CreatedAt, LastSeenAt: time.Now().UTC(), IdleExpiresAt: time.Now().UTC().Add(time.Hour),
		AbsoluteExpiresAt: old.AbsoluteExpiresAt,
	}
	grace := time.Now().UTC().Add(10 * time.Second).Truncate(time.Microsecond)
	if err := st.RotateSession(ctx, oldHash, next, grace); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	oldAfter, _ := st.GetSession(ctx, oldHash)
	if string(oldAfter.RotatedTo) != string(next.IDHash) || oldAfter.GraceExpiresAt == nil || !oldAfter.GraceExpiresAt.Equal(grace) {
		t.Fatalf("old session not linked to its successor: %+v", oldAfter)
	}
	got, err := st.GetSession(ctx, next.IDHash)
	if err != nil || !got.AbsoluteExpiresAt.Equal(old.AbsoluteExpiresAt) {
		t.Fatalf("successor = %+v, %v; absolute expiry must carry over", got, err)
	}
}

func TestRotateSessionIsAtomic(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	u := createUser(t, st, "u", false)
	oldHash := createSession(t, st, u.ID)
	dup := createSession(t, st, u.ID)
	existing, _ := st.GetSession(ctx, dup)

	err := st.RotateSession(ctx, oldHash, existing, time.Now().UTC().Add(time.Minute))
	if err == nil {
		t.Fatal("rotating onto an existing identifier must fail")
	}
	old, _ := st.GetSession(ctx, oldHash)
	if old.RotatedTo != nil || old.GraceExpiresAt != nil {
		t.Fatalf("failed rotation left the old session half-linked: %+v", old)
	}
}

func TestRevokeSessionAndUserSessions(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	a := createUser(t, st, "a", false)
	b := createUser(t, st, "b", false)
	a1, a2, b1 := createSession(t, st, a.ID), createSession(t, st, a.ID), createSession(t, st, b.ID)

	if err := st.RevokeSession(ctx, a1); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	revoked := func(h []byte) bool { s, _ := st.GetSession(ctx, h); return s.RevokedAt != nil }
	if !revoked(a1) || revoked(a2) || revoked(b1) {
		t.Fatalf("RevokeSession touched the wrong rows: a1=%v a2=%v b1=%v", revoked(a1), revoked(a2), revoked(b1))
	}
	if err := st.RevokeSession(ctx, []byte("unknown")); err != nil {
		t.Fatalf("revoking an unknown session should be a no-op: %v", err)
	}

	first, _ := st.GetSession(ctx, a1)
	if err := st.RevokeUserSessions(ctx, a.ID); err != nil {
		t.Fatalf("revoke user: %v", err)
	}
	if !revoked(a2) || revoked(b1) {
		t.Fatal("RevokeUserSessions must revoke all of the user's sessions and no one else's")
	}
	again, _ := st.GetSession(ctx, a1)
	if !again.RevokedAt.Equal(*first.RevokedAt) {
		t.Fatal("an already revoked session must keep its original revocation time")
	}
}

func TestRevokeGroupMembersSessions(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	member := createUser(t, st, "member", false)
	outsider := createUser(t, st, "outsider", false)
	g, _ := st.CreateGroup(ctx, "g", "")
	_ = st.AddGroupMember(ctx, g.ID, member.ID)
	m, o := createSession(t, st, member.ID), createSession(t, st, outsider.ID)

	if err := st.RevokeGroupMembersSessions(ctx, g.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	ms, _ := st.GetSession(ctx, m)
	os, _ := st.GetSession(ctx, o)
	if ms.RevokedAt == nil || os.RevokedAt != nil {
		t.Fatalf("member revoked=%v outsider revoked=%v", ms.RevokedAt != nil, os.RevokedAt != nil)
	}
}

func TestMarkGroupMembersSessionsForRotation(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	member := createUser(t, st, "member", false)
	outsider := createUser(t, st, "outsider", false)
	g, _ := st.CreateGroup(ctx, "g", "")
	_ = st.AddGroupMember(ctx, g.ID, member.ID)
	m, o := createSession(t, st, member.ID), createSession(t, st, outsider.ID)

	if err := st.MarkGroupMembersSessionsForRotation(ctx, g.ID); err != nil {
		t.Fatalf("mark: %v", err)
	}
	ms, _ := st.GetSession(ctx, m)
	os, _ := st.GetSession(ctx, o)
	if !ms.ForceRotate || os.ForceRotate {
		t.Fatalf("member force=%v outsider force=%v", ms.ForceRotate, os.ForceRotate)
	}
}

func TestDeleteExpiredSessions(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	u := createUser(t, st, "u", false)
	now := time.Now().UTC()
	mk := func(name string, absolute time.Time, revoked *time.Time) []byte {
		h := auth.HashToken(name)
		err := st.CreateSession(ctx, domain.Session{
			IDHash: h, UserID: u.ID, CSRFToken: "c", CreatedAt: now.Add(-3 * time.Hour), LastSeenAt: now,
			IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: absolute, RevokedAt: revoked,
		})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return h
	}
	longAgo, recent := now.Add(-3*time.Hour), now.Add(-time.Minute)
	live := mk("live", now.Add(time.Hour), nil)
	expired := mk("expired", now.Add(-time.Minute), nil)
	oldRevoked := mk("old-revoked", now.Add(time.Hour), &longAgo)
	freshRevoked := mk("fresh-revoked", now.Add(time.Hour), &recent)

	if err := st.DeleteExpiredSessions(ctx, now); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	exists := func(h []byte) bool { _, err := st.GetSession(ctx, h); return err == nil }
	if !exists(live) || !exists(freshRevoked) {
		t.Fatal("cleanup removed a session that is still needed")
	}
	if exists(expired) || exists(oldRevoked) {
		t.Fatal("cleanup kept an expired or long-revoked session")
	}
}

func TestGroupCRUD(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	g, err := st.CreateGroup(ctx, "backend", "core")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	other, _ := st.CreateGroup(ctx, "frontend", "")

	byID, err := st.GetGroup(ctx, g.ID)
	if err != nil || byID.Name != "backend" || byID.Description != "core" {
		t.Fatalf("GetGroup = %+v, %v", byID, err)
	}
	byName, err := st.GetGroupByName(ctx, "backend")
	if err != nil || byName.ID != g.ID {
		t.Fatalf("GetGroupByName = %+v, %v", byName, err)
	}
	if _, err := st.GetGroup(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown group error = %v; want ErrNotFound", err)
	}
	if _, err := st.GetGroupByName(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown group name error = %v; want ErrNotFound", err)
	}

	updated, err := st.UpdateGroup(ctx, g.ID, "platform", "renamed")
	if err != nil || updated.Name != "platform" || updated.Description != "renamed" {
		t.Fatalf("UpdateGroup = %+v, %v", updated, err)
	}
	if _, err := st.UpdateGroup(ctx, g.ID, other.Name, ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("rename onto existing error = %v; want ErrConflict", err)
	}
	if _, err := st.UpdateGroup(ctx, uuid.New(), "x", ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update unknown error = %v; want ErrNotFound", err)
	}

	all, err := st.ListGroups(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("ListGroups = %+v, %v", all, err)
	}
}

func TestRemoveMemberAndAccessChangeEffectiveLevel(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	u := createUser(t, st, "u", false)
	conn := createDB(t, st, "demo")
	view, _ := st.CreateGroup(ctx, "viewers", "")
	edit, _ := st.CreateGroup(ctx, "editors", "")
	for _, g := range []domain.Group{view, edit} {
		if err := st.AddGroupMember(ctx, g.ID, u.ID); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}
	_ = st.SetGroupAccess(ctx, view.ID, conn.ID, domain.LevelView)
	_ = st.SetGroupAccess(ctx, edit.ID, conn.ID, domain.LevelEdit)

	level := func() domain.AccessLevel { l, _ := st.EffectiveLevel(ctx, u.ID, conn.ID); return l }
	if level() != domain.LevelEdit {
		t.Fatalf("level = %v; want edit (highest wins)", level())
	}
	if err := st.RemoveGroupAccess(ctx, edit.ID, conn.ID); err != nil {
		t.Fatalf("remove access: %v", err)
	}
	if level() != domain.LevelView {
		t.Fatalf("level after dropping edit grant = %v; want view", level())
	}
	if err := st.RemoveGroupMember(ctx, view.ID, u.ID); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	if level() != domain.LevelNone {
		t.Fatalf("level after leaving every granting group = %v; want none", level())
	}
	if err := st.SetGroupAccess(ctx, edit.ID, conn.ID, domain.LevelNone); err != nil {
		t.Fatalf("setting level none should clear the grant: %v", err)
	}
	if grants, _ := st.ListGroupAccess(ctx, edit.ID); len(grants) != 0 {
		t.Fatalf("grants after none = %+v", grants)
	}
}

func TestUserListingAndPasswordUpdate(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	createUser(t, st, "Zed", false)
	u := createUser(t, st, "alice", false)

	users, err := st.ListUsers(ctx)
	if err != nil || len(users) != 2 || users[0].Username != "alice" {
		t.Fatalf("ListUsers = %+v, %v; want case-insensitive username order", users, err)
	}

	hash, _ := auth.HashPassword("another-pass-9")
	if err := st.UpdateUserPassword(ctx, u.ID, hash); err != nil {
		t.Fatalf("update password: %v", err)
	}
	got, _ := st.GetUserByID(ctx, u.ID)
	if ok, _ := auth.VerifyPassword(got.PasswordHash, "another-pass-9"); !ok {
		t.Fatal("new password does not verify")
	}
	if err := st.UpdateUserPassword(ctx, uuid.New(), hash); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown user error = %v; want ErrNotFound", err)
	}
}

func TestListDBConnectionsAndGet(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	createDB(t, st, "b-second")
	first := createDB(t, st, "a-first")

	conns, err := st.ListDBConnections(ctx)
	if err != nil || len(conns) != 2 {
		t.Fatalf("ListDBConnections = %+v, %v", conns, err)
	}
	got, err := st.GetDBConnection(ctx, first.ID)
	if err != nil || got.Name != "a-first" || string(got.PasswordEnc) != "enc" {
		t.Fatalf("GetDBConnection = %+v, %v", got, err)
	}
	if _, err := st.GetDBConnection(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown connection error = %v; want ErrNotFound", err)
	}
}
