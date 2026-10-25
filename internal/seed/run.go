package seed

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/brianvoe/gofakeit/v7"

	"leafygo/internal/migration"
	"leafygo/internal/secret"
	"leafygo/internal/seed/dataset"
	"leafygo/internal/seed/targets"
	"leafygo/internal/store"
)

// Seed tetap agar data sama di setiap volume baru.
const fakerSeed = 20261025

type Options struct {
	AdminUsername string
	AdminPassword string
	UserPassword  string

	Credentials targets.Credentials

	Targets   targets.Host
	Analytics targets.Host
	Legacy    targets.Host
}

type Deps struct {
	Store      *store.Store
	Box        *secret.Box
	Migrations *migration.Service
	Logger     *slog.Logger
}

type runner struct {
	deps Deps
	opts Options
	log  *slog.Logger
	now  time.Time
}

func Run(ctx context.Context, deps Deps, opts Options) error {
	r := &runner{deps: deps, opts: opts, log: deps.Logger, now: time.Now().UTC().Truncate(time.Second)}
	data := dataset.Generate(gofakeit.New(fakerSeed), r.now)

	list := targets.All(fakerSeed, opts.Targets, opts.Analytics)
	conns := make([]connSeed, 0, len(list)+1)
	for _, t := range list {
		seeded, err := t.Seed(ctx, opts.Credentials, data)
		if err != nil {
			return fmt.Errorf("seed %s: %w", t.DBName, err)
		}
		if seeded {
			r.log.Info("seeded target", "db", t.DBName)
		} else {
			r.log.Info("target already seeded", "db", t.DBName)
		}
		conns = append(conns, connSeed{Name: t.Conn, Host: t.Host, DBName: t.DBName, Username: opts.Credentials.AppUser, Password: opts.Credentials.AppPassword})
	}
	// Sengaja menunjuk host yang tidak ada.
	conns = append(conns, connSeed{Name: "Legacy ERP", Host: opts.Legacy, DBName: "erp", Username: "erp_reader", Password: "erp-reader-retired"})

	p, err := r.seedPlatform(ctx, conns)
	if err != nil {
		return err
	}
	if err := r.seedHistory(ctx, p); err != nil {
		return fmt.Errorf("seed history: %w", err)
	}
	r.log.Info("seed complete")
	return nil
}
