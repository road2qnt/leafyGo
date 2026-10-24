package store_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"leafygo/internal/auth"
	"leafygo/internal/domain"
	"leafygo/internal/platform"
	"leafygo/internal/seed"
	"leafygo/internal/store"
)

func testPool(t *testing.T) *store.Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is empty; skipping integration test")
	}
	ctx := context.Background()
	if err := platform.Migrate(ctx, url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	st := store.New(pool)
	if _, err := pool.Exec(ctx, `
		truncate users, groups, group_members, db_connections, db_access, sessions,
		         migration_history, migration_drafts`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return st
}

func createUser(t *testing.T, st *store.Store, username string, admin bool) domain.User {
	t.Helper()
	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := st.CreateUser(context.Background(), username, hash, admin)
	if err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return u
}

func createDB(t *testing.T, st *store.Store, name string) domain.DBConnection {
	t.Helper()
	c, err := st.CreateDBConnection(context.Background(), domain.DBConnection{
		Name: name, Host: "localhost", Port: 5432, DBName: "demo",
		Username: "u", PasswordEnc: []byte("enc"), SSLMode: "disable",
	})
	if err != nil {
		t.Fatalf("create db %s: %v", name, err)
	}
	return c
}

func insertRunning(t *testing.T, st *store.Store, dbID uuid.UUID, version int64, executedBy *uuid.UUID, started time.Time) domain.MigrationHistory {
	t.Helper()
	h := domain.MigrationHistory{
		ID:         uuid.New(),
		DBID:       dbID,
		Version:    version,
		SQLText:    "create table t(id int)",
		ExecutedBy: executedBy,
		StartedAt:  started,
		Status:     domain.MigrationRunning,
	}
	if err := st.InsertRunningMigration(context.Background(), h); err != nil {
		t.Fatalf("insert running migration: %v", err)
	}
	return h
}

func createSession(t *testing.T, st *store.Store, userID uuid.UUID) []byte {
	t.Helper()
	now := time.Now().UTC()
	hash := auth.HashToken("session-" + uuid.NewString())
	err := st.CreateSession(context.Background(), domain.Session{
		IDHash:            hash,
		UserID:            userID,
		CSRFToken:         "csrf-" + uuid.NewString(),
		CreatedAt:         now,
		LastSeenAt:        now,
		IdleExpiresAt:     now.Add(time.Hour),
		AbsoluteExpiresAt: now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return hash
}

func TestEffectiveLevelHighestWins(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()

	user := createUser(t, st, "ega", false)
	admin := createUser(t, st, "admin", true)

	g1, err := st.CreateGroup(ctx, "g-view", "")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	g2, err := st.CreateGroup(ctx, "g-edit", "")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	conn := createDB(t, st, "demo")

	if lvl, err := st.EffectiveLevel(ctx, user.ID, conn.ID); err != nil || lvl != domain.LevelNone {
		t.Fatalf("no-group level = %v, %v; want none", lvl, err)
	}
	if err := st.AddGroupMember(ctx, g1.ID, user.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := st.SetGroupAccess(ctx, g1.ID, conn.ID, domain.LevelView); err != nil {
		t.Fatalf("set access: %v", err)
	}
	if lvl, _ := st.EffectiveLevel(ctx, user.ID, conn.ID); lvl != domain.LevelView {
		t.Fatalf("level = %v; want view", lvl)
	}
	if err := st.AddGroupMember(ctx, g2.ID, user.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := st.SetGroupAccess(ctx, g2.ID, conn.ID, domain.LevelEdit); err != nil {
		t.Fatalf("set access: %v", err)
	}
	if lvl, _ := st.EffectiveLevel(ctx, user.ID, conn.ID); lvl != domain.LevelEdit {
		t.Fatalf("level = %v; want edit (highest wins)", lvl)
	}

	if lvl, _ := st.EffectiveLevel(ctx, admin.ID, conn.ID); lvl != domain.LevelNone {
		t.Fatalf("admin level = %v; want none", lvl)
	}

	dbs, err := st.ListAccessibleDBs(ctx, user.ID)
	if err != nil {
		t.Fatalf("list accessible: %v", err)
	}
	if len(dbs) != 1 || dbs[0].Level != domain.LevelEdit {
		t.Fatalf("accessible = %+v; want 1 db at edit", dbs)
	}
	dbs, err = st.ListAccessibleDBs(ctx, admin.ID)
	if err != nil {
		t.Fatalf("list accessible admin: %v", err)
	}
	if len(dbs) != 0 {
		t.Fatalf("admin accessible = %d; want 0", len(dbs))
	}
}

func TestUserActiveRoundTrip(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	actor := createUser(t, st, "actor", false)
	target := createUser(t, st, "target", false)

	got, err := st.GetUserByUsername(ctx, "target")
	if err != nil || !got.IsActive {
		t.Fatalf("new user active = %v, %v; want true", got.IsActive, err)
	}
	if err := st.SetUserActiveGuarded(ctx, target.ID, false, actor.ID); err != nil {
		t.Fatalf("disable: %v", err)
	}
	got, err = st.GetUserByUsername(ctx, "target")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if got.IsActive {
		t.Fatal("user should be inactive")
	}
}

func TestSetUserActiveGuardedRejectsSelfDisable(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	admin := createUser(t, st, "admin", true)

	err := st.SetUserActiveGuarded(ctx, admin.ID, false, admin.ID)
	if !errors.Is(err, store.ErrSelfMutation) {
		t.Fatalf("self disable = %v; want ErrSelfMutation", err)
	}
	got, _ := st.GetUserByID(ctx, admin.ID)
	if !got.IsActive {
		t.Fatal("admin must remain active")
	}
}

func TestSetUserActiveGuardedAllowsDisableAdminWithAnotherActiveAdmin(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	a := createUser(t, st, "admin-a", true)
	b := createUser(t, st, "admin-b", true)

	if err := st.SetUserActiveGuarded(ctx, b.ID, false, a.ID); err != nil {
		t.Fatalf("disable second admin: %v", err)
	}
	got, _ := st.GetUserByID(ctx, b.ID)
	if got.IsActive {
		t.Fatal("target admin should be inactive")
	}
	count, err := st.CountActivePlatformAdmins(ctx)
	if err != nil || count != 1 {
		t.Fatalf("active admins = %d, %v; want 1", count, err)
	}
}

func TestLastAdminGuard(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	a := createUser(t, st, "admin-a", true)
	b := createUser(t, st, "admin-b", true)
	actor := createUser(t, st, "actor", false)

	if err := st.SetUserActiveGuarded(ctx, b.ID, false, actor.ID); err != nil {
		t.Fatalf("disable one admin: %v", err)
	}
	if err := st.SetUserActiveGuarded(ctx, a.ID, false, actor.ID); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("disable last admin = %v; want ErrLastAdmin", err)
	}
	got, _ := st.GetUserByID(ctx, a.ID)
	if !got.IsActive {
		t.Fatal("last admin must remain active")
	}
}

func TestLastAdminGuardConcurrent(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	a := createUser(t, st, "admin-a", true)
	b := createUser(t, st, "admin-b", true)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errs <- st.SetUserActiveGuarded(ctx, b.ID, false, a.ID)
	}()
	go func() {
		defer wg.Done()
		<-start
		errs <- st.SetUserActiveGuarded(ctx, a.ID, false, b.ID)
	}()
	close(start)
	wg.Wait()
	close(errs)

	success := 0
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, store.ErrLastAdmin):
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("successful concurrent disables = %d; want 1", success)
	}
	count, err := st.CountActivePlatformAdmins(ctx)
	if err != nil || count < 1 {
		t.Fatalf("active admins after race = %d, %v; want >= 1", count, err)
	}
}

func TestAddGroupMemberRejectsPlatformAdmin(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	admin := createUser(t, st, "admin", true)
	group, err := st.CreateGroup(ctx, "group", "")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := st.AddGroupMember(ctx, group.ID, admin.ID); !errors.Is(err, store.ErrAdminGroupMember) {
		t.Fatalf("add admin = %v; want ErrAdminGroupMember", err)
	}
}

func TestDeleteGroupCascadesMembershipAndAccessAndReturnsMembers(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	u1 := createUser(t, st, "u1", false)
	u2 := createUser(t, st, "u2", false)
	group, err := st.CreateGroup(ctx, "group", "")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := st.AddGroupMember(ctx, group.ID, u1.ID); err != nil {
		t.Fatalf("add u1: %v", err)
	}
	if err := st.AddGroupMember(ctx, group.ID, u2.ID); err != nil {
		t.Fatalf("add u2: %v", err)
	}
	conn := createDB(t, st, "demo")
	if err := st.SetGroupAccess(ctx, group.ID, conn.ID, domain.LevelEdit); err != nil {
		t.Fatalf("set access: %v", err)
	}

	members, err := st.DeleteGroup(ctx, group.ID)
	if err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("returned members = %d; want 2", len(members))
	}
	if got, err := st.ListGroupMembers(ctx, group.ID); err != nil || len(got) != 0 {
		t.Fatalf("members after delete = %d, %v; want 0", len(got), err)
	}
	if got, err := st.ListGroupAccess(ctx, group.ID); err != nil || len(got) != 0 {
		t.Fatalf("access after delete = %d, %v; want 0", len(got), err)
	}
	if dbs, err := st.ListAccessibleDBs(ctx, u1.ID); err != nil || len(dbs) != 0 {
		t.Fatalf("u1 accessible after delete = %d, %v; want 0", len(dbs), err)
	}
}

func TestListGroupAccess(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	group, err := st.CreateGroup(ctx, "group", "")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	alpha := createDB(t, st, "alpha")
	beta := createDB(t, st, "beta")
	if err := st.SetGroupAccess(ctx, group.ID, beta.ID, domain.LevelEdit); err != nil {
		t.Fatalf("set beta: %v", err)
	}
	if err := st.SetGroupAccess(ctx, group.ID, alpha.ID, domain.LevelView); err != nil {
		t.Fatalf("set alpha: %v", err)
	}

	access, err := st.ListGroupAccess(ctx, group.ID)
	if err != nil {
		t.Fatalf("list access: %v", err)
	}
	if len(access) != 2 {
		t.Fatalf("access len = %d; want 2", len(access))
	}
	if access[0].DBName != "alpha" || access[0].Level != domain.LevelView {
		t.Fatalf("first access = %+v", access[0])
	}
	if access[1].DBName != "beta" || access[1].Level != domain.LevelEdit {
		t.Fatalf("second access = %+v", access[1])
	}
}

func TestRevokeGroupMembersSessionsExcept(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	m1 := createUser(t, st, "m1", false)
	m2 := createUser(t, st, "m2", false)
	actor := createUser(t, st, "actor", false)
	group, err := st.CreateGroup(ctx, "group", "")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := st.AddGroupMember(ctx, group.ID, m1.ID); err != nil {
		t.Fatalf("add m1: %v", err)
	}
	if err := st.AddGroupMember(ctx, group.ID, m2.ID); err != nil {
		t.Fatalf("add m2: %v", err)
	}
	h1 := createSession(t, st, m1.ID)
	h2 := createSession(t, st, m2.ID)
	hActor := createSession(t, st, actor.ID)

	if err := st.RevokeGroupMembersSessionsExcept(ctx, group.ID, actor.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	for _, hash := range [][]byte{h1, h2} {
		sess, err := st.GetSession(ctx, hash)
		if err != nil {
			t.Fatalf("get session: %v", err)
		}
		if sess.RevokedAt == nil {
			t.Fatal("member session should be revoked")
		}
	}
	sess, err := st.GetSession(ctx, hActor)
	if err != nil {
		t.Fatalf("get actor session: %v", err)
	}
	if sess.RevokedAt != nil {
		t.Fatal("actor session must remain active")
	}
}

func TestCreateGroupDuplicate(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	if _, err := st.CreateGroup(ctx, "Group", ""); err != nil {
		t.Fatalf("create group: %v", err)
	}
	if _, err := st.CreateGroup(ctx, "group", ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate group = %v; want ErrConflict", err)
	}
}

func TestCreateUserDuplicate(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	hash, _ := auth.HashPassword("password123")
	if _, err := st.CreateUser(ctx, "Ega", hash, false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := st.CreateUser(ctx, "ega", hash, false); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate user = %v; want ErrConflict", err)
	}
}

func TestEnsureAdminKeepsBootstrapAdmin(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	u := createUser(t, st, "bootstrap", false)

	if err := seed.EnsureAdmin(ctx, st, "bootstrap", "ignored-password"); err != nil {
		t.Fatalf("ensure admin: %v", err)
	}
	got, err := st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if !got.IsPlatformAdmin {
		t.Fatal("existing bootstrap user should be promoted")
	}

	if err := seed.EnsureAdmin(ctx, st, "fresh", "password123"); err != nil {
		t.Fatalf("ensure fresh admin: %v", err)
	}
	fresh, err := st.GetUserByUsername(ctx, "fresh")
	if err != nil {
		t.Fatalf("get fresh admin: %v", err)
	}
	if !fresh.IsPlatformAdmin {
		t.Fatal("fresh bootstrap user should be admin")
	}
}

func TestUpdateDBConnectionPreservesCreatedFieldsAndConflicts(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	creator := createUser(t, st, "creator", false)
	orig, err := st.CreateDBConnection(ctx, domain.DBConnection{
		Name: "one", Host: "localhost", Port: 5432, DBName: "demo",
		Username: "u", PasswordEnc: []byte("old"), SSLMode: "disable",
		CreatedBy: &creator.ID,
	})
	if err != nil {
		t.Fatalf("create db: %v", err)
	}
	time.Sleep(time.Millisecond)
	updated, err := st.UpdateDBConnection(ctx, domain.DBConnection{
		ID: orig.ID, Name: "one-renamed", Host: "db2", Port: 5433, DBName: "demo2",
		Username: "u2", PasswordEnc: []byte("new"), SSLMode: "require",
	})
	if err != nil {
		t.Fatalf("update db: %v", err)
	}
	if updated.CreatedAt.UnixMicro() != orig.CreatedAt.UnixMicro() {
		t.Fatalf("CreatedAt changed: %v -> %v", orig.CreatedAt, updated.CreatedAt)
	}
	if updated.CreatedBy == nil || *updated.CreatedBy != creator.ID {
		t.Fatalf("CreatedBy = %v; want %s", updated.CreatedBy, creator.ID)
	}
	if !updated.UpdatedAt.After(orig.UpdatedAt) {
		t.Fatalf("UpdatedAt did not advance: %v -> %v", orig.UpdatedAt, updated.UpdatedAt)
	}
	if updated.Name != "one-renamed" || updated.Host != "db2" || string(updated.PasswordEnc) != "new" {
		t.Fatalf("updated row = %+v", updated)
	}

	createDB(t, st, "two")
	_, err = st.UpdateDBConnection(ctx, domain.DBConnection{
		ID: updated.ID, Name: "two", Host: "db2", Port: 5433, DBName: "demo2",
		Username: "u2", PasswordEnc: []byte("new"), SSLMode: "require",
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate name update = %v; want ErrConflict", err)
	}
}

func TestDeleteDBConnectionCascadesHistoryDraftAccess(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	conn := createDB(t, st, "demo")
	group, _ := st.CreateGroup(ctx, "group", "")
	if err := st.SetGroupAccess(ctx, group.ID, conn.ID, domain.LevelView); err != nil {
		t.Fatalf("set access: %v", err)
	}
	h := insertRunning(t, st, conn.ID, 1, nil, time.Now().UTC())
	if err := st.FinishMigration(ctx, h.ID, conn.ID, domain.MigrationSuccess, "", time.Millisecond, nil, 0); err != nil {
		t.Fatalf("finish: %v", err)
	}
	draft, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{DBID: conn.ID, Title: "d", SQLText: "select 1"})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}

	if err := st.DeleteDBConnection(ctx, conn.ID); err != nil {
		t.Fatalf("delete db: %v", err)
	}
	if _, err := st.GetDBConnection(ctx, conn.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get deleted db = %v; want ErrNotFound", err)
	}
	if _, err := st.GetMigrationHistory(ctx, conn.ID, h.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("history after delete = %v; want ErrNotFound", err)
	}
	if _, err := st.GetMigrationDraft(ctx, conn.ID, draft.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("draft after delete = %v; want ErrNotFound", err)
	}
	var accessCount int
	if err := st.Pool().QueryRow(ctx, `select count(*) from db_access where db_id=$1`, conn.ID).Scan(&accessCount); err != nil {
		t.Fatalf("count access: %v", err)
	}
	if accessCount != 0 {
		t.Fatalf("db_access after delete = %d; want 0", accessCount)
	}
}

func TestDeleteDBConnectionRejectsRunningMigration(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	conn := createDB(t, st, "demo")
	insertRunning(t, st, conn.ID, 1, nil, time.Now().UTC())

	if err := st.DeleteDBConnection(ctx, conn.ID); !errors.Is(err, store.ErrRunningMigration) {
		t.Fatalf("delete running db = %v; want ErrRunningMigration", err)
	}
}

func TestDeleteDBConnectionAfterReapSucceeds(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	conn := createDB(t, st, "demo")
	insertRunning(t, st, conn.ID, 1, nil, time.Now().UTC().Add(-time.Hour))

	if _, err := st.ReapStaleRunningMigrations(ctx, conn.ID, 30*time.Minute); err != nil {
		t.Fatalf("reap: %v", err)
	}
	if err := st.DeleteDBConnection(ctx, conn.ID); err != nil {
		t.Fatalf("delete after reap: %v", err)
	}
}

func TestDeleteDBConnectionConcurrentWithRunningInsert(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	conn := createDB(t, st, "demo")

	tx, err := st.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		insert into migration_history (id, db_id, version, sql_text, started_at, status)
		values ($1,$2,1,'select 1',now(),'running')`, uuid.New(), conn.ID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("insert running in tx: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- st.DeleteDBConnection(ctx, conn.ID)
	}()
	time.Sleep(100 * time.Millisecond)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit insert: %v", err)
	}

	err = <-done
	if !errors.Is(err, store.ErrRunningMigration) {
		t.Fatalf("delete concurrent with insert = %v; want ErrRunningMigration", err)
	}
	var historyCount int
	if err := st.Pool().QueryRow(ctx, `select count(*) from migration_history where db_id=$1`, conn.ID).Scan(&historyCount); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if historyCount != 1 {
		t.Fatalf("history rows = %d; want 1", historyCount)
	}
}

func TestDraftCRUD(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	creator := createUser(t, st, "creator", false)
	editor := createUser(t, st, "editor", false)
	conn := createDB(t, st, "demo")

	draft, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{
		DBID: conn.ID, Title: "first", SQLText: "select 1", CreatedBy: &creator.ID,
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if draft.Revision != 1 {
		t.Fatalf("initial revision = %d; want 1", draft.Revision)
	}

	got, err := st.GetMigrationDraft(ctx, conn.ID, draft.ID)
	if err != nil {
		t.Fatalf("get draft: %v", err)
	}
	if got.SQLText != "select 1" || got.CreatedByName != "creator" {
		t.Fatalf("got draft = %+v", got)
	}

	updated, err := st.UpdateMigrationDraft(ctx, conn.ID, draft.ID, "second", "select 2", 1, editor.ID)
	if err != nil {
		t.Fatalf("update draft: %v", err)
	}
	if updated.Revision != 2 || updated.Title != "second" || updated.SQLText != "select 2" {
		t.Fatalf("updated draft = %+v", updated)
	}

	list, err := st.ListMigrationDrafts(ctx, conn.ID)
	if err != nil {
		t.Fatalf("list drafts: %v", err)
	}
	if len(list) != 1 || list[0].Revision != 2 || list[0].SQLText != "" {
		t.Fatalf("list = %+v", list)
	}

	if err := st.DeleteMigrationDraft(ctx, conn.ID, draft.ID); err != nil {
		t.Fatalf("delete draft: %v", err)
	}
	if _, err := st.GetMigrationDraft(ctx, conn.ID, draft.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get deleted draft = %v; want ErrNotFound", err)
	}
}

func TestDraftGetAndDeleteFilterDBID(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	connA := createDB(t, st, "a")
	connB := createDB(t, st, "b")
	draft, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{DBID: connA.ID, Title: "d", SQLText: "select 1"})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}

	if _, err := st.GetMigrationDraft(ctx, connB.ID, draft.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get via other db = %v; want ErrNotFound", err)
	}
	if err := st.DeleteMigrationDraft(ctx, connB.ID, draft.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete via other db = %v; want ErrNotFound", err)
	}
	if _, err := st.GetMigrationDraft(ctx, connA.ID, draft.ID); err != nil {
		t.Fatalf("draft should remain in own db: %v", err)
	}
}

func TestDraftRevisionMismatch(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	actor := createUser(t, st, "actor", false)
	conn := createDB(t, st, "demo")
	draft, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{DBID: conn.ID, Title: "d", SQLText: "select 1"})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}

	if _, err := st.UpdateMigrationDraft(ctx, conn.ID, draft.ID, "x", "select 2", 99, actor.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("mismatch = %v; want ErrConflict", err)
	}
	got, err := st.GetMigrationDraft(ctx, conn.ID, draft.ID)
	if err != nil {
		t.Fatalf("get draft: %v", err)
	}
	if got.Revision != 1 || got.Title != "d" || got.SQLText != "select 1" {
		t.Fatalf("draft changed after conflict: %+v", got)
	}
}

func TestDraftListOrderAndNoSQLText(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	conn := createDB(t, st, "demo")
	first, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{DBID: conn.ID, Title: "first", SQLText: "select 1"})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	time.Sleep(time.Millisecond)
	second, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{DBID: conn.ID, Title: "second", SQLText: "select 2"})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	list, err := st.ListMigrationDrafts(ctx, conn.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("order = %+v", list)
	}
	for _, d := range list {
		if d.SQLText != "" {
			t.Fatalf("list must not include SQL text: %+v", d)
		}
	}
}

func TestDraftCascadeOnDBDelete(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	conn := createDB(t, st, "demo")
	draft, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{DBID: conn.ID, Title: "d", SQLText: "select 1"})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if err := st.DeleteDBConnection(ctx, conn.ID); err != nil {
		t.Fatalf("delete db: %v", err)
	}
	if _, err := st.GetMigrationDraft(ctx, conn.ID, draft.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("draft after db delete = %v; want ErrNotFound", err)
	}
}

func TestAcquireMigrationLockAllocatesLinearVersions(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	conn := createDB(t, st, "demo")

	v1, release1, err := st.AcquireMigrationLock(ctx, conn.ID)
	if err != nil {
		t.Fatalf("acquire first: %v", err)
	}
	if v1 != 1 {
		t.Fatalf("first version = %d; want 1", v1)
	}
	insertRunning(t, st, conn.ID, v1, nil, time.Now().UTC())
	release1()

	v2, release2, err := st.AcquireMigrationLock(ctx, conn.ID)
	if err != nil {
		t.Fatalf("acquire second: %v", err)
	}
	if v2 != 2 {
		t.Fatalf("second version = %d; want 2", v2)
	}
	release2()
}

func TestAcquireMigrationLockSerializesSameDB(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	conn := createDB(t, st, "demo")

	_, release, err := st.AcquireMigrationLock(ctx, conn.ID)
	if err != nil {
		t.Fatalf("acquire first: %v", err)
	}

	shortCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, release2, err := st.AcquireMigrationLock(shortCtx, conn.ID)
		if err == nil {
			release2()
		}
		done <- err
	}()

	if err := <-done; !errors.Is(err, store.ErrLockTimeout) {
		t.Fatalf("second acquire = %v; want ErrLockTimeout", err)
	}
	release()

	longCtx, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer cancel2()
	_, release3, err := st.AcquireMigrationLock(longCtx, conn.ID)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	release3()
}

func TestAcquireMigrationLockDifferentDBParallel(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	a := createDB(t, st, "a")
	b := createDB(t, st, "b")

	_, releaseA, err := st.AcquireMigrationLock(ctx, a.ID)
	if err != nil {
		t.Fatalf("acquire a: %v", err)
	}
	defer releaseA()
	_, releaseB, err := st.AcquireMigrationLock(ctx, b.ID)
	if err != nil {
		t.Fatalf("acquire b: %v", err)
	}
	releaseB()
}

func TestInsertRunningMigrationAndFinishSuccess(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	user := createUser(t, st, "actor", false)
	conn := createDB(t, st, "demo")
	h := insertRunning(t, st, conn.ID, 1, &user.ID, time.Now().UTC())

	running, err := st.GetMigrationHistory(ctx, conn.ID, h.ID)
	if err != nil {
		t.Fatalf("get running: %v", err)
	}
	if running.Status != domain.MigrationRunning || running.FinishedAt != nil || running.DurationMS != nil {
		t.Fatalf("running row = %+v", running)
	}

	if err := st.FinishMigration(ctx, h.ID, conn.ID, domain.MigrationSuccess, "", 1500*time.Millisecond, nil, 0); err != nil {
		t.Fatalf("finish: %v", err)
	}
	got, err := st.GetMigrationHistory(ctx, conn.ID, h.ID)
	if err != nil {
		t.Fatalf("get finished: %v", err)
	}
	if got.Status != domain.MigrationSuccess || got.FinishedAt == nil || got.DurationMS == nil || *got.DurationMS != 1500 {
		t.Fatalf("finished row = %+v", got)
	}
	if got.ExecutedByName != "actor" || got.SQLText == "" {
		t.Fatalf("history join/sql missing: %+v", got)
	}
}

func TestFinishMigrationFailed(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	conn := createDB(t, st, "demo")
	h := insertRunning(t, st, conn.ID, 1, nil, time.Now().UTC())

	if err := st.FinishMigration(ctx, h.ID, conn.ID, domain.MigrationFailed, "boom", 5*time.Millisecond, nil, 0); err != nil {
		t.Fatalf("finish: %v", err)
	}
	got, err := st.GetMigrationHistory(ctx, conn.ID, h.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != domain.MigrationFailed || got.Error != "boom" || got.FinishedAt == nil {
		t.Fatalf("failed row = %+v", got)
	}
}

func TestFinishMigrationZeroRowsReturnsErrHistoryNotRunning(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	conn := createDB(t, st, "demo")
	h := insertRunning(t, st, conn.ID, 1, nil, time.Now().UTC())

	if err := st.FinishMigration(ctx, h.ID, conn.ID, domain.MigrationSuccess, "", time.Millisecond, nil, 0); err != nil {
		t.Fatalf("first finish: %v", err)
	}
	if err := st.FinishMigration(ctx, h.ID, conn.ID, domain.MigrationSuccess, "", time.Millisecond, nil, 0); !errors.Is(err, store.ErrHistoryNotRunning) {
		t.Fatalf("second finish = %v; want ErrHistoryNotRunning", err)
	}
}

func TestFinishMigrationDeletesMatchingDraftRevision(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	conn := createDB(t, st, "demo")
	draft, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{DBID: conn.ID, Title: "d", SQLText: "select 1"})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	h := insertRunning(t, st, conn.ID, 1, nil, time.Now().UTC())

	if err := st.FinishMigration(ctx, h.ID, conn.ID, domain.MigrationSuccess, "", time.Millisecond, &draft.ID, draft.Revision); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if _, err := st.GetMigrationDraft(ctx, conn.ID, draft.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("draft after finish = %v; want ErrNotFound", err)
	}
}

func TestFinishMigrationKeepsEditedDraft(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	actor := createUser(t, st, "actor", false)
	conn := createDB(t, st, "demo")
	draft, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{DBID: conn.ID, Title: "d", SQLText: "select 1"})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if _, err := st.UpdateMigrationDraft(ctx, conn.ID, draft.ID, "edited", "select 2", draft.Revision, actor.ID); err != nil {
		t.Fatalf("edit draft: %v", err)
	}
	h := insertRunning(t, st, conn.ID, 1, nil, time.Now().UTC())

	if err := st.FinishMigration(ctx, h.ID, conn.ID, domain.MigrationSuccess, "", time.Millisecond, &draft.ID, draft.Revision); err != nil {
		t.Fatalf("finish: %v", err)
	}
	got, err := st.GetMigrationDraft(ctx, conn.ID, draft.ID)
	if err != nil {
		t.Fatalf("edited draft should remain: %v", err)
	}
	if got.Revision != 2 || got.Title != "edited" {
		t.Fatalf("edited draft = %+v", got)
	}
}

func TestMarkRunningMigrationsFailed(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	conn := createDB(t, st, "demo")
	h := insertRunning(t, st, conn.ID, 1, nil, time.Now().UTC())

	count, err := st.MarkRunningMigrationsFailed(ctx)
	if err != nil {
		t.Fatalf("mark: %v", err)
	}
	if count != 1 {
		t.Fatalf("marked = %d; want 1", count)
	}
	got, err := st.GetMigrationHistory(ctx, conn.ID, h.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != domain.MigrationFailed || got.FinishedAt == nil || got.DurationMS != nil {
		t.Fatalf("marked row = %+v", got)
	}
	if got.Error != "server berhenti saat eksekusi; kondisi target tidak diketahui, periksa manual" {
		t.Fatalf("error text = %q", got.Error)
	}
}

func TestReapStaleRunningMigrations(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	conn := createDB(t, st, "demo")
	old := insertRunning(t, st, conn.ID, 1, nil, time.Now().UTC().Add(-time.Hour))
	fresh := insertRunning(t, st, conn.ID, 2, nil, time.Now().UTC())

	count, err := st.ReapStaleRunningMigrations(ctx, conn.ID, 30*time.Minute)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if count != 1 {
		t.Fatalf("reaped = %d; want 1", count)
	}
	oldGot, _ := st.GetMigrationHistory(ctx, conn.ID, old.ID)
	if oldGot.Status != domain.MigrationFailed || oldGot.Error == "" {
		t.Fatalf("old row = %+v", oldGot)
	}
	freshGot, _ := st.GetMigrationHistory(ctx, conn.ID, fresh.ID)
	if freshGot.Status != domain.MigrationRunning {
		t.Fatalf("fresh row = %+v", freshGot)
	}
	count, err = st.ReapStaleRunningMigrations(ctx, conn.ID, 30*time.Minute)
	if err != nil || count != 0 {
		t.Fatalf("second reap = %d, %v; want 0", count, err)
	}
}

func TestListMigrationHistoryNoSQLAndOrder(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	user := createUser(t, st, "actor", false)
	conn := createDB(t, st, "demo")
	h1 := insertRunning(t, st, conn.ID, 1, &user.ID, time.Now().UTC())
	if err := st.FinishMigration(ctx, h1.ID, conn.ID, domain.MigrationSuccess, "", time.Millisecond, nil, 0); err != nil {
		t.Fatalf("finish h1: %v", err)
	}
	h2 := insertRunning(t, st, conn.ID, 2, &user.ID, time.Now().UTC())
	if err := st.FinishMigration(ctx, h2.ID, conn.ID, domain.MigrationFailed, "boom", time.Millisecond, nil, 0); err != nil {
		t.Fatalf("finish h2: %v", err)
	}

	list, err := st.ListMigrationHistory(ctx, conn.ID, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].ID != h2.ID || list[1].ID != h1.ID {
		t.Fatalf("order = %+v", list)
	}
	for _, h := range list {
		if h.SQLText != "" {
			t.Fatalf("list must not include SQL text: %+v", h)
		}
		if h.ExecutedByName != "actor" {
			t.Fatalf("ExecutedByName = %q", h.ExecutedByName)
		}
	}
}

func TestGetMigrationHistoryIncludesSQLAndFiltersDB(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	connA := createDB(t, st, "a")
	connB := createDB(t, st, "b")
	h := insertRunning(t, st, connA.ID, 1, nil, time.Now().UTC())

	got, err := st.GetMigrationHistory(ctx, connA.ID, h.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SQLText != h.SQLText {
		t.Fatalf("SQLText = %q; want %q", got.SQLText, h.SQLText)
	}
	if _, err := st.GetMigrationHistory(ctx, connB.ID, h.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get with other db = %v; want ErrNotFound", err)
	}
}
