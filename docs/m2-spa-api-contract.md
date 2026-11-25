# SPA ↔ REST API contract (M2)

This is the handshake between the SPA (`web/`) and the Go API owned by P1/P2.
The SPA already calls every path below. While the endpoints are being built it
runs against an in-browser mock (`VITE_USE_MOCKS=true`), so the real API can land
incrementally without breaking the UI.

Sources of truth:

- TypeScript shapes: `web/src/api/types.ts`
- Client + error mapping: `web/src/api/client.ts`
- Path wrappers: `web/src/api/endpoints.ts`
- Mock behaviour/scenarios: `web/src/api/mock/server.ts`

## Conventions

- All paths are under `/api/v1/`.
- JSON in, JSON out; timestamps are RFC3339 strings.
- Session is the existing opaque `sid` cookie. Mutations require the
  `X-CSRF-Token` header returned by login/me.
- Every response should echo `X-Correlation-ID` and `X-Trace-ID`; the SPA shows
  both in error banners. Incoming `X-Correlation-ID` and W3C `traceparent` are
  propagated by `internal/web/middleware_otel.go`.
- 204 responses have no body.
- Errors use the M2 envelope:

```json
{
  "error": {
    "code": "PREDECESSOR_NOT_READY",
    "message": "The predecessor slot must be Active or queued first.",
    "correlation_id": "req-9f8a7b6c",
    "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736"
  }
}
```

## Auth

| Method | Path | Response |
|---|---|---|
| POST | `/auth/login` | `{ user, csrf_token, leader_of_groups: [{id,name}] }` |
| POST | `/auth/logout` | 204 |
| GET | `/auth/me` | same shape as login; 401 when no session |

`user`: `{ id, username, is_platform_admin, is_active, created_at }`.

## Admin (Platform Admin only)

Users:

| Method | Path | Body / response |
|---|---|---|
| GET | `/admin/users` | `{ users: User[] }` |
| POST | `/admin/users` | `{ username, password, is_platform_admin }` → `User` |
| POST | `/admin/users/{id}/password` | `{ password }` → 204 |
| POST | `/admin/users/{id}/active` | `{ is_active }` → `User` |
| DELETE | `/admin/users/{id}` | 204, or `409 USER_HAS_HISTORY` |

Groups:

| Method | Path | Body / response |
|---|---|---|
| GET | `/admin/groups` | `{ groups: Group[] }` |
| POST | `/admin/groups` | `{ name, description }` → `Group` |
| PUT | `/admin/groups/{id}` | `{ name, description }` → `Group` |
| PUT | `/admin/groups/{id}/leader` | `{ leader_id: string \| null }` → `Group` |
| PUT | `/admin/groups/{id}/members` | `{ user_ids: string[] }` → `Group` |
| PUT | `/admin/groups/{id}/access` | `{ grants: [{ db_id, level: "view"\|"edit" }] }` → `Group` |
| DELETE | `/admin/groups/{id}` | 204, or `409 GROUP_HAS_HISTORY` |
| POST | `/admin/groups/{id}/deactivate` | 204 (optional soft delete) |

`Group` carries `leader_id`, `leader_username`, `members: [{id,username}]`, and
`access: [{db_id, db_name, level}]`. The SPA shows a leader selector and an
optional soft-delete section.

Connections:

| Method | Path | Body / response |
|---|---|---|
| GET | `/admin/databases` | `{ databases: Database[] }` |
| POST | `/admin/databases` | `{ name, host, port, db_name, username, password, ssl_mode }` → `Database` |
| PUT | `/admin/databases/{id}` | same fields, `password` optional → `Database` |
| POST | `/admin/databases/{id}/test` | `{ ok, error?, latency_ms? }` |
| DELETE | `/admin/databases/{id}` | 204, or `409 DB_HAS_ACTIVE_WORK` |

## Database workspace (LevelView+)

| Method | Path | Response |
|---|---|---|
| GET | `/databases` | `{ databases: Database[] }` — only accessible DBs; each adds `level`, `lock_state`, `active_job_id` |
| GET | `/databases/{id}/overview` | `{ database, level, lock_state, active_job, stats, recent_history }` |
| GET | `/databases/{id}/schema` | `{ tables: SchemaTable[] }` (hide `_leafygo_*`) |
| GET | `/databases/{id}/data?table=&page=&page_size=&q=` | `DataResponse` |
| POST | `/databases/{id}/query` | `{ sql }` → `{ columns, rows, row_count, duration_ms, truncated, notices }` |
| POST | `/databases/{id}/query/explain` | `{ sql }` → `{ plan, duration_ms }` |

Stats are `{ table_count, row_estimate, total_size_bytes }`.
`active_job` is `Job | null`.

Platform Admin accounts must not access database tools; return `403` (M1 rule).

## Definitions & proposals (LevelEdit required to mutate)

| Method | Path | Body / response |
|---|---|---|
| GET | `/databases/{id}/migrations` | `{ migrations: MigrationDefinition[] }` |
| POST | `/databases/{id}/migrations` | `MigrationInput` → definition |
| PUT | `/databases/{id}/migrations/{mid}` | `MigrationInput` → definition |
| POST | `/databases/{id}/migrations/{mid}/validate` | `DryRunResult` |
| GET | `/databases/{id}/proposals` | `{ proposals: Proposal[] }` |
| POST | `/databases/{id}/proposals` | `{ migration_id, action_type, target_execution_id?, resolution?, reason? }` → `Proposal` |
| GET | `/databases/{id}/proposals/{pid}` | `ProposalDetail` |
| POST | `/databases/{id}/proposals/{pid}/approve` | `{ comment }` → `Proposal` |
| POST | `/databases/{id}/proposals/{pid}/reject` | `{ comment }` → `Proposal` |
| POST | `/databases/{id}/proposals/{pid}/withdraw` | `Proposal` |

`MigrationInput`:

```ts
{
  title, description,
  sql_up, sql_down,
  is_irreversible, irreversible_reason,
  execution_mode: "normal" | "batch",
  batch_config: { table_name, cursor_col, batch_size, updated_at_col? } | null,
  replaces_migration_id: string | null
}
```

`DryRunResult`: `{ dry_run_status: "passed"|"inconclusive"|"failed", message?, sqlstate?, duration_ms? }`.
`inconclusive` (57014/55P03) must **not** block review; the SPA renders a warning.

`ProposalDetail`:

```ts
{
  proposal: Proposal,
  migration: MigrationDefinition,
  approvers: Array<{
    group_id, group_name, leader_id, leader_username,
    access_level: "view"|"edit",
    decision: "approved"|"rejected"|null,
    comment, decided_at: string|null
  }>,
  can_approve: boolean,
  can_withdraw: boolean
}
```

When the last approval queues the job, include `job_id` on the returned
`Proposal`; the review page links straight to `/databases/{id}/jobs/{job_id}`.

`resolve_uncertain` requires `target_execution_id`, `resolution`
(`committed`/`not_committed`), and a non-empty `reason`. The UI labels it as an
optional/advanced action and surfaces `RESOLVE_UNCERTAIN_INVALID`.

## Jobs & history

| Method | Path | Response |
|---|---|---|
| GET | `/databases/{id}/jobs/{jobID}` | `{ job, lock_state, attempts: Execution[], progress }` |
| GET | `/databases/{id}/history?hide_rollback=true` | `{ executions: Execution[] }` |
| GET | `/workers/status` | `{ workers: [{ worker_id, hostname, active_jobs, last_seen_at, online }] }` (optional) |

`Job` includes `execution_id`, `root_execution_id`, cumulative
`completed_batches`/`committed_rows`, `total_batches`, `retry_count`,
`last_cursor`, `scope_min_cursor`, `scope_max_cursor`, `worker_id`,
`lock_state`, and `trace_id`.

`progress`: `{ completed_batches, total_batches, committed_rows, percent }`.

The SPA polls the job every 1.5s while `queued|running|partial|partially_reverted|uncertain`;
return cached/consistent cumulative numbers so the bar never regresses.

`Execution` includes `executed_sql` when available. The history page opens the
immutable SQL snapshot in a modal. The SPA mirrors the `hide_rollback` rule
client-side (`web/src/lib/history.ts`) and also relies on the server filter.

## Mandatory error codes used by the UI

`INVALID_MIGRATION_SQL`, `NON_TRANSACTIONAL_STATEMENT`,
`INVALID_BATCH_IDENTIFIER`, `INVALID_BATCH_CURSOR_TYPE`, `DRY_RUN_BUSY`,
`DUPLICATE_ACTIVE_PROPOSAL`, `PREDECESSOR_NOT_READY`, `PREDECESSOR_FAILED`,
`APPROVER_SET_CHANGED`, `NO_ACCESS_GROUPS`, `MIGRATION_NOT_SUPERSEDABLE`,
`USER_HAS_HISTORY`, `GROUP_HAS_HISTORY`, `DB_HAS_HISTORY`, `DB_HAS_ACTIVE_WORK`,
`UNCERTAIN_MANUAL_INTERVENTION`, `RESOLVE_UNCERTAIN_INVALID`,
`UNSAFE_BATCH_REVERT`, `FORBIDDEN_NOT_GROUP_LEADER`, `FORBIDDEN`.

Human guidance per code lives in `web/src/lib/status.ts` (`errorGuidance`).

## Metrics endpoint

- `GET /metrics` is public, outside auth, and is excluded from request spans/logs.
- `leafygo_migration_jobs_current{status,db_name}` must be registered only by
  `cmd/server` (`telemetry.PlatformJobCountSource`); worker replicas must not
  register it.
- Worker should expose `/metrics` on `:8081` for Prometheus (`prometheus.yml`).

## Remaining integration tasks (P1/P2)

1. Implement the JSON handlers and return the shapes above.
2. Add `job_id` to the approve response when the proposal queues.
3. Add `executed_sql` to history rows.
4. Implement `GET /workers/status` (optional) or leave it 404; the SPA handles
   that gracefully.
5. Wire telemetry helpers (`telemetry.StartSpan`, `LogSQL*`) into handlers,
   queue, and worker. See `docs/m2-ega-workplan.md`.
