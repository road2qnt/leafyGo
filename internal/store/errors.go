package store

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound                 = errors.New("not found")
	ErrConflict                 = errors.New("conflict")
	ErrLockTimeout              = errors.New("lock timeout")
	ErrLastAdmin                = errors.New("last admin")
	ErrSelfMutation             = errors.New("self mutation")
	ErrAdminGroupMember         = errors.New("platform admin cannot be group member")
	ErrRunningMigration         = errors.New("running migration")
	ErrHistoryNotRunning        = errors.New("history not running")
	ErrUserHasHistory           = errors.New("user has history")
	ErrGroupHasHistory          = errors.New("group has history")
	ErrDBHasActiveWork          = errors.New("database has active work")
	ErrInvalidGroupLeader       = errors.New("leader must be an active member of the group")
	ErrMigrationImmutable       = errors.New("migration definition is immutable")
	ErrActiveProposalExists     = errors.New("migration definition has an active proposal")
	ErrDuplicateActiveProposal  = errors.New("duplicate active proposal")
	ErrMigrationNotSupersedable = errors.New("migration is not supersedable")
	ErrInvalidMigrationDef      = errors.New("invalid migration definition")
)

func mapWriteError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}
