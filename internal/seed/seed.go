package seed

import (
	"context"
	"errors"

	"leafygo/internal/auth"
	"leafygo/internal/store"
)

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
