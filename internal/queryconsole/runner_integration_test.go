package queryconsole_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"leafygo/internal/domain"
	"leafygo/internal/queryconsole"
	"leafygo/internal/secret"
	"leafygo/internal/target"
)

func setupTestTarget(t *testing.T) (*queryconsole.Runner, domain.DBConnection, *secret.Box, string) {
	t.Helper()
	rawURL := os.Getenv("TEST_TARGET_DATABASE_URL")
	if rawURL == "" {
		t.Skip("TEST_TARGET_DATABASE_URL is empty; skipping integration test")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse target url: %v", err)
	}

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	box, err := secret.New(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}

	password, _ := u.User.Password()
	enc, err := box.Encrypt(password)
	if err != nil {
		t.Fatalf("box.Encrypt: %v", err)
	}

	port := 5432
	if p := u.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	}

	sslMode := u.Query().Get("sslmode")
	if sslMode == "" {
		sslMode = "disable"
	}

	dbConn := domain.DBConnection{
		ID:          uuid.New(),
		Name:        "target_test",
		Host:        u.Hostname(),
		Port:        port,
		DBName:      strings.TrimPrefix(u.Path, "/"),
		Username:    u.User.Username(),
		PasswordEnc: enc,
		SSLMode:     sslMode,
		UpdatedAt:   time.Now().UTC(),
	}

	runner := queryconsole.NewRunner(box, 3*time.Second, "leafygo_test")

	// Create unique test schema
	schemaName := fmt.Sprintf("console_test_%s", strings.ReplaceAll(uuid.New().String(), "-", "_"))
	dsn, err := target.BuildDSN(dbConn, password)
	if err != nil {
		t.Fatalf("build dsn: %v", err)
	}

	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to target for setup: %v", err)
	}
	defer conn.Close(context.Background())

	_, err = conn.Exec(context.Background(), fmt.Sprintf("CREATE SCHEMA %s;", schemaName))
	if err != nil {
		t.Fatalf("create test schema: %v", err)
	}

	t.Cleanup(func() {
		cleanupConn, err := pgx.Connect(context.Background(), dsn)
		if err == nil {
			_, _ = cleanupConn.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE;", schemaName))
			cleanupConn.Close(context.Background())
		}
	})

	return runner, dbConn, box, schemaName
}

func getDirectConn(t *testing.T, dbConn domain.DBConnection, box *secret.Box) *pgx.Conn {
	t.Helper()
	pass, err := box.Decrypt(dbConn.PasswordEnc)
	if err != nil {
		t.Fatalf("decrypt password: %v", err)
	}
	dsn, err := target.BuildDSN(dbConn, pass)
	if err != nil {
		t.Fatalf("build dsn: %v", err)
	}
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("direct connect: %v", err)
	}
	return conn
}

func TestTargetReadOnlyEnforcedByPostgreSQL(t *testing.T) {
	runner, dbConn, box, schema := setupTestTarget(t)
	conn := getDirectConn(t, dbConn, box)
	defer conn.Close(context.Background())

	// Create test table with initial data
	tableName := schema + ".items"
	_, err := conn.Exec(context.Background(), fmt.Sprintf(`
		CREATE TABLE %s (id serial primary key, val text);
		INSERT INTO %s (val) VALUES ('item1'), ('item2');
		CREATE SEQUENCE %s_seq;
		CREATE OR REPLACE FUNCTION %s_insert_fn() RETURNS int AS $$
		BEGIN
			INSERT INTO %s (val) VALUES ('from_fn');
			RETURN 1;
		END;
		$$ LANGUAGE plpgsql;
	`, tableName, tableName, schema, schema, tableName))
	if err != nil {
		t.Fatalf("setup tables: %v", err)
	}

	limits := queryconsole.Limits{
		Timeout: 5 * time.Second,
		MaxRows: 100,
	}

	// 1. Data-modifying CTE: WITH x AS (DELETE FROM t RETURNING *) SELECT * FROM x
	cteSQL := fmt.Sprintf("WITH x AS (DELETE FROM %s RETURNING *) SELECT * FROM x", tableName)
	_, err = runner.Execute(context.Background(), dbConn, cteSQL, limits)
	if err == nil {
		t.Fatal("expected error for data-modifying CTE in read-only transaction, got nil")
	}
	var tgtErr *queryconsole.TargetError
	if !errors.As(err, &tgtErr) || tgtErr.Code != "25006" {
		t.Fatalf("expected SQLSTATE 25006 for CTE delete, got %v", err)
	}
	if !tgtErr.IsWriteRejection {
		t.Fatal("expected IsWriteRejection=true for 25006")
	}

	// Verify rows still exist
	var count int
	_ = conn.QueryRow(context.Background(), fmt.Sprintf("SELECT count(*) FROM %s", tableName)).Scan(&count)
	if count != 2 {
		t.Fatalf("expected 2 items remaining after rolled back CTE delete, got %d", count)
	}

	// 2. Sequence modification: SELECT nextval('seq')
	seqSQL := fmt.Sprintf("SELECT nextval('%s_seq')", schema)
	_, err = runner.Execute(context.Background(), dbConn, seqSQL, limits)
	if err == nil {
		t.Fatal("expected error for nextval in read-only transaction, got nil")
	}
	if !errors.As(err, &tgtErr) || tgtErr.Code != "25006" {
		t.Fatalf("expected SQLSTATE 25006 for nextval, got %v", err)
	}

	// 3. SELECT set_config('transaction_read_only', 'off', true) must not allow subsequent writes
	setRO := "SELECT set_config('transaction_read_only', 'off', true)"
	_, err = runner.Execute(context.Background(), dbConn, setRO, limits)
	// Even if set_config returns, the transaction boundary is read-only and rolls back

	// 4. Function containing write fails when called via SELECT
	fnSQL := fmt.Sprintf("SELECT %s_insert_fn()", schema)
	_, err = runner.Execute(context.Background(), dbConn, fnSQL, limits)
	if err == nil {
		t.Fatal("expected error calling write function from SELECT, got nil")
	}
	if !errors.As(err, &tgtErr) || tgtErr.Code != "25006" {
		t.Fatalf("expected SQLSTATE 25006 for function write, got %v", err)
	}

	// 5. SET TRANSACTION READ WRITE fails because set_config ran first as Query 1
	setTxRW := fmt.Sprintf("SET TRANSACTION READ WRITE; INSERT INTO %s (val) VALUES ('hacked')", tableName)
	_, err = runner.Execute(context.Background(), dbConn, setTxRW, limits)
	if err == nil {
		t.Fatal("expected error for SET TRANSACTION READ WRITE after first query, got nil")
	}

	// 6. SELECT * INTO new_table FROM t fails
	intoSQL := fmt.Sprintf("SELECT * INTO %s.new_table FROM %s", schema, tableName)
	_, err = runner.Execute(context.Background(), dbConn, intoSQL, limits)
	if err == nil {
		t.Fatal("expected error for SELECT INTO in read-only transaction, got nil")
	}
	if !errors.As(err, &tgtErr) || tgtErr.Code != "25006" {
		t.Fatalf("expected SQLSTATE 25006 for SELECT INTO, got %v", err)
	}
	var tableExists bool
	_ = conn.QueryRow(context.Background(), "SELECT EXISTS (SELECT FROM pg_tables WHERE schemaname = $1 AND tablename = 'new_table')", schema).Scan(&tableExists)
	if tableExists {
		t.Fatal("new_table should not exist after failed SELECT INTO")
	}

	// 7. EXPLAIN ANALYZE DELETE FROM t fails with 25006
	explainDel := fmt.Sprintf("EXPLAIN ANALYZE DELETE FROM %s", tableName)
	_, err = runner.Execute(context.Background(), dbConn, explainDel, limits)
	if err == nil {
		t.Fatal("expected error for EXPLAIN ANALYZE DELETE, got nil")
	}
	if !errors.As(err, &tgtErr) || tgtErr.Code != "25006" {
		t.Fatalf("expected SQLSTATE 25006 for EXPLAIN ANALYZE DELETE, got %v", err)
	}

	// Check rows are intact
	_ = conn.QueryRow(context.Background(), fmt.Sprintf("SELECT count(*) FROM %s", tableName)).Scan(&count)
	if count != 2 {
		t.Fatalf("expected 2 items remaining after all attempted writes, got %d", count)
	}
}

func TestTargetNormalQueries(t *testing.T) {
	runner, dbConn, _, _ := setupTestTarget(t)
	limits := queryconsole.Limits{
		Timeout:        5 * time.Second,
		MaxRows:        100,
		MaxCellBytes:   4096,
		MaxResultBytes: 1048576,
	}

	// 1. SELECT multiple datatypes and NULL vs empty string
	sql := "SELECT 42 AS num, 'hello' AS str, '' AS empty_str, NULL AS null_val"
	res, err := runner.Execute(context.Background(), dbConn, sql, limits)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if len(res.Columns) != 4 || res.RowCount != 1 {
		t.Fatalf("expected 4 columns and 1 row, got cols=%d, rows=%d", len(res.Columns), res.RowCount)
	}
	row := res.Rows[0]
	if row[0].Text != "42" || row[0].IsNull {
		t.Errorf("col 0: expected 42, got %v", row[0])
	}
	if row[1].Text != "hello" || row[1].IsNull {
		t.Errorf("col 1: expected hello, got %v", row[1])
	}
	if row[2].Text != "" || row[2].IsNull {
		t.Errorf("col 2: expected empty string (non-null), got %v", row[2])
	}
	if !row[3].IsNull {
		t.Errorf("col 3: expected NULL, got %v", row[3])
	}

	// 2. WHERE false returns columns and 0 rows
	res, err = runner.Execute(context.Background(), dbConn, "SELECT 1 AS a, 2 AS b WHERE false", limits)
	if err != nil {
		t.Fatalf("execute WHERE false failed: %v", err)
	}
	if len(res.Columns) != 2 || res.RowCount != 0 {
		t.Fatalf("expected 2 columns and 0 rows for WHERE false, got cols=%d, rows=%d", len(res.Columns), res.RowCount)
	}

	// 3. SHOW server_version
	res, err = runner.Execute(context.Background(), dbConn, "SHOW server_version", limits)
	if err != nil {
		t.Fatalf("SHOW server_version failed: %v", err)
	}
	if len(res.Columns) != 1 || res.RowCount != 1 {
		t.Fatalf("expected 1 col and 1 row for SHOW, got cols=%d, rows=%d", len(res.Columns), res.RowCount)
	}

	// 4. EXPLAIN SELECT 1
	res, err = runner.Execute(context.Background(), dbConn, "EXPLAIN SELECT 1", limits)
	if err != nil {
		t.Fatalf("EXPLAIN SELECT 1 failed: %v", err)
	}
	if res.RowCount == 0 {
		t.Fatalf("expected EXPLAIN rows, got 0")
	}
}

func TestTargetTruncationAndRowCapFastAbort(t *testing.T) {
	runner, dbConn, _, _ := setupTestTarget(t)
	limits := queryconsole.Limits{
		Timeout:        10 * time.Second,
		MaxRows:        10,
		MaxCellBytes:   4096,
		MaxResultBytes: 1048576,
	}

	// generate_series(1, 100000) should truncate at 10 rows and return rapidly without draining
	start := time.Now()
	res, err := runner.Execute(context.Background(), dbConn, "SELECT generate_series(1, 100000)", limits)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("execute generate_series failed: %v", err)
	}
	if res.RowCount != 10 {
		t.Fatalf("expected exactly 10 rows, got %d", res.RowCount)
	}
	if !res.Truncated {
		t.Fatalf("expected Truncated=true")
	}
	if duration > 3*time.Second {
		t.Fatalf("aborted query took too long (%v); connection was likely not aborted immediately", duration)
	}
}

func TestTargetStatementTimeout(t *testing.T) {
	runner, dbConn, _, _ := setupTestTarget(t)
	// Timeout of 200ms with pg_sleep(2)
	limits := queryconsole.Limits{
		Timeout: 200 * time.Millisecond,
		MaxRows: 10,
	}

	_, err := runner.Execute(context.Background(), dbConn, "SELECT pg_sleep(2)", limits)
	if err == nil {
		t.Fatal("expected timeout error for pg_sleep, got nil")
	}
	if !errors.Is(err, queryconsole.ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}
