import type {
  ApprovalRow,
  ApprovalSummary,
  Database,
  DatabaseOverview,
  DataResponse,
  Execution,
  Group,
  Job,
  MigrationDefinition,
  Proposal,
  ProposalDetail,
  QueryResponse,
  SchemaColumn,
  SchemaTable,
  User,
  AuthResponse,
} from "./types";

type Any = Record<string, any>;

const activeProposalStates = new Set(["pending_review", "approved", "queued", "running"]);

export function normalizeAuth(raw: Any): AuthResponse {
  if (raw.user) {
    return raw as AuthResponse;
  }
  const leaders = Array.isArray(raw.leader_of_groups) ? raw.leader_of_groups : [];
  return {
    user: {
      id: String(raw.id ?? ""),
      username: String(raw.username ?? ""),
      is_platform_admin: Boolean(raw.is_platform_admin ?? raw.is_admin),
      is_active: Boolean(raw.is_active),
      created_at: String(raw.created_at ?? ""),
    },
    csrf_token: String(raw.csrf_token ?? ""),
    leader_of_groups: leaders.map((entry: Any | string) =>
      typeof entry === "string"
        ? { id: entry, name: `Group ${entry.slice(0, 8)}` }
        : { id: String(entry.id), name: String(entry.name ?? entry.id) },
    ),
  };
}

export function normalizeDatabase(raw: Any): Database {
  const lock = raw.lock ?? null;
  return {
    id: String(raw.id),
    name: String(raw.name ?? ""),
    host: String(raw.host ?? ""),
    port: Number(raw.port ?? 5432),
    db_name: String(raw.db_name ?? raw.dbname ?? ""),
    username: String(raw.username ?? ""),
    ssl_mode: String(raw.ssl_mode ?? raw.sslmode ?? "disable"),
    created_at: String(raw.created_at ?? ""),
    updated_at: String(raw.updated_at ?? raw.created_at ?? ""),
    level: raw.level ?? raw.access_level ?? undefined,
    lock_state: raw.lock_state ?? lock?.lock_state ?? null,
    active_job_id: raw.active_job_id ?? lock?.job_id ?? null,
  };
}

export function normalizeUser(raw: Any): User {
  return {
    id: String(raw.id),
    username: String(raw.username),
    is_platform_admin: Boolean(raw.is_platform_admin ?? raw.is_admin),
    is_active: Boolean(raw.is_active),
    created_at: String(raw.created_at ?? ""),
  };
}

export function normalizeGroup(raw: Any): Group {
  return {
    id: String(raw.id),
    name: String(raw.name ?? ""),
    description: String(raw.description ?? ""),
    leader_id: raw.leader_id ?? null,
    leader_username: String(raw.leader_username ?? ""),
    members: (raw.members ?? []).map((member: Any) => ({
      id: String(member.id),
      username: String(member.username),
    })),
    access: (raw.access ?? []).map((grant: Any) => ({
      db_id: String(grant.db_id),
      db_name: String(grant.db_name ?? ""),
      level: grant.level === "edit" ? "edit" : "view",
    })),
    created_at: String(raw.created_at ?? ""),
  };
}

function normalizeColumn(raw: Any): SchemaColumn {
  return {
    name: String(raw.name ?? raw.Name ?? ""),
    data_type: String(raw.data_type ?? raw.DataType ?? "text"),
    nullable: Boolean(raw.nullable ?? raw.Nullable),
    default_value: raw.default_value ?? raw.Default ?? null,
    is_primary_key: Boolean(raw.is_primary_key ?? raw.IsPK),
  };
}

export function normalizeTable(raw: Any): SchemaTable {
  return {
    schema: String(raw.schema ?? raw.Schema ?? "public"),
    name: String(raw.name ?? raw.Name ?? ""),
    row_estimate: Number(raw.row_estimate ?? raw.row_count ?? raw.RowCount ?? 0) || 0,
    size_bytes: Number(raw.size_bytes ?? 0) || 0,
    can_select: raw.can_select === undefined ? true : Boolean(raw.can_select),
    columns: (raw.columns ?? raw.Columns ?? []).map(normalizeColumn),
  };
}

export function normalizeData(raw: Any): DataResponse {
  const columnsRaw: Any[] = raw.columns ?? [];
  const names = columnsRaw.map((column) => (typeof column === "string" ? column : String(column.name ?? column.Name ?? "")));
  const columns = columnsRaw.map((column, index) => ({
    name: names[index],
    data_type: typeof column === "object" && column.numeric ? "numeric" : undefined,
  }));
  const rows = (raw.rows ?? []).map((row: Any) => {
    if (!Array.isArray(row)) {
      const out: Record<string, unknown> = {};
      names.forEach((name) => {
        const cell = row[name];
        out[name] =
          cell && typeof cell === "object" && "text" in cell
            ? cell.is_null || cell.IsNull
              ? null
              : cell.text
            : cell;
      });
      return out;
    }
    const out: Record<string, unknown> = {};
    names.forEach((name, index) => {
      const cell = row[index];
      if (cell && typeof cell === "object") {
        out[name] = cell.is_null || cell.IsNull ? null : (cell.text ?? cell.Text);
      } else {
        out[name] = cell;
      }
    });
    return out;
  });
  const page = Number(raw.page ?? 1);
  const pageSize = Number(raw.page_size ?? 20);
  // The viewer API omits row_count when it cannot count the table, and always sends has_next.
  const count = raw.total_rows ?? raw.row_count;
  const total =
    count === undefined || count === null
      ? typeof raw.has_next === "boolean"
        ? null
        : rows.length
      : Number(count);
  const knownPages = total === null ? page : Math.max(1, Math.ceil(total / pageSize));
  return {
    table: String(raw.table ?? ""),
    columns,
    rows,
    page,
    page_size: pageSize,
    total_rows: total,
    total_pages: raw.has_next ? Math.max(knownPages, page + 1) : knownPages,
    truncated: Boolean(raw.truncated),
  };
}

export function normalizeQuery(raw: Any): QueryResponse {
  const data = normalizeData(raw);
  return {
    columns: data.columns,
    rows: data.rows,
    row_count: Number(raw.row_count ?? data.rows.length),
    duration_ms: Number(raw.duration_ms ?? Math.round(Number(raw.duration_ns ?? 0) / 1e6)),
    truncated: Boolean(raw.truncated),
    notices: raw.notices ?? (raw.command_tag ? [String(raw.command_tag)] : []),
  };
}

export function normalizeMigration(raw: Any, all: MigrationDefinition[]): MigrationDefinition {
  const byId = new Map(all.map((migration) => [migration.id, migration]));
  const latestProposal = raw.latest_proposal ?? null;
  const latestExecution = raw.latest_execution ?? null;
  const replacesID = raw.replaces_migration_id ?? null;
  const supersededByID = raw.superseded_by_migration_id ?? null;
  return {
    ...raw,
    id: String(raw.id),
    db_id: String(raw.db_id),
    batch_config: raw.batch_config ?? null,
    replaces_migration_id: replacesID,
    replaces_title: raw.replaces_title ?? (replacesID ? byId.get(replacesID)?.title ?? null : null),
    replaces_effective_seq:
      raw.replaces_effective_seq ?? (replacesID ? byId.get(replacesID)?.effective_seq ?? null : null),
    is_active: Boolean(raw.is_active),
    is_superseded: Boolean(raw.is_superseded),
    superseded_by_migration_id: supersededByID,
    superseded_by_title:
      raw.superseded_by_title ?? (supersededByID ? byId.get(supersededByID)?.title ?? null : null),
    active_proposal:
      raw.active_proposal ??
      (latestProposal && activeProposalStates.has(latestProposal.status)
        ? { id: latestProposal.id, status: latestProposal.status, action_type: latestProposal.action_type }
        : null),
    latest_status:
      raw.latest_status ?? latestProposal?.status ?? latestExecution?.status ?? (raw.is_active ? "success" : null),
  } as MigrationDefinition;
}

function summarize(rows: ApprovalRow[]): ApprovalSummary {
  const approved = rows.filter((row) => row.decision === "approved").length;
  const rejected = rows.filter((row) => row.decision === "rejected").length;
  return { required: rows.length, approved, rejected, pending: rows.length - approved - rejected };
}

export function normalizeProposal(raw: Any, migrations: MigrationDefinition[]): Proposal {
  const migration = migrations.find((entry) => entry.id === raw.migration_id);
  return {
    ...raw,
    id: String(raw.id),
    db_id: String(raw.db_id),
    migration_id: String(raw.migration_id),
    migration_title: raw.migration_title ?? migration?.title ?? "",
    effective_seq: Number(raw.effective_seq ?? migration?.effective_seq ?? 0),
    replaces_title: raw.replaces_title ?? migration?.replaces_title ?? null,
    approval_summary: raw.approval_summary ?? summarize([]),
    job_id: raw.job_id ?? null,
    dry_run_status: raw.dry_run_status ?? null,
  } as Proposal;
}

export function normalizeProposalDetail(raw: Any, migrations: MigrationDefinition[]): ProposalDetail {
  const source = raw.proposal ?? raw;
  const proposal = normalizeProposal(source, migrations);
  const approvals: Any[] = raw.approvals ?? source.approvals ?? [];
  const approvers: ApprovalRow[] = (raw.approvers ?? source.approvers ?? []).map((approver: Any) => {
    const approval = approvals.find((entry) => entry.group_id === approver.group_id);
    return {
      group_id: String(approver.group_id),
      group_name: String(approver.group_name ?? ""),
      leader_id: String(approver.leader_id ?? ""),
      leader_username: String(approver.leader_username ?? ""),
      access_level: approver.access_level === "edit" ? "edit" : "view",
      decision: approval?.decision ?? null,
      comment: approval?.comment ?? "",
      decided_at: approval?.decided_at ?? null,
    };
  });
  proposal.approval_summary = approvers.length > 0 ? summarize(approvers) : proposal.approval_summary;
  const migration = raw.migration ?? migrations.find((entry) => entry.id === proposal.migration_id);
  if (!migration) {
    throw new Error("Migration definition for this proposal was not found.");
  }
  return {
    proposal,
    migration,
    approvers,
    can_approve: Boolean(raw.can_approve),
    can_withdraw: Boolean(raw.can_withdraw),
  };
}

export function normalizeExecution(raw: Any, migrations: MigrationDefinition[]): Execution {
  const byId = new Map(migrations.map((migration) => [migration.id, migration]));
  return {
    ...raw,
    execution_id: String(raw.execution_id),
    root_execution_id: String(raw.root_execution_id),
    rollback_of_execution_id: raw.rollback_of_execution_id ?? null,
    migration_title_snapshot: String(raw.migration_title_snapshot ?? byId.get(raw.migration_id)?.title ?? ""),
    superseded_by_title:
      raw.superseded_by_title ??
      (raw.superseded_by_migration_id ? byId.get(raw.superseded_by_migration_id)?.title ?? null : null),
    duration_ms: raw.duration_ms ?? null,
    executed_sql: raw.executed_sql ?? undefined,
  } as Execution;
}

export function normalizeJob(raw: Any, migrations: MigrationDefinition[], executions: Execution[]): Job {
  const migration = migrations.find((entry) => entry.id === raw.migration_id);
  const traceID =
    typeof raw.trace_parent === "string" && raw.trace_parent.split("-").length >= 3
      ? raw.trace_parent.split("-")[1]
      : raw.trace_id ?? null;
  return {
    ...raw,
    job_id: String(raw.job_id),
    db_id: String(raw.db_id),
    proposal_id: String(raw.proposal_id),
    execution_id: String(raw.execution_id),
    root_execution_id: String(raw.root_execution_id),
    migration_id: String(raw.migration_id),
    migration_title: raw.migration_title ?? migration?.title ?? "",
    queue_seq: Number(raw.queue_seq ?? 0),
    worker_id: raw.worker_id ?? null,
    lock_state: raw.lock_state ?? null,
    trace_id: traceID,
    attempts: executions,
  } as Job & { attempts: Execution[] };
}

export function normalizeOverview(raw: Any): DatabaseOverview {
  const database = normalizeDatabase(raw.database ?? {});
  return {
    database: { ...database, level: undefined, lock_state: undefined },
    level: raw.level ?? raw.access_level ?? database.level ?? "view",
    lock_state: raw.lock_state ?? database.lock_state ?? null,
    active_job: raw.active_job ?? null,
    active_job_id: raw.active_job_id ?? database.active_job_id ?? raw.active_job?.job_id ?? null,
    stats: {
      table_count: Number(raw.table_count ?? raw.stats?.table_count ?? 0),
      row_estimate: Number(raw.stats?.row_estimate ?? 0),
      total_size_bytes: Number(raw.stats?.total_size_bytes ?? 0),
    },
    recent_history: raw.recent_history ?? [],
  };
}
