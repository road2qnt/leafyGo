package telemetry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/trace"
)

func testLogger(t *testing.T, buf *bytes.Buffer) *slog.Logger {
	t.Helper()
	return NewLogger(LoggerOptions{
		Writer:  buf,
		Level:   slog.LevelDebug,
		Service: "leafygo-test",
		Env:     "test",
		Version: "0.0.1",
	})
}

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	raw := strings.TrimSpace(buf.String())
	if raw == "" {
		t.Fatal("logger produced no output")
	}
	lines := strings.Split(raw, "\n")
	out := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %v\n%s", err, line)
		}
		out = append(out, m)
	}
	return out
}

func spanContext(t *testing.T) context.Context {
	t.Helper()
	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	return trace.ContextWithSpanContext(context.Background(), sc)
}

func TestLoggerEmitsSingleLineJSONWithEventField(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(t, &buf)

	logger.Info("hello", "status", "ok")

	entries := logLines(t, &buf)
	if len(entries) != 1 {
		t.Fatalf("want 1 line, got %d", len(entries))
	}
	entry := entries[0]
	if entry["event"] != "hello" {
		t.Fatalf("event field = %v, want hello", entry["event"])
	}
	if entry["level"] != "INFO" {
		t.Fatalf("level = %v, want INFO", entry["level"])
	}
	if entry["service"] != "leafygo-test" {
		t.Fatalf("service = %v", entry["service"])
	}
	if _, ok := entry["time"].(string); !ok {
		t.Fatalf("time must be an RFC3339 string, got %T", entry["time"])
	}
	if _, err := time.Parse(time.RFC3339Nano, entry["time"].(string)); err != nil {
		t.Fatalf("time is not RFC3339Nano: %v", err)
	}
	if _, ok := entry["msg"]; ok {
		t.Fatal("msg must be renamed to event")
	}
}

func TestLoggerInjectsTraceActorDatabaseAndCorrelation(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(t, &buf)

	ctx := spanContext(t)
	ctx = WithCorrelationID(ctx, "req-9f8a7b6c")
	ctx = WithActor(ctx, "user:jibril")
	ctx = WithDatabase(ctx, "db-1", "inventory")

	logger.InfoContext(ctx, "migration.proposal.approved", "status", "completed")

	entry := logLines(t, &buf)[0]
	if entry["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace_id = %v", entry["trace_id"])
	}
	if entry["span_id"] != "00f067aa0ba902b7" {
		t.Fatalf("span_id = %v", entry["span_id"])
	}
	if entry["correlation_id"] != "req-9f8a7b6c" {
		t.Fatalf("correlation_id = %v", entry["correlation_id"])
	}
	if entry["actor"] != "user:jibril" {
		t.Fatalf("actor = %v", entry["actor"])
	}
	if entry["database"] != "inventory" {
		t.Fatalf("database = %v", entry["database"])
	}
	if entry["db_id"] != "db-1" {
		t.Fatalf("db_id = %v", entry["db_id"])
	}
}

func TestSanitizeRedactsCredentialsInSQLAndDSN(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		secrets []string
		want    []string
	}{
		{
			name:    "create role password",
			input:   `CREATE ROLE app WITH LOGIN PASSWORD 'supersecret'`,
			secrets: []string{"supersecret"},
			want:    []string{"'[REDACTED]'"},
		},
		{
			name:    "alter user encrypted password",
			input:   `ALTER USER app WITH ENCRYPTED PASSWORD 'another-secret'`,
			secrets: []string{"another-secret"},
			want:    []string{"'[REDACTED]'"},
		},
		{
			name:    "pgp sym encrypt key",
			input:   `SELECT pgp_sym_encrypt('payload', 'key-123')`,
			secrets: []string{"key-123"},
			want:    []string{"pgp_sym_encrypt('payload', '[REDACTED]')"},
		},
		{
			name:    "dsn password",
			input:   `failed to connect: postgres://leafygo:topsecret@db:5432/leafygo`,
			secrets: []string{"topsecret"},
			want:    []string{"postgres://leafygo:[REDACTED]@db:5432/leafygo"},
		},
		{
			name:    "key value password",
			input:   `host=db password=topsecret dbname=leafygo`,
			secrets: []string{"topsecret"},
			want:    []string{"password=[REDACTED]"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Sanitize(tc.input)
			for _, secret := range tc.secrets {
				if strings.Contains(got, secret) {
					t.Fatalf("secret %q leaked: %s", secret, got)
				}
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("sanitized text missing %q: %s", want, got)
				}
			}
		})
	}
}

func TestLoggerRedactsSensitiveAttributeKeys(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(t, &buf)

	logger.Info("login",
		"password", "hunter2",
		"password_enc", "ciphertext",
		"csrf_token", "csrf-secret",
		slog.Group("nested", "sid", "session-secret"),
		"dsn", "postgres://u:dsn-secret@h:5432/db",
	)

	line := buf.String()
	for _, secret := range []string{"hunter2", "ciphertext", "csrf-secret", "session-secret", "dsn-secret"} {
		if strings.Contains(line, secret) {
			t.Fatalf("secret %q leaked in %s", secret, line)
		}
	}
}

func TestLogSQLStartTruncatesQueryAndKeepsFullHash(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(t, &buf)
	SetDefaultLogger(logger)

	full := strings.Repeat("SELECT 1;", 600)
	sum := sha256.Sum256([]byte(full))

	LogSQLStart(context.Background(), SQLMeta{
		Source: SQLSourcePlatform,
		Query:  full,
		Params: []any{"a", "b"},
	})

	entry := logLines(t, &buf)[0]
	query, _ := entry["query"].(string)
	if len(query) > MaxLoggedQueryBytes {
		t.Fatalf("query length = %d, want <= %d", len(query), MaxLoggedQueryBytes)
	}
	if !strings.HasSuffix(query, "...[TRUNCATED]") {
		t.Fatalf("truncated query must carry the suffix, got %q", query[len(query)-20:])
	}
	if entry["query_sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("query_sha256 must hash the full untruncated query")
	}
	if entry["param_count"] != float64(2) {
		t.Fatalf("param_count = %v", entry["param_count"])
	}
}

func TestParamsSummaryPerSource(t *testing.T) {
	tests := []struct {
		name   string
		meta   SQLMeta
		assert func(t *testing.T, entry map[string]any, raw string)
	}{
		{
			name: "platform uses placeholders only",
			meta: SQLMeta{Source: SQLSourcePlatform, Query: "select 1", Params: []any{"alice", "hash", "sid"}},
			assert: func(t *testing.T, entry map[string]any, raw string) {
				summary, _ := entry["params_summary"].([]any)
				if len(summary) != 3 || summary[0] != "$1" || summary[2] != "$3" {
					t.Fatalf("params_summary = %v", summary)
				}
				if strings.Contains(raw, "alice") {
					t.Fatal("raw platform parameter leaked")
				}
			},
		},
		{
			name: "query console hashes values",
			meta: SQLMeta{Source: SQLSourceQueryConsole, Query: "select * from t where email=$1", Params: []any{"secret-filter"}},
			assert: func(t *testing.T, entry map[string]any, raw string) {
				summary, _ := entry["params_summary"].([]any)
				if len(summary) != 1 {
					t.Fatalf("params_summary = %v", summary)
				}
				value, _ := summary[0].(string)
				if !strings.HasPrefix(value, "sha256:") {
					t.Fatalf("want hashed parameter, got %v", value)
				}
				if strings.Contains(raw, "secret-filter") {
					t.Fatal("raw query console parameter leaked")
				}
			},
		},
		{
			name: "target viewer hashes values",
			meta: SQLMeta{Source: SQLSourceTargetViewer, Query: "select * from t where id=$1", Params: []any{"customer-42"}},
			assert: func(t *testing.T, entry map[string]any, raw string) {
				summary, _ := entry["params_summary"].([]any)
				value, _ := summary[0].(string)
				if !strings.HasPrefix(value, "sha256:") {
					t.Fatalf("want hashed parameter, got %v", value)
				}
				if strings.Contains(raw, "customer-42") {
					t.Fatal("raw viewer parameter leaked")
				}
			},
		},
		{
			name: "migration records cursor bounds only",
			meta: SQLMeta{
				Source:     SQLSourceMigration,
				Query:      "update t set x=1 where id between $1 and $2",
				Params:     []any{int64(100), int64(200), "not-allowed"},
				CursorFrom: ptrInt64(100),
				CursorTo:   ptrInt64(200),
				BatchSize:  ptrInt(50),
			},
			assert: func(t *testing.T, entry map[string]any, raw string) {
				summary, _ := entry["params_summary"].(map[string]any)
				if summary["cursor_from"] != float64(100) {
					t.Fatalf("cursor_from = %v", summary["cursor_from"])
				}
				if summary["cursor_to"] != float64(200) {
					t.Fatalf("cursor_to = %v", summary["cursor_to"])
				}
				if summary["batch_size"] != float64(50) {
					t.Fatalf("batch_size = %v", summary["batch_size"])
				}
				if _, ok := summary["not-allowed"]; ok {
					t.Fatal("non-cursor parameter leaked")
				}
				if strings.Contains(raw, "not-allowed") {
					t.Fatal("raw migration parameter leaked")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			SetDefaultLogger(testLogger(t, &buf))
			LogSQLStart(context.Background(), tc.meta)
			entry := logLines(t, &buf)[0]
			tc.assert(t, entry, buf.String())
		})
	}
}

func TestSQLEventTrioAndSQLState(t *testing.T) {
	var buf bytes.Buffer
	SetDefaultLogger(testLogger(t, &buf))
	ctx := spanContext(t)
	meta := SQLMeta{
		Source:      SQLSourceMigration,
		Query:       "update inventory set qty = qty - 1 where id = $1",
		Params:      []any{int64(7)},
		Actor:       "worker:w-1",
		DBID:        "db-1",
		DBName:      "inventory",
		MigrationID: "mig-1",
		JobID:       "job-1",
		ExecutionID: "exec-1",
		Attempt:     2,
		BatchNo:     3,
	}

	LogSQLStart(ctx, meta)
	LogSQLResult(ctx, meta, 1, 12*time.Millisecond, nil)
	LogSQLTxEnd(ctx, meta, "commit", 14*time.Millisecond, nil)

	entries := logLines(t, &buf)
	if len(entries) != 3 {
		t.Fatalf("want 3 events, got %d", len(entries))
	}
	wantEvents := []string{"sql.exec.start", "sql.exec.result", "sql.tx.commit"}
	for i, want := range wantEvents {
		if entries[i]["event"] != want {
			t.Fatalf("event[%d] = %v, want %v", i, entries[i]["event"], want)
		}
		if entries[i]["sql_source"] != "migration" {
			t.Fatalf("sql_source[%d] = %v", i, entries[i]["sql_source"])
		}
		if entries[i]["job_id"] != "job-1" || entries[i]["execution_id"] != "exec-1" {
			t.Fatalf("missing execution identifiers on event %d: %v", i, entries[i])
		}
	}
	if entries[1]["row_count"] != float64(1) {
		t.Fatalf("row_count = %v", entries[1]["row_count"])
	}
	if entries[1]["duration_ms"] != float64(12) {
		t.Fatalf("duration_ms = %v", entries[1]["duration_ms"])
	}
	if entries[1]["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace_id not propagated into SQL result: %v", entries[1])
	}

	buf.Reset()
	dbErr := &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}
	LogSQLResult(ctx, meta, 0, 5*time.Millisecond, dbErr)
	LogSQLTxEnd(ctx, meta, "rollback", 6*time.Millisecond, dbErr)

	entries = logLines(t, &buf)
	if entries[0]["level"] != "ERROR" {
		t.Fatalf("failed SQL result level = %v, want ERROR", entries[0]["level"])
	}
	if entries[0]["sqlstate"] != "23505" {
		t.Fatalf("sqlstate = %v", entries[0]["sqlstate"])
	}
	if entries[1]["event"] != "sql.tx.rollback" {
		t.Fatalf("rollback event = %v", entries[1]["event"])
	}
	if entries[1]["status"] != "rollback" {
		t.Fatalf("rollback status = %v", entries[1]["status"])
	}
}

func TestSQLResultErrorIsSanitized(t *testing.T) {
	var buf bytes.Buffer
	SetDefaultLogger(testLogger(t, &buf))

	err := errors.New("connection failed: postgres://app:leaked-password@db:5432/app")
	LogSQLResult(context.Background(), SQLMeta{Source: SQLSourcePlatform, Query: "select 1"}, 0, time.Millisecond, err)

	if strings.Contains(buf.String(), "leaked-password") {
		t.Fatalf("error password leaked: %s", buf.String())
	}
}

func TestLogSQLTxEndRejectsUnknownOutcomeAsRollbackEvent(t *testing.T) {
	var buf bytes.Buffer
	SetDefaultLogger(testLogger(t, &buf))

	LogSQLTxEnd(context.Background(), SQLMeta{Source: SQLSourcePlatform, Query: "select 1"}, "bogus", time.Millisecond, nil)

	entry := logLines(t, &buf)[0]
	if entry["event"] != "sql.tx.rollback" {
		t.Fatalf("unknown outcome must be logged as rollback, got %v", entry["event"])
	}
	if entry["status"] != "rollback" {
		t.Fatalf("status = %v", entry["status"])
	}
}

func TestNewCorrelationID(t *testing.T) {
	id := NewCorrelationID()
	if !strings.HasPrefix(id, "req-") {
		t.Fatalf("correlation id = %q", id)
	}
	if len(id) != len("req-")+12 {
		t.Fatalf("correlation id length = %d", len(id))
	}
	if id == NewCorrelationID() {
		t.Fatal("correlation ids must not repeat")
	}
}

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()
	ctx = WithCorrelationID(ctx, "req-1")
	ctx = WithActor(ctx, "alice")
	ctx = WithDatabase(ctx, "db-1", "billing")

	if CorrelationFrom(ctx) != "req-1" {
		t.Fatal("correlation helper lost value")
	}
	if ActorFrom(ctx) != "alice" {
		t.Fatal("actor helper lost value")
	}
	if DBID, DBName := DatabaseFrom(ctx); DBID != "db-1" || DBName != "billing" {
		t.Fatalf("database helper = %q/%q", DBID, DBName)
	}
}

func ptrInt64(v int64) *int64 { return &v }
func ptrInt(v int) *int       { return &v }
