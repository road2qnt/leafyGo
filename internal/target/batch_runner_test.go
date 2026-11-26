package target

import (
	"errors"
	"testing"
)

func TestParseBatchConfig(t *testing.T) {
	c, err := ParseBatchConfig([]byte(`{"table_name":"public.orders","cursor_col":"id","batch_size":500}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.TableName != "public.orders" || c.CursorCol != "id" || c.BatchSize != 500 {
		t.Fatalf("config = %+v", c)
	}
	for name, raw := range map[string]string{
		"empty":         ``,
		"not json":      `{`,
		"no table":      `{"cursor_col":"id","batch_size":5}`,
		"no cursor":     `{"table_name":"t","batch_size":5}`,
		"zero size":     `{"table_name":"t","cursor_col":"id","batch_size":0}`,
		"negative size": `{"table_name":"t","cursor_col":"id","batch_size":-1}`,
	} {
		if _, err := ParseBatchConfig([]byte(raw)); !errors.Is(err, ErrInvalidBatchConfig) {
			t.Errorf("%s: err = %v; want ErrInvalidBatchConfig", name, err)
		}
	}
}

func TestBatchConfigQuotesIdentifiers(t *testing.T) {
	c := BatchConfig{TableName: `sales.order"s`, CursorCol: `id; drop table x`}
	if got := c.QuotedTable(); got != `"sales"."order""s"` {
		t.Errorf("QuotedTable() = %s", got)
	}
	if got := c.QuotedCursor(); got != `"id; drop table x"` {
		t.Errorf("QuotedCursor() = %s", got)
	}
	if got := (BatchConfig{TableName: "orders"}).QuotedTable(); got != `"orders"` {
		t.Errorf("QuotedTable() = %s", got)
	}
}

func TestBindCursorRange(t *testing.T) {
	sql, args := BindCursorRange("update t set v = 1 where id >= {{cursor_from}} and id <= {{cursor_to}}; select 1", 11, 20)
	if sql != "update t set v = 1 where id >= 11 and id <= 20; select 1" || args != nil {
		t.Errorf("placeholder form = %q, %v", sql, args)
	}
	sql, args = BindCursorRange("update t set v = 1 where id between $1 and $2", 11, 20)
	if sql != "update t set v = 1 where id between $1 and $2" || len(args) != 2 || args[0] != int64(11) || args[1] != int64(20) {
		t.Errorf("parameter form = %q, %v", sql, args)
	}
}

func TestScopeTotalBatches(t *testing.T) {
	cases := []struct {
		count, size int64
		want        int
	}{{0, 100, 0}, {1, 100, 1}, {100, 100, 1}, {101, 100, 2}, {1000, 100, 10}, {5, 0, 0}}
	for _, c := range cases {
		if got := (Scope{Count: c.count}).TotalBatches(c.size); got != c.want {
			t.Errorf("TotalBatches(count=%d, size=%d) = %d; want %d", c.count, c.size, got, c.want)
		}
	}
}
