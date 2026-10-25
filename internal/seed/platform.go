package seed

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"leafygo/internal/auth"
	"leafygo/internal/domain"
	"leafygo/internal/seed/targets"
	"leafygo/internal/store"
)

type userSeed struct {
	Username string
	Admin    bool
	Inactive bool
}

var userSeeds = []userSeed{
	{Username: "sari", Admin: true},
	{Username: "andi"},
	{Username: "bayu"},
	{Username: "putri"},
	{Username: "hendra"},
	{Username: "rina"},
	{Username: "joko"},
	{Username: "wulan"},
	{Username: "eko", Inactive: true},
}

type groupSeed struct {
	Name        string
	Description string
	Members     []string
	Access      map[string]domain.AccessLevel
}

var groupSeeds = []groupSeed{
	{"backend", "Engineers who build the storefront and inventory services.", []string{"andi", "bayu", "eko"},
		map[string]domain.AccessLevel{"Storefront": domain.LevelEdit, "Inventory": domain.LevelEdit, "Legacy ERP": domain.LevelView}},
	{"data", "Analysts and data engineers.", []string{"bayu", "putri"},
		map[string]domain.AccessLevel{"Storefront": domain.LevelView, "Inventory": domain.LevelView, "Billing": domain.LevelView, "Analytics": domain.LevelEdit, "Legacy ERP": domain.LevelView}},
	{"finance", "Billing, revenue, and reconciliation.", []string{"hendra"},
		map[string]domain.AccessLevel{"Billing": domain.LevelEdit, "Storefront": domain.LevelView, "Analytics": domain.LevelView}},
	{"hr", "People operations.", []string{"rina"},
		map[string]domain.AccessLevel{"People": domain.LevelEdit}},
	{"support", "Customer support agents.", []string{"joko"},
		map[string]domain.AccessLevel{"Storefront": domain.LevelView}},
}

type connSeed struct {
	Name     string
	Host     targets.Host
	DBName   string
	Username string
	Password string
}

type platformResult struct {
	users map[string]domain.User
	conns map[string]domain.DBConnection
}

func (r *runner) seedPlatform(ctx context.Context, conns []connSeed) (platformResult, error) {
	st := r.deps.Store
	res := platformResult{users: map[string]domain.User{}, conns: map[string]domain.DBConnection{}}

	if err := EnsureAdmin(ctx, st, r.opts.AdminUsername, r.opts.AdminPassword); err != nil {
		return res, fmt.Errorf("ensure admin: %w", err)
	}
	admin, err := st.GetUserByUsername(ctx, r.opts.AdminUsername)
	if err != nil {
		return res, fmt.Errorf("load admin: %w", err)
	}

	for _, u := range userSeeds {
		user, err := st.GetUserByUsername(ctx, u.Username)
		if errors.Is(err, store.ErrNotFound) {
			hash, herr := auth.HashPassword(r.opts.UserPassword)
			if herr != nil {
				return res, herr
			}
			if user, err = st.CreateUser(ctx, u.Username, hash, u.Admin); err != nil {
				return res, fmt.Errorf("create user %s: %w", u.Username, err)
			}
			if u.Inactive {
				if err := st.SetUserActiveGuarded(ctx, user.ID, false, admin.ID); err != nil {
					return res, fmt.Errorf("deactivate %s: %w", u.Username, err)
				}
			}
			r.log.Info("created user", "username", u.Username)
		} else if err != nil {
			return res, err
		}
		res.users[u.Username] = user
	}

	existing, err := st.ListDBConnections(ctx)
	if err != nil {
		return res, err
	}
	for _, c := range existing {
		res.conns[strings.ToLower(c.Name)] = c
	}
	for _, c := range conns {
		if _, ok := res.conns[strings.ToLower(c.Name)]; ok {
			continue
		}
		enc, err := r.deps.Box.Encrypt(c.Password)
		if err != nil {
			return res, err
		}
		created, err := st.CreateDBConnection(ctx, domain.DBConnection{
			Name: c.Name, Host: c.Host.Host, Port: c.Host.Port, DBName: c.DBName,
			Username: c.Username, PasswordEnc: enc, SSLMode: "disable", CreatedBy: &admin.ID,
		})
		if err != nil {
			return res, fmt.Errorf("create connection %s: %w", c.Name, err)
		}
		res.conns[strings.ToLower(c.Name)] = created
		r.log.Info("created connection", "name", c.Name)
	}

	// Group yang sudah ada tidak disentuh agar perubahan lewat UI admin bertahan.
	for _, g := range groupSeeds {
		if _, err := st.GetGroupByName(ctx, g.Name); err == nil {
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			return res, err
		}
		group, err := st.CreateGroup(ctx, g.Name, g.Description)
		if err != nil {
			return res, fmt.Errorf("create group %s: %w", g.Name, err)
		}
		for _, m := range g.Members {
			if err := st.AddGroupMember(ctx, group.ID, res.users[m].ID); err != nil {
				return res, fmt.Errorf("add %s to %s: %w", m, g.Name, err)
			}
		}
		for name, level := range g.Access {
			conn, ok := res.conns[strings.ToLower(name)]
			if !ok {
				continue
			}
			if err := st.SetGroupAccess(ctx, group.ID, conn.ID, level); err != nil {
				return res, fmt.Errorf("grant %s on %s: %w", g.Name, name, err)
			}
		}
		r.log.Info("created group", "name", g.Name)
	}
	return res, nil
}

func (r platformResult) user(name string) (domain.User, error) {
	u, ok := r.users[name]
	if !ok || u.ID == uuid.Nil {
		return domain.User{}, fmt.Errorf("seed user %q missing", name)
	}
	return u, nil
}
