package targets

import (
	"context"
	"fmt"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/jackc/pgx/v5"

	"leafygo/internal/seed/dataset"
)

type Target struct {
	Conn   string
	DBName string
	Host   Host
	build  func(ctx context.Context, conn *pgx.Conn, d *dataset.Dataset) error
	// Dijalankan sebagai superuser, untuk objek yang sengaja tidak bisa dibaca app.
	restricted func(ctx context.Context, conn *pgx.Conn, d *dataset.Dataset) error
}

type Credentials struct {
	Superuser         string
	SuperuserPassword string
	AppUser           string
	AppPassword       string
}

// Faker per target agar target yang dilewati tidak mengubah data target lain.
func All(seed uint64, primary, analytics Host) []Target {
	billing := gofakeit.New(seed + 3)
	var methods []paymentMethod
	return []Target{
		{Conn: "Storefront", DBName: "storefront", Host: primary, build: buildStorefront},
		{Conn: "Inventory", DBName: "inventory", Host: primary, build: buildInventory(gofakeit.New(seed + 2))},
		{Conn: "Billing", DBName: "billing", Host: primary, build: buildBilling(billing, &methods), restricted: restrictBilling(billing, &methods)},
		{Conn: "People", DBName: "people", Host: primary, build: buildPeople(gofakeit.New(seed + 4))},
		{Conn: "Analytics", DBName: "analytics", Host: analytics, build: buildAnalytics(gofakeit.New(seed + 5))},
	}
}

// Seed membuat role dan database bila belum ada, lalu mengisi data hanya bila database masih kosong.
func (t Target) Seed(ctx context.Context, c Credentials, d *dataset.Dataset) (bool, error) {
	su, err := connect(ctx, t.Host.url(c.Superuser, c.SuperuserPassword, "postgres"))
	if err != nil {
		return false, err
	}
	defer su.Close(context.WithoutCancel(ctx))
	if err := ensureRole(ctx, su, c.AppUser, c.AppPassword); err != nil {
		return false, fmt.Errorf("ensure role: %w", err)
	}
	if err := ensureDatabase(ctx, su, t.DBName, c.AppUser); err != nil {
		return false, fmt.Errorf("ensure database: %w", err)
	}

	app, err := connect(ctx, t.Host.url(c.AppUser, c.AppPassword, t.DBName))
	if err != nil {
		return false, err
	}
	defer app.Close(context.WithoutCancel(ctx))
	if seeded, err := hasUserTables(ctx, app); err != nil || seeded {
		return false, err
	}

	tx, err := app.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := t.build(ctx, tx.Conn(), d); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}

	if t.restricted != nil {
		db, err := connect(ctx, t.Host.url(c.Superuser, c.SuperuserPassword, t.DBName))
		if err != nil {
			return false, err
		}
		defer db.Close(context.WithoutCancel(ctx))
		if err := t.restricted(ctx, db, d); err != nil {
			return false, fmt.Errorf("restricted objects: %w", err)
		}
	}
	return true, nil
}
