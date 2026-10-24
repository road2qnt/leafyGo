package store_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"leafygo/internal/auth"
	"leafygo/internal/domain"
	"leafygo/internal/store"
)

func testPool(t *testing.T) *store.Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is empty; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	st := store.New(pool)
	if _, err := pool.Exec(context.Background(), `
		truncate users, groups, group_members, db_connections, db_access, sessions`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return st
}

func TestEffectiveLevelHighestWins(t *testing.T) {
	st := testPool(t)
	ctx := context.Background()

	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	user, err := st.CreateUser(ctx, "ega", hash, false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	admin, err := st.CreateUser(ctx, "admin", hash, true)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}

	g1, err := st.CreateGroup(ctx, "g-view", "")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	g2, err := st.CreateGroup(ctx, "g-edit", "")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	conn, err := st.CreateDBConnection(ctx, domain.DBConnection{
		Name: "demo", Host: "localhost", Port: 5432, DBName: "demo",
		Username: "u", PasswordEnc: []byte("enc"), SSLMode: "disable",
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}

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
