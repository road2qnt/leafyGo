package target

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	placeholderFrom = "{{cursor_from}}"
	placeholderTo   = "{{cursor_to}}"
)

var ErrInvalidBatchConfig = errors.New("invalid batch config")

// Means another worker already wrote or reverted this batch, so the current worker is a zombie.
var ErrCheckpointConflict = errors.New("checkpoint already written by another execution")

type BatchConfig struct {
	TableName    string `json:"table_name"`
	CursorCol    string `json:"cursor_col"`
	BatchSize    int64  `json:"batch_size"`
	UpdatedAtCol string `json:"updated_at_col,omitempty"`
}

func ParseBatchConfig(raw []byte) (BatchConfig, error) {
	var c BatchConfig
	if len(raw) == 0 {
		return c, fmt.Errorf("%w: missing", ErrInvalidBatchConfig)
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("%w: %v", ErrInvalidBatchConfig, err)
	}
	if strings.TrimSpace(c.TableName) == "" || strings.TrimSpace(c.CursorCol) == "" || c.BatchSize <= 0 {
		return c, fmt.Errorf("%w: table_name, cursor_col and a positive batch_size are required", ErrInvalidBatchConfig)
	}
	return c, nil
}

// Quotes "schema.table" or "table" so it can be interpolated safely.
func (c BatchConfig) QuotedTable() string {
	return pgx.Identifier(strings.Split(c.TableName, ".")).Sanitize()
}

func (c BatchConfig) QuotedCursor() string {
	return pgx.Identifier{c.CursorCol}.Sanitize()
}

// Binds a batch range into the migration SQL. {{cursor_from}} and {{cursor_to}} become integer
// literals, which keeps multi statement scripts working. Otherwise the range goes in as $1 and $2.
func BindCursorRange(sql string, from, to int64) (string, []any) {
	if strings.Contains(sql, placeholderFrom) || strings.Contains(sql, placeholderTo) {
		r := strings.NewReplacer(placeholderFrom, strconv.FormatInt(from, 10), placeholderTo, strconv.FormatInt(to, 10))
		return r.Replace(sql), nil
	}
	return sql, []any{from, to}
}

type Scope struct {
	Min   *int64
	Max   *int64
	Count int64
}

func (s Scope) TotalBatches(batchSize int64) int {
	if s.Count == 0 || batchSize <= 0 {
		return 0
	}
	return int((s.Count + batchSize - 1) / batchSize)
}

func ScanScope(ctx context.Context, conn *pgx.Conn, c BatchConfig) (Scope, error) {
	var s Scope
	q := fmt.Sprintf(`select min(%[1]s)::bigint, max(%[1]s)::bigint, count(*) from %[2]s`, c.QuotedCursor(), c.QuotedTable())
	if err := conn.QueryRow(ctx, q).Scan(&s.Min, &s.Max, &s.Count); err != nil {
		return Scope{}, fmt.Errorf("scan batch scope: %s", sanitizeTargetError(err))
	}
	return s, nil
}

// Finds the cursor range of the next batch after lastCursor, never past scopeMax.
func NextRange(ctx context.Context, conn *pgx.Conn, c BatchConfig, lastCursor, scopeMax int64) (from, to int64, ok bool, err error) {
	q := fmt.Sprintf(`
		select min(sub.c)::bigint, max(sub.c)::bigint, count(*)
		from (
			select %[1]s as c from %[2]s
			where %[1]s > $1 and %[1]s <= $2
			order by %[1]s asc
			limit $3
		) sub`, c.QuotedCursor(), c.QuotedTable())
	var minC, maxC *int64
	var n int64
	if err := conn.QueryRow(ctx, q, lastCursor, scopeMax, c.BatchSize).Scan(&minC, &maxC, &n); err != nil {
		return 0, 0, false, fmt.Errorf("select next batch range: %s", sanitizeTargetError(err))
	}
	if n == 0 || minC == nil || maxC == nil {
		return 0, 0, false, nil
	}
	return *minC, *maxC, true, nil
}

type ForwardState struct {
	LastBatchNo  int
	LastCursor   *int64
	ActiveRows   int64
	ActiveCount  int
	AttemptCount int
	AttemptRows  int64
	HasReverted  bool
}

func ReadForwardState(ctx context.Context, conn *pgx.Conn, rootID, executionID uuid.UUID) (ForwardState, error) {
	var s ForwardState
	err := conn.QueryRow(ctx, `
		select coalesce(max(batch_no) filter (where reverted_at is null), 0),
		       max(cursor_to) filter (where reverted_at is null),
		       coalesce(sum(rows_affected) filter (where reverted_at is null), 0),
		       count(*) filter (where reverted_at is null),
		       count(*) filter (where reverted_at is null and written_by_execution_id = $2),
		       coalesce(sum(rows_affected) filter (where reverted_at is null and written_by_execution_id = $2), 0),
		       count(*) filter (where reverted_at is not null) > 0
		from _leafygo_batch_checkpoints
		where root_execution_id = $1`, rootID, executionID).
		Scan(&s.LastBatchNo, &s.LastCursor, &s.ActiveRows, &s.ActiveCount, &s.AttemptCount, &s.AttemptRows, &s.HasReverted)
	if err != nil {
		return ForwardState{}, fmt.Errorf("read forward checkpoints: %s", sanitizeTargetError(err))
	}
	return s, nil
}

type ReverseState struct {
	Remaining     int
	RemainingRows int64
	AttemptCount  int
	AttemptRows   int64
	AnyReverted   bool
}

func ReadReverseState(ctx context.Context, conn *pgx.Conn, rootID, executionID uuid.UUID) (ReverseState, error) {
	var s ReverseState
	err := conn.QueryRow(ctx, `
		select count(*) filter (where reverted_at is null),
		       coalesce(sum(rows_affected) filter (where reverted_at is null), 0),
		       count(*) filter (where reverted_at is not null and reverted_by_execution_id = $2),
		       coalesce(sum(rows_affected) filter (where reverted_at is not null and reverted_by_execution_id = $2), 0),
		       count(*) filter (where reverted_at is not null) > 0
		from _leafygo_batch_checkpoints
		where root_execution_id = $1`, rootID, executionID).
		Scan(&s.Remaining, &s.RemainingRows, &s.AttemptCount, &s.AttemptRows, &s.AnyReverted)
	if err != nil {
		return ReverseState{}, fmt.Errorf("read reverse checkpoints: %s", sanitizeTargetError(err))
	}
	return s, nil
}

type Checkpoint struct {
	BatchNo     int
	CursorFrom  int64
	CursorTo    int64
	Rows        int64
	CommittedAt time.Time
}

// Returns the highest batch of the root that is not reverted yet, or nil when all are reverted.
func NextRevertBatch(ctx context.Context, conn *pgx.Conn, rootID uuid.UUID) (*Checkpoint, error) {
	var c Checkpoint
	err := conn.QueryRow(ctx, `
		select batch_no, cursor_from, cursor_to, rows_affected, committed_at
		from _leafygo_batch_checkpoints
		where root_execution_id = $1 and reverted_at is null
		order by batch_no desc
		limit 1`, rootID).Scan(&c.BatchNo, &c.CursorFrom, &c.CursorTo, &c.Rows, &c.CommittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read next revert batch: %s", sanitizeTargetError(err))
	}
	return &c, nil
}

type BatchRun struct {
	RootID      uuid.UUID
	ExecutionID uuid.UUID
	BatchNo     int
	From        int64
	To          int64
	Epoch       int64
	Timeout     time.Duration
}

// Runs sql_up for one range and writes its checkpoint in the same transaction. A duplicate
// checkpoint key aborts the batch, which stops a zombie worker from applying it twice.
func RunForwardBatch(ctx context.Context, conn *pgx.Conn, sqlUp string, b BatchRun) (int64, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, classifyRunError(phaseConnect, err)
	}
	defer rollbackQuietly(tx)

	if err := setTxTimeout(ctx, tx, b.Timeout); err != nil {
		return 0, classifyRunError(phaseStatement, err)
	}
	rows, err := execBound(ctx, tx, sqlUp, b.From, b.To)
	if err != nil {
		return 0, classifyRunError(phaseStatement, err)
	}
	if _, err := tx.Exec(ctx, `
		insert into _leafygo_batch_checkpoints
			(root_execution_id, batch_no, written_by_execution_id, cursor_from, cursor_to, rows_affected, lock_epoch)
		values ($1, $2, $3, $4, $5, $6, $7)`,
		b.RootID, b.BatchNo, b.ExecutionID, b.From, b.To, rows, b.Epoch); err != nil {
		if isUniqueViolation(err) {
			return 0, classifyRunError(phaseStatement, ErrCheckpointConflict)
		}
		return 0, classifyRunError(phaseStatement, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, classifyRunError(phaseCommit, err)
	}
	return rows, nil
}

// Runs sql_down for one committed batch and marks its checkpoint reverted in the same transaction.
func RunReverseBatch(ctx context.Context, conn *pgx.Conn, sqlDown string, b BatchRun) (int64, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, classifyRunError(phaseConnect, err)
	}
	defer rollbackQuietly(tx)

	if err := setTxTimeout(ctx, tx, b.Timeout); err != nil {
		return 0, classifyRunError(phaseStatement, err)
	}
	rows, err := execBound(ctx, tx, sqlDown, b.From, b.To)
	if err != nil {
		return 0, classifyRunError(phaseStatement, err)
	}
	tag, err := tx.Exec(ctx, `
		update _leafygo_batch_checkpoints
		set reverted_at = now(), reverted_by_execution_id = $3
		where root_execution_id = $1 and batch_no = $2 and reverted_at is null`,
		b.RootID, b.BatchNo, b.ExecutionID)
	if err != nil {
		return 0, classifyRunError(phaseStatement, err)
	}
	if tag.RowsAffected() != 1 {
		return 0, classifyRunError(phaseStatement, ErrCheckpointConflict)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, classifyRunError(phaseCommit, err)
	}
	return rows, nil
}

func execBound(ctx context.Context, tx pgx.Tx, sql string, from, to int64) (int64, error) {
	bound, args := BindCursorRange(sql, from, to)
	if args == nil {
		tag, err := tx.Exec(ctx, bound, pgx.QueryExecModeSimpleProtocol)
		return tag.RowsAffected(), err
	}
	tag, err := tx.Exec(ctx, bound, args...)
	return tag.RowsAffected(), err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
