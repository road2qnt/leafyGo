package target

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"

	"leafygo/internal/domain"
	"leafygo/internal/secret"
)

const maxExplainOutputBytes = 64 * 1024

func truncateToBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	s = s[:maxBytes]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s
}

type Options struct {
	ConnectTimeout  time.Duration
	ViewerTimeout   time.Duration
	MaxConns        int32
	ApplicationName string
}

func (o *Options) applyDefaults() {
	if o.MaxConns <= 0 {
		o.MaxConns = 4
	}
	if o.ViewerTimeout <= 0 {
		o.ViewerTimeout = 5 * time.Second
	}
	if o.ConnectTimeout <= 0 {
		o.ConnectTimeout = 5 * time.Second
	}
	if o.ApplicationName == "" {
		o.ApplicationName = "leafygo"
	}
}

type poolEntry struct {
	pool      *pgxpool.Pool
	updatedAt time.Time
}

type Manager struct {
	box   *secret.Box
	opts  Options
	mu    sync.RWMutex
	pools map[uuid.UUID]*poolEntry
	sf    singleflight.Group
}

func New(box *secret.Box, opts Options) *Manager {
	opts.applyDefaults()
	return &Manager{
		box:   box,
		opts:  opts,
		pools: make(map[uuid.UUID]*poolEntry),
	}
}

func (m *Manager) Pool(ctx context.Context, db domain.DBConnection) (*pgxpool.Pool, error) {
	m.mu.RLock()
	entry, ok := m.pools[db.ID]
	if ok && entry.updatedAt.Equal(db.UpdatedAt) {
		pool := entry.pool
		m.mu.RUnlock()
		return pool, nil
	}
	m.mu.RUnlock()

	key := db.ID.String()
	res, err, _ := m.sf.Do(key, func() (any, error) {
		m.mu.RLock()
		existing, ok := m.pools[db.ID]
		if ok && existing.updatedAt.Equal(db.UpdatedAt) {
			p := existing.pool
			m.mu.RUnlock()
			return p, nil
		}
		m.mu.RUnlock()

		password, decErr := m.box.Decrypt(db.PasswordEnc)
		if decErr != nil {
			return nil, fmt.Errorf("decrypt password: %w", decErr)
		}

		dsn, dsnErr := buildDSN(db, password)
		if dsnErr != nil {
			return nil, fmt.Errorf("build dsn: %w", dsnErr)
		}

		poolCfg, cfgErr := pgxpool.ParseConfig(dsn)
		if cfgErr != nil {
			return nil, fmt.Errorf("parse pool config: %w", cfgErr)
		}

		poolCfg.MaxConns = m.opts.MaxConns
		poolCfg.MaxConnLifetime = 30 * time.Minute
		poolCfg.MaxConnIdleTime = 5 * time.Minute
		poolCfg.ConnConfig.ConnectTimeout = m.opts.ConnectTimeout
		if poolCfg.ConnConfig.RuntimeParams == nil {
			poolCfg.ConnConfig.RuntimeParams = make(map[string]string)
		}
		poolCfg.ConnConfig.RuntimeParams["application_name"] = m.opts.ApplicationName
		poolCfg.ConnConfig.RuntimeParams["statement_timeout"] = m.opts.ViewerTimeout.String()

		// Detached context with timeout for pool initialization
		initCtx, cancel := context.WithTimeout(context.Background(), m.opts.ConnectTimeout)
		defer cancel()

		p, initErr := pgxpool.NewWithConfig(initCtx, poolCfg)
		if initErr != nil {
			return nil, errors.New(sanitizeTargetError(initErr))
		}

		m.mu.Lock()
		if old, exists := m.pools[db.ID]; exists {
			go old.pool.Close()
		}
		m.pools[db.ID] = &poolEntry{
			pool:      p,
			updatedAt: db.UpdatedAt,
		}
		m.mu.Unlock()

		return p, nil
	})

	if err != nil {
		return nil, err
	}
	return res.(*pgxpool.Pool), nil
}

func (m *Manager) Run(ctx context.Context, db domain.DBConnection, sql string, timeout time.Duration) (time.Duration, error) {
	start := time.Now()

	password, err := m.box.Decrypt(db.PasswordEnc)
	if err != nil {
		return 0, classifyRunError(phaseConnect, fmt.Errorf("decrypt credentials: %w", err))
	}

	dsn, err := buildDSN(db, password)
	if err != nil {
		return 0, classifyRunError(phaseConnect, fmt.Errorf("build dsn: %w", err))
	}

	connCfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return 0, classifyRunError(phaseConnect, fmt.Errorf("parse config: %w", err))
	}
	connCfg.ConnectTimeout = m.opts.ConnectTimeout

	conn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		return 0, classifyRunError(phaseConnect, err)
	}

	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, classifyRunError(phaseConnect, err)
	}

	committed := false
	defer func() {
		if !committed {
			rbCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 5*time.Second)
			defer cancel()
			_ = tx.Rollback(rbCtx)
		}
	}()

	remaining := timeout
	if deadline, ok := ctx.Deadline(); ok {
		rem := time.Until(deadline)
		if rem <= 0 {
			remaining = time.Millisecond
		} else {
			remaining = rem
		}
	}
	if remaining < time.Millisecond {
		remaining = time.Millisecond
	}

	msStr := strconv.FormatInt(remaining.Milliseconds(), 10)
	if _, err := tx.Exec(ctx, "select set_config('statement_timeout', $1, true)", msStr); err != nil {
		return time.Since(start), classifyRunError(phaseStatement, err)
	}
	if _, err := tx.Exec(ctx, "select set_config('lock_timeout', '5s', true)"); err != nil {
		return time.Since(start), classifyRunError(phaseStatement, err)
	}

	if _, err := tx.Exec(ctx, sql, pgx.QueryExecModeSimpleProtocol); err != nil {
		return time.Since(start), classifyRunError(phaseStatement, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return time.Since(start), classifyRunError(phaseCommit, err)
	}
	committed = true

	return time.Since(start), nil
}

func (m *Manager) Explain(ctx context.Context, db domain.DBConnection, sql string, timeout time.Duration) (string, error) {
	password, err := m.box.Decrypt(db.PasswordEnc)
	if err != nil {
		return "", classifyRunError(phaseConnect, fmt.Errorf("decrypt credentials: %w", err))
	}

	dsn, err := buildDSN(db, password)
	if err != nil {
		return "", classifyRunError(phaseConnect, fmt.Errorf("build dsn: %w", err))
	}

	connCfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "", classifyRunError(phaseConnect, fmt.Errorf("parse config: %w", err))
	}
	connCfg.ConnectTimeout = m.opts.ConnectTimeout

	conn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		return "", classifyRunError(phaseConnect, err)
	}

	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return "", classifyRunError(phaseConnect, err)
	}

	defer func() {
		rbCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rbCtx)
	}()

	remaining := timeout
	if deadline, ok := ctx.Deadline(); ok {
		rem := time.Until(deadline)
		if rem <= 0 {
			remaining = time.Millisecond
		} else {
			remaining = rem
		}
	}
	if remaining < time.Millisecond {
		remaining = time.Millisecond
	}

	msStr := strconv.FormatInt(remaining.Milliseconds(), 10)
	if _, err := tx.Exec(ctx, "select set_config('statement_timeout', $1, true)", msStr); err != nil {
		return "", classifyRunError(phaseStatement, err)
	}
	if _, err := tx.Exec(ctx, "select set_config('lock_timeout', '5s', true)"); err != nil {
		return "", classifyRunError(phaseStatement, err)
	}

	explainSQL := "EXPLAIN (FORMAT TEXT) " + sql
	rows, err := tx.Query(ctx, explainSQL, pgx.QueryExecModeSimpleProtocol)
	if err != nil {
		return "", classifyRunError(phaseStatement, err)
	}
	defer rows.Close()

	var sb strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", classifyRunError(phaseStatement, err)
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(line)
		if sb.Len() >= maxExplainOutputBytes {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return "", classifyRunError(phaseStatement, err)
	}

	return truncateToBytes(sb.String(), maxExplainOutputBytes), nil
}

func (m *Manager) Test(ctx context.Context, db domain.DBConnection) error {
	password, err := m.box.Decrypt(db.PasswordEnc)
	if err != nil {
		return errors.New(sanitizeTargetError(fmt.Errorf("decrypt credentials: %w", err)))
	}

	dsn, err := buildDSN(db, password)
	if err != nil {
		return errors.New(sanitizeTargetError(err))
	}

	connCfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return errors.New(sanitizeTargetError(err))
	}
	connCfg.ConnectTimeout = m.opts.ConnectTimeout

	testCtx, cancel := context.WithTimeout(ctx, m.opts.ConnectTimeout)
	defer cancel()

	conn, err := pgx.ConnectConfig(testCtx, connCfg)
	if err != nil {
		return errors.New(sanitizeTargetError(err))
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(context.Background()), 3*time.Second)
		defer closeCancel()
		_ = conn.Close(closeCtx)
	}()

	if err := conn.Ping(testCtx); err != nil {
		return errors.New(sanitizeTargetError(err))
	}

	return nil
}

func (m *Manager) Invalidate(dbID uuid.UUID) {
	m.mu.Lock()
	entry, ok := m.pools[dbID]
	if ok {
		delete(m.pools, dbID)
	}
	m.mu.Unlock()

	if ok && entry != nil {
		go entry.pool.Close()
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	poolsToClose := make([]*pgxpool.Pool, 0, len(m.pools))
	for id, entry := range m.pools {
		poolsToClose = append(poolsToClose, entry.pool)
		delete(m.pools, id)
	}
	m.mu.Unlock()

	for _, p := range poolsToClose {
		p.Close()
	}
}
