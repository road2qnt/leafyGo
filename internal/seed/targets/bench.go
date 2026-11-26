package targets

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"leafygo/internal/seed/dataset"
)

// Sized so that one unbatched UPDATE over the whole table takes well over one second.
const BenchRows = 1_000_000

const (
	BenchDBName = "bench"
	BenchTable  = "large_migration_bench"
)

func quoteTable(table string) string {
	return pgx.Identifier(strings.Split(table, ".")).Sanitize()
}

// Statement whose duration proves the dataset is large enough.
func BenchUnbatchedUpdateSQL(table string) string {
	return `update ` + quoteTable(table) + ` set amount = amount + 1, updated_at = now()`
}

// The same change as BenchUnbatchedUpdateSQL, limited to one batch range.
func BenchBatchUpSQL(table string) string {
	return `update ` + quoteTable(table) + ` set amount = amount + 1, updated_at = now()
		where id >= {{cursor_from}} and id <= {{cursor_to}}`
}

func BenchBatchDownSQL(table string) string {
	return `update ` + quoteTable(table) + ` set amount = amount - 1, updated_at = now()
		where id >= {{cursor_from}} and id <= {{cursor_to}}`
}

// Creates the bench table with ids 1 to rows. The seeder and the batch migration tests share it.
func CreateBenchTable(ctx context.Context, conn *pgx.Conn, table string, rows int) error {
	q := quoteTable(table)
	return execAll(ctx, conn, `
		create table `+q+` (
			id          bigint primary key,
			account_ref text not null,
			amount      numeric(12,2) not null,
			status      text not null,
			updated_at  timestamptz not null default now()
		)`, fmt.Sprintf(`
		insert into %s (id, account_ref, amount, status)
		select g,
		       'ACC-' || lpad((g %% 50000)::text, 6, '0'),
		       (g %% 1000) + 0.5,
		       case when g %% 3 = 0 then 'settled' else 'pending' end
		from generate_series(1, %d) g`, q, rows))
}

func buildBench(ctx context.Context, conn *pgx.Conn, _ *dataset.Dataset) error {
	return CreateBenchTable(ctx, conn, BenchTable, BenchRows)
}

type BenchResult struct {
	Rows     int64
	Duration time.Duration
}

// Times the unbatched update inside a transaction that is rolled back, so the data never changes.
func MeasureUnbatchedUpdate(ctx context.Context, conn *pgx.Conn, table string) (BenchResult, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return BenchResult{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	start := time.Now()
	tag, err := tx.Exec(ctx, BenchUnbatchedUpdateSQL(table))
	if err != nil {
		return BenchResult{}, fmt.Errorf("unbatched update: %w", err)
	}
	return BenchResult{Rows: tag.RowsAffected(), Duration: time.Since(start)}, nil
}

func MeasureBench(ctx context.Context, h Host, c Credentials) (BenchResult, error) {
	conn, err := connect(ctx, h.url(c.AppUser, c.AppPassword, BenchDBName))
	if err != nil {
		return BenchResult{}, err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, `vacuum analyze `+BenchTable); err != nil {
		return BenchResult{}, fmt.Errorf("vacuum bench table: %w", err)
	}
	return MeasureUnbatchedUpdate(ctx, conn, BenchTable)
}
