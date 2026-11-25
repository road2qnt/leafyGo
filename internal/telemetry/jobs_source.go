package telemetry

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PlatformJobCountSource reads migration_jobs at scrape time. It is registered
// only by cmd/server so multiple worker replicas never double-count the gauge.
//
// Before the M2 schema exists the table/column is absent; that is treated as an
// empty result so a development server can still expose the other metrics.
func PlatformJobCountSource(pool *pgxpool.Pool) JobCountSource {
	return func(ctx context.Context) ([]JobCount, error) {
		if pool == nil {
			return nil, nil
		}
		rows, err := pool.Query(ctx, `
			SELECT j.status, d.name, count(*)
			FROM migration_jobs j
			JOIN db_connections d ON d.id = j.db_id
			WHERE d.deleted_at IS NULL
			GROUP BY j.status, d.name
			ORDER BY d.name, j.status`)
		if err != nil {
			if isUndefinedSchema(err) {
				return nil, nil
			}
			return nil, err
		}
		defer rows.Close()

		counts := make([]JobCount, 0, 8)
		for rows.Next() {
			var count JobCount
			if err := rows.Scan(&count.Status, &count.DBName, &count.Count); err != nil {
				return nil, err
			}
			counts = append(counts, count)
		}
		return counts, rows.Err()
	}
}

func isUndefinedSchema(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	// undefined_table / undefined_column: M2 migration not applied yet.
	return pgErr.Code == "42P01" || pgErr.Code == "42703"
}
