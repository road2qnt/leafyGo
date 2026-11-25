# M2 — Ega workplan (SPA, Logging, Tracing, Observability)

Scope frozen by `M2_PLAN.md` § Person 3. This file turns the architecture into an
executable checklist and records the integration handshake with P1/P2.

## Deliverables

| # | Deliverable | Files | Proof |
|---|-------------|-------|-------|
| 1 | Structured JSON activity logging, trace injection, SQL privacy/redaction | `internal/telemetry/logger.go`, `logger_test.go` | `go test ./internal/telemetry/` |
| 2 | OpenTelemetry tracing (console + OTLP/HTTP), W3C propagation | `internal/telemetry/tracer.go`, `tracer_test.go` | span parenting tests |
| 3 | Prometheus metrics + pgxpool collector + single-source `jobs_current` | `internal/telemetry/metrics.go`, `metrics_test.go` | gather assertions |
| 4 | HTTP tracing/correlation middleware + `/metrics` | `internal/web/middleware_otel.go`, `server.go` | middleware tests |
| 5 | Observability stack (pinned) | `deploy/observability/**` | `docker compose config`, YAML parse |
| 6 | Responsive SPA (React 19 + TS + Vite + Tailwind v4 + TanStack Query + Router) | `web/**` | `tsc`, `vitest`, `vite build` |
| 7 | SPA embedded in the Go binary | `internal/web/dist/**`, SPA handler | Go handler tests |
| 8 | Integration contract for P1/P2 | `docs/m2-spa-api-contract.md` | review |

## Task list

- [x] T1 logger + SQL privacy
- [x] T2 tracer + propagation
- [x] T3 metrics
- [x] T4 middleware + wiring
- [x] T5 observability stack
- [x] T6 SPA foundation
- [x] T7 M1 pages in SPA
- [x] T8 M2 workflow pages in SPA
- [x] T9 SPA embedding + build pipeline
- [x] T10 verification, docs, review

## Verification (2026-11-25)

| Check | Command | Result |
|---|---|---|
| Go unit/integration suite | `go test ./...` | pass |
| Go static analysis | `go vet ./...` | pass |
| Telemetry redaction/propagation tests | `go test ./internal/telemetry/` | pass |
| SPA unit tests | `cd web && npm test` | 17/17 pass |
| SPA typecheck + build | `cd web && npm run build` | pass |
| Root compose | `docker compose config` | pass |
| Observability compose | `docker compose -f deploy/observability/... config` | pass |
| Dashboard JSON/YAML | Python parse | pass |
| Browser walkthrough | Playwright 1440×900 + 390×844 | 17 pages, 0 console errors, no mobile horizontal overflow |

The browser walkthrough covered: login, dashboard, overview, migrations, proposal
review, running job monitor, history + Hide rollback (6 → 3 rows), admin users/
groups/connections, approval to `queued`, `PREDECESSOR_NOT_READY` rejection, and
mobile dashboard/proposal/job pages.

## Post-review fixes

- Sanitizer now redacts `password_hash`/`password_enc`, `E'...'` and
  dollar-quoted literals, `Key (...) = (...)` and `Failing row contains (...)`
  PostgreSQL details; migration history error text is sanitized before storage.
- Hold banners and access labels now read `overview.lock_state` /
  `overview.level`; the mock deliberately omits those decorations from the
  nested database object so the walkthrough catches regressions.
- `partially_reverted` jobs only offer reverse resume; forward retry is hidden.
  A successful normal rollback exposes **Propose forward** on the job monitor and
  in history.
- Dry-run rendering has four states (passed / inconclusive / failed / not
  validated) and blocks approval on failed.
- Group deactivate is `type="button"`, console exporter defaults to stdout,
  SQL default logger is wired, request/error logs use context-aware calls,
  cross-database result/selection state resets, and OTLP docs use `http://`.
- Local observability ports bind to `127.0.0.1`; `SPA_ROOT` is passed through
  compose.

Deferred minors: modal focus trap/return focus, `SqlEditor` accessible name on
the inner editor, filelog scope restricted to LeafyGo containers, and a
multi-replica API `jobs_current` aggregation strategy (documented single-replica
assumption).


## Telemetry integration points for P1/P2

HTTP spans, correlation ids, request logs, and the SPA host are already wired.
Use the contract helpers from `internal/telemetry`:

```go
ctx, span := telemetry.StartSpan(ctx, "worker.process_job",
    attribute.String("job_id", job.JobID))
defer span.End()

traceParent, traceState := telemetry.InjectTraceContext(ctx) // store in jobs row
ctx = telemetry.ExtractTraceContext(ctx, job.TraceParent, job.TraceState)

telemetry.LogSQLStart(ctx, meta)
telemetry.LogSQLResult(ctx, meta, rows, duration, err)
telemetry.LogSQLTxEnd(ctx, meta, "commit", duration, nil)
```

Worker metrics:

```go
metrics.ObserveQueueWait(mode, dbName, started.Sub(queuedAt))
metrics.ObserveLockWait(dbName, lockWait)
metrics.JobRetried(mode, dbName)
metrics.BatchProcessed(status, dbName, 1)
metrics.RowsProcessed(action, dbName, n)
metrics.JobCompleted(status, mode, action, dbName, duration)
metrics.RegisterPoolGroup(func() []telemetry.PoolStatsSource { ... })
```

`target.Manager.Snapshots()` was added so target pools can be registered lazily
without opening new connections. `cmd/server` already registers the platform
pool and all live target pools; `cmd/worker` should do the same for its pools.


## Decisions that affect teammates

1. **SPA mount point.** The SPA is served at `/app/` while the M2 JSON API is
   still being built; the M1 Go-template UI stays at `/` so the app remains
   demoable. Set `SPA_ROOT=true` to redirect `/` to `/app/` once P1/P2 endpoints
   are complete.
2. **API contract.** `docs/m2-spa-api-contract.md` is the handshake. The SPA
   already calls those paths. The committed bundle defaults to the real API;
   append `?mock=1` (or build with `VITE_USE_MOCKS=true`) for the in-browser
   demo. `?mock=0` disables it again.
3. **Telemetry API.** `internal/telemetry` exports exactly the functions in
   `M2_PLAN.md` § 7.2. P1/P2 only need to pass context + `SQLMeta`.
4. **Worker metrics port.** `cmd/worker` must expose `/metrics` on `:8081` for
   the Prometheus config; the server exposes `:8080/metrics` already.

