-- Jibril: tambah file 002, jangan edit file ini.

create table users (
    id uuid primary key,
    username text not null,
    password_hash text not null,
    is_platform_admin boolean not null default false,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);
create unique index users_username_lower_idx on users (lower(username));

create table groups (
    id uuid primary key,
    name text not null,
    description text not null default '',
    created_at timestamptz not null default now()
);
create unique index groups_name_lower_idx on groups (lower(name));

create table group_members (
    group_id uuid not null references groups(id) on delete cascade,
    user_id uuid not null references users(id) on delete cascade,
    added_at timestamptz not null default now(),
    primary key (group_id, user_id)
);

create table db_connections (
    id uuid primary key,
    name text not null,
    host text not null,
    port integer not null default 5432,
    dbname text not null,
    username text not null,
    password_enc bytea not null,
    sslmode text not null default 'disable',
    created_by uuid references users(id) on delete set null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);
create unique index db_connections_name_lower_idx on db_connections (lower(name));

create table db_access (
    db_id uuid not null references db_connections(id) on delete cascade,
    group_id uuid not null references groups(id) on delete cascade,
    level text not null check (level in ('view', 'edit')),
    primary key (db_id, group_id)
);

create table sessions (
    id_hash bytea primary key,
    user_id uuid not null references users(id) on delete cascade,
    csrf_token text not null,
    created_at timestamptz not null,
    last_seen_at timestamptz not null,
    idle_expires_at timestamptz not null,
    absolute_expires_at timestamptz not null,
    rotated_to bytea,
    grace_expires_at timestamptz,
    revoked_at timestamptz
);
create index sessions_user_idx on sessions(user_id);
create index sessions_absolute_idx on sessions(absolute_expires_at);
