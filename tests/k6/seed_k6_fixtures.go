// Seeds fixtures for migration_stress.js.
package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"leafygo/internal/auth"
	"leafygo/internal/domain"
	"leafygo/internal/platform"
	"leafygo/internal/secret"
	"leafygo/internal/store"
)

const (
	connPrefix    = "k6 Target "
	dbNamePrefix  = "k6_target_"
	reviewerUser  = "k6_reviewer"
	approverUser  = "k6_approver"
	readerUser    = "k6_reader"
	reviewerGroup = "k6-reviewers"
	approverGroup = "k6-approvers"
)

type options struct {
	databaseURL   string
	encryptionKey string
	adminAddr     string
	connAddr      string
	superuser     string
	superPassword string
	appUser       string
	appPassword   string
	password      string
	dbs           int
	proposals     int
	workMS        int
	out           string
}

type fixtureDB struct {
	Name      string      `json:"name"`
	ID        uuid.UUID   `json:"id"`
	Pool      string      `json:"pool"`
	Proposals []uuid.UUID `json:"proposals"`
}

type fixtures struct {
	GeneratedAt time.Time   `json:"generated_at"`
	WorkMS      int         `json:"work_ms"`
	Approver    string      `json:"approver"`
	Reader      string      `json:"reader"`
	Databases   []fixtureDB `json:"databases"`
}

type actors struct {
	reviewer, approver   domain.User
	reviewers, approvers domain.Group
}

func main() {
	if err := run(); err != nil {
		slog.Error("k6 fixtures failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	o := parseFlags()
	if o.dbs < 2 || o.dbs > 26 {
		return errors.New("-dbs must be between 2 and 26")
	}
	if o.proposals < 1 {
		return errors.New("-proposals must be positive")
	}
	key, err := decodeKey(o.encryptionKey)
	if err != nil {
		return err
	}
	box, err := secret.New(key)
	if err != nil {
		return err
	}
	connHost, connPort, err := splitAddr(o.connAddr)
	if err != nil {
		return fmt.Errorf("-targets-conn: %w", err)
	}
	if _, _, err := splitAddr(o.adminAddr); err != nil {
		return fmt.Errorf("-targets-admin: %w", err)
	}

	ctx := context.Background()
	pool, err := platform.Open(ctx, o.databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	st := store.New(pool)

	a, err := ensureActors(ctx, st, o.password)
	if err != nil {
		return err
	}
	if err := retireTargets(ctx, st); err != nil {
		return err
	}

	fx := fixtures{GeneratedAt: time.Now().UTC(), WorkMS: o.workMS, Approver: approverUser, Reader: readerUser}
	var seedTotal time.Duration
	for i := range o.dbs {
		letter := string(rune('a' + i))
		dbName := dbNamePrefix + letter
		if err := createTargetDB(ctx, o, dbName); err != nil {
			return fmt.Errorf("create %s: %w", dbName, err)
		}
		enc, err := box.Encrypt(o.appPassword)
		if err != nil {
			return err
		}
		conn, err := st.CreateDBConnection(ctx, domain.DBConnection{
			Name: connPrefix + strings.ToUpper(letter), Host: connHost, Port: connPort, DBName: dbName,
			Username: o.appUser, PasswordEnc: enc, SSLMode: "disable",
		})
		if err != nil {
			return err
		}
		for _, g := range []domain.Group{a.reviewers, a.approvers} {
			if err := st.SetGroupAccess(ctx, g.ID, conn.ID, domain.LevelEdit); err != nil {
				return fmt.Errorf("grant %s: %w", g.Name, err)
			}
		}

		start := time.Now()
		ids, err := seedProposals(ctx, st, conn.ID, a, o)
		if err != nil {
			return fmt.Errorf("seed proposals on %s: %w", conn.Name, err)
		}
		seedTotal += time.Since(start)

		poolTag := "multi_db"
		if i == 0 {
			poolTag = "same_db"
		}
		fx.Databases = append(fx.Databases, fixtureDB{Name: conn.Name, ID: conn.ID, Pool: poolTag, Proposals: ids})
		slog.Info("target ready", "name", conn.Name, "db_id", conn.ID, "pool", poolTag, "proposals", len(ids))
	}
	slog.Info("bulk insert finished", "proposals", o.dbs*o.proposals, "duration_ms", seedTotal.Milliseconds())

	return writeFixtures(o.out, fx)
}

func parseFlags() options {
	var o options
	flag.StringVar(&o.databaseURL, "database-url", env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/leafygo?sslmode=disable"), "platform database URL")
	flag.StringVar(&o.encryptionKey, "encryption-key", env("ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="), "key the app uses to encrypt connection passwords")
	flag.StringVar(&o.adminAddr, "targets-admin", env("K6_TARGETS_ADMIN", "localhost:5434"), "target server address reachable from this machine")
	flag.StringVar(&o.connAddr, "targets-conn", env("K6_TARGETS_CONN", "targets-db:5432"), "target server address stored in db_connections, as the app and worker reach it")
	flag.StringVar(&o.superuser, "superuser", env("SEED_SUPERUSER", "postgres"), "target superuser")
	flag.StringVar(&o.superPassword, "superuser-password", env("SEED_SUPERUSER_PASSWORD", "postgres"), "target superuser password")
	flag.StringVar(&o.appUser, "app-user", env("SEED_APP_DB_USER", "leafygo_app"), "target role used by the app")
	flag.StringVar(&o.appPassword, "app-password", env("SEED_APP_DB_PASSWORD", "leafygo_app"), "target role password")
	flag.StringVar(&o.password, "password", env("K6_PASSWORD", "k6-password-123"), "password for the k6 users")
	flag.IntVar(&o.dbs, "dbs", 5, "number of target databases, the first is the same_db pool")
	flag.IntVar(&o.proposals, "proposals", 1100, "pending proposals per target database")
	flag.IntVar(&o.workMS, "work-ms", 0, "pg_sleep added to every migration to simulate a heavier statement")
	flag.StringVar(&o.out, "out", "tests/k6/fixtures.json", "fixture file read by migration_stress.js")
	flag.Parse()
	return o
}

func ensureActors(ctx context.Context, st *store.Store, password string) (actors, error) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return actors{}, err
	}
	users := map[string]domain.User{}
	for _, name := range []string{reviewerUser, approverUser, readerUser} {
		u, err := st.GetUserByUsername(ctx, name)
		switch {
		case errors.Is(err, store.ErrNotFound):
			u, err = st.CreateUser(ctx, name, hash, false)
		case err == nil:
			err = st.UpdateUserPassword(ctx, u.ID, hash)
		}
		if err != nil {
			return actors{}, fmt.Errorf("user %s: %w", name, err)
		}
		if !u.IsActive {
			return actors{}, fmt.Errorf("user %s is inactive", name)
		}
		users[name] = u
	}

	reviewers, err := ensureGroup(ctx, st, reviewerGroup, users[reviewerUser], users[readerUser])
	if err != nil {
		return actors{}, err
	}
	approvers, err := ensureGroup(ctx, st, approverGroup, users[approverUser])
	if err != nil {
		return actors{}, err
	}
	return actors{reviewer: users[reviewerUser], approver: users[approverUser], reviewers: reviewers, approvers: approvers}, nil
}

// First member becomes leader.
func ensureGroup(ctx context.Context, st *store.Store, name string, members ...domain.User) (domain.Group, error) {
	g, err := st.GetGroupByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		g, err = st.CreateGroup(ctx, name, "Stress test fixture group.")
	}
	if err != nil {
		return domain.Group{}, fmt.Errorf("group %s: %w", name, err)
	}
	current, err := st.ListGroupMembers(ctx, g.ID)
	if err != nil {
		return domain.Group{}, err
	}
	for _, m := range members {
		if !containsUser(current, m.ID) {
			if err := st.AddGroupMember(ctx, g.ID, m.ID); err != nil {
				return domain.Group{}, fmt.Errorf("add %s to %s: %w", m.Username, name, err)
			}
		}
	}
	return st.SetGroupLeader(ctx, g.ID, &members[0].ID)
}

func containsUser(users []domain.User, id uuid.UUID) bool {
	for _, u := range users {
		if u.ID == id {
			return true
		}
	}
	return false
}

func retireTargets(ctx context.Context, st *store.Store) error {
	conns, err := st.ListDBConnections(ctx)
	if err != nil {
		return err
	}
	for _, c := range conns {
		if !strings.HasPrefix(c.Name, connPrefix) {
			continue
		}
		if err := st.DeleteDBConnection(ctx, c.ID); err != nil {
			if errors.Is(err, store.ErrDBHasActiveWork) {
				return fmt.Errorf("%s still has queued or running jobs, wait for the worker to drain it", c.Name)
			}
			return fmt.Errorf("retire %s: %w", c.Name, err)
		}
		slog.Info("retired previous target", "name", c.Name)
	}
	return nil
}

func createTargetDB(ctx context.Context, o options, dbName string) error {
	su, err := pgx.Connect(ctx, targetURL(o.adminAddr, o.superuser, o.superPassword, "postgres"))
	if err != nil {
		return err
	}
	defer su.Close(ctx)
	if err := ensureRole(ctx, su, o.appUser, o.appPassword); err != nil {
		return err
	}
	ident := pgx.Identifier{dbName}.Sanitize()
	if _, err := su.Exec(ctx, "drop database if exists "+ident+" with (force)"); err != nil {
		return err
	}
	if _, err := su.Exec(ctx, "create database "+ident+" owner "+pgx.Identifier{o.appUser}.Sanitize()); err != nil {
		return err
	}

	app, err := pgx.Connect(ctx, targetURL(o.adminAddr, o.appUser, o.appPassword, dbName))
	if err != nil {
		return err
	}
	defer app.Close(ctx)
	_, err = app.Exec(ctx, `
		create table k6_events (
			id            bigint generated always as identity primary key,
			migration_seq bigint not null,
			created_at    timestamptz not null default now()
		)`)
	return err
}

func ensureRole(ctx context.Context, su *pgx.Conn, role, password string) error {
	var exists bool
	if err := su.QueryRow(ctx, `select exists(select 1 from pg_roles where rolname = $1)`, role).Scan(&exists); err != nil || exists {
		return err
	}
	var stmt string
	if err := su.QueryRow(ctx, `select format('create role %I login password %L', $1::text, $2::text)`, role, password).Scan(&stmt); err != nil {
		return err
	}
	_, err := su.Exec(ctx, stmt)
	return err
}

func migrationSQL(seq, workMS int) (up, down string) {
	up = fmt.Sprintf("insert into k6_events (migration_seq) values (%d);", seq)
	if workMS > 0 {
		up = fmt.Sprintf("select pg_sleep(%.3f);\n%s", float64(workMS)/1000, up)
	}
	down = fmt.Sprintf("delete from k6_events where migration_seq = %d;", seq)
	return up, down
}

func seedProposals(ctx context.Context, st *store.Store, dbID uuid.UUID, a actors, o options) ([]uuid.UUID, error) {
	n := o.proposals
	defs := make([][]any, 0, n)
	props := make([][]any, 0, n)
	approvers := make([][]any, 0, 2*n)
	approvals := make([][]any, 0, n)
	ids := make([]uuid.UUID, 0, n)

	db, reviewer, approver := pgUUID(dbID), pgUUID(a.reviewer.ID), pgUUID(a.approver.ID)
	reviewers, approversGroup := pgUUID(a.reviewers.ID), pgUUID(a.approvers.ID)
	for seq := 1; seq <= n; seq++ {
		defID, propID := uuid.New(), uuid.New()
		up, down := migrationSQL(seq, o.workMS)
		defs = append(defs, []any{
			pgUUID(defID), db, int64(seq), int64(seq), int32(1),
			fmt.Sprintf("k6 stress %04d", seq), "Generated by seed_k6_fixtures.", up, down,
			domain.ExecutionModeNormal, reviewer, reviewer,
		})
		props = append(props, []any{
			pgUUID(propID), db, pgUUID(defID), int32(1), domain.ActionApply, domain.ProposalPendingReview, reviewer,
		})
		approvers = append(approvers,
			[]any{pgUUID(propID), reviewers, reviewer, "edit"},
			[]any{pgUUID(propID), approversGroup, approver, "edit"},
		)
		approvals = append(approvals, []any{pgUUID(propID), reviewers, reviewer, "approved", "k6 fixture"})
		ids = append(ids, propID)
	}

	tx, err := st.Pool().Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	copies := []struct {
		table   string
		columns []string
		rows    [][]any
	}{
		{"migration_definitions", []string{"id", "db_id", "seq_version", "effective_seq", "revision", "title", "description", "sql_up", "sql_down", "execution_mode", "created_by", "updated_by"}, defs},
		{"migration_proposals", []string{"id", "db_id", "migration_id", "migration_revision", "action_type", "status", "proposed_by"}, props},
		{"migration_proposal_approvers", []string{"proposal_id", "group_id", "leader_id", "access_level"}, approvers},
		{"migration_approvals", []string{"proposal_id", "group_id", "leader_id", "decision", "comment"}, approvals},
	}
	for _, c := range copies {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{c.table}, c.columns, pgx.CopyFromRows(c.rows)); err != nil {
			return nil, fmt.Errorf("copy %s: %w", c.table, err)
		}
	}
	return ids, tx.Commit(ctx)
}

func writeFixtures(path string, fx fixtures) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(fx, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return err
	}
	slog.Info("fixtures written", "path", path)
	return nil
}

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

func targetURL(addr, user, password, dbname string) string {
	u := url.URL{Scheme: "postgres", User: url.UserPassword(user, password), Host: addr, Path: "/" + dbname, RawQuery: "sslmode=disable"}
	return u.String()
}

func splitAddr(addr string) (string, int, error) {
	host, rawPort, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port %q", rawPort)
	}
	return host, port, nil
}

func decodeKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	for _, dec := range []func(string) ([]byte, error){base64.StdEncoding.DecodeString, base64.RawStdEncoding.DecodeString, hex.DecodeString} {
		if b, err := dec(raw); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, errors.New("encryption key must decode to exactly 32 bytes (base64 or hex)")
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
