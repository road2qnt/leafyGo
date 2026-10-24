package seed

import (
	"context"
	"errors"

	"leafygo/internal/auth"
	"leafygo/internal/store"
)

// EnsureAdmin creates the bootstrap platform admin when it does not exist and
// promotes it if it was created earlier without the flag.
//
// Slice ownership (leafyGo): Ega. Demo seeding (siti/budi/groups/connection)
// belongs to Putra and lives in DemoSeed (to be added by Putra).
func EnsureAdmin(ctx context.Context, st *store.Store, username, password string) error {
	if username == "" || password == "" {
		return nil
	}
	user, err := st.GetUserByUsername(ctx, username)
	if err == nil {
		if !user.IsPlatformAdmin {
			return st.SetUserAdmin(ctx, user.ID, true)
		}
		return nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	_, err = st.CreateUser(ctx, username, hash, true)
	return err
}
