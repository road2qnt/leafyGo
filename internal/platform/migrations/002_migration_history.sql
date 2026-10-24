create table migration_history (
    id uuid primary key,
    db_id uuid not null references db_connections(id) on delete cascade,
    version bigint not null,
    sql_text text not null,
    executed_by uuid references users(id) on delete set null,
    started_at timestamptz not null,
    finished_at timestamptz,
    status text not null check (status in ('running', 'success', 'failed')),
    error text not null default '',
    duration_ms integer check (duration_ms is null or duration_ms >= 0),
    unique (db_id, version)
);
create index migration_history_db_version_idx
    on migration_history (db_id, version desc);
create index migration_history_running_idx
    on migration_history (status) where status = 'running';
