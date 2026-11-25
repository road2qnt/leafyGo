package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"leafygo/internal/config"
	"leafygo/internal/telemetry"
)

func installTestTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	return recorder
}

func parseLogEntry(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("no log output")
	}
	lines := strings.Split(line, "\n")
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &entry); err != nil {
		t.Fatalf("invalid JSON log: %v", err)
	}
	return entry
}

func TestTelemetryMiddlewareCreatesSpanAndCorrelatesLog(t *testing.T) {
	recorder := installTestTracer(t)
	var buf bytes.Buffer
	logger := telemetry.NewLogger(telemetry.LoggerOptions{Writer: &buf})

	handler := telemetryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := requestInfoFrom(r.Context())
		if info == nil {
			t.Fatal("request info missing from context")
		}
		info.Actor = "user:ega"
		info.DBID = "db-1"
		info.DBName = "inventory"
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/databases/db-1/overview", nil)
	req.Header.Set("X-Correlation-ID", "req-test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("X-Correlation-ID"); got != "req-test" {
		t.Fatalf("X-Correlation-ID = %q", got)
	}
	traceID := rec.Header().Get("X-Trace-ID")
	if len(traceID) != 32 {
		t.Fatalf("X-Trace-ID = %q", traceID)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(spans))
	}
	attrs := map[string]any{}
	for _, attr := range spans[0].Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsInterface()
	}
	if attrs["http.method"] != "GET" {
		t.Fatalf("http.method = %v", attrs["http.method"])
	}
	if attrs["http.status_code"] != int64(http.StatusCreated) {
		t.Fatalf("http.status_code = %v", attrs["http.status_code"])
	}
	if attrs["actor"] != "user:ega" || attrs["db_name"] != "inventory" {
		t.Fatalf("actor/db span attrs = %v / %v", attrs["actor"], attrs["db_name"])
	}

	entry := parseLogEntry(t, &buf)
	if entry["event"] != "http.request" {
		t.Fatalf("event = %v", entry["event"])
	}
	if entry["correlation_id"] != "req-test" {
		t.Fatalf("correlation_id = %v", entry["correlation_id"])
	}
	if entry["trace_id"] != traceID {
		t.Fatalf("log trace_id = %v, header %s", entry["trace_id"], traceID)
	}
	if entry["actor"] != "user:ega" || entry["database"] != "inventory" {
		t.Fatalf("log actor/database = %v / %v", entry["actor"], entry["database"])
	}
	if entry["status"] != float64(http.StatusCreated) {
		t.Fatalf("log status = %v", entry["status"])
	}
}

func TestTelemetryMiddlewareExtractsIncomingTraceparent(t *testing.T) {
	recorder := installTestTracer(t)
	var buf bytes.Buffer
	logger := telemetry.NewLogger(telemetry.LoggerOptions{Writer: &buf})

	handler := telemetryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/databases/db-1/proposals/p-1/approve", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(spans))
	}
	if got := spans[0].SpanContext().TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace id = %s", got)
	}
	if rec.Header().Get("X-Trace-ID") != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("response trace id = %q", rec.Header().Get("X-Trace-ID"))
	}
}

func TestTelemetryMiddlewareGeneratesCorrelationID(t *testing.T) {
	installTestTracer(t)
	var buf bytes.Buffer
	logger := telemetry.NewLogger(telemetry.LoggerOptions{Writer: &buf})
	handler := telemetryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	correlationID := rec.Header().Get("X-Correlation-ID")
	if !strings.HasPrefix(correlationID, "req-") {
		t.Fatalf("correlation id = %q", correlationID)
	}
}

func TestTelemetryMiddlewareSkipsMetricsAndAssets(t *testing.T) {
	recorder := installTestTracer(t)
	var buf bytes.Buffer
	logger := telemetry.NewLogger(telemetry.LoggerOptions{Writer: &buf})
	handler := telemetryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{"/metrics", "/static/css/app.css", "/app/assets/index-abc.js"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	}
	if spans := recorder.Ended(); len(spans) != 0 {
		t.Fatalf("static/metrics requests must not create spans, got %d", len(spans))
	}
	if strings.TrimSpace(buf.String()) != "" {
		t.Fatalf("static/metrics requests must not be logged: %s", buf.String())
	}
}

func TestServerMetricsEndpoint(t *testing.T) {
	installTestTracer(t)
	var buf bytes.Buffer
	metrics := telemetry.NewMetrics(telemetry.MetricsOptions{})
	metrics.JobRetried("batch", "inventory")

	s := &Server{
		cfg:    config.Config{},
		logger: telemetry.NewLogger(telemetry.LoggerOptions{Writer: &buf}),
	}
	s.SetMetricsHandler(metrics.Handler())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "leafygo_migration_job_retries_total") {
		t.Fatalf("metrics body missing series: %s", rec.Body.String())
	}
}

func TestRequestInfoContextRoundTrip(t *testing.T) {
	ctx := withRequestInfo(context.Background(), &requestInfo{Actor: "worker:w1"})
	info := requestInfoFrom(ctx)
	if info == nil || info.Actor != "worker:w1" {
		t.Fatalf("request info = %+v", info)
	}
	if requestInfoFrom(context.Background()) != nil {
		t.Fatal("request info must not appear without the middleware")
	}
}
