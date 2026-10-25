package introspect

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("relation not found")
	ErrNoSelect = errors.New("no select privilege")
)

// Nilai sel dipotong agar halaman tetap ringan.
const maxCellChars = 201

type Table struct {
	Schema        string
	Name          string
	Kind          string
	EstimatedRows *int64
	CanSelect     bool
}

type Column struct {
	Name     string
	DataType string
	Nullable bool
	Default  string
	IsPK     bool
	Ordinal  int
}

type Constraint struct {
	Name       string
	Type       string
	Definition string
}

type Index struct {
	Name       string
	Unique     bool
	Primary    bool
	Definition string
}

type TableDetail struct {
	Table
	Columns     []Column
	Constraints []Constraint
	Indexes     []Index
	PrimaryKey  []string
}

type Cell struct {
	Text   string
	IsNull bool
}

type Page struct {
	Columns []string
	Rows    [][]Cell
	HasNext bool
	Ordered bool
}

const relationFilter = `
	c.relkind in ('r','p','v','m','f')
	and not c.relispartition
	and n.nspname not in ('pg_catalog','information_schema')
	and n.nspname not like 'pg_toast%'
	and n.nspname not like 'pg_temp%'
	and has_schema_privilege(n.oid, 'USAGE')`

const relationColumns = `
	n.nspname, c.relname, c.relkind::text,
	case when c.relkind in ('r','p','m','f') and c.reltuples >= 0 then c.reltuples::bigint end,
	has_table_privilege(c.oid, 'SELECT')`

func ListTables(ctx context.Context, pool *pgxpool.Pool) ([]Table, error) {
	rows, err := pool.Query(ctx, `
		select `+relationColumns+`
		from pg_class c join pg_namespace n on n.oid=c.relnamespace
		where `+relationFilter+`
		order by n.nspname, c.relname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Table
	for rows.Next() {
		var t Table
		if err := rows.Scan(&t.Schema, &t.Name, &t.Kind, &t.EstimatedRows, &t.CanSelect); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func Describe(ctx context.Context, pool *pgxpool.Pool, schema, name string) (TableDetail, error) {
	var d TableDetail
	var oid uint32
	err := pool.QueryRow(ctx, `
		select c.oid, `+relationColumns+`
		from pg_class c join pg_namespace n on n.oid=c.relnamespace
		where `+relationFilter+` and n.nspname=$1 and c.relname=$2`, schema, name).
		Scan(&oid, &d.Schema, &d.Name, &d.Kind, &d.EstimatedRows, &d.CanSelect)
	if errors.Is(err, pgx.ErrNoRows) {
		return TableDetail{}, ErrNotFound
	}
	if err != nil {
		return TableDetail{}, err
	}

	rows, err := pool.Query(ctx, `
		select a.attname, format_type(a.atttypid, a.atttypmod), not a.attnotnull,
		       coalesce(pg_get_expr(ad.adbin, ad.adrelid), ''),
		       coalesce(a.attnum = any(pk.conkey), false)
		from pg_attribute a
		left join pg_attrdef ad on ad.adrelid=a.attrelid and ad.adnum=a.attnum
		left join pg_constraint pk on pk.conrelid=a.attrelid and pk.contype='p'
		where a.attrelid=$1 and a.attnum > 0 and not a.attisdropped
		order by a.attnum`, oid)
	if err != nil {
		return TableDetail{}, err
	}
	for rows.Next() {
		c := Column{Ordinal: len(d.Columns) + 1}
		if err := rows.Scan(&c.Name, &c.DataType, &c.Nullable, &c.Default, &c.IsPK); err != nil {
			rows.Close()
			return TableDetail{}, err
		}
		if c.IsPK {
			d.PrimaryKey = append(d.PrimaryKey, c.Name)
		}
		d.Columns = append(d.Columns, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return TableDetail{}, err
	}

	rows, err = pool.Query(ctx, `
		select conname, contype::text, pg_get_constraintdef(oid)
		from pg_constraint where conrelid=$1
		order by conname`, oid)
	if err != nil {
		return TableDetail{}, err
	}
	for rows.Next() {
		var c Constraint
		if err := rows.Scan(&c.Name, &c.Type, &c.Definition); err != nil {
			rows.Close()
			return TableDetail{}, err
		}
		d.Constraints = append(d.Constraints, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return TableDetail{}, err
	}

	rows, err = pool.Query(ctx, `
		select ic.relname, i.indisunique, i.indisprimary, pg_get_indexdef(i.indexrelid)
		from pg_index i join pg_class ic on ic.oid=i.indexrelid
		where i.indrelid=$1
		order by ic.relname`, oid)
	if err != nil {
		return TableDetail{}, err
	}
	for rows.Next() {
		var ix Index
		if err := rows.Scan(&ix.Name, &ix.Unique, &ix.Primary, &ix.Definition); err != nil {
			rows.Close()
			return TableDetail{}, err
		}
		d.Indexes = append(d.Indexes, ix)
	}
	rows.Close()
	return d, rows.Err()
}

// Urut berdasarkan primary key bila ada, selain itu urutan tidak dijamin stabil.
func ReadPage(ctx context.Context, pool *pgxpool.Pool, d TableDetail, page, pageSize int) (Page, error) {
	if !d.CanSelect {
		return Page{}, ErrNoSelect
	}
	if page < 1 {
		page = 1
	}
	out := Page{Ordered: len(d.PrimaryKey) > 0}
	if len(d.Columns) == 0 {
		return out, nil
	}

	selects := make([]string, len(d.Columns))
	for i, c := range d.Columns {
		out.Columns = append(out.Columns, c.Name)
		selects[i] = fmt.Sprintf("left(%s::text, %d)", pgx.Identifier{c.Name}.Sanitize(), maxCellChars)
	}
	query := "select " + strings.Join(selects, ", ") + " from " + pgx.Identifier{d.Schema, d.Name}.Sanitize()
	if out.Ordered {
		keys := make([]string, len(d.PrimaryKey))
		for i, k := range d.PrimaryKey {
			keys[i] = pgx.Identifier{k}.Sanitize()
		}
		query += " order by " + strings.Join(keys, ", ")
	}
	query += " limit $1 offset $2"

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Page{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	rows, err := tx.Query(ctx, query, pageSize+1, (page-1)*pageSize)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()

	for rows.Next() {
		if len(out.Rows) == pageSize {
			out.HasNext = true
			break
		}
		vals := make([]*string, len(d.Columns))
		dest := make([]any, len(vals))
		for i := range vals {
			dest[i] = &vals[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return Page{}, err
		}
		row := make([]Cell, len(vals))
		for i, v := range vals {
			if v == nil {
				row[i] = Cell{IsNull: true}
			} else {
				row[i] = Cell{Text: *v}
			}
		}
		out.Rows = append(out.Rows, row)
	}
	return out, rows.Err()
}
