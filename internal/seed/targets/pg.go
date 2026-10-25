package targets

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5"
)

type Host struct {
	Host string
	Port int
}

func (h Host) url(user, password, dbname string) string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, password),
		Host:     net.JoinHostPort(h.Host, strconv.Itoa(h.Port)),
		Path:     "/" + dbname,
		RawQuery: "sslmode=disable",
	}
	return u.String()
}

func connect(ctx context.Context, dsn string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	return pgx.ConnectConfig(ctx, cfg)
}

func ensureRole(ctx context.Context, su *pgx.Conn, role, password string) error {
	var exists bool
	if err := su.QueryRow(ctx, `select exists(select 1 from pg_roles where rolname=$1)`, role).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	var stmt string
	if err := su.QueryRow(ctx, `select format('create role %I login password %L', $1::text, $2::text)`, role, password).Scan(&stmt); err != nil {
		return err
	}
	_, err := su.Exec(ctx, stmt)
	return err
}

func ensureDatabase(ctx context.Context, su *pgx.Conn, name, owner string) error {
	var exists bool
	if err := su.QueryRow(ctx, `select exists(select 1 from pg_database where datname=$1)`, name).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err := su.Exec(ctx, "create database "+pgx.Identifier{name}.Sanitize()+" owner "+pgx.Identifier{owner}.Sanitize())
	return err
}

func hasUserTables(ctx context.Context, conn *pgx.Conn) (bool, error) {
	var exists bool
	err := conn.QueryRow(ctx, `
		select exists(select 1 from pg_tables where schemaname not in ('pg_catalog', 'information_schema'))`).Scan(&exists)
	return exists, err
}

func copyRows(ctx context.Context, conn *pgx.Conn, table []string, columns []string, rows [][]any) error {
	_, err := conn.CopyFrom(ctx, pgx.Identifier(table), columns, pgx.CopyFromRows(rows))
	if err != nil {
		return fmt.Errorf("copy %v: %w", table, err)
	}
	return nil
}

// Sinkronkan sequence identity setelah COPY dengan id eksplisit.
func resetSequences(ctx context.Context, conn *pgx.Conn, tables ...string) error {
	for _, t := range tables {
		if _, err := conn.Exec(ctx, fmt.Sprintf(
			`select setval(pg_get_serial_sequence('%s', 'id'), coalesce((select max(id) from %s), 1))`, t, t)); err != nil {
			return fmt.Errorf("reset sequence %s: %w", t, err)
		}
	}
	return nil
}

func execAll(ctx context.Context, conn *pgx.Conn, stmts ...string) error {
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			return fmt.Errorf("%w\n%s", err, s)
		}
	}
	return nil
}
