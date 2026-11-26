package target

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"leafygo/internal/domain"
)

const targetLockSQL = `select pg_try_advisory_lock(hashtextextended('leafygo_target_migration', 0))`

const evidenceTablesSQL = `
create table if not exists _leafygo_executions (
    execution_id uuid primary key,
    migration_id uuid not null,
    action_type  text not null,
    lock_epoch   bigint not null,
    committed_at timestamptz not null default now()
);
create table if not exists _leafygo_batch_checkpoints (
    root_execution_id        uuid not null,
    batch_no                 int not null,
    written_by_execution_id  uuid not null,
    cursor_from              bigint not null,
    cursor_to                bigint not null,
    rows_affected            bigint not null,
    lock_epoch               bigint not null,
    committed_at             timestamptz not null default now(),
    reverted_at              timestamptz,
    reverted_by_execution_id uuid,
    primary key (root_execution_id, batch_no)
)`

var ErrTargetBusy = errors.New("another session holds the target migration lock")

// Names the session of one execution so recovery can find it in pg_stat_activity.
func ExecAppName(executionID uuid.UUID) string { return "leafygo-exec-" + executionID.String() }

// Opens a direct, non pooled connection. Session advisory locks and the per execution
// application_name both need the same backend for the whole execution.
func OpenConn(ctx context.Context, db domain.DBConnection, password, appName string, connectTimeout time.Duration) (*pgx.Conn, error) {
	dsn, err := buildDSN(db, password)
	if err != nil {
		return nil, &RunError{Err: fmt.Errorf("build dsn: %w", err)}
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, &RunError{Err: fmt.Errorf("parse config: %w", err)}
	}
	cfg.ConnectTimeout = connectTimeout
	cfg.RuntimeParams["application_name"] = appName
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, &RunError{Err: errors.New(sanitizeTargetError(err))}
	}
	return conn, nil
}

// Opens the execution connection, creates the evidence tables and takes the target advisory lock.
func OpenExecConn(ctx context.Context, db domain.DBConnection, password string, executionID uuid.UUID, connectTimeout time.Duration) (*pgx.Conn, error) {
	conn, err := OpenConn(ctx, db, password, ExecAppName(executionID), connectTimeout)
	if err != nil {
		return nil, err
	}
	ok, err := TryTargetLock(ctx, conn)
	if err != nil {
		CloseConn(conn)
		return nil, err
	}
	if !ok {
		CloseConn(conn)
		return nil, ErrTargetBusy
	}
	if _, err := conn.Exec(ctx, evidenceTablesSQL, pgx.QueryExecModeSimpleProtocol); err != nil {
		CloseConn(conn)
		return nil, &RunError{Err: fmt.Errorf("create evidence tables: %s", sanitizeTargetError(err))}
	}
	return conn, nil
}

func TryTargetLock(ctx context.Context, conn *pgx.Conn) (bool, error) {
	var ok bool
	if err := conn.QueryRow(ctx, targetLockSQL).Scan(&ok); err != nil {
		return false, &RunError{Err: fmt.Errorf("target advisory lock: %s", sanitizeTargetError(err))}
	}
	return ok, nil
}

func CloseConn(conn *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = conn.Close(ctx)
}

// Severs the socket immediately. Safe to call from another goroutine while a query is running.
func AbortConn(conn *pgx.Conn) {
	if nc := conn.PgConn().Conn(); nc != nil {
		_ = nc.Close()
	}
}

func setTxTimeout(ctx context.Context, tx pgx.Tx, timeout time.Duration) error {
	if timeout <= 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `select set_config('statement_timeout', $1, true)`, strconv.FormatInt(timeout.Milliseconds(), 10))
	return err
}

// Runs the whole migration and its evidence row in one transaction. Errors are *RunError,
// with Uncertain set when the commit itself failed.
func RunNormal(ctx context.Context, conn *pgx.Conn, sql string, executionID, migrationID uuid.UUID, action string, epoch int64, timeout time.Duration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return classifyRunError(phaseConnect, err)
	}
	defer rollbackQuietly(tx)

	if err := setTxTimeout(ctx, tx, timeout); err != nil {
		return classifyRunError(phaseStatement, err)
	}
	if _, err := tx.Exec(ctx, sql, pgx.QueryExecModeSimpleProtocol); err != nil {
		return classifyRunError(phaseStatement, err)
	}
	if _, err := tx.Exec(ctx, `
		insert into _leafygo_executions (execution_id, migration_id, action_type, lock_epoch)
		values ($1, $2, $3, $4)`, executionID, migrationID, action, epoch); err != nil {
		return classifyRunError(phaseStatement, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return classifyRunError(phaseCommit, err)
	}
	return nil
}

func rollbackQuietly(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

// Reports whether a normal mode execution committed. A missing evidence table means nothing committed.
func NormalEvidence(ctx context.Context, conn *pgx.Conn, executionID uuid.UUID) (bool, error) {
	var exists bool
	if err := conn.QueryRow(ctx, `select to_regclass('_leafygo_executions') is not null`).Scan(&exists); err != nil {
		return false, fmt.Errorf("read execution evidence: %s", sanitizeTargetError(err))
	}
	if !exists {
		return false, nil
	}
	var found bool
	err := conn.QueryRow(ctx, `select exists (select 1 from _leafygo_executions where execution_id = $1)`, executionID).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("read execution evidence: %s", sanitizeTargetError(err))
	}
	return found, nil
}

// Returns backend pids still connected under the given application_name.
func SessionPIDs(ctx context.Context, conn *pgx.Conn, appName string) ([]int32, error) {
	rows, err := conn.Query(ctx, `
		select pid from pg_stat_activity
		where application_name = $1 and pid <> pg_backend_pid()`, appName)
	if err != nil {
		return nil, fmt.Errorf("read pg_stat_activity: %s", sanitizeTargetError(err))
	}
	return pgx.CollectRows(rows, pgx.RowTo[int32])
}

func TerminateSession(ctx context.Context, conn *pgx.Conn, pid int32) error {
	_, err := conn.Exec(ctx, `select pg_terminate_backend($1)`, pid)
	return err
}
