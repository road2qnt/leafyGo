package store

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrConflict          = errors.New("conflict")
	ErrLockTimeout       = errors.New("lock timeout")
	ErrLastAdmin         = errors.New("last admin")
	ErrSelfMutation      = errors.New("self mutation")
	ErrAdminGroupMember  = errors.New("platform admin cannot be group member")
	ErrRunningMigration  = errors.New("running migration")
	ErrHistoryNotRunning = errors.New("history not running")
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
