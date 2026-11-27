package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"leafygo/internal/telemetry"
)

func TestAPIQueueWorkerTraceAndLogCorrelation(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	var spanBuf bytes.Buffer
	tp, err := telemetry.NewTracerProvider(context.Background(), telemetry.TracerOptions{
		ServiceName:    "leafygo-api",
		ServiceVersion: "m2",
		Env:            "test",
		Console:        true,
		ConsoleWriter:  &spanBuf,
		SpanProcessors: []sdktrace.SpanProcessor{recorder},
	})
	if err != nil {
		t.Fatalf("NewTracerProvider: %v", err)
	}
	tp.Install()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	var logBuf bytes.Buffer
	logger := telemetry.NewLogger(telemetry.LoggerOptions{
		Writer:  &logBuf,
		Service: "leafygo-worker",
		Env:     "test",
		Version: "m2",
	})

	// 1. API request span + queue.publish span
	apiCtx := telemetry.WithCorrelationID(context.Background(), "req-corr-001")
	apiCtx = telemetry.WithActor(apiCtx, "user:andi")
	apiCtx = telemetry.WithDatabase(apiCtx, "db-uuid-1", "bench")
	apiCtx, apiSpan := telemetry.StartSpan(apiCtx, "HTTP POST /api/v1/databases/{id}/proposals/{pid}/approve")
	pubCtx, pubSpan := telemetry.StartSpan(apiCtx, "queue.publish", attribute.String("db_name", "bench"))
	traceParent, traceState := telemetry.InjectTraceContext(pubCtx)
	pubSpan.End()
	apiSpan.End()

	if traceParent == "" {
		t.Fatal("expected non-empty traceparent from queue.publish span")
	}

	// 2. Worker extracts trace context from queue row and runs lock + batch + SQL
	workerCtx := telemetry.ExtractTraceContext(context.Background(), traceParent, traceState)
	workerCtx = telemetry.WithCorrelationID(workerCtx, "req-corr-001")
	workerCtx = telemetry.WithActor(workerCtx, "worker:worker-1")
	workerCtx = telemetry.WithDatabase(workerCtx, "db-uuid-1", "bench")

	workerCtx, jobSpan := telemetry.StartSpan(workerCtx, "worker.process_job", attribute.String("db_name", "bench"))
	_, lockSpan := telemetry.StartSpan(workerCtx, "worker.acquire_db_lock", attribute.String("db_name", "bench"))
	lockSpan.End()

	batchCtx, batchSpan := telemetry.StartSpan(workerCtx, "worker.execute_batch", attribute.Int("batch_no", 1))
	from, to, bs := int64(1), int64(10000), 10000
	meta := telemetry.SQLMeta{
		Source:      telemetry.SQLSourceMigration,
		Query:       "UPDATE large_migration_bench SET amount = amount + 1 WHERE id >= 1 AND id <= 10000",
		DBID:        "db-uuid-1",
		DBName:      "bench",
		MigrationID: "mig-1",
		JobID:       "job-1",
		ExecutionID: "exec-1",
		Attempt:     1,
		BatchNo:     1,
		CursorFrom:  &from,
		CursorTo:    &to,
		BatchSize:   &bs,
	}
	telemetry.LogSQLStartWith(logger, batchCtx, meta)
	telemetry.LogSQLResultWith(logger, batchCtx, meta, 10000, 42*time.Millisecond, nil)
	telemetry.LogSQLTxEndWith(logger, batchCtx, meta, "commit", 42*time.Millisecond, nil)
	batchSpan.End()
	jobSpan.End()

	// 3. Verify all spans share the exact same TraceID
	spans := recorder.Ended()
	if len(spans) != 5 {
		t.Fatalf("expected 5 spans, got %d", len(spans))
	}
	rootTraceID := spans[0].SpanContext().TraceID().String()
	for _, sp := range spans {
		if got := sp.SpanContext().TraceID().String(); got != rootTraceID {
			t.Fatalf("span %s trace_id=%s, want %s", sp.Name(), got, rootTraceID)
		}
	}

	// 4. Verify all JSON log lines carry the matching trace_id and correlation_id
	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 SQL log lines, got %d: %s", len(lines), logBuf.String())
	}
	for _, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("invalid JSON log line: %v", err)
		}
		if rec["trace_id"] != rootTraceID {
			t.Fatalf("log trace_id=%v, want %s", rec["trace_id"], rootTraceID)
		}
		if rec["correlation_id"] != "req-corr-001" {
			t.Fatalf("log correlation_id=%v, want req-corr-001", rec["correlation_id"])
		}
	}

	// 5. Verify Prometheus metrics output for worker
	metrics := telemetry.NewMetrics(telemetry.MetricsOptions{Logger: logger})
	metrics.ObserveQueueWait("batch", "bench", 120*time.Millisecond)
	metrics.ObserveLockWait("bench", 15*time.Millisecond)
	metrics.BatchProcessed("success", "bench", 1)
	metrics.RowsProcessed("apply", "bench", 10000)
	metrics.JobCompleted("success", "batch", "apply", "bench", 450*time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rr, req)
	body := rr.Body.String()
	for _, metricName := range []string{
		"leafygo_migration_jobs_total",
		"leafygo_migration_queue_wait_duration_seconds",
		"leafygo_migration_lock_wait_duration_seconds",
		"leafygo_migration_execution_duration_seconds",
		"leafygo_migration_batches_processed_total",
		"leafygo_migration_rows_processed_total",
	} {
		if !strings.Contains(body, metricName) {
			t.Fatalf("expected metric %q in /metrics output", metricName)
		}
	}
}
