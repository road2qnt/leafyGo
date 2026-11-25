-- 1. Setiap Group memiliki Leader yang ditetapkan + Soft Delete pada db_connections (& groups [OPSIONAL]) agar History tidak pernah terhapus
ALTER TABLE groups
    ADD COLUMN leader_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN deleted_at TIMESTAMPTZ; -- [OPSIONAL — jika waktu memungkinkan] (Fallback: tolak delete dengan 409 GROUP_HAS_HISTORY)

ALTER TABLE db_connections
    ADD COLUMN deleted_at TIMESTAMPTZ;

-- Ganti unique index lama pada db_connections agar nama database dapat digunakan kembali setelah soft-delete
DROP INDEX IF EXISTS db_connections_name_lower_idx;
CREATE UNIQUE INDEX db_connections_name_lower_active_idx
    ON db_connections (lower(name))
    WHERE deleted_at IS NULL;

-- 2. Definisi Migration
CREATE TABLE migration_definitions (
    id                         UUID PRIMARY KEY,
    db_id                      UUID NOT NULL REFERENCES db_connections(id) ON DELETE RESTRICT,
    seq_version                BIGINT NOT NULL,               -- Nomor sekuens raw pembuatan definisi pada db_id
    effective_seq              BIGINT NOT NULL,               -- Slot urutan linear efektif (= seq_version sendiri, atau effective_seq milik chain root yang digantikannya)
    revision                   INT NOT NULL DEFAULT 1,        -- Naik saat diedit selama masih draft
    title                      TEXT NOT NULL,
    description                TEXT NOT NULL DEFAULT '',
    sql_up                     TEXT NOT NULL,
    sql_down                   TEXT NOT NULL DEFAULT '',
    is_irreversible            BOOLEAN NOT NULL DEFAULT FALSE,
    irreversible_reason        TEXT NOT NULL DEFAULT '',
    execution_mode             TEXT NOT NULL DEFAULT 'normal' CHECK (execution_mode IN ('normal', 'batch')),
    batch_config               JSONB,                         -- {"table_name": "...", "cursor_col": "id", "batch_size": 5000, "updated_at_col": "updated_at" [OPSIONAL]}
    is_immutable               BOOLEAN NOT NULL DEFAULT FALSE,-- Di-set TRUE saat transisi Revalidating -> Executing sebelum SQL pertama dikirim ke Target DB
    replaces_migration_id      UUID REFERENCES migration_definitions(id) ON DELETE RESTRICT,
    is_superseded              BOOLEAN NOT NULL DEFAULT FALSE,
    superseded_by_migration_id UUID REFERENCES migration_definitions(id) ON DELETE RESTRICT,
    superseded_at              TIMESTAMPTZ,
    created_by                 UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_by                 UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (db_id, seq_version),
    CHECK (is_irreversible = TRUE AND length(trim(irreversible_reason)) > 0 OR is_irreversible = FALSE AND length(trim(sql_down)) > 0),
    CHECK (replaces_migration_id IS NULL OR replaces_migration_id <> id)
);
CREATE INDEX migration_definitions_db_effective_seq_idx ON migration_definitions (db_id, effective_seq ASC, seq_version ASC);

-- 3. Pengajuan Perubahan / Change Request Proposal
CREATE TABLE migration_proposals (
    id                      UUID PRIMARY KEY,
    db_id                   UUID NOT NULL REFERENCES db_connections(id) ON DELETE RESTRICT,
    migration_id            UUID NOT NULL REFERENCES migration_definitions(id) ON DELETE RESTRICT,
    migration_revision      INT NOT NULL,
    action_type             TEXT NOT NULL CHECK (action_type IN ('apply', 'rollback', 'forward', 'batch_retry', 'batch_revert', 'resolve_uncertain')),
    target_execution_id     UUID,                      -- Diisi saat rollback/retry/revert/resolve_uncertain merujuk execution_id tertentu
    resolution              TEXT CHECK (resolution IS NULL OR resolution IN ('committed', 'not_committed')), -- [OPSIONAL]
    reason                  TEXT NOT NULL DEFAULT '',  -- [OPSIONAL] Wajib diisi untuk resolve_uncertain
    status                  TEXT NOT NULL DEFAULT 'draft'
                            CHECK (status IN ('draft', 'pending_review', 'withdrawn', 'approved', 'rejected', 'queued', 'running', 'partial', 'partially_reverted', 'uncertain', 'completed', 'failed')),
    proposed_by             UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        action_type <> 'resolve_uncertain'
        OR (target_execution_id IS NOT NULL AND resolution IN ('committed', 'not_committed') AND length(trim(reason)) > 0)
    )
);

-- Partial Unique Index: Mencegah 2 proposal aktif secara konkuren untuk migration_id yang sama (409 DUPLICATE_ACTIVE_PROPOSAL).
-- Status hold ('partial', 'partially_reverted', 'uncertain') BUKAN bagian dari himpunan aktif di index ini maupun di trigger
-- trg_migration_definitions_immutable agar proposal pemulihan (batch_retry, batch_revert, resolve_uncertain) pada migration_id yang sama dapat dibuat.
CREATE UNIQUE INDEX migration_proposals_one_active_per_migration_idx
    ON migration_proposals (migration_id)
    WHERE status IN ('pending_review', 'approved', 'queued', 'running');

-- Trigger Guard pada migration_definitions:
CREATE OR REPLACE FUNCTION enforce_migration_definitions_immutability()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.sql_up IS DISTINCT FROM OLD.sql_up
       OR NEW.sql_down IS DISTINCT FROM OLD.sql_down
       OR NEW.is_irreversible IS DISTINCT FROM OLD.is_irreversible
       OR NEW.execution_mode IS DISTINCT FROM OLD.execution_mode
       OR NEW.batch_config IS DISTINCT FROM OLD.batch_config
       OR NEW.replaces_migration_id IS DISTINCT FROM OLD.replaces_migration_id
    THEN
        IF OLD.is_immutable = TRUE THEN
            RAISE EXCEPTION 'migration_definitions specification columns are permanently immutable once execution has started';
        END IF;

        IF EXISTS (
            SELECT 1 FROM migration_proposals
            WHERE migration_id = OLD.id
              AND status IN ('pending_review', 'approved', 'queued', 'running')
        ) THEN
            RAISE EXCEPTION 'migration_definitions cannot be modified while an active proposal exists';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_migration_definitions_immutable
BEFORE UPDATE ON migration_definitions
FOR EACH ROW EXECUTE FUNCTION enforce_migration_definitions_immutability();

-- 4. Snapshot Required Approvers Saat Proposal Diajukan ke Pending Review
CREATE TABLE migration_proposal_approvers (
    proposal_id  UUID NOT NULL REFERENCES migration_proposals(id) ON DELETE RESTRICT,
    group_id     UUID NOT NULL REFERENCES groups(id) ON DELETE RESTRICT,
    leader_id    UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    access_level TEXT NOT NULL CHECK (access_level IN ('view', 'edit')),
    PRIMARY KEY (proposal_id, group_id)
);

-- 5. Persetujuan Leader Group
CREATE TABLE migration_approvals (
    proposal_id UUID NOT NULL REFERENCES migration_proposals(id) ON DELETE RESTRICT,
    group_id    UUID NOT NULL REFERENCES groups(id) ON DELETE RESTRICT,
    leader_id   UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    decision    TEXT NOT NULL CHECK (decision IN ('approved', 'rejected')),
    comment     TEXT NOT NULL DEFAULT '',
    decided_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (proposal_id, group_id),
    FOREIGN KEY (proposal_id, group_id) REFERENCES migration_proposal_approvers(proposal_id, group_id) ON DELETE RESTRICT
);

-- 6. Migration History (Mencatat SEMUA attempt)
CREATE TABLE migration_executions (
    execution_id             UUID PRIMARY KEY,
    root_execution_id        UUID NOT NULL REFERENCES migration_executions(execution_id) ON DELETE RESTRICT,
    db_id                    UUID NOT NULL REFERENCES db_connections(id) ON DELETE RESTRICT,
    migration_id             UUID NOT NULL REFERENCES migration_definitions(id) ON DELETE RESTRICT,
    proposal_id              UUID REFERENCES migration_proposals(id) ON DELETE SET NULL,
    seq_order                BIGSERIAL,
    migration_revision       INT NOT NULL,
    action_type              TEXT NOT NULL CHECK (action_type IN ('apply', 'rollback', 'forward', 'batch_retry', 'batch_revert', 'resolve_uncertain')),
    executed_sql             TEXT NOT NULL,
    executed_by              UUID REFERENCES users(id) ON DELETE SET NULL,
    executed_by_username     TEXT NOT NULL,
    db_name_snapshot         TEXT NOT NULL,
    migration_title_snapshot TEXT NOT NULL,
    started_at               TIMESTAMPTZ,
    finished_at              TIMESTAMPTZ,
    duration_ms              INT,
    status                   TEXT NOT NULL CHECK (status IN ('queued', 'running', 'success', 'failed', 'partial', 'partially_reverted', 'uncertain')),
    error_message            TEXT NOT NULL DEFAULT '',
    rollback_of_execution_id UUID REFERENCES migration_executions(execution_id) ON DELETE RESTRICT,
    committed_batches        INT NOT NULL DEFAULT 0,
    total_batches            INT NOT NULL DEFAULT 1,
    committed_rows           BIGINT NOT NULL DEFAULT 0,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX migration_executions_db_seq_idx ON migration_executions (db_id, seq_order DESC);
CREATE INDEX migration_executions_root_idx ON migration_executions (root_execution_id);

-- Trigger Guard Immutability pada migration_executions:
CREATE OR REPLACE FUNCTION enforce_migration_executions_immutability()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'migration_executions rows are immutable and cannot be deleted';
    END IF;

    IF NEW.execution_id <> OLD.execution_id
       OR NEW.root_execution_id <> OLD.root_execution_id
       OR NEW.db_id <> OLD.db_id
       OR NEW.migration_id <> OLD.migration_id
       OR NEW.seq_order <> OLD.seq_order
       OR NEW.migration_revision <> OLD.migration_revision
       OR NEW.action_type <> OLD.action_type
       OR NEW.executed_sql <> OLD.executed_sql
       OR (NEW.executed_by IS DISTINCT FROM OLD.executed_by AND NEW.executed_by IS NOT NULL)
       OR NEW.executed_by_username <> OLD.executed_by_username
       OR NEW.db_name_snapshot <> OLD.db_name_snapshot
       OR NEW.migration_title_snapshot <> OLD.migration_title_snapshot
       OR NEW.rollback_of_execution_id IS DISTINCT FROM OLD.rollback_of_execution_id
       OR NEW.created_at <> OLD.created_at
    THEN
        RAISE EXCEPTION 'immutable audit columns on migration_executions cannot be modified';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_migration_executions_immutable
BEFORE UPDATE OR DELETE ON migration_executions
FOR EACH ROW EXECUTE FUNCTION enforce_migration_executions_immutability();

-- 7. Asynchronous Job Queue
CREATE TABLE migration_jobs (
    job_id              UUID PRIMARY KEY,
    queue_seq           BIGSERIAL NOT NULL,
    db_id               UUID NOT NULL REFERENCES db_connections(id) ON DELETE RESTRICT,
    proposal_id         UUID NOT NULL UNIQUE REFERENCES migration_proposals(id) ON DELETE RESTRICT,
    migration_id        UUID NOT NULL REFERENCES migration_definitions(id) ON DELETE RESTRICT,
    execution_id        UUID NOT NULL UNIQUE REFERENCES migration_executions(execution_id) ON DELETE RESTRICT,
    action_type         TEXT NOT NULL CHECK (action_type IN ('apply', 'rollback', 'forward', 'batch_retry', 'batch_revert', 'resolve_uncertain')),
    execution_mode      TEXT NOT NULL CHECK (execution_mode IN ('normal', 'batch')),
    status              TEXT NOT NULL DEFAULT 'queued'
                        CHECK (status IN ('queued', 'running', 'partial', 'partially_reverted', 'success', 'failed', 'uncertain')),
    retry_count         INT NOT NULL DEFAULT 0,
    max_retries         INT NOT NULL DEFAULT 3,
    next_run_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_cursor         BIGINT,
    scope_min_cursor    BIGINT,
    scope_max_cursor    BIGINT,
    total_batches       INT NOT NULL DEFAULT 1,
    completed_batches   INT NOT NULL DEFAULT 0,
    committed_rows      BIGINT NOT NULL DEFAULT 0,
    trace_parent        TEXT NOT NULL DEFAULT '',
    trace_state         TEXT NOT NULL DEFAULT '',
    correlation_id      TEXT NOT NULL DEFAULT '',
    worker_id           TEXT,
    last_error          TEXT NOT NULL DEFAULT '',
    queued_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at          TIMESTAMPTZ,
    finished_at         TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX migration_jobs_poll_idx ON migration_jobs (db_id, queue_seq ASC) WHERE status = 'queued';

-- 8. Cross-Worker Per-Database Lock Table
CREATE TABLE database_migration_locks (
    db_id               UUID PRIMARY KEY REFERENCES db_connections(id) ON DELETE CASCADE,
    job_id              UUID NOT NULL REFERENCES migration_jobs(job_id) ON DELETE CASCADE,
    execution_id        UUID NOT NULL REFERENCES migration_executions(execution_id) ON DELETE CASCADE,
    migration_id        UUID NOT NULL REFERENCES migration_definitions(id) ON DELETE CASCADE,
    worker_id           TEXT NOT NULL,
    lock_state          TEXT NOT NULL CHECK (lock_state IN ('running', 'partial_hold', 'partially_reverted_hold', 'uncertain_hold')),
    previous_lock_state TEXT CHECK (previous_lock_state IS NULL OR previous_lock_state IN ('running', 'partial_hold', 'partially_reverted_hold', 'uncertain_hold')),
    lock_epoch          BIGINT NOT NULL DEFAULT 1,
    lease_until         TIMESTAMPTZ NOT NULL,
    next_probe_at       TIMESTAMPTZ,
    acquired_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 9. Worker Liveness & Heartbeats Table [OPSIONAL]
CREATE TABLE worker_heartbeats (
    worker_id    TEXT PRIMARY KEY,
    hostname     TEXT NOT NULL,
    started_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    active_jobs  INT NOT NULL DEFAULT 0
);

