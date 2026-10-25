package queryconsole_test

import (
	"errors"
	"strings"
	"testing"

	"leafygo/internal/queryconsole"
)

func TestValidateAllowlist(t *testing.T) {
	valid := []string{
		"SELECT 1",
		"select * from users",
		"  \n\t SELECT id FROM products;",
		"-- line comment\nSELECT 42",
		"/* block comment */ SELECT version()",
		"/* nested /* comment */ */ WITH t AS (SELECT 1) SELECT * FROM t",
		"with x as (select 1) select * from x;",
		"TABLE my_table;",
		"table schema.table",
		"VALUES (1, 'a'), (2, 'b')",
		"values (1);",
		"SHOW server_version",
		"show timezone;",
		"EXPLAIN SELECT 1",
		"explain (format text) select 1;",
		"SELECT 'semi;colon;in;string'",
		"SELECT $$semi;colon;in;dollar;quote$$;",
	}

	for _, sql := range valid {
		t.Run(sql, func(t *testing.T) {
			stmt, err := queryconsole.Validate(sql, 65536)
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", sql, err)
			}
			if stmt == "" {
				t.Fatalf("expected non-empty validated statement for %q", sql)
			}
		})
	}
}

func TestValidateDisallowedKeywords(t *testing.T) {
	disallowed := []string{
		"INSERT INTO t VALUES (1)",
		"insert into users (name) values ('a')",
		"UPDATE t SET x = 1",
		"update users set active = false",
		"DELETE FROM t WHERE id = 1",
		"delete from products",
		"CREATE TABLE new_table (id int)",
		"create index idx on t(x)",
		"ALTER TABLE t ADD COLUMN c text",
		"DROP TABLE t",
		"drop schema s cascade",
		"SET search_path TO public",
		"set timezone = 'UTC'",
		"RESET search_path",
		"DO $$ BEGIN NULL; END $$",
		"CALL my_procedure()",
		"COPY t FROM '/tmp/data.csv'",
		"LOCK TABLE t IN ACCESS EXCLUSIVE MODE",
		"BEGIN",
		"COMMIT",
		"ROLLBACK",
		"START TRANSACTION",
		"SAVEPOINT sp",
		"-- comment\nINSERT INTO t VALUES (1)",
		"/* block */ DELETE FROM t",
		"DROP DATABASE leafygo",
		"GRANT ALL ON t TO public",
	}

	for _, sql := range disallowed {
		t.Run(sql, func(t *testing.T) {
			_, err := queryconsole.Validate(sql, 65536)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", sql)
			}
			var tgtErr *queryconsole.TargetError
			if !errors.As(err, &tgtErr) {
				t.Fatalf("expected *TargetError for %q, got %T: %v", sql, err, err)
			}
			if !tgtErr.IsWriteRejection {
				t.Fatalf("expected IsWriteRejection=true for %q", sql)
			}
			if tgtErr.Message != queryconsole.MsgWriteRejection {
				t.Fatalf("expected message %q, got %q", queryconsole.MsgWriteRejection, tgtErr.Message)
			}
		})
	}
}

func TestValidateMultiStatement(t *testing.T) {
	multi := []string{
		"SELECT 1; SELECT 2",
		"SELECT 1; SELECT 2;",
		"SELECT 1; \n SELECT 2",
		"SHOW search_path; SHOW timezone;",
	}

	for _, sql := range multi {
		t.Run(sql, func(t *testing.T) {
			_, err := queryconsole.Validate(sql, 65536)
			if err == nil {
				t.Fatalf("expected error for multi-statement %q, got nil", sql)
			}
			if !errors.Is(err, queryconsole.ErrValidation) {
				t.Fatalf("expected ErrValidation for %q, got %v", sql, err)
			}
			if !strings.Contains(err.Error(), "only runs one statement at a time") {
				t.Fatalf("expected statement count message, got %q", err.Error())
			}
		})
	}
}

func TestValidateEmptyAndSize(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		max  int64
		msg  string
	}{
		{"empty", "", 65536, "must not be empty"},
		{"whitespace", "   \t\n  ", 65536, "must not be empty"},
		{"comments only", "-- just a comment\n", 65536, "must not be empty"},
		{"nul character", "SELECT \x00 1", 65536, "must not contain NUL"},
		{"size exceeded", "SELECT 1", 4, "must be at most 4 bytes"},
		{"unclosed string", "SELECT 'unterminated", 65536, "unclosed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := queryconsole.Validate(tt.sql, tt.max)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
			if !errors.Is(err, queryconsole.ErrValidation) {
				t.Fatalf("expected ErrValidation, got %v", err)
			}
			if !strings.Contains(err.Error(), tt.msg) {
				t.Fatalf("expected message containing %q, got %q", tt.msg, err.Error())
			}
		})
	}
}
