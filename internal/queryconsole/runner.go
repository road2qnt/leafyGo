package queryconsole

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"leafygo/internal/domain"
	"leafygo/internal/secret"
	"leafygo/internal/target"
)

// QueryRunner executes an ad-hoc SQL query against a target database.
type QueryRunner interface {
	Execute(ctx context.Context, db domain.DBConnection, sql string, limits Limits) (Result, error)
}

// Runner implements QueryRunner using one-shot target connections and read-only transactions.
type Runner struct {
	box            *secret.Box
	connectTimeout time.Duration
	appName        string
}

// NewRunner creates a new Runner.
func NewRunner(box *secret.Box, connectTimeout time.Duration, appName string) *Runner {
	if connectTimeout <= 0 {
		connectTimeout = 5 * time.Second
	}
	if appName == "" {
		appName = "leafygo"
	}
	return &Runner{
		box:            box,
		connectTimeout: connectTimeout,
		appName:        appName,
	}
}

func truncateToBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	s = s[:maxBytes]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s
}

// ClassifyTargetQueryError maps target query execution errors to queryconsole error types.
func ClassifyTargetQueryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrTimeout
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "55P03": // lock_not_available
			return &TargetError{
				Message:  "Table is currently locked. Try again later.",
				Code:     pgErr.Code,
				Position: int(pgErr.Position),
			}
		case "57014": // query_canceled (statement_timeout)
			return ErrTimeout
		case "25006": // read_only_sql_transaction
			return &TargetError{
				Message:          MsgWriteRejection,
				Code:             pgErr.Code,
				Position:         int(pgErr.Position),
				IsWriteRejection: true,
			}
		default:
			sanitized := target.ErrorText(err)
			return &TargetError{
				Message:  sanitized,
				Code:     pgErr.Code,
				Position: int(pgErr.Position),
			}
		}
	}

	sanitized := target.ErrorText(err)
	lower := strings.ToLower(sanitized)
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline") {
		return ErrTimeout
	}
	return &TargetError{
		Message: sanitized,
	}
}

// CalculateStatementTimeout computes the statement_timeout budget from limits and context deadline.
// It guarantees a minimum of 1ms and never 0.
func CalculateStatementTimeout(ctx context.Context, limitTimeout time.Duration) time.Duration {
	remaining := limitTimeout
	if remaining <= 0 {
		remaining = 15 * time.Second
	}
	if deadline, ok := ctx.Deadline(); ok {
		rem := time.Until(deadline)
		if rem <= 0 {
			remaining = time.Millisecond
		} else if rem < remaining {
			remaining = rem
		}
	}
	if remaining < time.Millisecond {
		remaining = time.Millisecond
	}
	return remaining
}

// Execute executes a single SQL query in a read-only transaction on a one-shot connection.
// It executes set_config as Query 1 and user SQL as Query 2 via pgx extended protocol.
func (r *Runner) Execute(ctx context.Context, db domain.DBConnection, sql string, limits Limits) (Result, error) {
	password, err := r.box.Decrypt(db.PasswordEnc)
	if err != nil {
		return Result{}, ClassifyTargetQueryError(fmt.Errorf("decrypt credentials: %w", err))
	}

	dsn, err := target.BuildDSN(db, password)
	if err != nil {
		return Result{}, ClassifyTargetQueryError(fmt.Errorf("build dsn: %w", err))
	}

	connCfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return Result{}, ClassifyTargetQueryError(fmt.Errorf("parse config: %w", err))
	}
	connCfg.ConnectTimeout = r.connectTimeout

	conn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		return Result{}, ClassifyTargetQueryError(err)
	}

	var connClosed bool
	defer func() {
		if !connClosed {
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 3*time.Second)
			defer cancel()
			_ = conn.Close(closeCtx)
		}
	}()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Result{}, ClassifyTargetQueryError(err)
	}

	remaining := CalculateStatementTimeout(ctx, limits.Timeout)

	// Query 1: set_config executed before user SQL
	msStr := strconv.FormatInt(remaining.Milliseconds(), 10)
	if _, err := tx.Exec(ctx, "select set_config('statement_timeout', $1, true)", msStr); err != nil {
		rbCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(rbCtx)
		return Result{}, ClassifyTargetQueryError(err)
	}
	if _, err := tx.Exec(ctx, "select set_config('lock_timeout', '3s', true)"); err != nil {
		rbCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(rbCtx)
		return Result{}, ClassifyTargetQueryError(err)
	}
	appName := r.appName
	if appName == "" {
		appName = "leafygo"
	}
	if _, err := tx.Exec(ctx, "select set_config('application_name', $1, true)", appName); err != nil {
		rbCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(rbCtx)
		return Result{}, ClassifyTargetQueryError(err)
	}

	// Query 2: User SQL execution via extended protocol
	start := time.Now()
	rows, err := tx.Query(ctx, sql, pgx.QueryResultFormats{pgx.TextFormatCode})
	if err != nil {
		rbCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(rbCtx)
		return Result{}, ClassifyTargetQueryError(err)
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	cols := make([]string, len(fields))
	for i, f := range fields {
		cols[i] = string(f.Name)
	}

	var resultRows [][]Cell
	var totalBytes int64
	truncated := false
	aborted := false

	maxRows := limits.MaxRows
	if maxRows <= 0 {
		maxRows = 1000
	}

	for rows.Next() {
		if len(resultRows) >= maxRows {
			truncated = true
			aborted = true
			break
		}

		rawVals := rows.RawValues()
		rowCells := make([]Cell, len(rawVals))
		for i, b := range rawVals {
			if b == nil {
				rowCells[i] = Cell{IsNull: true}
				continue
			}
			valStr := string(b)
			cellTruncated := false
			if limits.MaxCellBytes > 0 && int64(len(valStr)) > limits.MaxCellBytes {
				valStr = truncateToBytes(valStr, int(limits.MaxCellBytes))
				cellTruncated = true
			}
			totalBytes += int64(len(valStr))
			rowCells[i] = Cell{
				Text:      valStr,
				IsNull:    false,
				Truncated: cellTruncated,
			}
		}
		resultRows = append(resultRows, rowCells)

		if limits.MaxResultBytes > 0 && totalBytes > limits.MaxResultBytes {
			truncated = true
			aborted = true
			break
		}
	}

	if !aborted {
		if err := rows.Err(); err != nil {
			rows.Close()
			rbCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 3*time.Second)
			defer cancel()
			_ = tx.Rollback(rbCtx)
			return Result{}, ClassifyTargetQueryError(err)
		}
	}

	cmdTag := rows.CommandTag().String()
	duration := time.Since(start)

	if aborted {
		rows.Close()
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 1*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
		connClosed = true
	} else {
		rows.Close()
		rbCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(rbCtx)
	}

	return Result{
		Columns:    cols,
		Rows:       resultRows,
		CommandTag: cmdTag,
		RowCount:   len(resultRows),
		Truncated:  truncated,
		Duration:   duration,
	}, nil
}
