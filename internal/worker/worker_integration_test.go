package worker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leafygo/internal/config"
	"leafygo/internal/domain"
	"leafygo/internal/platform"
	"leafygo/internal/secret"
	"leafygo/internal/seed/targets"
	"leafygo/internal/store"
	"leafygo/internal/target"
)

const testSchema = "lg_worker_test"

type env struct {
	t         *testing.T
	ctx       context.Context
	dbURL     string
	pool      *pgxpool.Pool
	st        *store.Store
	box       *secret.Box
	targetCfg *pgx.ConnConfig
	db        domain.DBConnection
	group     uuid.UUID
	leader    uuid.UUID
}

type jobRef struct {
	JobID       uuid.UUID
	ProposalID  uuid.UUID
	ExecutionID uuid.UUID
	MigrationID uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	targetURL := os.Getenv("TEST_TARGET_DATABASE_URL")
	if dbURL == "" || targetURL == "" {
		t.Skip("TEST_DATABASE_URL or TEST_TARGET_DATABASE_URL is empty; skipping integration test")
	}
	ctx := context.Background()
	if err := platform.Migrate(ctx, dbURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `
		truncate users, groups, group_members, db_connections, db_access, sessions,
		         migration_history, migration_drafts, worker_heartbeats cascade`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("secret box: %v", err)
	}
	targetCfg, err := pgx.ParseConfig(targetURL)
	if err != nil {
		t.Fatalf("parse target url: %v", err)
	}
	e := &env{t: t, ctx: ctx, dbURL: dbURL, pool: pool, st: store.New(pool), box: box, targetCfg: targetCfg}

	e.leader = uuid.New()
	e.group = uuid.New()
	if _, err := pool.Exec(ctx, `insert into users (id, username, password_hash) values ($1, 'lead', 'x')`, e.leader); err != nil {
		t.Fatalf("insert leader: %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into groups (id, name, leader_id) values ($1, 'team', $2)`, e.group, e.leader); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	e.db = e.addDB("orders", targetCfg)
	e.targetExec(`
		drop schema if exists ` + testSchema + ` cascade;
		create schema ` + testSchema + `;
		drop table if exists public._leafygo_executions, public._leafygo_batch_checkpoints`)
	t.Cleanup(func() { e.targetExec(`drop schema if exists ` + testSchema + ` cascade`) })
	return e
}

func (e *env) addDB(name string, cfg *pgx.ConnConfig) domain.DBConnection {
	e.t.Helper()
	enc, err := e.box.Encrypt(cfg.Password)
	if err != nil {
		e.t.Fatalf("encrypt: %v", err)
	}
	db, err := e.st.CreateDBConnection(e.ctx, domain.DBConnection{
		Name: name, Host: cfg.Host, Port: int(cfg.Port), DBName: cfg.Database,
		Username: cfg.User, PasswordEnc: enc, SSLMode: "disable",
	})
	if err != nil {
		e.t.Fatalf("create db connection: %v", err)
	}
	if _, err := e.pool.Exec(e.ctx, `insert into db_access (db_id, group_id, level) values ($1, $2, 'edit')`, db.ID, e.group); err != nil {
		e.t.Fatalf("grant access: %v", err)
	}
	return db
}

func (e *env) targetConn(cfg *pgx.ConnConfig) *pgx.Conn {
	e.t.Helper()
	conn, err := pgx.ConnectConfig(e.ctx, cfg)
	if err != nil {
		e.t.Fatalf("connect target: %v", err)
	}
	e.t.Cleanup(func() { target.CloseConn(conn) })
	return conn
}

func (e *env) targetExec(sql string, args ...any) {
	e.t.Helper()
	conn, err := pgx.ConnectConfig(e.ctx, e.targetCfg)
	if err != nil {
		e.t.Fatalf("connect target: %v", err)
	}
	defer target.CloseConn(conn)
	if _, err := conn.Exec(e.ctx, sql, args...); err != nil {
		e.t.Fatalf("target exec %q: %v", sql, err)
	}
}

func (e *env) targetInt(sql string, args ...any) int64 {
	e.t.Helper()
	conn, err := pgx.ConnectConfig(e.ctx, e.targetCfg)
	if err != nil {
		e.t.Fatalf("connect target: %v", err)
	}
	defer target.CloseConn(conn)
	var n int64
	if err := conn.QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("target query %q: %v", sql, err)
	}
	return n
}

func (e *env) migration(db domain.DBConnection, sqlUp, sqlDown, mode, batchConfig string) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	var cfg any
	if batchConfig != "" {
		cfg = batchConfig
	}
	_, err := e.pool.Exec(e.ctx, `
		insert into migration_definitions (id, db_id, seq_version, effective_seq, title, sql_up, sql_down, execution_mode, batch_config)
		select $1, $2, coalesce(max(seq_version), 0) + 1, coalesce(max(seq_version), 0) + 1, 'm', $3, $4, $5, $6::jsonb
		from migration_definitions where db_id = $2`, id, db.ID, sqlUp, sqlDown, mode, cfg)
	if err != nil {
		e.t.Fatalf("insert definition: %v", err)
	}
	return id
}

// Enqueues an approved job the way the approve handler will. root and targetExec are nil for apply.
func (e *env) enqueue(db domain.DBConnection, migrationID uuid.UUID, action, mode string, root, targetExec *uuid.UUID) jobRef {
	e.t.Helper()
	ref := jobRef{ProposalID: uuid.New(), ExecutionID: uuid.New(), MigrationID: migrationID}
	rootID := ref.ExecutionID
	if root != nil {
		rootID = *root
	}
	tx, err := e.pool.Begin(e.ctx)
	if err != nil {
		e.t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(e.ctx) }()
	steps := []struct {
		sql  string
		args []any
	}{
		{`insert into migration_proposals (id, db_id, migration_id, migration_revision, action_type, target_execution_id, status)
		  values ($1, $2, $3, 1, $4, $5, 'queued')`, []any{ref.ProposalID, db.ID, migrationID, action, targetExec}},
		{`insert into migration_proposal_approvers (proposal_id, group_id, leader_id, access_level)
		  select $1, a.group_id, g.leader_id, a.level from db_access a join groups g on g.id = a.group_id where a.db_id = $2`,
			[]any{ref.ProposalID, db.ID}},
		{`insert into migration_approvals (proposal_id, group_id, leader_id, decision)
		  select proposal_id, group_id, leader_id, 'approved' from migration_proposal_approvers where proposal_id = $1`,
			[]any{ref.ProposalID}},
		{`insert into migration_executions (
			execution_id, root_execution_id, db_id, migration_id, proposal_id, migration_revision, action_type,
			executed_sql, executed_by_username, db_name_snapshot, migration_title_snapshot, status, rollback_of_execution_id)
		  values ($1, $2, $3, $4, $5, 1, $6, 'sql', 'lead', 'orders', 'm', 'queued', $7)`,
			[]any{ref.ExecutionID, rootID, db.ID, migrationID, ref.ProposalID, action, targetExec}},
	}
	for _, s := range steps {
		if _, err := tx.Exec(e.ctx, s.sql, s.args...); err != nil {
			e.t.Fatalf("enqueue fixture: %v", err)
		}
	}
	job, err := e.st.EnqueueMigrationJobTx(e.ctx, tx, store.EnqueueInput{
		DBID: db.ID, ProposalID: ref.ProposalID, MigrationID: migrationID, ExecutionID: ref.ExecutionID,
		RootExecutionID: rootID, ActionType: action, ExecutionMode: mode,
	})
	if err != nil {
		e.t.Fatalf("enqueue: %v", err)
	}
	if err := tx.Commit(e.ctx); err != nil {
		e.t.Fatalf("commit: %v", err)
	}
	ref.JobID = job.ID
	return ref
}

func (e *env) worker(mods ...func(*config.WorkerConfig)) *Worker {
	cfg := config.WorkerConfig{
		ID:                "w-" + uuid.NewString()[:8],
		MaxConcurrentJobs: 4,
		PollInterval:      100 * time.Millisecond,
		LeaseTTL:          15 * time.Second,
		HeartbeatInterval: 5 * time.Second,
		RecoveryInterval:  time.Second,
		ReprobeInterval:   15 * time.Second,
		ProbeTimeout:      3 * time.Second,
		ExecTimeout:       time.Minute,
		ShutdownTimeout:   10 * time.Second,
	}
	for _, m := range mods {
		m(&cfg)
	}
	var out io.Writer = io.Discard
	if testing.Verbose() {
		out = os.Stderr
	}
	return New(e.st, e.box, e.dbURL, cfg, 5*time.Second, slog.New(slog.NewTextHandler(out, nil)))
}

// Drains the queue until nothing claimable is left, like the worker loop does after every job.
func (e *env) runAll(w *Worker) {
	e.t.Helper()
	for range 50 {
		w.drain(e.ctx, e.ctx)
		w.jobs.Wait()
		var ready int
		if err := e.pool.QueryRow(e.ctx, `
			select count(*) from migration_jobs j
			where j.status = 'queued' and j.next_run_at <= now()
			  and not exists (select 1 from database_migration_locks l where l.db_id = j.db_id and l.lock_state <> 'running')`).
			Scan(&ready); err != nil {
			e.t.Fatalf("count ready jobs: %v", err)
		}
		if ready == 0 {
			return
		}
	}
	e.t.Fatal("queue did not drain")
}

type jobState struct {
	Job, Execution, Proposal string
	LastError                string
	ExecBatches              int
	ExecRows                 int64
	JobBatches               int
	JobRows                  int64
	TotalBatches             int
}

func (e *env) state(ref jobRef) jobState {
	e.t.Helper()
	var s jobState
	if err := e.pool.QueryRow(e.ctx, `
		select j.status, e.status, p.status, j.last_error, e.committed_batches, e.committed_rows,
		       j.completed_batches, j.committed_rows, j.total_batches
		from migration_jobs j
		join migration_executions e on e.execution_id = j.execution_id
		join migration_proposals p on p.id = j.proposal_id
		where j.job_id = $1`, ref.JobID).
		Scan(&s.Job, &s.Execution, &s.Proposal, &s.LastError, &s.ExecBatches, &s.ExecRows, &s.JobBatches, &s.JobRows, &s.TotalBatches); err != nil {
		e.t.Fatalf("load state: %v", err)
	}
	return s
}

func (e *env) lockState(db domain.DBConnection) string {
	e.t.Helper()
	l, err := e.st.GetDBLock(e.ctx, db.ID)
	if err != nil {
		return ""
	}
	return l.LockState
}

func (e *env) immutable(migrationID uuid.UUID) bool {
	e.t.Helper()
	var v bool
	if err := e.pool.QueryRow(e.ctx, `select is_immutable from migration_definitions where id = $1`, migrationID).Scan(&v); err != nil {
		e.t.Fatalf("load definition: %v", err)
	}
	return v
}

func (e *env) expectState(ref jobRef, job, proposal string) jobState {
	e.t.Helper()
	s := e.state(ref)
	if s.Job != job || s.Execution != job || s.Proposal != proposal {
		e.t.Fatalf("state = %+v; want job/execution %s, proposal %s", s, job, proposal)
	}
	return s
}

const (
	benchRows   = 1000
	benchConfig = `{"table_name":"` + testSchema + `.bench","cursor_col":"id","batch_size":100}`
	incUp       = `update ` + testSchema + `.bench set v = v + 1 where id >= {{cursor_from}} and id <= {{cursor_to}}`
	incDown     = `update ` + testSchema + `.bench set v = v - 1 where id >= {{cursor_from}} and id <= {{cursor_to}}`
	// Fails from id 551 on while the flag is 0, so batches 1 to 5 commit and batch 6 fails.
	flakyUp = `update ` + testSchema + `.bench set v = v + case when id > 550 then 1 / (select ok from ` + testSchema + `.flag) else 1 end
		where id between $1 and $2`
)

func (e *env) createBench() {
	e.targetExec(`
		create table ` + testSchema + `.bench (id bigint primary key, v int not null default 0);
		insert into ` + testSchema + `.bench (id) select generate_series(1, 1000);
		create table ` + testSchema + `.flag (ok int not null);
		insert into ` + testSchema + `.flag values (0)`)
}

func (e *env) benchSum(where string) int64 {
	return e.targetInt(`select coalesce(sum(v), 0) from ` + testSchema + `.bench where ` + where)
}

func TestNormalApplySucceeds(t *testing.T) {
	e := newEnv(t)
	mig := e.migration(e.db, `create table `+testSchema+`.t1 (id int); insert into `+testSchema+`.t1 values (1), (2)`,
		`drop table `+testSchema+`.t1`, domain.ModeNormal, "")
	ref := e.enqueue(e.db, mig, domain.ActionApply, domain.ModeNormal, nil, nil)

	e.runAll(e.worker())

	s := e.expectState(ref, domain.JobSuccess, "completed")
	if s.ExecBatches != 1 || s.JobBatches != 1 {
		t.Fatalf("progress = %+v", s)
	}
	if !e.immutable(mig) {
		t.Fatal("definition was not frozen")
	}
	if l := e.lockState(e.db); l != "" {
		t.Fatalf("lock = %q; want released", l)
	}
	if n := e.targetInt(`select count(*) from ` + testSchema + `.t1`); n != 2 {
		t.Fatalf("rows = %d", n)
	}
	if n := e.targetInt(`select count(*) from _leafygo_executions where execution_id = $1`, ref.ExecutionID); n != 1 {
		t.Fatalf("evidence rows = %d", n)
	}
}

func TestNormalFailureRollsBackWholeScript(t *testing.T) {
	e := newEnv(t)
	mig := e.migration(e.db, `create table `+testSchema+`.t2 (id int); select 1 / 0`, `drop table `+testSchema+`.t2`, domain.ModeNormal, "")
	ref := e.enqueue(e.db, mig, domain.ActionApply, domain.ModeNormal, nil, nil)

	e.runAll(e.worker())

	s := e.expectState(ref, domain.JobFailed, domain.JobFailed)
	if !strings.HasPrefix(s.LastError, CodeMigrationFailed) || !strings.Contains(s.LastError, "22012") {
		t.Fatalf("last error = %q", s.LastError)
	}
	if n := e.targetInt(`select count(*) from information_schema.tables where table_schema = $1 and table_name = 't2'`, testSchema); n != 0 {
		t.Fatal("first statement was not rolled back")
	}
	if n := e.targetInt(`select count(*) from _leafygo_executions`); n != 0 {
		t.Fatalf("evidence rows = %d", n)
	}
	if l := e.lockState(e.db); l != "" {
		t.Fatalf("lock = %q; want released", l)
	}
}

func TestRevalidationFailsWhenGroupGainsAccess(t *testing.T) {
	e := newEnv(t)
	mig := e.migration(e.db, `create table `+testSchema+`.t3 (id int)`, `drop table `+testSchema+`.t3`, domain.ModeNormal, "")
	ref := e.enqueue(e.db, mig, domain.ActionApply, domain.ModeNormal, nil, nil)
	other := uuid.New()
	if _, err := e.pool.Exec(e.ctx, `insert into groups (id, name, leader_id) values ($1, 'late', $2)`, other, e.leader); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if _, err := e.pool.Exec(e.ctx, `insert into db_access (db_id, group_id, level) values ($1, $2, 'view')`, e.db.ID, other); err != nil {
		t.Fatalf("grant access: %v", err)
	}

	e.runAll(e.worker())

	s := e.expectState(ref, domain.JobFailed, domain.JobFailed)
	if !strings.HasPrefix(s.LastError, CodeApproverSetChanged) {
		t.Fatalf("last error = %q", s.LastError)
	}
	if e.immutable(mig) {
		t.Fatal("definition was frozen although nothing ran")
	}
	if n := e.targetInt(`select count(*) from information_schema.tables where table_schema = $1 and table_name = 't3'`, testSchema); n != 0 {
		t.Fatal("migration ran on the target")
	}
	if l := e.lockState(e.db); l != "" {
		t.Fatalf("lock = %q; want released", l)
	}
}

func TestSuccessorFailsWhenPredecessorFailed(t *testing.T) {
	e := newEnv(t)
	first := e.migration(e.db, `select 1 / 0`, `select 1`, domain.ModeNormal, "")
	second := e.migration(e.db, `create table `+testSchema+`.t4 (id int)`, `drop table `+testSchema+`.t4`, domain.ModeNormal, "")
	a := e.enqueue(e.db, first, domain.ActionApply, domain.ModeNormal, nil, nil)
	b := e.enqueue(e.db, second, domain.ActionApply, domain.ModeNormal, nil, nil)

	e.runAll(e.worker())

	e.expectState(a, domain.JobFailed, domain.JobFailed)
	s := e.expectState(b, domain.JobFailed, domain.JobFailed)
	if !strings.HasPrefix(s.LastError, CodePredecessorFailed) {
		t.Fatalf("last error = %q", s.LastError)
	}
	if e.immutable(second) {
		t.Fatal("successor definition was frozen")
	}
}

func TestDrainRunsBacklogInQueueOrder(t *testing.T) {
	e := newEnv(t)
	var refs []jobRef
	for i := 1; i <= 3; i++ {
		name := testSchema + ".seq" + string(rune('0'+i))
		mig := e.migration(e.db, `create table `+name+` (id int)`, `drop table `+name, domain.ModeNormal, "")
		refs = append(refs, e.enqueue(e.db, mig, domain.ActionApply, domain.ModeNormal, nil, nil))
	}

	w := e.worker()
	w.drain(e.ctx, e.ctx)
	w.jobs.Wait()
	e.runAll(w)

	var prev time.Time
	for i, ref := range refs {
		e.expectState(ref, domain.JobSuccess, "completed")
		var finished time.Time
		if err := e.pool.QueryRow(e.ctx, `select finished_at from migration_jobs where job_id = $1`, ref.JobID).Scan(&finished); err != nil {
			t.Fatalf("load finished_at: %v", err)
		}
		if i > 0 && !finished.After(prev) {
			t.Fatalf("job %d finished before job %d", i, i-1)
		}
		prev = finished
	}
}

func TestDrainRespectsConcurrencyCap(t *testing.T) {
	e := newEnv(t)
	dbs := []domain.DBConnection{e.db}
	for _, name := range []string{"leafygo_target_test_b", "leafygo_target_test_c"} {
		e.ensureDatabase(name)
		cfg := e.targetCfg.Copy()
		cfg.Database = name
		dbs = append(dbs, e.addDB(name, cfg))
	}
	var refs []jobRef
	for _, db := range dbs {
		mig := e.migration(db, `select pg_sleep(1)`, `select 1`, domain.ModeNormal, "")
		refs = append(refs, e.enqueue(db, mig, domain.ActionApply, domain.ModeNormal, nil, nil))
	}

	w := e.worker(func(c *config.WorkerConfig) { c.MaxConcurrentJobs = 2 })
	w.drain(e.ctx, e.ctx)
	var running int
	if err := e.pool.QueryRow(e.ctx, `select count(*) from migration_jobs where status = 'running'`).Scan(&running); err != nil {
		t.Fatalf("count running: %v", err)
	}
	if running != 2 {
		t.Fatalf("running jobs = %d; want 2", running)
	}
	w.jobs.Wait()
	e.runAll(w)
	for _, ref := range refs {
		e.expectState(ref, domain.JobSuccess, "completed")
	}
}

func (e *env) ensureDatabase(name string) {
	e.t.Helper()
	conn, err := pgx.ConnectConfig(e.ctx, e.targetCfg)
	if err != nil {
		e.t.Fatalf("connect target: %v", err)
	}
	defer target.CloseConn(conn)
	var exists bool
	if err := conn.QueryRow(e.ctx, `select exists (select 1 from pg_database where datname = $1)`, name).Scan(&exists); err != nil {
		e.t.Fatalf("check database: %v", err)
	}
	if !exists {
		if _, err := conn.Exec(e.ctx, `create database `+pgx.Identifier{name}.Sanitize()); err != nil {
			e.t.Fatalf("create database: %v", err)
		}
	}
}

func TestBatchApplyThenRollback(t *testing.T) {
	e := newEnv(t)
	e.createBench()
	mig := e.migration(e.db, incUp, incDown, domain.ModeBatch, benchConfig)
	apply := e.enqueue(e.db, mig, domain.ActionApply, domain.ModeBatch, nil, nil)
	w := e.worker()
	e.runAll(w)

	s := e.expectState(apply, domain.JobSuccess, "completed")
	if s.JobBatches != 10 || s.JobRows != benchRows || s.TotalBatches != 10 || s.ExecBatches != 10 || s.ExecRows != benchRows {
		t.Fatalf("progress = %+v", s)
	}
	if got := e.benchSum("true"); got != benchRows {
		t.Fatalf("sum = %d; want %d", got, benchRows)
	}
	if n := e.targetInt(`select count(*) from _leafygo_batch_checkpoints where root_execution_id = $1`, apply.ExecutionID); n != 10 {
		t.Fatalf("checkpoints = %d", n)
	}

	rollback := e.enqueue(e.db, mig, domain.ActionRollback, domain.ModeBatch, &apply.ExecutionID, &apply.ExecutionID)
	e.runAll(w)

	s = e.expectState(rollback, domain.JobSuccess, "completed")
	if s.ExecBatches != 10 || s.JobBatches != 0 || s.JobRows != 0 {
		t.Fatalf("rollback progress = %+v", s)
	}
	if got := e.benchSum("true"); got != 0 {
		t.Fatalf("sum after rollback = %d", got)
	}
	if n := e.targetInt(`select count(*) from _leafygo_batch_checkpoints where reverted_by_execution_id = $1`, rollback.ExecutionID); n != 10 {
		t.Fatalf("reverted checkpoints = %d", n)
	}
	if l := e.lockState(e.db); l != "" {
		t.Fatalf("lock = %q; want released", l)
	}
}

func (e *env) partialApply() (uuid.UUID, jobRef, *Worker) {
	e.t.Helper()
	e.createBench()
	mig := e.migration(e.db, flakyUp, incDown, domain.ModeBatch, benchConfig)
	apply := e.enqueue(e.db, mig, domain.ActionApply, domain.ModeBatch, nil, nil)
	w := e.worker()
	e.runAll(w)

	s := e.expectState(apply, domain.JobPartial, domain.JobPartial)
	if s.JobBatches != 5 || s.JobRows != 500 {
		e.t.Fatalf("partial progress = %+v", s)
	}
	if l := e.lockState(e.db); l != domain.LockPartialHold {
		e.t.Fatalf("lock = %q; want partial_hold", l)
	}
	if got := e.benchSum("id <= 500"); got != 500 {
		e.t.Fatalf("committed batches sum = %d", got)
	}
	if got := e.benchSum("id > 500"); got != 0 {
		e.t.Fatalf("failed batch left changes, sum = %d", got)
	}
	return mig, apply, w
}

func TestBatchPartialThenRetryFromCheckpoint(t *testing.T) {
	e := newEnv(t)
	mig, apply, w := e.partialApply()

	blocked := e.migration(e.db, `create table `+testSchema+`.after (id int)`, `drop table `+testSchema+`.after`, domain.ModeNormal, "")
	next := e.enqueue(e.db, blocked, domain.ActionApply, domain.ModeNormal, nil, nil)
	e.runAll(w)
	e.expectState(next, domain.JobQueued, domain.JobQueued)

	e.targetExec(`update ` + testSchema + `.flag set ok = 1`)
	retry := e.enqueue(e.db, mig, domain.ActionBatchRetry, domain.ModeBatch, &apply.ExecutionID, &apply.ExecutionID)
	e.runAll(w)

	s := e.expectState(retry, domain.JobSuccess, "completed")
	if s.ExecBatches != 5 || s.ExecRows != 500 || s.JobBatches != 10 || s.JobRows != benchRows {
		t.Fatalf("retry progress = %+v", s)
	}
	if got := e.benchSum("true"); got != benchRows {
		t.Fatalf("sum = %d; every row must be applied exactly once", got)
	}
	if n := e.targetInt(`select count(*) from _leafygo_batch_checkpoints where root_execution_id = $1`, apply.ExecutionID); n != 10 {
		t.Fatalf("checkpoints = %d", n)
	}
	e.expectState(apply, domain.JobPartial, domain.JobPartial)
	e.expectState(next, domain.JobSuccess, "completed")
}

func TestBatchRevertFromPartial(t *testing.T) {
	e := newEnv(t)
	mig, apply, w := e.partialApply()

	revert := e.enqueue(e.db, mig, domain.ActionBatchRevert, domain.ModeBatch, &apply.ExecutionID, &apply.ExecutionID)
	e.runAll(w)

	s := e.expectState(revert, domain.JobSuccess, "completed")
	if s.ExecBatches != 5 || s.JobBatches != 0 {
		t.Fatalf("revert progress = %+v", s)
	}
	if got := e.benchSum("true"); got != 0 {
		t.Fatalf("sum after revert = %d", got)
	}
	if l := e.lockState(e.db); l != "" {
		t.Fatalf("lock = %q; want released", l)
	}
}

func TestBatchRetryAfterRevertIsRejected(t *testing.T) {
	e := newEnv(t)
	mig, apply, w := e.partialApply()

	e.targetExec(`update _leafygo_batch_checkpoints set reverted_at = now(), reverted_by_execution_id = $1
		where root_execution_id = $1 and batch_no = 5`, apply.ExecutionID)
	retry := e.enqueue(e.db, mig, domain.ActionBatchRetry, domain.ModeBatch, &apply.ExecutionID, &apply.ExecutionID)
	e.runAll(w)

	s := e.expectState(retry, domain.JobFailed, domain.JobFailed)
	if !strings.HasPrefix(s.LastError, CodeBatchNoCollision) {
		t.Fatalf("last error = %q", s.LastError)
	}
	l, err := e.st.GetDBLock(e.ctx, e.db.ID)
	if err != nil || l.LockState != domain.LockPartialHold || l.ExecutionID != apply.ExecutionID {
		t.Fatalf("lock = %+v, %v; want the partial hold restored", l, err)
	}
}

func TestShutdownHandsOffAndRecoveryResumesBatches(t *testing.T) {
	e := newEnv(t)
	e.createBench()
	mig := e.migration(e.db, incUp, incDown, domain.ModeBatch, benchConfig)
	apply := e.enqueue(e.db, mig, domain.ActionApply, domain.ModeBatch, nil, nil)

	first := e.worker()
	first.afterBatch = func(_ uuid.UUID, batchNo int) {
		if batchNo == 3 {
			first.stopping.Store(true)
		}
	}
	first.drain(e.ctx, e.ctx)
	first.jobs.Wait()

	s := e.expectState(apply, domain.JobRunning, "running")
	if s.JobBatches != 3 {
		t.Fatalf("progress before hand off = %+v", s)
	}

	second := e.worker()
	time.Sleep(10 * time.Millisecond)
	second.recoverOnce(e.ctx, e.ctx)
	second.jobs.Wait()

	s = e.expectState(apply, domain.JobSuccess, "completed")
	if s.JobBatches != 10 || s.ExecBatches != 10 || s.ExecRows != benchRows {
		t.Fatalf("progress after resume = %+v", s)
	}
	if got := e.benchSum("true"); got != benchRows {
		t.Fatalf("sum = %d; resumed batches must not run twice", got)
	}
}

// Claims a job and starts its execution without running it, as if the worker died right after.
func (e *env) claimAndStart(ref jobRef) *store.ClaimedJob {
	e.t.Helper()
	c, err := e.st.ClaimNextJob(e.ctx, "dead-worker", 15*time.Second)
	if err != nil || c == nil || c.Job.ID != ref.JobID {
		e.t.Fatalf("claim = %+v, %v", c, err)
	}
	if err := e.st.StartExecution(e.ctx, c.Lease, ref.MigrationID, true); err != nil {
		e.t.Fatalf("start execution: %v", err)
	}
	return c
}

func (e *env) expireLease() {
	e.t.Helper()
	if _, err := e.pool.Exec(e.ctx, `update database_migration_locks set lease_until = now() - interval '1 second'`); err != nil {
		e.t.Fatalf("expire lease: %v", err)
	}
}

func (e *env) writeEvidence(ref jobRef) {
	e.t.Helper()
	password, _ := e.box.Decrypt(e.db.PasswordEnc)
	conn, err := target.OpenExecConn(e.ctx, e.db, password, uuid.New(), 5*time.Second)
	if err != nil {
		e.t.Fatalf("open exec conn: %v", err)
	}
	defer target.CloseConn(conn)
	if _, err := conn.Exec(e.ctx, `insert into _leafygo_executions (execution_id, migration_id, action_type, lock_epoch)
		values ($1, $2, 'apply', 1)`, ref.ExecutionID, ref.MigrationID); err != nil {
		e.t.Fatalf("write evidence: %v", err)
	}
}

func TestRecoveryRequeuesNormalJobWithoutEvidence(t *testing.T) {
	e := newEnv(t)
	mig := e.migration(e.db, `create table `+testSchema+`.t5 (id int)`, `drop table `+testSchema+`.t5`, domain.ModeNormal, "")
	ref := e.enqueue(e.db, mig, domain.ActionApply, domain.ModeNormal, nil, nil)
	e.claimAndStart(ref)
	e.expireLease()

	e.worker().recoverOnce(e.ctx, e.ctx)

	e.expectState(ref, domain.JobQueued, domain.JobQueued)
	job, err := e.st.GetJob(e.ctx, ref.JobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.RetryCount != 1 || !strings.HasPrefix(job.LastError, CodeExecutionLeaseRecovery) {
		t.Fatalf("job = %+v", job)
	}
	if l := e.lockState(e.db); l != "" {
		t.Fatalf("lock = %q; want released", l)
	}
}

func TestBusyWorkerLeavesExpiredLeaseAlone(t *testing.T) {
	e := newEnv(t)
	mig := e.migration(e.db, `select 1`, `select 1`, domain.ModeNormal, "")
	ref := e.enqueue(e.db, mig, domain.ActionApply, domain.ModeNormal, nil, nil)
	e.claimAndStart(ref)
	e.expireLease()

	w := e.worker(func(c *config.WorkerConfig) { c.MaxConcurrentJobs = 1 })
	w.sem <- struct{}{}
	w.recoverOnce(e.ctx, e.ctx)

	l, err := e.st.GetDBLock(e.ctx, e.db.ID)
	if err != nil {
		t.Fatalf("get lock: %v", err)
	}
	if l.WorkerID == w.ID() || l.LockEpoch != 1 {
		t.Fatalf("lock = %+v; a worker without a free slot must not take it over", l)
	}

	<-w.sem
	w.recoverOnce(e.ctx, e.ctx)
	e.expectState(ref, domain.JobQueued, domain.JobQueued)
	if len(w.sem) != 0 {
		t.Fatalf("slots in use after recovery = %d; want 0", len(w.sem))
	}
}

func TestRecoveryCompletesNormalJobWithEvidence(t *testing.T) {
	e := newEnv(t)
	mig := e.migration(e.db, `select 1`, `select 1`, domain.ModeNormal, "")
	ref := e.enqueue(e.db, mig, domain.ActionApply, domain.ModeNormal, nil, nil)
	e.claimAndStart(ref)
	e.writeEvidence(ref)
	e.expireLease()

	e.worker().recoverOnce(e.ctx, e.ctx)

	e.expectState(ref, domain.JobSuccess, "completed")
	if l := e.lockState(e.db); l != "" {
		t.Fatalf("lock = %q; want released", l)
	}
}

func TestRecoveryRequeuesJobThatNeverStarted(t *testing.T) {
	e := newEnv(t)
	mig := e.migration(e.db, `select 1`, `select 1`, domain.ModeNormal, "")
	ref := e.enqueue(e.db, mig, domain.ActionApply, domain.ModeNormal, nil, nil)
	if c, err := e.st.ClaimNextJob(e.ctx, "dead-worker", 15*time.Second); err != nil || c == nil {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	e.expireLease()

	e.worker().recoverOnce(e.ctx, e.ctx)

	e.expectState(ref, domain.JobQueued, domain.JobQueued)
	if e.immutable(mig) {
		t.Fatal("definition frozen for a job that never started")
	}
}

func TestReprobeSettlesUncertainNormalJob(t *testing.T) {
	e := newEnv(t)
	mig := e.migration(e.db, `select 1`, `select 1`, domain.ModeNormal, "")
	ref := e.enqueue(e.db, mig, domain.ActionApply, domain.ModeNormal, nil, nil)
	c := e.claimAndStart(ref)
	if err := e.st.MarkUncertain(e.ctx, c.Lease, 0, CodeUncertain+": commit timed out"); err != nil {
		t.Fatalf("mark uncertain: %v", err)
	}
	e.expectState(ref, domain.JobUncertain, domain.JobUncertain)
	if l := e.lockState(e.db); l != domain.LockUncertainHold {
		t.Fatalf("lock = %q; want uncertain_hold", l)
	}
	e.writeEvidence(ref)

	e.worker().recoverOnce(e.ctx, e.ctx)

	e.expectState(ref, domain.JobSuccess, "completed")
	if l := e.lockState(e.db); l != "" {
		t.Fatalf("lock = %q; want released", l)
	}
}

func TestLostLeaseAbortsNormalExecution(t *testing.T) {
	e := newEnv(t)
	mig := e.migration(e.db, `create table `+testSchema+`.slow (id int); select pg_sleep(30)`, `select 1`, domain.ModeNormal, "")
	ref := e.enqueue(e.db, mig, domain.ActionApply, domain.ModeNormal, nil, nil)
	w := e.worker(func(c *config.WorkerConfig) { c.HeartbeatInterval = 100 * time.Millisecond })
	w.drain(e.ctx, e.ctx)

	deadline := time.Now().Add(5 * time.Second)
	for e.state(ref).Execution != domain.JobRunning {
		if time.Now().After(deadline) {
			t.Fatal("execution did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := e.pool.Exec(e.ctx, `update database_migration_locks set lock_epoch = lock_epoch + 1, worker_id = 'recovery'`); err != nil {
		t.Fatalf("steal lease: %v", err)
	}

	done := make(chan struct{})
	go func() {
		w.jobs.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("execution kept running after losing its lease")
	}

	e.expectState(ref, domain.JobRunning, "running")
	if n := e.targetInt(`select count(*) from information_schema.tables where table_schema = $1 and table_name = 'slow'`, testSchema); n != 0 {
		t.Fatal("aborted transaction left changes on the target")
	}
	deadline = time.Now().Add(5 * time.Second)
	for e.targetInt(`select count(*) from pg_stat_activity where application_name = $1`, target.ExecAppName(ref.ExecutionID)) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("old execution session is still alive")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestCorrectiveMigrationSupersedesFailedOne(t *testing.T) {
	e := newEnv(t)
	first := e.migration(e.db, `create table `+testSchema+`.base (id int)`, `drop table `+testSchema+`.base`, domain.ModeNormal, "")
	broken := e.migration(e.db, `select 1 / 0`, `select 1`, domain.ModeNormal, "")
	w := e.worker()
	e.enqueue(e.db, first, domain.ActionApply, domain.ModeNormal, nil, nil)
	failed := e.enqueue(e.db, broken, domain.ActionApply, domain.ModeNormal, nil, nil)
	e.runAll(w)
	e.expectState(failed, domain.JobFailed, domain.JobFailed)

	fix := uuid.New()
	if _, err := e.pool.Exec(e.ctx, `
		insert into migration_definitions (id, db_id, seq_version, effective_seq, title, sql_up, sql_down, replaces_migration_id)
		values ($1, $2, 3, 2, 'fix', 'create table `+testSchema+`.fixed (id int)', 'drop table `+testSchema+`.fixed', $3)`,
		fix, e.db.ID, broken); err != nil {
		t.Fatalf("insert corrective definition: %v", err)
	}
	ref := e.enqueue(e.db, fix, domain.ActionApply, domain.ModeNormal, nil, nil)
	e.runAll(w)

	e.expectState(ref, domain.JobSuccess, "completed")
	var superseded bool
	var by *uuid.UUID
	if err := e.pool.QueryRow(e.ctx, `select is_superseded, superseded_by_migration_id from migration_definitions where id = $1`, broken).
		Scan(&superseded, &by); err != nil {
		t.Fatalf("load definition: %v", err)
	}
	if !superseded || by == nil || *by != fix {
		t.Fatalf("superseded = %v by %v; want true by %s", superseded, by, fix)
	}
}

// Runs the change that takes over a second unbatched as a batch migration on the seeder's dataset.
func TestBatchMigrationOnBenchDataset(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the full bench dataset")
	}
	e := newEnv(t)
	table := testSchema + "." + targets.BenchTable
	conn := e.targetConn(e.targetCfg)
	if err := targets.CreateBenchTable(e.ctx, conn, table, targets.BenchRows); err != nil {
		t.Fatalf("create bench table: %v", err)
	}
	if _, err := conn.Exec(e.ctx, `vacuum analyze `+table); err != nil {
		t.Fatalf("vacuum bench table: %v", err)
	}
	unbatched, err := targets.MeasureUnbatchedUpdate(e.ctx, conn, table)
	if err != nil {
		t.Fatalf("measure unbatched update: %v", err)
	}
	if unbatched.Rows != targets.BenchRows {
		t.Fatalf("unbatched update touched %d rows; want %d", unbatched.Rows, targets.BenchRows)
	}
	t.Logf("unbatched update: %d rows in %d ms", unbatched.Rows, unbatched.Duration.Milliseconds())
	if unbatched.Duration <= time.Second {
		t.Logf("unbatched update took one second or less on this machine, raise targets.BenchRows")
	}

	const batchSize = 10_000
	cfg := fmt.Sprintf(`{"table_name":%q,"cursor_col":"id","batch_size":%d}`, table, batchSize)
	mig := e.migration(e.db, targets.BenchBatchUpSQL(table), targets.BenchBatchDownSQL(table), domain.ModeBatch, cfg)
	ref := e.enqueue(e.db, mig, domain.ActionApply, domain.ModeBatch, nil, nil)
	start := time.Now()
	e.runAll(e.worker())
	t.Logf("batch migration: %d rows in %d ms", targets.BenchRows, time.Since(start).Milliseconds())

	s := e.expectState(ref, domain.JobSuccess, "completed")
	wantBatches := targets.BenchRows / batchSize
	if s.JobBatches != wantBatches || s.TotalBatches != wantBatches || s.JobRows != targets.BenchRows || s.ExecRows != targets.BenchRows {
		t.Fatalf("state = %+v; want %d batches and %d rows", s, wantBatches, targets.BenchRows)
	}
	if n := e.targetInt(`select count(*) from ` + table + ` where amount <> (id % 1000) + 1.5`); n != 0 {
		t.Fatalf("%d rows were not updated exactly once", n)
	}
	if n := e.targetInt(`select count(*) from _leafygo_batch_checkpoints where root_execution_id = $1`, ref.ExecutionID); n != int64(wantBatches) {
		t.Fatalf("checkpoints = %d; want %d", n, wantBatches)
	}
}
