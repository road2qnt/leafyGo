package migration

import (
	"errors"
)

var (
	ErrForbidden         = errors.New("forbidden")
	ErrNotFound          = errors.New("not found")
	ErrConflict          = errors.New("conflict")
	ErrLockTimeout       = errors.New("lock timeout")
	ErrBusy              = errors.New("busy")
	ErrValidation        = errors.New("validation")
	ErrTarget            = errors.New("target error")
	ErrHistoryNotRunning = errors.New("history not running")
	ErrRunningMigration  = errors.New("running migration")
)
