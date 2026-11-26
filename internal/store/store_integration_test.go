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
		         migration_history, migration_drafts,
		         migration_definitions, migration_proposals, migration_proposal_approvers,
		         migration_approvals, migration_executions, migration_jobs,
		         database_migration_locks, worker_heartbeats cascade`); err != nil {
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

func TestMarkSessionsForRotation(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()
	member := createUser(t, st, "member", false)
	other := createUser(t, st, "other", false)
	group, err := st.CreateGroup(ctx, "group", "")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := st.AddGroupMember(ctx, group.ID, member.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	hMember := createSession(t, st, member.ID)
	hOther := createSession(t, st, other.ID)

	if err := st.MarkGroupMembersSessionsForRotation(ctx, group.ID); err != nil {
		t.Fatalf("mark group: %v", err)
	}
	sess, err := st.GetSession(ctx, hMember)
	if err != nil || !sess.ForceRotate || sess.RevokedAt != nil {
		t.Fatalf("member session = %+v, %v; want marked and active", sess, err)
	}
	sess, err = st.GetSession(ctx, hOther)
	if err != nil || sess.ForceRotate {
		t.Fatalf("other session = %+v, %v; want untouched", sess, err)
	}

	if err := st.MarkUserSessionsForRotation(ctx, other.ID); err != nil {
		t.Fatalf("mark user: %v", err)
	}
	sess, err = st.GetSession(ctx, hOther)
	if err != nil || !sess.ForceRotate {
		t.Fatalf("other session = %+v, %v; want marked", sess, err)
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

func TestGroupLeaderAssignmentAndRequiredApproverGroups(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()

	leader := createUser(t, st, "leader1", false)
	outsider := createUser(t, st, "outsider", false)
	inactive := createUser(t, st, "inactive", false)
	actor := createUser(t, st, "actor", true)
	if err := st.SetUserActiveGuarded(ctx, inactive.ID, false, actor.ID); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}

	g, err := st.CreateGroup(ctx, "backend", "Backend team")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	// Non-member cannot be leader
	if _, err := st.SetGroupLeader(ctx, g.ID, &outsider.ID); !errors.Is(err, store.ErrInvalidGroupLeader) {
		t.Fatalf("outsider leader = %v; want ErrInvalidGroupLeader", err)
	}

	// Inactive member cannot be leader
	if err := st.AddGroupMember(ctx, g.ID, inactive.ID); err != nil {
		t.Fatalf("add inactive member: %v", err)
	}
	if _, err := st.SetGroupLeader(ctx, g.ID, &inactive.ID); !errors.Is(err, store.ErrInvalidGroupLeader) {
		t.Fatalf("inactive leader = %v; want ErrInvalidGroupLeader", err)
	}

	// Active member can be leader
	if err := st.AddGroupMember(ctx, g.ID, leader.ID); err != nil {
		t.Fatalf("add active member: %v", err)
	}
	updated, err := st.SetGroupLeader(ctx, g.ID, &leader.ID)
	if err != nil {
		t.Fatalf("set active leader: %v", err)
	}
	if updated.LeaderID == nil || *updated.LeaderID != leader.ID || updated.LeaderUsername != "leader1" {
		t.Fatalf("updated group leader = %+v", updated)
	}

	leaderGroups, err := st.ListUserLeaderGroupIDs(ctx, leader.ID)
	if err != nil || len(leaderGroups) != 1 || leaderGroups[0] != g.ID {
		t.Fatalf("ListUserLeaderGroupIDs = %v, %v", leaderGroups, err)
	}

	conn := createDB(t, st, "prod")
	if err := st.SetGroupAccess(ctx, g.ID, conn.ID, domain.LevelEdit); err != nil {
		t.Fatalf("set access: %v", err)
	}
	approvers, err := st.ListRequiredApproverGroups(ctx, conn.ID)
	if err != nil || len(approvers) != 1 {
		t.Fatalf("ListRequiredApproverGroups = %+v, %v", approvers, err)
	}
	if approvers[0].GroupID != g.ID || approvers[0].LeaderID == nil || *approvers[0].LeaderID != leader.ID || !approvers[0].LeaderActive || approvers[0].AccessLevel != domain.LevelEdit {
		t.Fatalf("unexpected approver group: %+v", approvers[0])
	}

	// Removing member clears leader_id
	if err := st.RemoveGroupMember(ctx, g.ID, leader.ID); err != nil {
		t.Fatalf("remove leader member: %v", err)
	}
	afterRemove, err := st.GetGroup(ctx, g.ID)
	if err != nil {
		t.Fatalf("get group: %v", err)
	}
	if afterRemove.LeaderID != nil {
		t.Fatalf("expected leader_id to be cleared after member removal, got %v", afterRemove.LeaderID)
	}
}

func TestM2DeletionPolicyAndImmutabilityTriggers(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()

	admin := createUser(t, st, "admin", true)
	proposer := createUser(t, st, "proposer", false)
	executorOnly := createUser(t, st, "executor_only", false)
	cleanUser := createUser(t, st, "clean_user", false)

	gWithHist, _ := st.CreateGroup(ctx, "dba-hist", "")
	gClean, _ := st.CreateGroup(ctx, "dba-clean", "")
	_ = st.AddGroupMember(ctx, gWithHist.ID, proposer.ID)
	_, _ = st.SetGroupLeader(ctx, gWithHist.ID, &proposer.ID)

	conn := createDB(t, st, "orders-db")
	_ = st.SetGroupAccess(ctx, gWithHist.ID, conn.ID, domain.LevelEdit)

	// Clean user can be hard-deleted
	if err := st.DeleteUserGuarded(ctx, cleanUser.ID, admin.ID); err != nil {
		t.Fatalf("delete clean user: %v", err)
	}
	if _, err := st.GetUserByID(ctx, cleanUser.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected clean user to be deleted, got %v", err)
	}

	// Clean group can be hard-deleted
	if _, err := st.DeleteGroup(ctx, gClean.ID); err != nil {
		t.Fatalf("delete clean group: %v", err)
	}

	// Insert migration_definition, proposal, proposal_approver, execution
	defID := uuid.New()
	_, err := st.Pool().Exec(ctx, `
		insert into migration_definitions (id, db_id, seq_version, effective_seq, title, sql_up, sql_down, is_irreversible, created_by)
		values ($1, $2, 1, 1, 'init', 'CREATE TABLE t(id int);', 'DROP TABLE t;', false, $3)`,
		defID, conn.ID, proposer.ID)
	if err != nil {
		t.Fatalf("insert migration_definition: %v", err)
	}

	propID := uuid.New()
	_, err = st.Pool().Exec(ctx, `
		insert into migration_proposals (id, db_id, migration_id, migration_revision, action_type, status, proposed_by)
		values ($1, $2, $3, 1, 'apply', 'pending_review', $4)`,
		propID, conn.ID, defID, proposer.ID)
	if err != nil {
		t.Fatalf("insert migration_proposal: %v", err)
	}

	// Trigger trg_migration_definitions_immutable must reject spec modification while active proposal exists
	if _, err := st.Pool().Exec(ctx, `update migration_definitions set sql_up = 'SELECT 2;' where id = $1`, defID); err == nil {
		t.Fatal("expected trg_migration_definitions_immutable to block sql_up update while active proposal exists")
	}

	// Partial unique index migration_proposals_one_active_per_migration_idx must block second active proposal
	if _, err := st.Pool().Exec(ctx, `
		insert into migration_proposals (id, db_id, migration_id, migration_revision, action_type, status, proposed_by)
		values ($1, $2, $3, 1, 'apply', 'pending_review', $4)`,
		uuid.New(), conn.ID, defID, proposer.ID); err == nil {
		t.Fatal("expected partial unique index to block second active proposal on same migration_id")
	}

	_, err = st.Pool().Exec(ctx, `
		insert into migration_proposal_approvers (proposal_id, group_id, leader_id, access_level)
		values ($1, $2, $3, 'edit')`, propID, gWithHist.ID, proposer.ID)
	if err != nil {
		t.Fatalf("insert migration_proposal_approvers: %v", err)
	}

	execID := uuid.New()
	_, err = st.Pool().Exec(ctx, `
		insert into migration_executions (
			execution_id, root_execution_id, db_id, migration_id, proposal_id,
			migration_revision, action_type, executed_sql, executed_by,
			executed_by_username, db_name_snapshot, migration_title_snapshot, status
		) values ($1, $1, $2, $3, $4, 1, 'apply', 'CREATE TABLE t(id int);', $5, 'executor_only', 'orders-db', 'init', 'success')`,
		execID, conn.ID, defID, propID, executorOnly.ID)
	if err != nil {
		t.Fatalf("insert migration_execution: %v", err)
	}

	// User with proposal/definition/approver history cannot be hard-deleted -> ErrUserHasHistory
	if err := st.DeleteUserGuarded(ctx, proposer.ID, admin.ID); !errors.Is(err, store.ErrUserHasHistory) {
		t.Fatalf("delete user with history = %v; want ErrUserHasHistory", err)
	}

	// Group with approver history cannot be hard-deleted -> ErrGroupHasHistory
	if _, err := st.DeleteGroup(ctx, gWithHist.ID); !errors.Is(err, store.ErrGroupHasHistory) {
		t.Fatalf("delete group with history = %v; want ErrGroupHasHistory", err)
	}

	// User who ONLY appears in migration_executions.executed_by CAN be hard-deleted, setting executed_by = NULL while keeping executed_by_username
	if err := st.DeleteUserGuarded(ctx, executorOnly.ID, admin.ID); err != nil {
		t.Fatalf("delete executor_only user: %v", err)
	}
	var execBy *uuid.UUID
	var execUsername string
	if err := st.Pool().QueryRow(ctx, `select executed_by, executed_by_username from migration_executions where execution_id = $1`, execID).
		Scan(&execBy, &execUsername); err != nil {
		t.Fatalf("query execution after user delete: %v", err)
	}
	if execBy != nil || execUsername != "executor_only" {
		t.Fatalf("expected executed_by=nil and executed_by_username='executor_only', got %v, %q", execBy, execUsername)
	}

	// Trigger trg_migration_executions_immutable must block DELETE and modification of executed_sql
	if _, err := st.Pool().Exec(ctx, `delete from migration_executions where execution_id = $1`, execID); err == nil {
		t.Fatal("expected trg_migration_executions_immutable to block DELETE")
	}
	if _, err := st.Pool().Exec(ctx, `update migration_executions set executed_sql = 'SELECT 99;' where execution_id = $1`, execID); err == nil {
		t.Fatal("expected trg_migration_executions_immutable to block executed_sql update")
	}

	// Active job blocks DB delete with ErrDBHasActiveWork
	jobID := uuid.New()
	_, err = st.Pool().Exec(ctx, `
		insert into migration_jobs (job_id, db_id, proposal_id, migration_id, execution_id, action_type, execution_mode, status)
		values ($1, $2, $3, $4, $5, 'apply', 'normal', 'running')`,
		jobID, conn.ID, propID, defID, execID)
	if err != nil {
		t.Fatalf("insert migration_job: %v", err)
	}
	if err := st.DeleteDBConnection(ctx, conn.ID); !errors.Is(err, store.ErrDBHasActiveWork) {
		t.Fatalf("delete db with active job = %v; want ErrDBHasActiveWork", err)
	}

	// Finish job -> DB delete performs soft-delete (deleted_at IS NOT NULL) and allows reusing the name
	if _, err := st.Pool().Exec(ctx, `update migration_jobs set status = 'success' where job_id = $1`, jobID); err != nil {
		t.Fatalf("complete job: %v", err)
	}
	if err := st.DeleteDBConnection(ctx, conn.ID); err != nil {
		t.Fatalf("soft-delete db with history: %v", err)
	}
	if _, err := st.GetDBConnection(ctx, conn.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get soft-deleted db = %v; want ErrNotFound", err)
	}

	// History row still exists in DB!
	var count int
	if err := st.Pool().QueryRow(ctx, `select count(*) from migration_executions where execution_id = $1`, execID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("execution count after db soft-delete = %d, %v; want 1", count, err)
	}

	// Reusing the same database name after soft-delete succeeds!
	reused := createDB(t, st, "orders-db")
	if reused.ID == conn.ID {
		t.Fatal("expected new UUID for reused DB name")
	}
}

func TestMigrationDefinitionsEffectiveSeqAndRelaxedSupersede(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()

	user := createUser(t, st, "dev", false)
	conn := createDB(t, st, "app-db")

	insertExec := func(migID uuid.UUID, actionType, status string, rollbackOf *uuid.UUID) uuid.UUID {
		t.Helper()
		execID := uuid.New()
		_, err := st.Pool().Exec(ctx, `
			insert into migration_executions (
				execution_id, root_execution_id, db_id, migration_id,
				migration_revision, action_type, executed_sql, executed_by,
				executed_by_username, db_name_snapshot, migration_title_snapshot,
				status, rollback_of_execution_id
			) values ($1, $1, $2, $3, 1, $4, 'SELECT 1;', $5, 'dev', 'app-db', 'm', $6, $7)`,
			execID, conn.ID, migID, actionType, user.ID, status, rollbackOf)
		if err != nil {
			t.Fatalf("insert execution (%s, %s): %v", actionType, status, err)
		}
		return execID
	}

	// 1. Create A(seq=1, eff=1), B(seq=2, eff=2), C(seq=3, eff=3)
	migA, err := st.CreateMigrationDefinition(ctx, domain.MigrationDefinition{
		DBID: conn.ID, Title: "A", SQLUp: "CREATE TABLE a(id int);", SQLDown: "DROP TABLE a;", CreatedBy: &user.ID,
	})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	if migA.SeqVersion != 1 || migA.EffectiveSeq != 1 || migA.Revision != 1 {
		t.Fatalf("migA = %+v", migA)
	}

	migB, err := st.CreateMigrationDefinition(ctx, domain.MigrationDefinition{
		DBID: conn.ID, Title: "B", SQLUp: "CREATE TABLE b(id int);", SQLDown: "DROP TABLE b;", CreatedBy: &user.ID,
	})
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	if migB.SeqVersion != 2 || migB.EffectiveSeq != 2 {
		t.Fatalf("migB = %+v", migB)
	}

	migC, err := st.CreateMigrationDefinition(ctx, domain.MigrationDefinition{
		DBID: conn.ID, Title: "C", SQLUp: "CREATE TABLE c(id int);", SQLDown: "DROP TABLE c;", CreatedBy: &user.ID,
	})
	if err != nil {
		t.Fatalf("create C: %v", err)
	}
	if migC.SeqVersion != 3 || migC.EffectiveSeq != 3 {
		t.Fatalf("migC = %+v", migC)
	}

	// 2. Edit definition while draft increments revision
	migCUpdated, err := st.UpdateMigrationDefinition(ctx, domain.MigrationDefinition{
		ID: migC.ID, DBID: conn.ID, Revision: 1,
		Title: "C-v2", SQLUp: "CREATE TABLE c(id bigint);", SQLDown: "DROP TABLE c;", UpdatedBy: &user.ID,
	})
	if err != nil {
		t.Fatalf("update C: %v", err)
	}
	if migCUpdated.Revision != 2 || migCUpdated.Title != "C-v2" {
		t.Fatalf("migCUpdated = %+v", migCUpdated)
	}

	// Active proposal blocks UpdateMigrationDefinition with ErrActiveProposalExists
	propID := uuid.New()
	if _, err := st.Pool().Exec(ctx, `
		insert into migration_proposals (id, db_id, migration_id, migration_revision, action_type, status, proposed_by)
		values ($1, $2, $3, 2, 'apply', 'pending_review', $4)`,
		propID, conn.ID, migC.ID, user.ID); err != nil {
		t.Fatalf("insert active proposal on C: %v", err)
	}
	if _, err := st.UpdateMigrationDefinition(ctx, domain.MigrationDefinition{
		ID: migC.ID, DBID: conn.ID, Title: "C-v3", SQLUp: "SELECT 1;", SQLDown: "SELECT 1;",
	}); !errors.Is(err, store.ErrActiveProposalExists) {
		t.Fatalf("edit with active proposal = %v; want ErrActiveProposalExists", err)
	}
	if _, err := st.Pool().Exec(ctx, `update migration_proposals set status = 'withdrawn' where id = $1`, propID); err != nil {
		t.Fatalf("withdraw proposal: %v", err)
	}

	// 3. A succeeds (Active), B fails (immutable)
	insertExec(migA.ID, domain.ActionApply, domain.StatusSuccess, nil)
	if _, err := st.Pool().Exec(ctx, `update migration_definitions set is_immutable = true where id in ($1, $2)`, migA.ID, migB.ID); err != nil {
		t.Fatalf("mark A and B immutable: %v", err)
	}
	insertExec(migB.ID, domain.ActionApply, domain.StatusFailed, nil)

	// Immutable definition cannot be edited
	if _, err := st.UpdateMigrationDefinition(ctx, domain.MigrationDefinition{
		ID: migB.ID, DBID: conn.ID, Title: "B-edit", SQLUp: "SELECT 1;", SQLDown: "SELECT 1;",
	}); !errors.Is(err, store.ErrMigrationImmutable) {
		t.Fatalf("edit immutable B = %v; want ErrMigrationImmutable", err)
	}

	// Superseding A (which is currently Active) must fail with ErrMigrationNotSupersedable
	if _, err := st.CreateMigrationDefinition(ctx, domain.MigrationDefinition{
		DBID: conn.ID, Title: "A-prime", SQLUp: "SELECT 1;", SQLDown: "SELECT 1;", ReplacesMigrationID: &migA.ID,
	}); !errors.Is(err, store.ErrMigrationNotSupersedable) {
		t.Fatalf("supersede active A = %v; want ErrMigrationNotSupersedable", err)
	}

	// Superseding C (slot 3 while slot 2 is not yet Active) must fail with ErrMigrationNotSupersedable
	if _, err := st.Pool().Exec(ctx, `update migration_definitions set is_immutable = true where id = $1`, migC.ID); err != nil {
		t.Fatalf("mark C immutable: %v", err)
	}
	if _, err := st.CreateMigrationDefinition(ctx, domain.MigrationDefinition{
		DBID: conn.ID, Title: "C-prime", SQLUp: "SELECT 1;", SQLDown: "SELECT 1;", ReplacesMigrationID: &migC.ID,
	}); !errors.Is(err, store.ErrMigrationNotSupersedable) {
		t.Fatalf("supersede slot 3 while slot 2 inactive = %v; want ErrMigrationNotSupersedable", err)
	}

	// 4. Create B' replacing B -> seq_version = 4, effective_seq = 2
	migBPrime, err := st.CreateMigrationDefinition(ctx, domain.MigrationDefinition{
		DBID: conn.ID, Title: "B-prime", SQLUp: "CREATE TABLE b1(id int);", SQLDown: "DROP TABLE b1;",
		ReplacesMigrationID: &migB.ID, CreatedBy: &user.ID,
	})
	if err != nil {
		t.Fatalf("create B': %v", err)
	}
	if migBPrime.SeqVersion != 4 || migBPrime.EffectiveSeq != 2 {
		t.Fatalf("migBPrime seq=%d eff=%d; want seq=4 eff=2", migBPrime.SeqVersion, migBPrime.EffectiveSeq)
	}

	// While B' has an in-flight/partial execution, superseding B' is blocked
	bPrimeExec := insertExec(migBPrime.ID, domain.ActionApply, domain.StatusPartial, nil)
	if _, err := st.CreateMigrationDefinition(ctx, domain.MigrationDefinition{
		DBID: conn.ID, Title: "B-double-prime", SQLUp: "SELECT 1;", SQLDown: "SELECT 1;",
		ReplacesMigrationID: &migBPrime.ID,
	}); !errors.Is(err, store.ErrMigrationNotSupersedable) {
		t.Fatalf("supersede B' while partial = %v; want ErrMigrationNotSupersedable", err)
	}

	// Mark B' execution as failed -> Chained Supersede B'' replacing B' succeeds with effective_seq = 2
	if _, err := st.Pool().Exec(ctx, `update migration_executions set status = 'failed' where execution_id = $1`, bPrimeExec); err != nil {
		t.Fatalf("mark B' exec failed: %v", err)
	}
	migB2, err := st.CreateMigrationDefinition(ctx, domain.MigrationDefinition{
		DBID: conn.ID, Title: "B-double-prime", SQLUp: "CREATE TABLE b2(id int);", SQLDown: "DROP TABLE b2;",
		ReplacesMigrationID: &migBPrime.ID, CreatedBy: &user.ID,
	})
	if err != nil {
		t.Fatalf("create B'': %v", err)
	}
	if migB2.SeqVersion != 5 || migB2.EffectiveSeq != 2 {
		t.Fatalf("migB2 seq=%d eff=%d; want seq=5 eff=2", migB2.SeqVersion, migB2.EffectiveSeq)
	}

	// 5. B'' succeeds -> MarkSupersededChain marks B and B' as is_superseded = true
	b2ApplyExec := insertExec(migB2.ID, domain.ActionApply, domain.StatusSuccess, nil)
	if err := st.MarkSupersededChain(ctx, conn.ID, migB2.EffectiveSeq, migB2.ID); err != nil {
		t.Fatalf("MarkSupersededChain: %v", err)
	}
	gotB, _ := st.GetMigrationDefinition(ctx, conn.ID, migB.ID)
	gotBPrime, _ := st.GetMigrationDefinition(ctx, conn.ID, migBPrime.ID)
	gotB2, _ := st.GetMigrationDefinition(ctx, conn.ID, migB2.ID)
	if !gotB.IsSuperseded || !gotBPrime.IsSuperseded || gotB2.IsSuperseded || !gotB2.IsActive {
		t.Fatalf("superseded/active flags: B=%v B'=%v B''(sup=%v, active=%v)",
			gotB.IsSuperseded, gotBPrime.IsSuperseded, gotB2.IsSuperseded, gotB2.IsActive)
	}

	// 6. Relaxed Supersede: B'' is rolled back (rollback -> success), then forward fails (forward -> failed).
	// Historic apply->success on B'' was cancelled by rollback->success, so creating B''' replacing B'' MUST succeed!
	insertExec(migB2.ID, domain.ActionRollback, domain.StatusSuccess, &b2ApplyExec)
	insertExec(migB2.ID, domain.ActionForward, domain.StatusFailed, nil)

	migB3, err := st.CreateMigrationDefinition(ctx, domain.MigrationDefinition{
		DBID: conn.ID, Title: "B-triple-prime", SQLUp: "CREATE TABLE b3(id int);", SQLDown: "DROP TABLE b3;",
		ReplacesMigrationID: &migB2.ID, CreatedBy: &user.ID,
	})
	if err != nil {
		t.Fatalf("relaxed supersede B''' after rollback + failed forward: %v", err)
	}
	if migB3.SeqVersion != 6 || migB3.EffectiveSeq != 2 {
		t.Fatalf("migB3 seq=%d eff=%d; want seq=6 eff=2", migB3.SeqVersion, migB3.EffectiveSeq)
	}
}

func TestMultiGroupApprovalQuorumIntersectionPredecessorGateAndAutoEnqueue(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()

	leaderEdit := createUser(t, st, "leader_edit", false)
	leaderView := createUser(t, st, "leader_view", false)
	leaderRevoked := createUser(t, st, "leader_revoked", false)
	outsider := createUser(t, st, "outsider", false)

	conn := createDB(t, st, "core-db")

	def1, err := st.CreateMigrationDefinition(ctx, domain.MigrationDefinition{
		DBID: conn.ID, Title: "m1", SQLUp: "CREATE TABLE m1(id int);", SQLDown: "DROP TABLE m1;", CreatedBy: &leaderEdit.ID,
	})
	if err != nil {
		t.Fatalf("create def1: %v", err)
	}
	def2, err := st.CreateMigrationDefinition(ctx, domain.MigrationDefinition{
		DBID: conn.ID, Title: "m2", SQLUp: "CREATE TABLE m2(id int);", SQLDown: "DROP TABLE m2;", CreatedBy: &leaderEdit.ID,
	})
	if err != nil {
		t.Fatalf("create def2: %v", err)
	}

	// 1. Submitting proposal when DB has 0 access groups -> ErrNoAccessGroups (422 NO_ACCESS_GROUPS)
	if _, err := st.SubmitProposalToReview(ctx, store.SubmitProposalInput{
		DBID: conn.ID, MigrationID: def1.ID, ActionType: domain.ActionApply, ProposedBy: leaderEdit.ID,
	}); !errors.Is(err, store.ErrNoAccessGroups) {
		t.Fatalf("submit with no access groups = %v; want ErrNoAccessGroups", err)
	}

	// 2. Group without active leader -> ErrMissingGroupLeader (422)
	gEdit, _ := st.CreateGroup(ctx, "g-edit", "")
	_ = st.AddGroupMember(ctx, gEdit.ID, leaderEdit.ID)
	_ = st.SetGroupAccess(ctx, gEdit.ID, conn.ID, domain.LevelEdit)

	if _, err := st.SubmitProposalToReview(ctx, store.SubmitProposalInput{
		DBID: conn.ID, MigrationID: def1.ID, ActionType: domain.ActionApply, ProposedBy: leaderEdit.ID,
	}); !errors.Is(err, store.ErrMissingGroupLeader) {
		t.Fatalf("submit without group leader = %v; want ErrMissingGroupLeader", err)
	}

	_, _ = st.SetGroupLeader(ctx, gEdit.ID, &leaderEdit.ID)

	gView, _ := st.CreateGroup(ctx, "g-view", "")
	_ = st.AddGroupMember(ctx, gView.ID, leaderView.ID)
	_, _ = st.SetGroupLeader(ctx, gView.ID, &leaderView.ID)
	_ = st.SetGroupAccess(ctx, gView.ID, conn.ID, domain.LevelView)

	gRevoked, _ := st.CreateGroup(ctx, "g-revoked", "")
	_ = st.AddGroupMember(ctx, gRevoked.ID, leaderRevoked.ID)
	_, _ = st.SetGroupLeader(ctx, gRevoked.ID, &leaderRevoked.ID)
	_ = st.SetGroupAccess(ctx, gRevoked.ID, conn.ID, domain.LevelView)

	// 3. Submit proposal for def1 and def2
	prop1, err := st.SubmitProposalToReview(ctx, store.SubmitProposalInput{
		DBID: conn.ID, MigrationID: def1.ID, ActionType: domain.ActionApply, ProposedBy: leaderEdit.ID,
	})
	if err != nil {
		t.Fatalf("submit prop1: %v", err)
	}
	if prop1.Status != domain.ProposalPendingReview || len(prop1.Approvers) != 3 {
		t.Fatalf("prop1 = %+v", prop1)
	}

	// Duplicate active proposal on def1 -> ErrDuplicateActiveProposal
	if _, err := st.SubmitProposalToReview(ctx, store.SubmitProposalInput{
		DBID: conn.ID, MigrationID: def1.ID, ActionType: domain.ActionApply, ProposedBy: leaderEdit.ID,
	}); !errors.Is(err, store.ErrDuplicateActiveProposal) {
		t.Fatalf("duplicate active proposal = %v; want ErrDuplicateActiveProposal", err)
	}

	// Withdraw prop1 -> status='withdrawn', increments def1.revision to 2, allows re-submitting
	withdrawn, err := st.WithdrawProposal(ctx, conn.ID, prop1.ID)
	if err != nil || withdrawn.Status != domain.ProposalWithdrawn {
		t.Fatalf("withdraw prop1 = %+v, %v", withdrawn, err)
	}
	def1AfterWithdraw, _ := st.GetMigrationDefinition(ctx, conn.ID, def1.ID)
	if def1AfterWithdraw.Revision != 2 {
		t.Fatalf("def1 revision after withdraw = %d; want 2", def1AfterWithdraw.Revision)
	}

	// Re-submit prop1 and also submit prop2
	prop1, err = st.SubmitProposalToReview(ctx, store.SubmitProposalInput{
		DBID: conn.ID, MigrationID: def1.ID, ActionType: domain.ActionApply, ProposedBy: leaderEdit.ID,
	})
	if err != nil {
		t.Fatalf("resubmit prop1: %v", err)
	}
	prop2, err := st.SubmitProposalToReview(ctx, store.SubmitProposalInput{
		DBID: conn.ID, MigrationID: def2.ID, ActionType: domain.ActionApply, ProposedBy: leaderEdit.ID,
	})
	if err != nil {
		t.Fatalf("submit prop2: %v", err)
	}

	// 4. Revoke access of gRevoked after submission -> quorum intersection becomes {gEdit, gView} (2 groups)
	if err := st.RemoveGroupAccess(ctx, gRevoked.ID, conn.ID); err != nil {
		t.Fatalf("remove gRevoked access: %v", err)
	}

	// Outsider is rejected with ErrNotGroupLeader
	if _, err := st.ApproveProposal(ctx, store.ApproveProposalInput{
		DBID: conn.ID, ProposalID: prop2.ID, Actor: outsider,
	}); !errors.Is(err, store.ErrNotGroupLeader) {
		t.Fatalf("outsider approve = %v; want ErrNotGroupLeader", err)
	}

	// 5. Out-of-order approval on prop2 (slot 2) while prop1 (slot 1) is still pending_review:
	// First leader (leaderEdit - self-approval as proposer-leader) approves prop2 -> pending_review (1/2)
	res2First, err := st.ApproveProposal(ctx, store.ApproveProposalInput{
		DBID: conn.ID, ProposalID: prop2.ID, Actor: leaderEdit, Comment: "lgtm 1",
	})
	if err != nil {
		t.Fatalf("prop2 first leader approve: %v", err)
	}
	if res2First.Enqueued || res2First.ApprovedCount != 1 || res2First.RequiredCount != 2 {
		t.Fatalf("res2First = %+v", res2First)
	}

	// Final leader (leaderView) approves prop2 while slot 1 is still pending_review -> 409 PREDECESSOR_NOT_READY,
	// transaction rolls back and prop2 remains in pending_review!
	if _, err := st.ApproveProposal(ctx, store.ApproveProposalInput{
		DBID: conn.ID, ProposalID: prop2.ID, Actor: leaderView, Comment: "lgtm 2",
	}); !errors.Is(err, store.ErrPredecessorNotReady) {
		t.Fatalf("out-of-order final approve on prop2 = %v; want ErrPredecessorNotReady", err)
	}
	prop2Check, _ := st.GetMigrationProposal(ctx, conn.ID, prop2.ID)
	if prop2Check.Status != domain.ProposalPendingReview {
		t.Fatalf("prop2 status after predecessor gate rejection = %q; want pending_review", prop2Check.Status)
	}

	// 6. Approve prop1 (slot 1): leaderEdit approves, then two concurrent requests from leaderView race -> exactly 1 job created
	if _, err := st.ApproveProposal(ctx, store.ApproveProposalInput{
		DBID: conn.ID, ProposalID: prop1.ID, Actor: leaderEdit,
	}); err != nil {
		t.Fatalf("prop1 first approve: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	results := make(chan store.ApproveProposalResult, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := st.ApproveProposal(ctx, store.ApproveProposalInput{
				DBID: conn.ID, ProposalID: prop1.ID, Actor: leaderView,
			})
			if e != nil {
				errs <- e
			} else {
				results <- r
			}
		}()
	}
	wg.Wait()
	close(errs)
	close(results)

	if len(results) != 1 {
		t.Fatalf("concurrent final approve successes = %d; want 1", len(results))
	}
	res1Final := <-results
	if !res1Final.Enqueued || res1Final.Proposal.Status != domain.ProposalQueued || res1Final.Job == nil || res1Final.Execution == nil {
		t.Fatalf("res1Final = %+v", res1Final)
	}
	if res1Final.Execution.RootExecutionID != res1Final.Execution.ExecutionID || res1Final.Execution.RollbackOfExecutionID != nil {
		t.Fatalf("apply execution lineage = %+v", res1Final.Execution)
	}

	// 7. Now that slot 1 has a 'queued' job, final leader approval on prop2 (slot 2) passes the Predecessor Gate!
	res2Final, err := st.ApproveProposal(ctx, store.ApproveProposalInput{
		DBID: conn.ID, ProposalID: prop2.ID, Actor: leaderView,
	})
	if err != nil {
		t.Fatalf("prop2 final approve after slot 1 queued: %v", err)
	}
	if !res2Final.Enqueued || res2Final.Job.QueueSeq <= res1Final.Job.QueueSeq {
		t.Fatalf("expected prop2 job queue_seq (%d) > prop1 job queue_seq (%d)", res2Final.Job.QueueSeq, res1Final.Job.QueueSeq)
	}
}

func TestActiveMigrationStackLIFORollbackLinearForwardAndBatchLineage(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()

	leader := createUser(t, st, "leader", false)
	conn := createDB(t, st, "stack-db")
	grp, _ := st.CreateGroup(ctx, "dba", "")
	_ = st.AddGroupMember(ctx, grp.ID, leader.ID)
	_, _ = st.SetGroupLeader(ctx, grp.ID, &leader.ID)
	_ = st.SetGroupAccess(ctx, grp.ID, conn.ID, domain.LevelEdit)

	// Create A(1), B(2), C(3)
	migA, err := st.CreateMigrationDefinition(ctx, domain.MigrationDefinition{
		DBID: conn.ID, Title: "A", SQLUp: "CREATE TABLE a(id int);", SQLDown: "DROP TABLE a;", CreatedBy: &leader.ID,
	})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	migB, err := st.CreateMigrationDefinition(ctx, domain.MigrationDefinition{
		DBID: conn.ID, Title: "B", SQLUp: "CREATE TABLE b(id int);", SQLDown: "DROP TABLE b;", CreatedBy: &leader.ID,
	})
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	migC, err := st.CreateMigrationDefinition(ctx, domain.MigrationDefinition{
		DBID: conn.ID, Title: "C", SQLUp: "CREATE TABLE c(id int);", SQLDown: "DROP TABLE c;", CreatedBy: &leader.ID,
	})
	if err != nil {
		t.Fatalf("create C: %v", err)
	}

	// A succeeds, B fails
	execA, err := st.InsertMigrationExecution(ctx, domain.MigrationExecution{
		DBID: conn.ID, MigrationID: migA.ID, MigrationRevision: 1,
		ActionType: domain.ActionApply, ExecutedSQL: migA.SQLUp,
		ExecutedBy: &leader.ID, ExecutedByUsername: leader.Username,
		DBNameSnapshot: conn.Name, MigrationTitleSnapshot: migA.Title,
		Status: domain.StatusSuccess,
	})
	if err != nil {
		t.Fatalf("insert execA: %v", err)
	}
	_, err = st.InsertMigrationExecution(ctx, domain.MigrationExecution{
		DBID: conn.ID, MigrationID: migB.ID, MigrationRevision: 1,
		ActionType: domain.ActionApply, ExecutedSQL: migB.SQLUp,
		ExecutedBy: &leader.ID, ExecutedByUsername: leader.Username,
		DBNameSnapshot: conn.Name, MigrationTitleSnapshot: migB.Title,
		Status: domain.StatusFailed, ErrorMessage: "boom",
	})
	if err != nil {
		t.Fatalf("insert execB failed: %v", err)
	}

	// Create B'(seq=4, eff=2) replacing B(2)
	migBPrime, err := st.CreateMigrationDefinition(ctx, domain.MigrationDefinition{
		DBID: conn.ID, Title: "B-prime", SQLUp: "CREATE TABLE b_fixed(id int);", SQLDown: "DROP TABLE b_fixed;",
		ReplacesMigrationID: &migB.ID, CreatedBy: &leader.ID,
	})
	if err != nil {
		t.Fatalf("create B': %v", err)
	}
	if migBPrime.SeqVersion != 4 || migBPrime.EffectiveSeq != 2 {
		t.Fatalf("B' seq=%d eff=%d; want 4, 2", migBPrime.SeqVersion, migBPrime.EffectiveSeq)
	}

	// Activate B'(eff=2) then C(eff=3)
	execBPrime, err := st.InsertMigrationExecution(ctx, domain.MigrationExecution{
		DBID: conn.ID, MigrationID: migBPrime.ID, MigrationRevision: 1,
		ActionType: domain.ActionApply, ExecutedSQL: migBPrime.SQLUp,
		ExecutedBy: &leader.ID, ExecutedByUsername: leader.Username,
		DBNameSnapshot: conn.Name, MigrationTitleSnapshot: migBPrime.Title,
		Status: domain.StatusSuccess,
	})
	if err != nil {
		t.Fatalf("insert execBPrime: %v", err)
	}
	if err := st.MarkSupersededChain(ctx, conn.ID, migBPrime.EffectiveSeq, migBPrime.ID); err != nil {
		t.Fatalf("MarkSupersededChain: %v", err)
	}
	execC, err := st.InsertMigrationExecution(ctx, domain.MigrationExecution{
		DBID: conn.ID, MigrationID: migC.ID, MigrationRevision: 1,
		ActionType: domain.ActionApply, ExecutedSQL: migC.SQLUp,
		ExecutedBy: &leader.ID, ExecutedByUsername: leader.Username,
		DBNameSnapshot: conn.Name, MigrationTitleSnapshot: migC.Title,
		Status: domain.StatusSuccess,
	})
	if err != nil {
		t.Fatalf("insert execC: %v", err)
	}

	// Active stack order must be [A(eff=1), B'(eff=2), C(eff=3)]
	stack, err := st.GetActiveMigrationStack(ctx, conn.ID)
	if err != nil || len(stack) != 3 {
		t.Fatalf("GetActiveMigrationStack = %+v, %v", stack, err)
	}
	if stack[0].Definition.ID != migA.ID || stack[1].Definition.ID != migBPrime.ID || stack[2].Definition.ID != migC.ID {
		t.Fatalf("stack order = [%v, %v, %v]; want [A, B', C]",
			stack[0].Definition.Title, stack[1].Definition.Title, stack[2].Definition.Title)
	}

	// LIFO Rollback target must be C(eff=3) first, NOT B'(raw seq=4, eff=2)!
	top, err := st.GetTopActiveMigrationForRollback(ctx, conn.ID)
	if err != nil || top == nil || top.Definition.ID != migC.ID || top.ActiveExecutionID != execC.ExecutionID {
		t.Fatalf("GetTopActiveMigrationForRollback = %+v, %v; want C", top, err)
	}

	// Rollback C -> success; next LIFO Rollback target must be B'(eff=2)
	_, err = st.InsertMigrationExecution(ctx, domain.MigrationExecution{
		DBID: conn.ID, MigrationID: migC.ID, MigrationRevision: 1,
		ActionType: domain.ActionRollback, ExecutedSQL: migC.SQLDown,
		ExecutedBy: &leader.ID, ExecutedByUsername: leader.Username,
		DBNameSnapshot: conn.Name, MigrationTitleSnapshot: migC.Title,
		Status: domain.StatusSuccess, RollbackOfExecutionID: &execC.ExecutionID,
	})
	if err != nil {
		t.Fatalf("insert rollback C: %v", err)
	}
	top2, err := st.GetTopActiveMigrationForRollback(ctx, conn.ID)
	if err != nil || top2 == nil || top2.Definition.ID != migBPrime.ID || top2.ActiveExecutionID != execBPrime.ExecutionID {
		t.Fatalf("GetTopActiveMigrationForRollback after C rolled back = %+v, %v; want B'", top2, err)
	}

	// Rollback B' -> success; now only A(eff=1) is Active.
	// Linear Forward must select B'(eff=2) first before C(eff=3)!
	_, err = st.InsertMigrationExecution(ctx, domain.MigrationExecution{
		DBID: conn.ID, MigrationID: migBPrime.ID, MigrationRevision: 1,
		ActionType: domain.ActionRollback, ExecutedSQL: migBPrime.SQLDown,
		ExecutedBy: &leader.ID, ExecutedByUsername: leader.Username,
		DBNameSnapshot: conn.Name, MigrationTitleSnapshot: migBPrime.Title,
		Status: domain.StatusSuccess, RollbackOfExecutionID: &execBPrime.ExecutionID,
	})
	if err != nil {
		t.Fatalf("insert rollback B': %v", err)
	}
	nextFwd, err := st.GetNextEligibleMigrationForForward(ctx, conn.ID)
	if err != nil || nextFwd == nil || nextFwd.ID != migBPrime.ID {
		t.Fatalf("GetNextEligibleMigrationForForward = %+v, %v; want B'(eff=2)", nextFwd, err)
	}

	// Verify ListMigrationExecutions returns all 6 attempts in descending seq_order
	allExecs, err := st.ListMigrationExecutions(ctx, conn.ID)
	if err != nil || len(allExecs) != 6 {
		t.Fatalf("ListMigrationExecutions len = %d, %v; want 6", len(allExecs), err)
	}
	if allExecs[len(allExecs)-1].ExecutionID != execA.ExecutionID {
		t.Fatalf("oldest execution = %v; want execA (%v)", allExecs[len(allExecs)-1].ExecutionID, execA.ExecutionID)
	}
}
