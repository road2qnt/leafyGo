# LeafyGo M2 observability stack

Pinned local stack: **OTel Collector 0.114.0**, **Prometheus v2.55.1**,
**Tempo 2.6.1**, **Loki 3.2.1**, **Grafana 11.3.0**.

## Run

```bash
# 1. application stack first (creates the leafygo_default network)
docker compose up -d

# 2. observability stack
docker compose -f deploy/observability/docker-compose.observability.yml up -d
```

| Service | URL |
|---|---|
| Grafana | http://localhost:3000 (anonymous admin, local only) |
| Prometheus | http://localhost:9090 |
| OTLP gRPC / HTTP | `localhost:4317` / `localhost:4318` |
| Collector health | http://localhost:13133 |

The dashboard **LeafyGo — Migration Queue & Observability** is provisioned
automatically in the `LeafyGo` folder.

## Point the app/worker at the collector

`.env` or shell:

```ini
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318
OTEL_TRACES_CONSOLE=false
OTEL_TRACES_SAMPLE_RATIO=1
METRICS_ENABLED=true
```

`OTEL_TRACES_CONSOLE=true` (default outside production) prints one JSON span per
line to stdout, which is the collector-free proof required by the milestone.
When the endpoint is set, traces also go to Tempo over OTLP/HTTP.

Prometheus scrapes:

- `app:8080/metrics` (server, including the single-source `jobs_current` gauge)
- `worker:8081/metrics` (worker counters/histograms/pool stats)
- `otel-collector:8889` (collector self-metrics)

## Logs

`log/slog` writes one JSON object per line to stdout. The collector's `filelog`
receiver tails Docker's JSON log files (`/var/lib/docker/containers/*/*-json.log`)
and forwards them to Loki's OTLP endpoint. This works on Linux hosts. On Docker
Desktop (macOS/Windows) the host log path is not mounted into the VM; use
`docker compose logs` instead or swap the receiver for your platform's log driver.

The demo stack runs the collector as root **only** to read Docker's root-owned
log files. Do not copy that setting to production.

## Trace/log correlation

- Every log line carries `trace_id`, `span_id`, `correlation_id`, `actor`, and
  `database` when available.
- Loki derives a **TraceID** field from the JSON log; clicking it opens Tempo.
- Tempo's datasource has `tracesToLogsV2` configured back to Loki.
- Span chain: `HTTP ...` -> `queue.publish` -> `worker.process_job` ->
  `worker.acquire_db_lock` -> `worker.revalidate_migration` ->
  `worker.execute_batch` -> `target.db.query`.

## Notes

- `jobs_current` must stay server-only; the worker binary must not register the
  `JobCounts` source (otherwise replicas double-count). Run a single API replica
  for this demo, or aggregate the gauge explicitly in PromQL.
- The OTLP HTTP exporter is used (`otlptracehttp`) to keep the demo firewall
  friendly; gRPC is still accepted by the collector. The endpoint must include
  `http://` for the local plaintext collector.
- Host ports are bound to `127.0.0.1`; the app/worker reach the stack over the
  Docker network. Anonymous Grafana admin is local-demo only.

## Sample outputs (Logs, Spans, Correlation & Metrics)

### 1. Structured JSON logs (`log/slog` → stdout)

API approval request and correlated worker SQL lifecycle (`sql.exec.start`, `sql.exec.result`, `sql.tx.commit`) sharing the same `trace_id` and `correlation_id`:

```json
{"time":"2026-11-27T14:40:10.102Z","level":"INFO","event":"http.request","service":"leafygo-api","env":"development","version":"m2-ega","method":"POST","path":"/api/v1/databases/0195f0a1-1111-7000-8000-000000000001/proposals/0195f0a1-2222-7000-8000-000000000002/approve","route":"/api/v1/databases/{id}/proposals/{pid}/approve","status":200,"duration_ms":18,"actor":"user:andi","db_id":"0195f0a1-1111-7000-8000-000000000001","db_name":"bench","database":"bench","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7","correlation_id":"req-9a4e1c2b7f10"}
{"time":"2026-11-27T14:40:10.145Z","level":"INFO","event":"job.claimed","service":"leafygo-worker","env":"development","version":"m2-ega","worker_id":"worker-1","job_id":"0195f0a1-3333-7000-8000-000000000003","execution_id":"0195f0a1-4444-7000-8000-000000000004","root_execution_id":"0195f0a1-4444-7000-8000-000000000004","migration_id":"0195f0a1-5555-7000-8000-000000000005","db_id":"0195f0a1-1111-7000-8000-000000000001","action":"apply","mode":"batch","attempt":1,"status":"running","lock_epoch":1,"actor":"worker:worker-1","database":"bench","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"7a2e4c9f11b03d82","correlation_id":"req-9a4e1c2b7f10"}
{"time":"2026-11-27T14:40:10.160Z","level":"INFO","event":"sql.exec.start","service":"leafygo-worker","env":"development","version":"m2-ega","sql_source":"migration","query":"UPDATE large_migration_bench SET amount = amount + 1 WHERE id >= 1 AND id <= 10000","query_sha256":"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08","param_count":0,"params_summary":{"batch_size":10000,"cursor_from":1,"cursor_to":10000},"actor":"worker:worker-1","db_id":"0195f0a1-1111-7000-8000-000000000001","db_name":"bench","database":"bench","migration_id":"0195f0a1-5555-7000-8000-000000000005","job_id":"0195f0a1-3333-7000-8000-000000000003","execution_id":"0195f0a1-4444-7000-8000-000000000004","attempt":1,"batch_no":1,"status":"started","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"c19f44d0e8ab2104","correlation_id":"req-9a4e1c2b7f10"}
{"time":"2026-11-27T14:40:10.202Z","level":"INFO","event":"sql.exec.result","service":"leafygo-worker","env":"development","version":"m2-ega","sql_source":"migration","query":"UPDATE large_migration_bench SET amount = amount + 1 WHERE id >= 1 AND id <= 10000","query_sha256":"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08","param_count":0,"params_summary":{"batch_size":10000,"cursor_from":1,"cursor_to":10000},"actor":"worker:worker-1","db_id":"0195f0a1-1111-7000-8000-000000000001","db_name":"bench","database":"bench","migration_id":"0195f0a1-5555-7000-8000-000000000005","job_id":"0195f0a1-3333-7000-8000-000000000003","execution_id":"0195f0a1-4444-7000-8000-000000000004","attempt":1,"batch_no":1,"status":"success","rows_affected":10000,"row_count":10000,"duration_ms":42,"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"c19f44d0e8ab2104","correlation_id":"req-9a4e1c2b7f10"}
{"time":"2026-11-27T14:40:10.203Z","level":"INFO","event":"sql.tx.commit","service":"leafygo-worker","env":"development","version":"m2-ega","sql_source":"migration","query":"UPDATE large_migration_bench SET amount = amount + 1 WHERE id >= 1 AND id <= 10000","query_sha256":"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08","param_count":0,"params_summary":{"batch_size":10000,"cursor_from":1,"cursor_to":10000},"actor":"worker:worker-1","db_id":"0195f0a1-1111-7000-8000-000000000001","db_name":"bench","database":"bench","migration_id":"0195f0a1-5555-7000-8000-000000000005","job_id":"0195f0a1-3333-7000-8000-000000000003","execution_id":"0195f0a1-4444-7000-8000-000000000004","attempt":1,"batch_no":1,"status":"commit","duration_ms":42,"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"c19f44d0e8ab2104","correlation_id":"req-9a4e1c2b7f10"}
```

Read-only SQL (Query Console) with automatic credential redaction:

```json
{"time":"2026-11-27T14:41:02.011Z","level":"INFO","event":"sql.exec.start","service":"leafygo-api","sql_source":"query_console","query":"SELECT id FROM users WHERE password = '[REDACTED]'","query_sha256":"3c4f...","param_count":0,"params_summary":[],"actor":"user:andi","db_id":"0195f0a1-1111-7000-8000-000000000001","db_name":"storefront","database":"storefront","status":"started","trace_id":"9d12ab4488c012e7a1bc34de56f78901","span_id":"11a2b3c4d5e6f708","correlation_id":"req-4c2f9a01b3de"}
{"time":"2026-11-27T14:41:02.018Z","level":"INFO","event":"sql.exec.result","service":"leafygo-api","sql_source":"query_console","query":"SELECT id FROM users WHERE password = '[REDACTED]'","query_sha256":"3c4f...","param_count":0,"params_summary":[],"actor":"user:andi","db_id":"0195f0a1-1111-7000-8000-000000000001","db_name":"storefront","database":"storefront","status":"success","rows_affected":1,"row_count":1,"duration_ms":7,"trace_id":"9d12ab4488c012e7a1bc34de56f78901","span_id":"11a2b3c4d5e6f708","correlation_id":"req-4c2f9a01b3de"}
{"time":"2026-11-27T14:41:02.019Z","level":"INFO","event":"sql.tx.rollback","service":"leafygo-api","sql_source":"query_console","query":"SELECT id FROM users WHERE password = '[REDACTED]'","query_sha256":"3c4f...","param_count":0,"params_summary":[],"actor":"user:andi","db_id":"0195f0a1-1111-7000-8000-000000000001","db_name":"storefront","database":"storefront","status":"rollback","duration_ms":7,"trace_id":"9d12ab4488c012e7a1bc34de56f78901","span_id":"11a2b3c4d5e6f708","correlation_id":"req-4c2f9a01b3de"}
```

### 2. OpenTelemetry console span output (`OTEL_TRACES_CONSOLE=true`)

Parent-child span waterfall across API → Queue → Worker → Lock → Batch (`TraceID: 4bf92f3577b34da6a3ce929d0e0e4736`):

```json
{"Name":"queue.publish","SpanContext":{"TraceID":"4bf92f3577b34da6a3ce929d0e0e4736","SpanID":"5e8d1a02c4f67b90"},"Parent":{"TraceID":"4bf92f3577b34da6a3ce929d0e0e4736","SpanID":"00f067aa0ba902b7"},"Attributes":[{"Key":"db_name","Value":{"Type":"STRING","Value":"bench"}},{"Key":"action","Value":{"Type":"STRING","Value":"apply"}},{"Key":"mode","Value":{"Type":"STRING","Value":"batch"}},{"Key":"job_id","Value":{"Type":"STRING","Value":"0195f0a1-3333-7000-8000-000000000003"}}]}
{"Name":"worker.acquire_db_lock","SpanContext":{"TraceID":"4bf92f3577b34da6a3ce929d0e0e4736","SpanID":"8b3f20a9d1e45c67"},"Parent":{"TraceID":"4bf92f3577b34da6a3ce929d0e0e4736","SpanID":"7a2e4c9f11b03d82"},"Attributes":[{"Key":"db_name","Value":{"Type":"STRING","Value":"bench"}}]}
{"Name":"worker.execute_batch","SpanContext":{"TraceID":"4bf92f3577b34da6a3ce929d0e0e4736","SpanID":"c19f44d0e8ab2104"},"Parent":{"TraceID":"4bf92f3577b34da6a3ce929d0e0e4736","SpanID":"7a2e4c9f11b03d82"},"Attributes":[{"Key":"batch_no","Value":{"Type":"INT64","Value":1}},{"Key":"cursor_from","Value":{"Type":"INT64","Value":1}},{"Key":"cursor_to","Value":{"Type":"INT64","Value":10000}},{"Key":"rows_affected","Value":{"Type":"INT64","Value":10000}}]}
{"Name":"worker.process_job","SpanContext":{"TraceID":"4bf92f3577b34da6a3ce929d0e0e4736","SpanID":"7a2e4c9f11b03d82"},"Parent":{"TraceID":"4bf92f3577b34da6a3ce929d0e0e4736","SpanID":"5e8d1a02c4f67b90"},"Attributes":[{"Key":"worker_id","Value":{"Type":"STRING","Value":"worker-1"}},{"Key":"db_name","Value":{"Type":"STRING","Value":"bench"}},{"Key":"action","Value":{"Type":"STRING","Value":"apply"}},{"Key":"mode","Value":{"Type":"STRING","Value":"batch"}}]}
```

### 3. Prometheus `/metrics` output (`app:8080/metrics` & `worker:8081/metrics`)

```prometheus
# Server (:8080/metrics)
leafygo_migration_jobs_current{db_name="bench",status="success"} 1
leafygo_db_pool_connections{db_name="platform",pool_role="platform",state="active"} 1
leafygo_db_pool_connections{db_name="platform",pool_role="platform",state="idle"} 3

# Worker (:8081/metrics)
leafygo_migration_jobs_total{action="apply",db_name="bench",mode="batch",status="success"} 1
leafygo_migration_batches_processed_total{db_name="bench",status="success"} 100
leafygo_migration_rows_processed_total{action="apply",db_name="bench"} 1000000
leafygo_migration_queue_wait_duration_seconds_count{db_name="bench",mode="batch"} 1
leafygo_migration_lock_wait_duration_seconds_count{db_name="bench"} 1
leafygo_migration_execution_duration_seconds_count{action="apply",db_name="bench",mode="batch",status="success"} 1
leafygo_db_pool_connections{db_name="platform",pool_role="worker",state="idle"} 2
```
