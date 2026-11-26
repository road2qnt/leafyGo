package migration

import (
	"errors"
)

var (
	ErrForbidden                = errors.New("forbidden")
	ErrNotFound                 = errors.New("not found")
	ErrConflict                 = errors.New("conflict")
	ErrLockTimeout              = errors.New("lock timeout")
	ErrBusy                     = errors.New("busy")
	ErrValidation               = errors.New("validation")
	ErrTarget                   = errors.New("target error")
	ErrHistoryNotRunning        = errors.New("history not running")
	ErrRunningMigration         = errors.New("running migration")
	ErrInvalidMigrationSQL      = errors.New("invalid migration sql")
	ErrNonTransactionalStmt     = errors.New("non-transactional statement is not permitted in migrations")
	ErrInvalidBatchIdentifier   = errors.New("invalid batch table or column identifier")
	ErrInvalidBatchCursorType   = errors.New("batch cursor column must be an integer type")
	ErrDryRunBusy               = errors.New("dry-run busy")
	ErrDuplicateActiveProposal  = errors.New("duplicate active proposal")
	ErrMigrationImmutable       = errors.New("migration definition is immutable")
	ErrMigrationNotSupersedable = errors.New("migration is not supersedable")
	ErrNoAccessGroups           = errors.New("no active access groups for database")
	ErrMissingGroupLeader       = errors.New("one or more access groups do not have an active leader")
	ErrNotGroupLeader           = errors.New("user is not a required group leader for this proposal")
	ErrPredecessorNotReady      = errors.New("predecessor migration is not ready")
)
