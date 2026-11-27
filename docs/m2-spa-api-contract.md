# SPA ↔ REST API contract (M2, post-merge)

This is the handshake between the SPA (`web/`) and the Go handlers that shipped in
`origin/main` (`internal/web/handlers_api_*.go`). The SPA normalizes the actual
response shapes in `web/src/api/normalize.ts`; `web/src/api/endpoints.ts` is the
only place that knows raw field names.

While the backend was still being built the SPA ran on an in-browser mock. The
committed bundle defaults to the real API. Demo mode is still available with
`?mock=1` (persisted in localStorage; `?mock=0` disables it).

## Conventions

- All paths are under `/api/v1/`.
- Session is the existing opaque `sid` cookie. Mutations require
  `X-CSRF-Token` from login/me.
- Every response echoes `X-Correlation-ID` and `X-Trace-ID`.
- Errors use the M2 envelope:

```json
{ "error": { "code": "...", "message": "...", "correlation_id": "...", "trace_id": "..." } }
```

## Auth

| Method | Path | Actual response |
|---|---|---|
| POST | `/auth/login` | flat user: `{id, username, is_platform_admin, is_admin, is_active, csrf_token, leader_of_groups:[uuid]}` |
| POST | `/auth/logout` | `{status:"logged_out"}` |
| GET | `/auth/me` | same flat shape |

The SPA adapter turns this into `{user, csrf_token, leader_of_groups:[{id,name}]}`
(names fall back to a short id because the endpoint only returns UUIDs).

## Admin

Users: `GET/POST /admin/users`, `POST /admin/users/{id}/password`,
`POST /admin/users/{id}/active` (`{is_active}`), `DELETE /admin/users/{id}`.

Groups:

| Method | Path | Body |
|---|---|---|
| GET/POST | `/admin/groups` | `{name, description, leader_id}` |
| PUT | `/admin/groups/{id}` | `{name, description}` |
| PUT | `/admin/groups/{id}/leader` | `{leader_id}` (required) |
| POST | `/admin/groups/{id}/members` | `{user_id}` (one member) |
| DELETE | `/admin/groups/{id}/members/{userID}` | — |
| POST | `/admin/groups/{id}/access` | `{db_id, level: "view"|"edit"|"none"}` |
| DELETE | `/admin/groups/{id}?soft=true` | optional soft delete |

The SPA's “save members/access” computes the diff and calls these single-item
endpoints. A hard delete with approval history returns `409 GROUP_HAS_HISTORY`;
soft delete is the safe fallback.

Connections:

| Method | Path | Notes |
|---|---|---|
| GET/POST | `/admin/databases` | request uses `dbname` and `sslmode` (not `db_name`/`ssl_mode`) |
| PUT | `/admin/databases/{id}` | same fields; empty password keeps the secret |
| POST | `/admin/databases/{id}/test` | `{status:"ok", message}` |
| DELETE | `/admin/databases/{id}` | `409 DB_HAS_ACTIVE_WORK` while locked/active |

The SPA maps `dbname`/`sslmode` to its `db_name`/`ssl_mode` model in
`normalizeDatabase`.

## Database workspace

| Method | Path | Actual response |
|---|---|---|
| GET | `/databases` | `{databases:[{...connection, access_level, lock_state, lock}]}` |
| GET | `/databases/{id}/overview` | `{database, access_level, lock_state, lock, table_count, active_migration_stack, required_approver_groups}` |
| GET | `/databases/{id}/schema` | `{tables:[{schema,name,kind,row_count,can_select}]}` |
| GET | `/databases/{id}/schema?schema=&table=` | adds `detail:{columns,constraints,indexes,primary_key}` |
| GET | `/databases/{id}/data?table=&page=&limit=` | `{columns:[{name,numeric}], rows:[[{text,is_null}]], page, page_size, row_count, has_next}` |
| POST | `/databases/{id}/query` | queryconsole Result: `{columns:[string], rows:[[Cell]], row_count, truncated, duration_ns, command_tag}` |
| POST | `/databases/{id}/query/explain` | `{plan, duration_ms}` (added in this branch) |
| POST | `/databases/{id}/migrations/{mid}/validate` | `{dry_run_status, message, sqlstate}` (added in this branch) |

Normalization notes:

- Overview has no eager history/stats; the SPA fetches `history` separately and
  shows `—` when row/size estimates are absent. `lock.job_id` becomes
  `active_job_id`, then the job is fetched for the progress card.
- Schema list has no columns; the SPA fetches the selected table detail.
- Data/query rows are cell-array responses; the adapter turns them into objects
  keyed by column name.
- Data search is applied client-side to the current page because the API has no
  free-text filter.

## Definitions, proposals, history, jobs

| Method | Path | Actual response |
|---|---|---|
| GET | `/databases/{id}/migrations` | `{migrations, active_stack, top_rollback_target, next_forward_target}` |
| POST | `/databases/{id}/migrations` | definition (`revision` required on update) |
| PUT | `/databases/{id}/migrations/{mid}` | definition |
| GET | `/databases/{id}/proposals` | `{proposals}` |
| POST | `/databases/{id}/proposals` | `{proposal, dry_run_status, dry_run_warning, dry_run}` |
| GET | `/databases/{id}/proposals/{pid}` | proposal + `approvers[]` + `approvals[]` |
| POST | `/databases/{id}/proposals/{pid}/approve` | `{status, proposal, job_id?, execution_id?, ...}` |
| POST | `/databases/{id}/proposals/{pid}/reject` | `{status, proposal}` |
| POST | `/databases/{id}/proposals/{pid}/withdraw` | `{status, proposal}` |
| GET | `/databases/{id}/history?hide_rollback=true` | `{hide_rollback, executions}` |
| GET | `/databases/{id}/jobs/{jobID}` | flat job (`jobStatusResponse`) |

Definitions already carry `latest_proposal`/`latest_execution`; the adapter
derives `active_proposal` and `latest_status` from them. Proposal detail is
joined into an approval matrix; `can_approve`/`can_withdraw` are computed in the
SPA from the current user and the approver rows.

The job endpoint is flat. The adapter joins `migrations` for the title and
fetches history to expose attempts for the same `root_execution_id`. Added job
fields in this branch: `queue_seq`, `worker_id`, `trace_parent`, `trace_state`,
`correlation_id`.

`hide_rollback` is server-authoritative; the SPA refetches with the flag.

## Optional / not implemented

- `GET /workers/status` (worker liveness) is not implemented. The SPA shows the
  optional “endpoint unavailable” callout; queue behaviour is unaffected.
- `query/to-draft` is not used; the SPA creates a normal migration definition
  instead.

## Error codes used by the UI

`INVALID_MIGRATION_SQL`, `NON_TRANSACTIONAL_STATEMENT`,
`INVALID_BATCH_IDENTIFIER`, `INVALID_BATCH_CURSOR_TYPE`, `DRY_RUN_BUSY`,
`DUPLICATE_ACTIVE_PROPOSAL`, `PREDECESSOR_NOT_READY`, `PREDECESSOR_FAILED`,
`APPROVER_SET_CHANGED`, `NO_ACCESS_GROUPS`, `MISSING_GROUP_LEADER`,
`MIGRATION_NOT_SUPERSEDABLE`, `USER_HAS_HISTORY`, `GROUP_HAS_HISTORY`,
`DB_HAS_HISTORY`, `DB_HAS_ACTIVE_WORK`, `UNCERTAIN_MANUAL_INTERVENTION`,
`RESOLVE_UNCERTAIN_INVALID`, `UNSAFE_BATCH_REVERT`,
`FORBIDDEN_NOT_GROUP_LEADER`, `FORBIDDEN`.

Guidance text per code: `web/src/lib/status.ts`.

## Metrics

`GET /metrics` is public and excluded from request spans/logs.
`leafygo_migration_jobs_current` is registered only by `cmd/server`
(`telemetry.PlatformJobCountSource`); worker replicas must not register it.

## Telemetry integration points (P1/P2)

HTTP spans, correlation ids, request logs, SQL helpers, and the SPA host are
wired for the API server. Worker spans/metrics still need the calls listed in
`docs/m2-ega-workplan.md`:

```go
ctx, span := telemetry.StartSpan(ctx, "worker.process_job")
traceParent, traceState := telemetry.InjectTraceContext(ctx)
ctx = telemetry.ExtractTraceContext(ctx, job.TraceParent, job.TraceState)
telemetry.LogSQLStart(ctx, meta)
telemetry.LogSQLResult(ctx, meta, rows, duration, err)
telemetry.LogSQLTxEnd(ctx, meta, "commit", duration, nil)
```
