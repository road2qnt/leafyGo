package platform_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"leafygo/internal/platform"
)

func testDB(t *testing.T) (*pgxpool.Pool, string) {
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
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, url
}

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(context.Background(), `
		select exists(
			select 1 from information_schema.tables
			where table_schema='public' and table_name=$1)`, name).Scan(&exists); err != nil {
		t.Fatalf("check table %s: %v", name, err)
	}
	return exists
}

func TestMigrateCreatesMigrationTables(t *testing.T) {
	pool, _ := testDB(t)
	ctx := context.Background()

	for _, name := range []string{"migration_history", "migration_drafts"} {
		if !tableExists(t, pool, name) {
			t.Fatalf("table %s does not exist", name)
		}
	}

	var nullable, def string
	if err := pool.QueryRow(ctx, `
		select is_nullable, coalesce(column_default, '')
		from information_schema.columns
		where table_schema='public' and table_name='users' and column_name='is_active'`).
		Scan(&nullable, &def); err != nil {
		t.Fatalf("users.is_active column: %v", err)
	}
	if nullable != "NO" {
		t.Fatalf("users.is_active nullable = %s; want NO", nullable)
	}
	if !strings.Contains(strings.ToLower(def), "true") {
		t.Fatalf("users.is_active default = %q; want true", def)
	}

	var uniqueFound bool
	rows, err := pool.Query(ctx, `
		select indexdef from pg_indexes
		where schemaname='public' and tablename='migration_history'`)
	if err != nil {
		t.Fatalf("query indexes: %v", err)
	}
	for rows.Next() {
		var def string
		if err := rows.Scan(&def); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		lower := strings.ToLower(def)
		if strings.Contains(lower, "unique") && strings.Contains(lower, "db_id, version") {
			uniqueFound = true
		}
	}
	rows.Close()
	if !uniqueFound {
		t.Fatal("unique (db_id, version) index/constraint not found")
	}

	for _, name := range []string{"migration_history_running_idx", "migration_drafts_db_updated_idx"} {
		var exists bool
		if err := pool.QueryRow(ctx, `
			select exists(select 1 from pg_indexes
			where schemaname='public' and indexname=$1)`, name).Scan(&exists); err != nil {
			t.Fatalf("check index %s: %v", name, err)
		}
		if !exists {
			t.Fatalf("index %s does not exist", name)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	pool, url := testDB(t)
	if err := platform.Migrate(context.Background(), url); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `
		select count(*) from schema_migrations where version in (2,3,4)`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 3 {
		t.Fatalf("migration rows for 2,3,4 = %d; want 3", count)
	}
}

func createTestDBConnection(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		insert into db_connections (id, name, host, port, dbname, username, password_enc, sslmode)
		values ($1,$2,'localhost',5432,'demo','u',$3,'disable')`,
		id, "plan01-"+id.String(), []byte("enc"))
	if err != nil {
		t.Fatalf("insert db connection: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from db_connections where id=$1`, id)
	})
	return id
}

func TestMigrationHistoryCheckConstraint(t *testing.T) {
	pool, _ := testDB(t)
	ctx := context.Background()
	dbID := createTestDBConnection(t, pool)

	_, err := pool.Exec(ctx, `
		insert into migration_history (id, db_id, version, sql_text, started_at, status)
		values ($1,$2,1,'select 1',now(),'bogus')`, uuid.New(), dbID)
	if err == nil {
		t.Fatal("expected check constraint failure for invalid status")
	}

	if _, err := pool.Exec(ctx, `
		insert into migration_history (id, db_id, version, sql_text, started_at, finished_at, status)
		values ($1,$2,2,'select 1',now(),NULL,'running')`, uuid.New(), dbID); err != nil {
		t.Fatalf("insert running history: %v", err)
	}
}

func TestMigrationCascade(t *testing.T) {
	pool, _ := testDB(t)
	ctx := context.Background()
	dbID := createTestDBConnection(t, pool)

	historyID := uuid.New()
	if _, err := pool.Exec(ctx, `
		insert into migration_history (id, db_id, version, sql_text, started_at, finished_at, status)
		values ($1,$2,1,'select 1',now(),now(),'success')`, historyID, dbID); err != nil {
		t.Fatalf("insert history: %v", err)
	}
	draftID := uuid.New()
	if _, err := pool.Exec(ctx, `
		insert into migration_drafts (id, db_id, title, sql_text, revision, created_at, updated_at)
		values ($1,$2,'draft','select 1',1,now(),now())`, draftID, dbID); err != nil {
		t.Fatalf("insert draft: %v", err)
	}

	if _, err := pool.Exec(ctx, `delete from db_connections where id=$1`, dbID); err != nil {
		t.Fatalf("delete db connection: %v", err)
	}

	var historyCount, draftCount int
	if err := pool.QueryRow(ctx, `select count(*) from migration_history where id=$1`, historyID).Scan(&historyCount); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from migration_drafts where id=$1`, draftID).Scan(&draftCount); err != nil {
		t.Fatalf("count draft: %v", err)
	}
	if historyCount != 0 || draftCount != 0 {
		t.Fatalf("cascade left history=%d draft=%d", historyCount, draftCount)
	}

}
