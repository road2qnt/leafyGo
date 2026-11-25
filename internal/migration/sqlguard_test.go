package migration

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"leafygo/internal/domain"
)

func TestStatements(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		expected []string
		wantErr  bool
	}{
		{
			name:     "simple multiple statements",
			sql:      "CREATE TABLE a (id int); CREATE TABLE b (id int);",
			expected: []string{"CREATE TABLE a (id int)", "CREATE TABLE b (id int)"},
		},
		{
			name:     "semicolon in single quote string literal",
			sql:      "INSERT INTO t VALUES ('hello;world'); SELECT 1;",
			expected: []string{"INSERT INTO t VALUES ('hello;world')", "SELECT 1"},
		},
		{
			name:     "escaped single quote in string literal",
			sql:      "INSERT INTO t VALUES ('it''s a test; yes'); SELECT 2;",
			expected: []string{"INSERT INTO t VALUES ('it''s a test; yes')", "SELECT 2"},
		},
		{
			name:     "escape string literal with backslash",
			sql:      `INSERT INTO t VALUES (E'escaped \; semicolon'); SELECT 3;`,
			expected: []string{`INSERT INTO t VALUES (E'escaped \; semicolon')`, "SELECT 3"},
		},
		{
			name:     "double quotes identifier with semicolon",
			sql:      `CREATE TABLE "my;table" (id int); SELECT * FROM "my;table";`,
			expected: []string{`CREATE TABLE "my;table" (id int)`, `SELECT * FROM "my;table"`},
		},
		{
			name:     "escaped double quote",
			sql:      `CREATE TABLE "my""table;" (id int);`,
			expected: []string{`CREATE TABLE "my""table;" (id int)`},
		},
		{
			name:     "dollar quote empty tag",
			sql:      "CREATE FUNCTION f() RETURNS void AS $$ BEGIN; RETURN; END; $$ LANGUAGE plpgsql; SELECT 1;",
			expected: []string{"CREATE FUNCTION f() RETURNS void AS $$ BEGIN; RETURN; END; $$ LANGUAGE plpgsql", "SELECT 1"},
		},
		{
			name:     "dollar quote custom tag",
			sql:      "CREATE FUNCTION f() RETURNS void AS $func$ SELECT ';' AS sc; $func$; SELECT 2;",
			expected: []string{"CREATE FUNCTION f() RETURNS void AS $func$ SELECT ';' AS sc; $func$", "SELECT 2"},
		},
		{
			name:     "line comment with semicolon",
			sql:      "SELECT 1; -- this is a comment; with semicolon\nSELECT 2;",
			expected: []string{"SELECT 1", "-- this is a comment; with semicolon\nSELECT 2"},
		},
		{
			name:     "block comment with semicolon",
			sql:      "SELECT 1; /* comment with ; semicolon */ SELECT 2;",
			expected: []string{"SELECT 1", "/* comment with ; semicolon */ SELECT 2"},
		},
		{
			name:     "nested block comment",
			sql:      "SELECT 1; /* outer /* inner ; */ outer */ SELECT 2;",
			expected: []string{"SELECT 1", "/* outer /* inner ; */ outer */ SELECT 2"},
		},
		{
			name:     "empty statements ignored",
			sql:      ";;  ;\n\t; SELECT 1;  ; ;",
			expected: []string{"SELECT 1"},
		},
		{
			name:    "unclosed single quote error",
			sql:     "SELECT 'unclosed string",
			wantErr: true,
		},
		{
			name:    "unclosed double quote error",
			sql:     `SELECT "unclosed identifier`,
			wantErr: true,
		},
		{
			name:    "unclosed escape string error",
			sql:     `SELECT E'unclosed escape`,
			wantErr: true,
		},
		{
			name:    "unclosed dollar quote error",
			sql:     "SELECT $tag$ unclosed",
			wantErr: true,
		},
		{
			name:    "unclosed block comment error",
			sql:     "/* unclosed comment SELECT 1;",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Statements(tc.sql)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Statements() err = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidMigrationSQL) {
					t.Errorf("expected ErrInvalidMigrationSQL, got %v", err)
				}
				return
			}
			if len(got) != len(tc.expected) {
				t.Fatalf("Statements() returned %d statements, want %d. Got: %v", len(got), len(tc.expected), got)
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("Statements()[%d] = %q, want %q", i, got[i], tc.expected[i])
				}
			}
		})
	}
}

func TestRejectTxControl(t *testing.T) {
	rejectedTx := []string{
		"BEGIN",
		"begin;",
		"START TRANSACTION",
		"start   transaction;",
		"COMMIT",
		"commit;",
		"END",
		"end;",
		"ROLLBACK",
		"rollback;",
		"ABORT",
		"abort;",
		"SAVEPOINT sp1",
		"savepoint my_sp;",
		"RELEASE sp1",
		"RELEASE SAVEPOINT sp1",
		"release savepoint sp1;",
		"ROLLBACK TO sp1",
		"ROLLBACK TO SAVEPOINT sp1",
		"PREPARE TRANSACTION 'foo'",
		"COMMIT PREPARED 'foo'",
		"ROLLBACK PREPARED 'foo'",
		"/* leading block comment */ COMMIT",
		"/* nested /* comment */ */ ROLLBACK",
		"-- leading line comment\nBEGIN",
		"BEGIN ATOMIC\n SELECT 1;",
		// DO $$ ... $$ recursive scanning
		"DO $$ BEGIN PERFORM 1; COMMIT; END $$",
		"DO $$ BEGIN IF true THEN ROLLBACK; END IF; END $$",
		"DO LANGUAGE plpgsql $body$ BEGIN SAVEPOINT sp; END $body$",
		"DO $$ BEGIN /* comment */ START TRANSACTION; END $$",
	}

	for _, stmt := range rejectedTx {
		t.Run("reject_tx_"+stmt, func(t *testing.T) {
			err := RejectTxControl([]string{stmt})
			if !errors.Is(err, ErrInvalidMigrationSQL) {
				t.Errorf("expected ErrInvalidMigrationSQL for %q, got %v", stmt, err)
			}
		})
	}

	rejectedNonTx := []string{
		"CREATE INDEX CONCURRENTLY idx_users_email ON users(email)",
		"CREATE UNIQUE INDEX CONCURRENTLY idx_users_code ON users(code)",
		"DROP INDEX CONCURRENTLY idx_users_email",
		"REINDEX CONCURRENTLY TABLE users",
		"REINDEX TABLE CONCURRENTLY users",
		"REINDEX INDEX CONCURRENTLY idx_users_email",
		"VACUUM users",
		"VACUUM FULL",
		"CLUSTER users USING idx_users_pkey",
		"CREATE DATABASE newdb",
		"DROP DATABASE olddb",
		"CREATE TABLESPACE fastspace LOCATION '/ssd'",
		"DROP TABLESPACE fastspace",
		"ALTER SYSTEM SET work_mem = '64MB'",
		"/* comment */ CREATE INDEX /* inner */ CONCURRENTLY idx_t ON t(a)",
	}

	for _, stmt := range rejectedNonTx {
		t.Run("reject_nontx_"+stmt, func(t *testing.T) {
			err := RejectTxControl([]string{stmt})
			if !errors.Is(err, ErrNonTransactionalStmt) {
				t.Errorf("expected ErrNonTransactionalStmt for %q, got %v", stmt, err)
			}
		})
	}

	accepted := []string{
		"CREATE TABLE commitment (id int);",
		"INSERT INTO logs (action) VALUES ('COMMIT');",
		"INSERT INTO logs (action) VALUES ('BEGIN; ROLLBACK;');",
		`SELECT "ROLLBACK", "COMMIT", "BEGIN" FROM "END";`,
		"SELECT * FROM endings; -- COMMIT;",
		"/* outer /* COMMIT; ROLLBACK; */ */ SELECT 1;",
		"SET LOCAL statement_timeout = 0;",
		"ALTER TABLE users ADD COLUMN is_active boolean DEFAULT true;",
		"CREATE INDEX idx_users_email ON users(email);",
		"DROP INDEX idx_users_email;",
		"REINDEX TABLE users;",
		"DO $$ BEGIN PERFORM 1; INSERT INTO logs VALUES ('COMMIT'); END $$;",
		"DO $tag$ BEGIN UPDATE users SET name = 'ROLLBACK' WHERE id = 1; END $tag$;",
		"CREATE FUNCTION f() RETURNS void AS $$ BEGIN RETURN; END $$ LANGUAGE plpgsql;",
	}

	for _, stmt := range accepted {
		t.Run("accept_"+stmt, func(t *testing.T) {
			stmts, err := Statements(stmt)
			if err != nil {
				t.Fatalf("Statements(%q) failed: %v", stmt, err)
			}
			if err := RejectTxControl(stmts); err != nil {
				t.Errorf("expected acceptance for %q, got error: %v", stmt, err)
			}
		})
	}
}

func TestSanitizeBatchIdentifiersAndPlaceholders(t *testing.T) {
	schema, rel, quoted, err := SanitizeTableIdentifier("users")
	if err != nil || schema != "public" || rel != "users" || quoted != `"public"."users"` {
		t.Fatalf("SanitizeTableIdentifier(users) = (%q, %q, %q, %v)", schema, rel, quoted, err)
	}

	schema, rel, quoted, err = SanitizeTableIdentifier("billing.invoices")
	if err != nil || schema != "billing" || rel != "invoices" || quoted != `"billing"."invoices"` {
		t.Fatalf("SanitizeTableIdentifier(billing.invoices) = (%q, %q, %q, %v)", schema, rel, quoted, err)
	}

	for _, badTable := range []string{"", "a.b.c", ".users", "public.", "users;drop table x", `users"bad`} {
		if _, _, _, err := SanitizeTableIdentifier(badTable); !errors.Is(err, ErrInvalidBatchIdentifier) {
			t.Errorf("SanitizeTableIdentifier(%q) = %v; want ErrInvalidBatchIdentifier", badTable, err)
		}
	}

	colQuoted, err := SanitizeColumnIdentifier("cursor_id")
	if err != nil || colQuoted != `"cursor_id"` {
		t.Fatalf("SanitizeColumnIdentifier(cursor_id) = (%q, %v)", colQuoted, err)
	}
	for _, badCol := range []string{"", "public.id", "id;select 1", "123col"} {
		if _, err := SanitizeColumnIdentifier(badCol); !errors.Is(err, ErrInvalidBatchIdentifier) {
			t.Errorf("SanitizeColumnIdentifier(%q) = %v; want ErrInvalidBatchIdentifier", badCol, err)
		}
	}

	// Batch placeholders & binding
	if err := ValidateBatchSQLPlaceholders("UPDATE users SET active = true WHERE id >= {{cursor_from}} AND id <= {{cursor_to}}"); err != nil {
		t.Fatalf("expected valid {{cursor_from}}/{{cursor_to}}, got %v", err)
	}
	if err := ValidateBatchSQLPlaceholders("UPDATE users SET active = true WHERE id BETWEEN $1 AND $2"); err != nil {
		t.Fatalf("expected valid $1/$2, got %v", err)
	}
	if err := ValidateBatchSQLPlaceholders("UPDATE users SET active = true WHERE id >= $10"); !errors.Is(err, ErrInvalidMigrationSQL) {
		t.Fatalf("expected missing placeholders error for $10 only, got %v", err)
	}

	bound := BindBatchSQL("UPDATE users SET v = 1 WHERE id >= {{cursor_from}} AND id <= {{cursor_to}}", 1, 0)
	if bound != "UPDATE users SET v = 1 WHERE id >= 1 AND id <= 0" {
		t.Fatalf("BindBatchSQL braces = %q", bound)
	}
	boundPos := BindBatchSQL("UPDATE users SET v = 1 WHERE id >= $1 AND id <= $2", 101, 200)
	if boundPos != "UPDATE users SET v = 1 WHERE id >= 101 AND id <= 200" {
		t.Fatalf("BindBatchSQL positional = %q", boundPos)
	}
}

func TestDryRunGuardSingleFlightAndCooldown(t *testing.T) {
	guard := NewDryRunGuard(2 * time.Second)
	dbA := uuid.New()
	dbB := uuid.New()
	user1 := uuid.New()
	user2 := uuid.New()

	rel1, err := guard.Acquire(dbA, user1)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	// Second concurrent dry-run on same dbA by another user -> ErrDryRunBusy
	if _, err := guard.Acquire(dbA, user2); !errors.Is(err, ErrDryRunBusy) {
		t.Fatalf("concurrent same DB = %v; want ErrDryRunBusy", err)
	}

	// Concurrent dry-run on another dbB by same user1 -> ErrDryRunBusy
	if _, err := guard.Acquire(dbB, user1); !errors.Is(err, ErrDryRunBusy) {
		t.Fatalf("concurrent same user = %v; want ErrDryRunBusy", err)
	}

	// Concurrent dry-run on different dbB by different user2 -> allowed
	rel2, err := guard.Acquire(dbB, user2)
	if err != nil {
		t.Fatalf("different DB different user acquire: %v", err)
	}
	rel2()
	rel1()

	// Immediate re-acquire by user1 within 2s cooldown -> ErrDryRunBusy
	if _, err := guard.Acquire(dbA, user1); !errors.Is(err, ErrDryRunBusy) {
		t.Fatalf("cooldown violation = %v; want ErrDryRunBusy", err)
	}
}

func TestClassifyDryRunExecError(t *testing.T) {
	for _, code := range []string{"57014", "55P03"} {
		res, err := classifyDryRunExecError(&pgconn.PgError{Code: code, Message: "timeout"})
		if err != nil {
			t.Fatalf("SQLSTATE %s should be inconclusive without error, got %v", code, err)
		}
		if res.Status != DryRunStatusInconclusive || res.SQLState != code || res.Warning == "" {
			t.Fatalf("SQLSTATE %s result = %+v", code, res)
		}
	}

	for _, code := range []string{"42601", "42P01", "42703"} {
		_, err := classifyDryRunExecError(&pgconn.PgError{Code: code, Message: "syntax or catalog error"})
		if !errors.Is(err, ErrInvalidMigrationSQL) {
			t.Fatalf("SQLSTATE %s err = %v; want ErrInvalidMigrationSQL", code, err)
		}
	}
}

func TestValidateDefinitionLexical(t *testing.T) {
	def := domain.MigrationDefinition{
		Title:         "batch mig",
		SQLUp:         "UPDATE users SET active = true WHERE id >= {{cursor_from}} AND id <= {{cursor_to}};",
		SQLDown:       "UPDATE users SET active = false WHERE id >= {{cursor_from}} AND id <= {{cursor_to}};",
		ExecutionMode: domain.ExecutionModeBatch,
		BatchConfig: &domain.BatchConfig{
			TableName: "public.users",
			CursorCol: "id",
			BatchSize: 1000,
		},
	}
	if err := ValidateDefinitionLexical(def, domain.ActionApply); err != nil {
		t.Fatalf("ValidateDefinitionLexical valid batch: %v", err)
	}

	// resolve_uncertain skips SQL lexical validation
	badDef := def
	badDef.SQLUp = "COMMIT;"
	if err := ValidateDefinitionLexical(badDef, domain.ActionResolveUncertain); err != nil {
		t.Fatalf("resolve_uncertain should skip SQL validation, got %v", err)
	}
}

func TestIsExplainable(t *testing.T) {
	valid := []string{
		"SELECT 1;",
		"select * from users;",
		"WITH cte AS (SELECT 1) SELECT * FROM cte;",
		"with cte as (select 1) select 2;",
		"INSERT INTO users (id) VALUES (1);",
		"insert into users values (2);",
		"UPDATE users SET name='test';",
		"update users set id=1;",
		"DELETE FROM users WHERE id=1;",
		"delete from users;",
		"MERGE INTO target USING source ON target.id = source.id WHEN MATCHED THEN UPDATE SET name=source.name;",
		"merge into t using s on t.id = s.id when not matched then insert values (1);",
		"VALUES (1, 'a'), (2, 'b');",
		"values (1);",
		"   \n\t  SELECT 1;",
		"-- line comment\nSELECT 2;",
		"/* block comment */ SELECT 3;",
		"/* outer /* inner */ */ SELECT 4;",
		"-- comment 1\n/* comment 2 */\n\n  INSERT INTO t VALUES (1);",
	}

	for _, stmt := range valid {
		t.Run("valid_"+stmt, func(t *testing.T) {
			if !IsExplainable(stmt) {
				t.Errorf("expected statement to be explainable: %q", stmt)
			}
		})
	}

	invalid := []string{
		"CREATE TABLE test (id int);",
		"create index idx on test(id);",
		"ALTER TABLE test ADD COLUMN name text;",
		"DROP TABLE test;",
		"TRUNCATE TABLE test;",
		"GRANT ALL ON test TO public;",
		"REVOKE ALL ON test FROM public;",
		"VACUUM FULL;",
		"ANALYZE test;",
		"SET search_path TO test;",
		"SELECTING 1;",
		"UPDATER set a=1;",
		"",
		"   ",
		"-- only comment\n",
		"/* only block comment */",
	}

	for _, stmt := range invalid {
		t.Run("invalid_"+stmt, func(t *testing.T) {
			if IsExplainable(stmt) {
				t.Errorf("expected statement to NOT be explainable: %q", stmt)
			}
		})
	}
}
