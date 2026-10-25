package introspect_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"leafygo/internal/introspect"
)

const schema = "lg_introspect_test"

func targetPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("TEST_TARGET_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_TARGET_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `drop schema if exists `+schema+` cascade`)
		pool.Close()
	})
	if _, err := pool.Exec(ctx, `
		drop schema if exists `+schema+` cascade;
		create schema `+schema+`;
		create table `+schema+`.parents (id int primary key, name text not null);
		create table `+schema+`.children (
			a int, b int, parent_id int references `+schema+`.parents(id),
			label text default 'x', amount numeric(10,2), long_text text,
			primary key (a, b), constraint amount_positive check (amount > 0));
		create unique index children_label_uq on `+schema+`.children (label);
		create table `+schema+`.heap (v int);
		create view `+schema+`.parent_names as select name from `+schema+`.parents;
		insert into `+schema+`.parents select g, 'p' || g from generate_series(1, 5) g;
		insert into `+schema+`.children values (1, 1, 1, 'a', 1.50, repeat('z', 500)), (1, 2, null, 'b', null, null);`); err != nil {
		t.Fatalf("prepare target: %v", err)
	}
	return pool
}

func TestIsNumeric(t *testing.T) {
	tests := map[string]bool{
		"integer": true, "bigint": true, "smallint": true, "numeric(10,2)": true,
		"double precision": true, "real": true, "money": true,
		"text": false, "character varying(20)": false, "boolean": false, "timestamp with time zone": false, "": false,
	}
	for typ, want := range tests {
		if got := introspect.IsNumeric(typ); got != want {
			t.Errorf("IsNumeric(%q) = %v; want %v", typ, got, want)
		}
	}
}

func TestListTables(t *testing.T) {
	pool := targetPool(t)
	tables, err := introspect.ListTables(context.Background(), pool)
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	byName := map[string]introspect.Table{}
	for _, tb := range tables {
		if tb.Schema == "pg_catalog" || tb.Schema == "information_schema" {
			t.Errorf("system relation leaked: %s.%s", tb.Schema, tb.Name)
		}
		if tb.Schema == schema {
			byName[tb.Name] = tb
		}
	}
	for _, name := range []string{"parents", "children", "heap", "parent_names"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("%s missing from listing", name)
		}
	}
	if k := byName["parent_names"].Kind; k != "v" {
		t.Errorf("view kind = %q; want v", k)
	}
	if rc := byName["parents"].RowCount; rc == nil || *rc != 5 {
		t.Errorf("parents row count = %v; want 5", rc)
	}
	if rc := byName["heap"].RowCount; rc == nil || *rc != 0 {
		t.Errorf("empty table row count = %v; want 0", rc)
	}
	if !byName["parents"].CanSelect {
		t.Error("CanSelect should be true for the owner")
	}
}

func TestDescribe(t *testing.T) {
	pool := targetPool(t)
	d, err := introspect.Describe(context.Background(), pool, schema, "children")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if len(d.Columns) != 6 {
		t.Fatalf("columns = %d; want 6", len(d.Columns))
	}
	cols := map[string]introspect.Column{}
	for i, c := range d.Columns {
		if c.Ordinal != i+1 {
			t.Errorf("column %s ordinal = %d; want %d", c.Name, c.Ordinal, i+1)
		}
		cols[c.Name] = c
	}
	if !cols["a"].IsPK || !cols["b"].IsPK || cols["parent_id"].IsPK {
		t.Errorf("primary key flags wrong: %+v", cols)
	}
	if strings.Join(d.PrimaryKey, ",") != "a,b" {
		t.Errorf("PrimaryKey = %v; want [a b]", d.PrimaryKey)
	}
	if cols["label"].Default != "'x'::text" {
		t.Errorf("label default = %q", cols["label"].Default)
	}
	if cols["amount"].DataType != "numeric(10,2)" {
		t.Errorf("amount type = %q", cols["amount"].DataType)
	}
	if cols["a"].Nullable || !cols["parent_id"].Nullable {
		t.Errorf("nullable flags wrong: a=%v parent_id=%v", cols["a"].Nullable, cols["parent_id"].Nullable)
	}

	types := map[string]string{}
	for _, c := range d.Constraints {
		types[c.Name] = c.Type
	}
	if types["amount_positive"] != "c" {
		t.Errorf("check constraint missing: %v", types)
	}
	var hasFK, hasPK bool
	for _, c := range d.Constraints {
		hasFK = hasFK || c.Type == "f" && strings.Contains(c.Definition, "REFERENCES")
		hasPK = hasPK || c.Type == "p"
	}
	if !hasFK || !hasPK {
		t.Errorf("constraints = %+v; want a primary key and a foreign key", d.Constraints)
	}

	var primary, unique bool
	for _, ix := range d.Indexes {
		primary = primary || ix.Primary
		unique = unique || ix.Name == "children_label_uq" && ix.Unique && strings.Contains(ix.Definition, "UNIQUE")
	}
	if !primary || !unique {
		t.Errorf("indexes = %+v; want primary and unique", d.Indexes)
	}
}

func TestDescribeNotFound(t *testing.T) {
	pool := targetPool(t)
	for _, tt := range []struct{ schema, name string }{
		{schema, "missing"},
		{"no_such_schema", "parents"},
		{"pg_catalog", "pg_class"},
	} {
		_, err := introspect.Describe(context.Background(), pool, tt.schema, tt.name)
		if !errors.Is(err, introspect.ErrNotFound) {
			t.Errorf("Describe(%s.%s) error = %v; want ErrNotFound", tt.schema, tt.name, err)
		}
	}
}

func TestReadPagePaginationAndOrder(t *testing.T) {
	pool := targetPool(t)
	ctx := context.Background()
	d, err := introspect.Describe(ctx, pool, schema, "parents")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}

	p1, err := introspect.ReadPage(ctx, pool, d, 1, 2)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if !p1.Ordered || !p1.HasNext || len(p1.Rows) != 2 {
		t.Fatalf("page 1 = ordered %v next %v rows %d", p1.Ordered, p1.HasNext, len(p1.Rows))
	}
	if p1.Rows[0][0].Text != "1" || p1.Rows[1][1].Text != "p2" {
		t.Errorf("page 1 rows = %+v", p1.Rows)
	}
	if !p1.Columns[0].Numeric || p1.Columns[1].Numeric {
		t.Errorf("column numeric flags = %+v", p1.Columns)
	}

	p3, err := introspect.ReadPage(ctx, pool, d, 3, 2)
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if p3.HasNext || len(p3.Rows) != 1 || p3.Rows[0][0].Text != "5" {
		t.Errorf("last page = next %v rows %+v", p3.HasNext, p3.Rows)
	}

	beyond, err := introspect.ReadPage(ctx, pool, d, 9, 2)
	if err != nil || len(beyond.Rows) != 0 || beyond.HasNext {
		t.Errorf("page beyond end = %+v, %v", beyond, err)
	}
	clamped, err := introspect.ReadPage(ctx, pool, d, -4, 2)
	if err != nil || len(clamped.Rows) != 2 || clamped.Rows[0][0].Text != "1" {
		t.Errorf("page < 1 should read the first page: %+v, %v", clamped, err)
	}
}

func TestReadPageNullsTruncationAndUnordered(t *testing.T) {
	pool := targetPool(t)
	ctx := context.Background()
	d, err := introspect.Describe(ctx, pool, schema, "children")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	page, err := introspect.ReadPage(ctx, pool, d, 1, 10)
	if err != nil {
		t.Fatalf("ReadPage: %v", err)
	}
	idx := map[string]int{}
	for i, c := range page.Columns {
		idx[c.Name] = i
	}
	if len(page.Rows) != 2 {
		t.Fatalf("rows = %d; want 2", len(page.Rows))
	}
	if long := page.Rows[0][idx["long_text"]]; long.IsNull || len(long.Text) >= 500 {
		t.Errorf("long cell not truncated: null=%v len=%d", long.IsNull, len(long.Text))
	}
	if !page.Rows[1][idx["amount"]].IsNull || !page.Rows[1][idx["parent_id"]].IsNull {
		t.Error("SQL NULL must be reported as IsNull, not as empty text")
	}

	heap, err := introspect.Describe(ctx, pool, schema, "heap")
	if err != nil {
		t.Fatalf("Describe heap: %v", err)
	}
	empty, err := introspect.ReadPage(ctx, pool, heap, 1, 10)
	if err != nil || empty.Ordered || len(empty.Rows) != 0 {
		t.Errorf("table without a primary key = %+v, %v", empty, err)
	}
}

func TestReadPageWithoutSelectPrivilege(t *testing.T) {
	pool := targetPool(t)
	d := introspect.TableDetail{Table: introspect.Table{Schema: schema, Name: "parents", CanSelect: false}}
	if _, err := introspect.ReadPage(context.Background(), pool, d, 1, 10); !errors.Is(err, introspect.ErrNoSelect) {
		t.Fatalf("error = %v; want ErrNoSelect", err)
	}
}

func TestReadPageIsReadOnly(t *testing.T) {
	pool := targetPool(t)
	ctx := context.Background()
	d, _ := introspect.Describe(ctx, pool, schema, "parents")
	d.Name = `parents"; delete from ` + schema + `.parents; --`
	if _, err := introspect.ReadPage(ctx, pool, d, 1, 10); err == nil {
		t.Fatal("a hostile table name must not resolve to a relation")
	}
	var n int
	if err := pool.QueryRow(ctx, `select count(*) from `+schema+`.parents`).Scan(&n); err != nil || n != 5 {
		t.Fatalf("rows after hostile name = %d, %v; want 5", n, err)
	}
}
