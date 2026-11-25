alter table groups
    add column leader_id uuid references users(id) on delete set null,
    add column deleted_at timestamptz;

alter table db_connections
    add column deleted_at timestamptz;

drop index if exists db_connections_name_lower_idx;
create unique index db_connections_name_lower_active_idx
    on db_connections (lower(name))
    where deleted_at is null;

create table migration_definitions (
    id                         uuid primary key,
    db_id                      uuid not null references db_connections(id) on delete restrict,
    seq_version                bigint not null,
    effective_seq              bigint not null,
    revision                   int not null default 1,
    title                      text not null,
    description                text not null default '',
    sql_up                     text not null,
    sql_down                   text not null default '',
    is_irreversible            boolean not null default false,
    irreversible_reason        text not null default '',
    execution_mode             text not null default 'normal' check (execution_mode in ('normal', 'batch')),
    batch_config               jsonb,
    is_immutable               boolean not null default false,
    replaces_migration_id      uuid references migration_definitions(id) on delete restrict,
    is_superseded              boolean not null default false,
    superseded_by_migration_id uuid references migration_definitions(id) on delete restrict,
    superseded_at              timestamptz,
    created_by                 uuid references users(id) on delete set null,
    updated_by                 uuid references users(id) on delete set null,
    created_at                 timestamptz not null default now(),
    updated_at                 timestamptz not null default now(),
    unique (db_id, seq_version),
    check (is_irreversible = true and length(trim(irreversible_reason)) > 0 or is_irreversible = false and length(trim(sql_down)) > 0),
    check (replaces_migration_id is null or replaces_migration_id <> id)
);
create index migration_definitions_db_effective_seq_idx
    on migration_definitions (db_id, effective_seq asc, seq_version asc);

create table migration_proposals (
    id                  uuid primary key,
    db_id               uuid not null references db_connections(id) on delete restrict,
    migration_id        uuid not null references migration_definitions(id) on delete restrict,
    migration_revision  int not null,
    action_type         text not null check (action_type in ('apply', 'rollback', 'forward', 'batch_retry', 'batch_revert', 'resolve_uncertain')),
    target_execution_id uuid,
    resolution          text check (resolution is null or resolution in ('committed', 'not_committed')),
    reason              text not null default '',
    status              text not null default 'draft'
                        check (status in ('draft', 'pending_review', 'withdrawn', 'approved', 'rejected', 'queued', 'running', 'partial', 'partially_reverted', 'uncertain', 'completed', 'failed')),
    proposed_by         uuid references users(id) on delete set null,
    created_at          timestamptz not null default now(),
    updated_at          timestamptz not null default now(),
    check (
        action_type <> 'resolve_uncertain'
        or (target_execution_id is not null and resolution in ('committed', 'not_committed') and length(trim(reason)) > 0)
    )
);

create unique index migration_proposals_one_active_per_migration_idx
    on migration_proposals (migration_id)
    where status in ('pending_review', 'approved', 'queued', 'running');

create or replace function enforce_migration_definitions_immutability()
returns trigger as $$
begin
    if new.sql_up is distinct from old.sql_up
       or new.sql_down is distinct from old.sql_down
       or new.is_irreversible is distinct from old.is_irreversible
       or new.execution_mode is distinct from old.execution_mode
       or new.batch_config is distinct from old.batch_config
       or new.replaces_migration_id is distinct from old.replaces_migration_id
    then
        if old.is_immutable = true then
            raise exception 'migration_definitions specification columns are permanently immutable once execution has started';
        end if;

        if exists (
            select 1 from migration_proposals
            where migration_id = old.id
              and status in ('pending_review', 'approved', 'queued', 'running')
        ) then
            raise exception 'migration_definitions cannot be modified while an active proposal exists';
        end if;
    end if;

    return new;
end;
$$ language plpgsql;

create trigger trg_migration_definitions_immutable
before update on migration_definitions
for each row execute function enforce_migration_definitions_immutability();

create table migration_proposal_approvers (
    proposal_id  uuid not null references migration_proposals(id) on delete restrict,
    group_id     uuid not null references groups(id) on delete restrict,
    leader_id    uuid not null references users(id) on delete restrict,
    access_level text not null check (access_level in ('view', 'edit')),
    primary key (proposal_id, group_id)
);

create table migration_approvals (
    proposal_id uuid not null references migration_proposals(id) on delete restrict,
    group_id    uuid not null references groups(id) on delete restrict,
    leader_id   uuid not null references users(id) on delete restrict,
    decision    text not null check (decision in ('approved', 'rejected')),
    comment     text not null default '',
    decided_at  timestamptz not null default now(),
    primary key (proposal_id, group_id),
    foreign key (proposal_id, group_id) references migration_proposal_approvers(proposal_id, group_id) on delete restrict
);

create table migration_executions (
    execution_id             uuid primary key,
    root_execution_id        uuid not null references migration_executions(execution_id) on delete restrict,
    db_id                    uuid not null references db_connections(id) on delete restrict,
    migration_id             uuid not null references migration_definitions(id) on delete restrict,
    proposal_id              uuid references migration_proposals(id) on delete set null,
    seq_order                bigserial,
    migration_revision       int not null,
    action_type              text not null check (action_type in ('apply', 'rollback', 'forward', 'batch_retry', 'batch_revert', 'resolve_uncertain')),
    executed_sql             text not null,
    executed_by              uuid references users(id) on delete set null,
    executed_by_username     text not null,
    db_name_snapshot         text not null,
    migration_title_snapshot text not null,
    started_at               timestamptz,
    finished_at              timestamptz,
    duration_ms              int,
    status                   text not null check (status in ('queued', 'running', 'success', 'failed', 'partial', 'partially_reverted', 'uncertain')),
    error_message            text not null default '',
    rollback_of_execution_id uuid references migration_executions(execution_id) on delete restrict,
    committed_batches        int not null default 0,
    total_batches            int not null default 1,
    committed_rows           bigint not null default 0,
    created_at               timestamptz not null default now()
);
create index migration_executions_db_seq_idx on migration_executions (db_id, seq_order desc);
create index migration_executions_root_idx on migration_executions (root_execution_id);

create or replace function enforce_migration_executions_immutability()
returns trigger as $$
begin
    if tg_op = 'DELETE' then
        raise exception 'migration_executions rows are immutable and cannot be deleted';
    end if;

    if new.execution_id <> old.execution_id
       or new.root_execution_id <> old.root_execution_id
       or new.db_id <> old.db_id
       or new.migration_id <> old.migration_id
       or new.seq_order <> old.seq_order
       or new.migration_revision <> old.migration_revision
       or new.action_type <> old.action_type
       or new.executed_sql <> old.executed_sql
       or (new.executed_by is distinct from old.executed_by and new.executed_by is not null)
       or new.executed_by_username <> old.executed_by_username
       or new.db_name_snapshot <> old.db_name_snapshot
       or new.migration_title_snapshot <> old.migration_title_snapshot
       or new.rollback_of_execution_id is distinct from old.rollback_of_execution_id
       or new.created_at <> old.created_at
    then
        raise exception 'immutable audit columns on migration_executions cannot be modified';
    end if;

    return new;
end;
$$ language plpgsql;

create trigger trg_migration_executions_immutable
before update or delete on migration_executions
for each row execute function enforce_migration_executions_immutability();

create table migration_jobs (
    job_id            uuid primary key,
    queue_seq         bigserial not null,
    db_id             uuid not null references db_connections(id) on delete restrict,
    proposal_id       uuid not null unique references migration_proposals(id) on delete restrict,
    migration_id      uuid not null references migration_definitions(id) on delete restrict,
    execution_id      uuid not null unique references migration_executions(execution_id) on delete restrict,
    action_type       text not null check (action_type in ('apply', 'rollback', 'forward', 'batch_retry', 'batch_revert', 'resolve_uncertain')),
    execution_mode    text not null check (execution_mode in ('normal', 'batch')),
    status            text not null default 'queued'
                      check (status in ('queued', 'running', 'partial', 'partially_reverted', 'success', 'failed', 'uncertain')),
    retry_count       int not null default 0,
    max_retries       int not null default 3,
    next_run_at       timestamptz not null default now(),
    last_cursor       bigint,
    scope_min_cursor  bigint,
    scope_max_cursor  bigint,
    total_batches     int not null default 1,
    completed_batches int not null default 0,
    committed_rows    bigint not null default 0,
    trace_parent      text not null default '',
    trace_state       text not null default '',
    correlation_id    text not null default '',
    worker_id         text,
    last_error        text not null default '',
    queued_at         timestamptz not null default now(),
    started_at        timestamptz,
    finished_at       timestamptz,
    updated_at        timestamptz not null default now()
);
create index migration_jobs_poll_idx on migration_jobs (db_id, queue_seq asc) where status = 'queued';

create table database_migration_locks (
    db_id               uuid primary key references db_connections(id) on delete cascade,
    job_id              uuid not null references migration_jobs(job_id) on delete cascade,
    execution_id        uuid not null references migration_executions(execution_id) on delete cascade,
    migration_id        uuid not null references migration_definitions(id) on delete cascade,
    worker_id           text not null,
    lock_state          text not null check (lock_state in ('running', 'partial_hold', 'partially_reverted_hold', 'uncertain_hold')),
    previous_lock_state text check (previous_lock_state is null or previous_lock_state in ('running', 'partial_hold', 'partially_reverted_hold', 'uncertain_hold')),
    lock_epoch          bigint not null default 1,
    lease_until         timestamptz not null,
    next_probe_at       timestamptz,
    acquired_at         timestamptz not null default now(),
    updated_at          timestamptz not null default now()
);

create table worker_heartbeats (
    worker_id    text primary key,
    hostname     text not null,
    started_at   timestamptz not null default now(),
    last_seen_at timestamptz not null default now(),
    active_jobs  int not null default 0
);
