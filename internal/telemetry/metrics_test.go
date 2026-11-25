package telemetry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

type fakePool struct {
	role, name string
	stats      PoolStats
}

func (f fakePool) PoolRole() string { return f.role }
func (f fakePool) PoolName() string { return f.name }
func (f fakePool) Stats() PoolStats { return f.stats }

func gatherFamilies(t *testing.T, m *Metrics) map[string]*dto.MetricFamily {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	out := make(map[string]*dto.MetricFamily, len(families))
	for _, family := range families {
		out[family.GetName()] = family
	}
	return out
}

func labelsOf(metric *dto.Metric) map[string]string {
	out := map[string]string{}
	for _, pair := range metric.Label {
		out[pair.GetName()] = pair.GetValue()
	}
	return out
}

func TestQueueAndMigrationMetrics(t *testing.T) {
	m := NewMetrics(MetricsOptions{})

	m.JobCompleted("success", "batch", "apply", "inventory", 2*time.Second)
	m.JobRetried("batch", "inventory")
	m.ObserveQueueWait("batch", "inventory", 1500*time.Millisecond)
	m.ObserveLockWait("inventory", 20*time.Millisecond)
	m.BatchProcessed("success", "inventory", 3)
	m.RowsProcessed("apply", "inventory", 1500)

	families := gatherFamilies(t, m)

	jobsTotal, ok := families["leafygo_migration_jobs_total"]
	if !ok {
		t.Fatal("leafygo_migration_jobs_total missing")
	}
	labels := labelsOf(jobsTotal.Metric[0])
	want := map[string]string{
		"status": "success", "mode": "batch", "action": "apply", "db_name": "inventory",
	}
	for key, value := range want {
		if labels[key] != value {
			t.Fatalf("label %s = %q, want %q", key, labels[key], value)
		}
	}

	assertCounter(t, families, "leafygo_migration_job_retries_total", "mode", "batch", 1)
	assertCounter(t, families, "leafygo_migration_batches_processed_total", "status", "success", 3)
	assertCounter(t, families, "leafygo_migration_rows_processed_total", "action", "apply", 1500)

	queue := families["leafygo_migration_queue_wait_duration_seconds"]
	if queue == nil || queue.GetType().String() != "HISTOGRAM" {
		t.Fatalf("queue wait histogram missing: %v", queue)
	}
	if queue.Metric[0].GetHistogram().GetSampleCount() != 1 {
		t.Fatalf("queue wait sample count = %d", queue.Metric[0].GetHistogram().GetSampleCount())
	}
	if families["leafygo_migration_lock_wait_duration_seconds"] == nil {
		t.Fatal("lock wait histogram missing")
	}
	if families["leafygo_migration_execution_duration_seconds"] == nil {
		t.Fatal("execution duration histogram missing")
	}
}

func assertCounter(t *testing.T, families map[string]*dto.MetricFamily, name, label, value string, want float64) {
	t.Helper()
	family, ok := families[name]
	if !ok {
		t.Fatalf("%s missing", name)
	}
	for _, metric := range family.Metric {
		if labelsOf(metric)[label] == value {
			if got := metric.GetCounter().GetValue(); got != want {
				t.Fatalf("%s{%s=%s} = %v, want %v", name, label, value, got, want)
			}
			return
		}
	}
	t.Fatalf("%s{%s=%s} not found", name, label, value)
}

func TestJobsCurrentGaugeFromSingleSource(t *testing.T) {
	fetched := false
	m := NewMetrics(MetricsOptions{
		JobCounts: func(ctx context.Context) ([]JobCount, error) {
			fetched = true
			return []JobCount{
				{Status: "queued", DBName: "inventory", Count: 3},
				{Status: "running", DBName: "inventory", Count: 1},
				{Status: "bogus", DBName: "inventory", Count: 9},
			}, nil
		},
	})

	families := gatherFamilies(t, m)
	if !fetched {
		t.Fatal("job count source was not called at scrape time")
	}
	family, ok := families["leafygo_migration_jobs_current"]
	if !ok {
		t.Fatal("leafygo_migration_jobs_current missing")
	}
	if len(family.Metric) != 2 {
		t.Fatalf("want 2 low-cardinality series, got %d", len(family.Metric))
	}
	found := false
	for _, metric := range family.Metric {
		labels := labelsOf(metric)
		if labels["status"] == "queued" && labels["db_name"] == "inventory" {
			found = true
			if metric.GetGauge().GetValue() != 3 {
				t.Fatalf("queued gauge = %v", metric.GetGauge().GetValue())
			}
		}
		if labels["status"] == "bogus" {
			t.Fatal("unknown status must be skipped to protect cardinality")
		}
	}
	if !found {
		t.Fatal("queued inventory series missing")
	}
}

func TestJobsCurrentAbsentWithoutSource(t *testing.T) {
	m := NewMetrics(MetricsOptions{})
	families := gatherFamilies(t, m)
	if _, ok := families["leafygo_migration_jobs_current"]; ok {
		t.Fatal("worker-side metrics must not expose jobs_current")
	}
}

func TestJobsCurrentFailureIsNonFatal(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	m := NewMetrics(MetricsOptions{
		Logger: logger,
		JobCounts: func(context.Context) ([]JobCount, error) {
			return nil, errors.New("platform db unavailable")
		},
	})
	families := gatherFamilies(t, m)
	if _, ok := families["leafygo_migration_jobs_current"]; ok {
		t.Fatal("no series should be emitted when the source fails")
	}
	if !strings.Contains(buf.String(), "jobs_current") {
		t.Fatalf("scrape failure must be logged: %s", buf.String())
	}
}

func TestPoolCollector(t *testing.T) {
	m := NewMetrics(MetricsOptions{})
	m.RegisterPool(fakePool{
		role: "target", name: "billing",
		stats: PoolStats{Active: 2, Idle: 3, Total: 5, Max: 10, WaitSeconds: 1.25, WaitCount: 4},
	})

	families := gatherFamilies(t, m)
	connections, ok := families["leafygo_db_pool_connections"]
	if !ok {
		t.Fatal("leafygo_db_pool_connections missing")
	}
	states := map[string]float64{}
	for _, metric := range connections.Metric {
		labels := labelsOf(metric)
		if labels["pool_role"] != "target" || labels["db_name"] != "billing" {
			t.Fatalf("unexpected labels: %v", labels)
		}
		states[labels["state"]] = metric.GetGauge().GetValue()
	}
	want := map[string]float64{"active": 2, "idle": 3, "total": 5, "max": 10}
	for state, value := range want {
		if states[state] != value {
			t.Fatalf("state %s = %v, want %v", state, states[state], value)
		}
	}

	assertCounter(t, families, "leafygo_db_pool_wait_duration_seconds_total", "db_name", "billing", 1.25)
	assertCounter(t, families, "leafygo_db_pool_wait_count_total", "db_name", "billing", 4)
}

func TestPoolCollectorGroupIsEvaluatedPerScrape(t *testing.T) {
	m := NewMetrics(MetricsOptions{})
	var target *fakePool
	m.RegisterPoolGroup(func() []PoolStatsSource {
		if target == nil {
			return nil
		}
		return []PoolStatsSource{*target}
	})

	families := gatherFamilies(t, m)
	if _, ok := families["leafygo_db_pool_connections"]; ok {
		t.Fatal("no target series expected before a pool exists")
	}

	value := fakePool{
		role: "target", name: "people",
		stats: PoolStats{Active: 1, Idle: 0, Total: 1, Max: 4},
	}
	target = &value

	families = gatherFamilies(t, m)
	family, ok := families["leafygo_db_pool_connections"]
	if !ok {
		t.Fatal("dynamic target pool was not collected")
	}
	found := false
	for _, metric := range family.Metric {
		labels := labelsOf(metric)
		if labels["db_name"] == "people" && labels["state"] == "active" {
			found = true
			if metric.GetGauge().GetValue() != 1 {
				t.Fatalf("active = %v", metric.GetGauge().GetValue())
			}
		}
	}
	if !found {
		t.Fatal("people target pool series missing")
	}
}

func TestMetricsHandler(t *testing.T) {
	m := NewMetrics(MetricsOptions{})
	m.JobRetried("normal", "billing")

	server := httptest.NewServer(m.Handler())
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "leafygo_migration_job_retries_total") {
		t.Fatalf("metrics body missing series: %s", body)
	}
}

func TestNormalizeLabels(t *testing.T) {
	if got := normalizeStatus("PARTIAL"); got != "partial" {
		t.Fatalf("normalizeStatus = %q", got)
	}
	if got := normalizeStatus("not-a-status"); got != "unknown" {
		t.Fatalf("normalizeStatus = %q", got)
	}
	if got := normalizeLabel("  "); got != "unknown" {
		t.Fatalf("normalizeLabel = %q", got)
	}
	if got := normalizeExecutionMode("BATCH"); got != "batch" {
		t.Fatalf("normalizeExecutionMode = %q", got)
	}
	if got := normalizeAction("rollback"); got != "rollback" {
		t.Fatalf("normalizeAction = %q", got)
	}
}

func TestMetricsWithExplicitRegistryDoesNotDoubleRegister(t *testing.T) {
	registry := prometheus.NewRegistry()
	m1 := NewMetrics(MetricsOptions{Registry: registry})
	m1.JobRetried("batch", "inventory")
	m2 := NewMetrics(MetricsOptions{Registry: registry})
	_ = m2
}
