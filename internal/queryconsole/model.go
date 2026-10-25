package queryconsole

import (
	"time"

	"leafygo/internal/domain"
)

// Limits defines execution boundaries for an ad-hoc query.
type Limits struct {
	Timeout        time.Duration
	MaxRows        int
	MaxCellBytes   int64
	MaxResultBytes int64
}

// ExecuteInput contains the caller and target information for executing a query.
type ExecuteInput struct {
	Actor domain.User
	DB    domain.DBConnection
	Level domain.AccessLevel
	SQL   string
}

// Cell represents an individual column value formatted for display.
type Cell struct {
	Text      string
	IsNull    bool
	Truncated bool
}

// Result holds the formatted columns, rows, and metadata of a single query result.
type Result struct {
	Columns    []string
	Rows       [][]Cell
	CommandTag string
	RowCount   int
	Truncated  bool
	Duration   time.Duration
}
