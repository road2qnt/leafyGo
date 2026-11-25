package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// JobCount is one (status, database) bucket for the jobs_current gauge.
type JobCount struct {
	Status string
	DBName string
	Count  int64
}

// JobCountSource queries Platform DB at scrape time. Only cmd/server registers
// it, which keeps jobs_current single-sourced across worker replicas.
type JobCountSource func(ctx context.Context) ([]JobCount, error)

// PoolStats is a provider-neutral snapshot of a pgxpool.
type PoolStats struct {
	Active      int32
	Idle        int32
	Total       int32
	Max         int32
	WaitSeconds float64
	WaitCount   int64
}

// PoolStatsSource exposes connection pool statistics to the collector.
type PoolStatsSource interface {
	PoolRole() string
	PoolName() string
	Stats() PoolStats
}

// PgxPoolSource adapts a real pgxpool to PoolStatsSource.
type PgxPoolSource struct {
	Pool *pgxpool.Pool
	Role string
	Name string
}

func (s PgxPoolSource) PoolRole() string { return s.Role }
func (s PgxPoolSource) PoolName() string { return s.Name }

func (s PgxPoolSource) Stats() PoolStats {
	if s.Pool == nil {
		return PoolStats{}
	}
	stat := s.Pool.Stat()
	return PoolStats{
		Active:      stat.AcquiredConns(),
		Idle:        stat.IdleConns(),
		Total:       stat.TotalConns(),
		Max:         stat.MaxConns(),
		WaitSeconds: stat.EmptyAcquireWaitTime().Seconds(),
		WaitCount:   stat.EmptyAcquireCount(),
	}
}

// MetricsOptions configures the registry and the optional server-side gauge.
type MetricsOptions struct {
	Registry  *prometheus.Registry
	JobCounts JobCountSource
	Logger    *slog.Logger
}

// Metrics owns every LeafyGo Prometheus collector.
type Metrics struct {
	registry *prometheus.Registry

	jobsTotal         *prometheus.CounterVec
	jobRetries        *prometheus.CounterVec
	queueWait         *prometheus.HistogramVec
	lockWait          *prometheus.HistogramVec
	executionDuration *prometheus.HistogramVec
	batches           *prometheus.CounterVec
	rows              *prometheus.CounterVec

	pools *poolCollector
}

var durationBuckets = []float64{
	0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600,
}

// NewMetrics builds the metric vectors and registers them.
func NewMetrics(opts MetricsOptions) *Metrics {
	registry := opts.Registry
	if registry == nil {
		registry = prometheus.NewRegistry()
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	m := &Metrics{
		registry: registry,
		jobsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "leafygo",
			Name:      "migration_jobs_total",
			Help:      "Total terminal migration jobs by status, mode, action, and database.",
		}, []string{"status", "mode", "action", "db_name"}),
		jobRetries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "leafygo",
			Name:      "migration_job_retries_total",
			Help:      "Total migration job retries by mode and database.",
		}, []string{"mode", "db_name"}),
		queueWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "leafygo",
			Name:      "migration_queue_wait_duration_seconds",
			Help:      "Time a job spent queued before a worker started it.",
			Buckets:   durationBuckets,
		}, []string{"mode", "db_name"}),
		lockWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "leafygo",
			Name:      "migration_lock_wait_duration_seconds",
			Help:      "Time a worker waited to acquire the per-database migration lock.",
			Buckets:   durationBuckets,
		}, []string{"db_name"}),
		executionDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "leafygo",
			Name:      "migration_execution_duration_seconds",
			Help:      "Total migration execution duration by mode, action, status, and database.",
			Buckets:   durationBuckets,
		}, []string{"mode", "action", "status", "db_name"}),
		batches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "leafygo",
			Name:      "migration_batches_processed_total",
			Help:      "Total migration batches processed by status and database.",
		}, []string{"status", "db_name"}),
		rows: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "leafygo",
			Name:      "migration_rows_processed_total",
			Help:      "Total rows committed by migration action and database.",
		}, []string{"action", "db_name"}),
		pools: newPoolCollector("leafygo"),
	}

	for _, collector := range []prometheus.Collector{
		m.jobsTotal, m.jobRetries, m.queueWait, m.lockWait,
		m.executionDuration, m.batches, m.rows, m.pools,
	} {
		registerIgnoringDuplicate(registry, collector)
	}
	if opts.JobCounts != nil {
		registerIgnoringDuplicate(registry, &jobsCurrentCollector{
			desc: prometheus.NewDesc(
				"leafygo_migration_jobs_current",
				"Current migration jobs by status and database (server-only single source).",
				[]string{"status", "db_name"}, nil,
			),
			source: opts.JobCounts,
			logger: logger,
		})
	}
	return m
}

func registerIgnoringDuplicate(registry *prometheus.Registry, collector prometheus.Collector) {
	if err := registry.Register(collector); err != nil {
		var already prometheus.AlreadyRegisteredError
		if !errors.As(err, &already) {
			panic(err)
		}
	}
}

// Registry exposes the registry for tests and embedding.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// Handler returns the Prometheus scrape handler.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// RegisterPool adds a connection-pool source. Safe for concurrent use.
func (m *Metrics) RegisterPool(source PoolStatsSource) {
	if source != nil {
		m.pools.add(source)
	}
}

// RegisterPoolGroup adds a function evaluated on every scrape. Use it for pools
// that are created lazily (target connections).
func (m *Metrics) RegisterPoolGroup(group func() []PoolStatsSource) {
	if group != nil {
		m.pools.addGroup(group)
	}
}

// JobCompleted records a terminal job and its execution duration.
func (m *Metrics) JobCompleted(status, mode, action, dbName string, duration time.Duration) {
	status = normalizeStatus(status)
	mode = normalizeExecutionMode(mode)
	action = normalizeAction(action)
	dbName = normalizeLabel(dbName)
	m.jobsTotal.WithLabelValues(status, mode, action, dbName).Inc()
	m.executionDuration.WithLabelValues(mode, action, status, dbName).Observe(duration.Seconds())
}

// ObserveExecution records duration without incrementing the terminal counter.
func (m *Metrics) ObserveExecution(status, mode, action, dbName string, duration time.Duration) {
	m.executionDuration.WithLabelValues(
		normalizeExecutionMode(mode), normalizeAction(action),
		normalizeStatus(status), normalizeLabel(dbName),
	).Observe(duration.Seconds())
}

// JobRetried records one retry attempt.
func (m *Metrics) JobRetried(mode, dbName string) {
	m.jobRetries.WithLabelValues(normalizeExecutionMode(mode), normalizeLabel(dbName)).Inc()
}

// ObserveQueueWait records queued_at -> started_at duration.
func (m *Metrics) ObserveQueueWait(mode, dbName string, duration time.Duration) {
	m.queueWait.WithLabelValues(normalizeExecutionMode(mode), normalizeLabel(dbName)).Observe(duration.Seconds())
}

// ObserveLockWait records time spent acquiring the per-database lock.
func (m *Metrics) ObserveLockWait(dbName string, duration time.Duration) {
	m.lockWait.WithLabelValues(normalizeLabel(dbName)).Observe(duration.Seconds())
}

// BatchProcessed adds n processed batches.
func (m *Metrics) BatchProcessed(status, dbName string, n int) {
	if n <= 0 {
		return
	}
	m.batches.WithLabelValues(normalizeStatus(status), normalizeLabel(dbName)).Add(float64(n))
}

// RowsProcessed adds n committed rows.
func (m *Metrics) RowsProcessed(action, dbName string, n int64) {
	if n <= 0 {
		return
	}
	m.rows.WithLabelValues(normalizeAction(action), normalizeLabel(dbName)).Add(float64(n))
}

func normalizeStatus(status string) string {
	value := strings.ToLower(strings.TrimSpace(status))
	switch value {
	case "queued", "running", "partial", "partially_reverted", "success", "failed", "uncertain":
		return value
	case "completed":
		return "success"
	default:
		return "unknown"
	}
}

func normalizeExecutionMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "normal":
		return "normal"
	case "batch":
		return "batch"
	default:
		return "unknown"
	}
}

func normalizeAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "apply", "rollback", "forward", "batch_retry", "batch_revert", "resolve_uncertain":
		return strings.ToLower(strings.TrimSpace(action))
	default:
		return "unknown"
	}
}

func normalizeLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func isKnownJobStatus(status string) bool {
	switch status {
	case "queued", "running", "partial", "partially_reverted", "success", "failed", "uncertain":
		return true
	default:
		return false
	}
}

type jobsCurrentCollector struct {
	desc   *prometheus.Desc
	source JobCountSource
	logger *slog.Logger
}

func (c *jobsCurrentCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

func (c *jobsCurrentCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	counts, err := c.source(ctx)
	if err != nil {
		c.logger.Warn("metrics.jobs_current.scrape_failed", "error", Sanitize(err.Error()))
		return
	}
	for _, count := range counts {
		status := strings.ToLower(strings.TrimSpace(count.Status))
		if !isKnownJobStatus(status) {
			continue
		}
		value := count.Count
		if value < 0 {
			value = 0
		}
		ch <- prometheus.MustNewConstMetric(
			c.desc, prometheus.GaugeValue, float64(value),
			status, normalizeLabel(count.DBName),
		)
	}
}

type poolCollector struct {
	mu      sync.RWMutex
	sources []PoolStatsSource
	groups  []func() []PoolStatsSource

	connectionsDesc *prometheus.Desc
	waitDesc        *prometheus.Desc
	waitCountDesc   *prometheus.Desc
}

func newPoolCollector(namespace string) *poolCollector {
	return &poolCollector{
		connectionsDesc: prometheus.NewDesc(
			namespace+"_db_pool_connections",
			"PostgreSQL pool connections by role, database, and state.",
			[]string{"pool_role", "db_name", "state"}, nil,
		),
		waitDesc: prometheus.NewDesc(
			namespace+"_db_pool_wait_duration_seconds_total",
			"Cumulative time spent waiting for an empty pool.",
			[]string{"pool_role", "db_name"}, nil,
		),
		waitCountDesc: prometheus.NewDesc(
			namespace+"_db_pool_wait_count_total",
			"Cumulative acquisitions that had to wait for an empty pool.",
			[]string{"pool_role", "db_name"}, nil,
		),
	}
}

func (c *poolCollector) add(source PoolStatsSource) {
	c.mu.Lock()
	c.sources = append(c.sources, source)
	c.mu.Unlock()
}

func (c *poolCollector) addGroup(group func() []PoolStatsSource) {
	c.mu.Lock()
	c.groups = append(c.groups, group)
	c.mu.Unlock()
}

func (c *poolCollector) snapshot() []PoolStatsSource {
	c.mu.RLock()
	out := make([]PoolStatsSource, len(c.sources))
	copy(out, c.sources)
	groups := make([]func() []PoolStatsSource, len(c.groups))
	copy(groups, c.groups)
	c.mu.RUnlock()

	for _, group := range groups {
		out = append(out, group()...)
	}
	return out
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.connectionsDesc
	ch <- c.waitDesc
	ch <- c.waitCountDesc
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	for _, source := range c.snapshot() {
		if source == nil || source.PoolName() == "" {
			continue
		}
		role := normalizeLabel(source.PoolRole())
		name := normalizeLabel(source.PoolName())
		stats := source.Stats()
		states := []struct {
			state string
			value int32
		}{
			{"active", stats.Active},
			{"idle", stats.Idle},
			{"total", stats.Total},
			{"max", stats.Max},
		}
		for _, state := range states {
			ch <- prometheus.MustNewConstMetric(c.connectionsDesc, prometheus.GaugeValue, float64(state.value), role, name, state.state)
		}
		ch <- prometheus.MustNewConstMetric(c.waitDesc, prometheus.CounterValue, stats.WaitSeconds, role, name)
		ch <- prometheus.MustNewConstMetric(c.waitCountDesc, prometheus.CounterValue, float64(stats.WaitCount), role, name)
	}
}
