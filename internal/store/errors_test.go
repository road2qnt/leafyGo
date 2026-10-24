package store

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapWriteErrorUniqueViolation(t *testing.T) {
	err := mapWriteError(&pgconn.PgError{Code: "23505"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("mapWriteError = %v; want ErrConflict", err)
	}
}

func TestMapWriteErrorPassThrough(t *testing.T) {
	plain := errors.New("plain")
	if got := mapWriteError(plain); !errors.Is(got, plain) {
		t.Fatalf("plain error changed: %v", got)
	}
	other := &pgconn.PgError{Code: "23503"}
	if got := mapWriteError(other); got != other {
		t.Fatalf("other pg error changed: %v", got)
	}
}

func TestCheckAdminDeactivation(t *testing.T) {
	target := uuid.New()
	actor := uuid.New()

	if err := checkAdminDeactivation(target, target, true, 2, false); !errors.Is(err, ErrSelfMutation) {
		t.Fatalf("self disable = %v; want ErrSelfMutation", err)
	}
	if err := checkAdminDeactivation(target, actor, true, 1, false); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last admin = %v; want ErrLastAdmin", err)
	}
	if err := checkAdminDeactivation(target, actor, true, 2, false); err != nil {
		t.Fatalf("disable with another admin = %v; want nil", err)
	}
	if err := checkAdminDeactivation(target, actor, false, 1, false); err != nil {
		t.Fatalf("disable non-admin = %v; want nil", err)
	}
	if err := checkAdminDeactivation(target, target, true, 1, true); err != nil {
		t.Fatalf("enable = %v; want nil", err)
	}
}
