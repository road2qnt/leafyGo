package migration

import (
	"testing"
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
	rejected := []string{
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
		"release savepoint sp1;",
		"PREPARE TRANSACTION 'foo'",
		"COMMIT PREPARED 'foo'",
		"ROLLBACK PREPARED 'foo'",
		"/* leading block comment */ COMMIT",
		"/* nested /* comment */ */ ROLLBACK",
		"-- leading line comment\nBEGIN",
		"BEGIN ATOMIC\n SELECT 1;", // Documented false-positive
	}

	for _, stmt := range rejected {
		t.Run("reject_"+stmt, func(t *testing.T) {
			err := RejectTxControl([]string{stmt})
			if err == nil {
				t.Errorf("expected rejection for %q, got nil", stmt)
			}
		})
	}

	accepted := []string{
		"CREATE TABLE commitment (id int);",
		"INSERT INTO logs (action) VALUES ('beginning');",
		"SELECT * FROM endings;",
		"SET LOCAL statement_timeout = 0;",
		"ALTER TABLE users ADD COLUMN is_active boolean DEFAULT true;",
		"/* comment */ SELECT 1;",
		"-- comment\nSELECT 2;",
	}

	for _, stmt := range accepted {
		t.Run("accept_"+stmt, func(t *testing.T) {
			err := RejectTxControl([]string{stmt})
			if err != nil {
				t.Errorf("expected acceptance for %q, got error: %v", stmt, err)
			}
		})
	}
}
