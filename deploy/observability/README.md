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
OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector:4318
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
  `JobCounts` source (otherwise replicas double-count).
- The OTLP HTTP exporter is used (`otlptracehttp`) to keep the demo firewall
  friendly; gRPC is still accepted by the collector.
