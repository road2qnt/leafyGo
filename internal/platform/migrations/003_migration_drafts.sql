create table migration_drafts (
    id uuid primary key,
    db_id uuid not null references db_connections(id) on delete cascade,
    title text not null default '',
    sql_text text not null,
    revision integer not null default 1,
    created_by uuid references users(id) on delete set null,
    updated_by uuid references users(id) on delete set null,
    created_at timestamptz not null,
    updated_at timestamptz not null
);
create index migration_drafts_db_updated_idx
    on migration_drafts (db_id, updated_at desc);
