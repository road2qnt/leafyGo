package queryconsole

import (
	"errors"
	"fmt"
)

var (
	// ErrValidation indicates a syntax, statement limit, or parameter validation failure.
	ErrValidation = errors.New("validation error")

	// ErrForbidden indicates the actor lacks sufficient database access.
	ErrForbidden = errors.New("forbidden")

	// ErrBusy indicates no concurrency slots were available within the timeout window.
	ErrBusy = errors.New("database is busy")

	// ErrTimeout indicates execution timed out.
	ErrTimeout = errors.New("query timeout")

	// ErrTarget indicates a query failure on the target database.
	ErrTarget = errors.New("target error")
)

// MsgWriteRejection is the user-facing message shown when a write or DDL statement is rejected.
const MsgWriteRejection = "Console hanya untuk membaca data. Untuk mengubah data atau schema, gunakan Migration."

// TargetError encapsulates target SQL errors with SQLSTATE and statement position.
type TargetError struct {
	Message          string
	Code             string // SQLSTATE if available
	Position         int    // Character offset in SQL if available
	IsWriteRejection bool
}

func (e *TargetError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" {
		return fmt.Sprintf("[%s] %s", e.Code, e.Message)
	}
	return e.Message
}
