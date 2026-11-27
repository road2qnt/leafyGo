import { api, queryString } from "./client";
import {
  normalizeAuth,
  normalizeData,
  normalizeDatabase,
  normalizeExecution,
  normalizeGroup,
  normalizeJob,
  normalizeMigration,
  normalizeOverview,
  normalizeProposal,
  normalizeProposalDetail,
  normalizeQuery,
  normalizeTable,
  normalizeUser,
} from "./normalize";
import type {
  AuthResponse,
  BatchConfig,
  BatchTestResult,
  Database,
  DatabaseOverview,
  DataResponse,
  DryRunResult,
  ExplainResponse,
  Group,
  HistoryResponse,
  JobDetail,
  JobsListResponse,
  MigrationDefinition,
  Proposal,
  ProposalDetail,
  QueryResponse,
  SchemaResponse,
  SchemaTable,
  User,
  WorkersResponse,
} from "./types";

type Any = Record<string, any>;

export const authApi = {
  login: async (username: string, password: string) =>
    normalizeAuth(await api<Any>("/auth/login", { json: { username, password } })),
  logout: () => api<void>("/auth/logout", { method: "POST" }),
  me: async () => normalizeAuth(await api<Any>("/auth/me")),
};

export const adminApi = {
  users: async () => {
    const raw = await api<{ users: Any[] }>("/admin/users");
    return { users: (raw.users ?? []).map(normalizeUser) };
  },
  createUser: async (input: { username: string; password: string; is_platform_admin: boolean }) =>
    normalizeUser(await api<Any>("/admin/users", { json: input })),
  resetPassword: (userId: string, password: string) =>
    api<void>(`/admin/users/${userId}/password`, { json: { password } }),
  setActive: async (userId: string, isActive: boolean) =>
    normalizeUser(await api<Any>(`/admin/users/${userId}/active`, { json: { is_active: isActive } })),
  deleteUser: (userId: string) => api<void>(`/admin/users/${userId}`, { method: "DELETE" }),

  groups: async () => {
    const raw = await api<{ groups: Any[] }>("/admin/groups");
    return { groups: (raw.groups ?? []).map(normalizeGroup) };
  },
  group: async (groupId: string) =>
    normalizeGroup(await api<Any>(`/admin/groups/${groupId}`)),
  createGroup: async (input: { name: string; description: string; leader_id: string }) =>
    normalizeGroup(await api<Any>("/admin/groups", { json: input })),
  updateGroup: async (groupId: string, input: { name: string; description: string }) =>
    normalizeGroup(await api<Any>(`/admin/groups/${groupId}`, { method: "PUT", json: input })),
  setLeader: async (groupId: string, leaderId: string) =>
    normalizeGroup(await api<Any>(`/admin/groups/${groupId}/leader`, { method: "PUT", json: { leader_id: leaderId } })),
  addMember: async (groupId: string, userId: string) =>
    normalizeGroup(await api<Any>(`/admin/groups/${groupId}/members`, { json: { user_id: userId } })),
  removeMember: async (groupId: string, userId: string) =>
    normalizeGroup(await api<Any>(`/admin/groups/${groupId}/members/${userId}`, { method: "DELETE" })),
  setDatabaseAccess: async (groupId: string, dbId: string, level: "view" | "edit" | "none") =>
    normalizeGroup(await api<Any>(`/admin/groups/${groupId}/access`, { json: { db_id: dbId, level } })),

  setMembers: async (groupId: string, userIds: string[], leaderId?: string) => {
    const current = await api<Any>(`/admin/groups/${groupId}`);
    const wanted = new Set(userIds);
    if (leaderId) wanted.add(leaderId);
    const existing = new Set<string>((current.members ?? []).map((member: Any) => String(member.id)));
    let latest: Any = current;
    for (const userId of wanted) {
      if (!existing.has(userId)) {
        latest = await api<Any>(`/admin/groups/${groupId}/members`, { json: { user_id: userId } });
      }
    }
    if (leaderId) {
      latest = await api<Any>(`/admin/groups/${groupId}/leader`, {
        method: "PUT",
        json: { leader_id: leaderId },
      });
    }
    for (const userId of existing) {
      if (!wanted.has(userId)) {
        latest = await api<Any>(`/admin/groups/${groupId}/members/${userId}`, { method: "DELETE" });
      }
    }
    return normalizeGroup(latest);
  },

  setAccess: async (groupId: string, grants: Array<{ db_id: string; level: "view" | "edit" }>) => {
    const current = await api<Any>(`/admin/groups/${groupId}`);
    const wanted = new Map(grants.map((grant) => [grant.db_id, grant.level]));
    const existing = new Map<string, string>(
      (current.access ?? []).map((grant: Any) => [String(grant.db_id), String(grant.level)]),
    );
    let latest: Any = current;
    for (const [dbId, level] of wanted) {
      if (existing.get(dbId) !== level) {
        latest = await api<Any>(`/admin/groups/${groupId}/access`, { json: { db_id: dbId, level } });
      }
    }
    for (const dbId of existing.keys()) {
      if (!wanted.has(dbId)) {
        latest = await api<Any>(`/admin/groups/${groupId}/access`, { json: { db_id: dbId, level: "none" } });
      }
    }
    return normalizeGroup(latest);
  },

  deleteGroup: (groupId: string) => api<void>(`/admin/groups/${groupId}`, { method: "DELETE" }),
  deactivateGroup: (groupId: string) =>
    api<void>(`/admin/groups/${groupId}${queryString({ soft: true })}`, { method: "DELETE" }),

  databases: async () => {
    const raw = await api<{ databases: Any[] }>("/admin/databases");
    return { databases: (raw.databases ?? []).map(normalizeDatabase) };
  },
  createDatabase: async (input: {
    name: string;
    host: string;
    port: number;
    db_name: string;
    username: string;
    password: string;
    ssl_mode: string;
  }) =>
    normalizeDatabase(
      await api<Any>("/admin/databases", {
        json: {
          name: input.name,
          host: input.host,
          port: input.port,
          dbname: input.db_name,
          username: input.username,
          password: input.password,
          sslmode: input.ssl_mode,
        },
      }),
    ),
  updateDatabase: async (
    dbId: string,
    input: {
      name: string;
      host: string;
      port: number;
      db_name: string;
      username: string;
      password?: string;
      ssl_mode: string;
    },
  ) =>
    normalizeDatabase(
      await api<Any>(`/admin/databases/${dbId}`, {
        method: "PUT",
        json: {
          name: input.name,
          host: input.host,
          port: input.port,
          dbname: input.db_name,
          username: input.username,
          password: input.password ?? "",
          sslmode: input.ssl_mode,
        },
      }),
    ),
  testDatabase: async (dbId: string): Promise<BatchTestResult> => {
    const raw = await api<Any>(`/admin/databases/${dbId}/test`, { method: "POST" });
    const ok = typeof raw.ok === "boolean" ? raw.ok : raw.status === "ok";
    return { ok, latency_ms: raw.latency_ms ?? 0, error: ok ? undefined : (raw.error ?? raw.message) };
  },
  deleteDatabase: (dbId: string) => api<void>(`/admin/databases/${dbId}`, { method: "DELETE" }),
};

export const databaseApi = {
  list: async () => {
    const raw = await api<{ databases: Any[] }>("/databases");
    return { databases: (raw.databases ?? []).map(normalizeDatabase) };
  },
  overview: async (dbId: string) => normalizeOverview(await api<Any>(`/databases/${dbId}/overview`)),
  schema: async (dbId: string): Promise<SchemaResponse> => {
    const raw = await api<{ tables: Any[] }>(`/databases/${dbId}/schema`);
    return { tables: (raw.tables ?? []).map(normalizeTable) };
  },
  schemaTable: async (dbId: string, table: SchemaTable): Promise<SchemaTable> => {
    const raw = await api<Any>(
      `/databases/${dbId}/schema${queryString({ schema: table.schema, table: table.name })}`,
    );
    const detail = raw.detail ?? (raw.tables ?? []).find((entry: Any) => entry.name === table.name) ?? raw;
    return normalizeTable(detail);
  },
  data: async (
    dbId: string,
    params: { schema?: string; table: string; page: number; page_size: number; q?: string },
  ): Promise<DataResponse> => {
    const raw = await api<Any>(
      `/databases/${dbId}/data${queryString({
        schema: params.schema,
        table: params.table,
        page: params.page,
        limit: params.page_size,
      })}`,
    );
    return normalizeData({ ...raw, table: params.table, page_size: params.page_size });
  },
  query: async (dbId: string, sql: string): Promise<QueryResponse> =>
    normalizeQuery(await api<Any>(`/databases/${dbId}/query`, { json: { sql } })),
  explain: (dbId: string, sql: string) =>
    api<ExplainResponse>(`/databases/${dbId}/query/explain`, { json: { sql } }),
};

export interface MigrationInput {
  title: string;
  description: string;
  sql_up: string;
  sql_down: string;
  is_irreversible: boolean;
  irreversible_reason: string;
  execution_mode: "normal" | "batch";
  batch_config: BatchConfig | null;
  replaces_migration_id: string | null;
  revision?: number;
}

async function definitionList(dbId: string): Promise<MigrationDefinition[]> {
  const raw = await api<{ migrations: Any[] }>(`/databases/${dbId}/migrations`);
  const rows = raw.migrations ?? [];
  return rows.map((row) => normalizeMigration(row, rows.map((entry) => normalizeMigration(entry, []))));
}

export const migrationApi = {
  list: async (dbId: string) => ({ migrations: await definitionList(dbId) }),
  get: async (dbId: string, migrationId: string) => {
    const migrations = await definitionList(dbId);
    const found = migrations.find((entry) => entry.id === migrationId);
    if (!found) throw new Error("Migration definition not found.");
    return found;
  },
  create: async (dbId: string, input: MigrationInput) => {
    const created = await api<Any>(`/databases/${dbId}/migrations`, {
      json: {
        title: input.title,
        description: input.description,
        sql_up: input.sql_up,
        sql_down: input.sql_down,
        is_irreversible: input.is_irreversible,
        irreversible_reason: input.irreversible_reason,
        execution_mode: input.execution_mode,
        batch_config: input.batch_config,
        replaces_migration_id: input.replaces_migration_id,
      },
    });
    return normalizeMigration(created, await definitionList(dbId));
  },
  update: async (dbId: string, migrationId: string, input: MigrationInput) => {
    const updated = await api<Any>(`/databases/${dbId}/migrations/${migrationId}`, {
      method: "PUT",
      json: {
        revision: input.revision,
        title: input.title,
        description: input.description,
        sql_up: input.sql_up,
        sql_down: input.sql_down,
        is_irreversible: input.is_irreversible,
        irreversible_reason: input.irreversible_reason,
        execution_mode: input.execution_mode,
        batch_config: input.batch_config,
        replaces_migration_id: input.replaces_migration_id,
      },
    });
    return normalizeMigration(updated, await definitionList(dbId));
  },
  delete: (dbId: string, migrationId: string) =>
    api<void>(`/databases/${dbId}/migrations/${migrationId}`, { method: "DELETE" }),
  validate: async (dbId: string, migrationId: string): Promise<DryRunResult> => {
    const raw = await api<Any>(`/databases/${dbId}/migrations/${migrationId}/validate`, { method: "POST" });
    return {
      dry_run_status: raw.dry_run_status ?? "passed",
      message: raw.message ?? raw.dry_run_warning,
      sqlstate: raw.sqlstate ?? raw.sql_state,
      duration_ms: raw.duration_ms,
    };
  },

  proposals: async (dbId: string) => {
    const [raw, migrations] = await Promise.all([
      api<{ proposals: Any[] }>(`/databases/${dbId}/proposals`),
      definitionList(dbId),
    ]);
    const rows = raw.proposals ?? [];
    const details = await Promise.all(
      rows.map((row) =>
        api<Any>(`/databases/${dbId}/proposals/${row.id}`).catch(() => null),
      ),
    );
    return {
      proposals: rows.map((row, index) => {
        const detail = details[index];
        return detail
          ? normalizeProposalDetail(detail, migrations).proposal
          : normalizeProposal(row, migrations);
      }),
    };
  },
  proposal: async (dbId: string, proposalId: string): Promise<ProposalDetail> => {
    const [raw, migrations] = await Promise.all([
      api<Any>(`/databases/${dbId}/proposals/${proposalId}`),
      definitionList(dbId),
    ]);
    return normalizeProposalDetail(raw, migrations);
  },
  createProposal: async (
    dbId: string,
    input: {
      migration_id: string;
      action_type: "apply" | "rollback" | "forward" | "batch_retry" | "batch_revert" | "resolve_uncertain";
      target_execution_id?: string | null;
      resolution?: "committed" | "not_committed" | null;
      reason?: string;
    },
  ) => {
    const [raw, migrations] = await Promise.all([
      api<Any>(`/databases/${dbId}/proposals`, { json: input }),
      definitionList(dbId),
    ]);
    const proposal = normalizeProposal(raw.proposal ?? raw, migrations);
    proposal.dry_run_status = raw.dry_run_status ?? proposal.dry_run_status;
    return proposal;
  },
  approve: async (dbId: string, proposalId: string, comment: string) => {
    const [raw, migrations] = await Promise.all([
      api<Any>(`/databases/${dbId}/proposals/${proposalId}/approve`, { json: { comment } }),
      definitionList(dbId),
    ]);
    const proposal = normalizeProposal(raw.proposal ?? raw, migrations);
    proposal.job_id = raw.job_id ?? null;
    return proposal;
  },
  reject: async (dbId: string, proposalId: string, comment: string) => {
    const [raw, migrations] = await Promise.all([
      api<Any>(`/databases/${dbId}/proposals/${proposalId}/reject`, { json: { comment } }),
      definitionList(dbId),
    ]);
    return normalizeProposal(raw.proposal ?? raw, migrations);
  },
  withdraw: async (dbId: string, proposalId: string) => {
    const [raw, migrations] = await Promise.all([
      api<Any>(`/databases/${dbId}/proposals/${proposalId}/withdraw`, { method: "POST" }),
      definitionList(dbId),
    ]);
    return normalizeProposal(raw.proposal ?? raw, migrations);
  },
  deleteProposal: (dbId: string, proposalId: string) =>
    api<void>(`/databases/${dbId}/proposals/${proposalId}`, { method: "DELETE" }),
};

export const jobApi = {
  list: async (dbId: string): Promise<JobsListResponse> => {
    const [raw, migrations] = await Promise.all([
      api<{ jobs: Any[]; lock_state?: JobsListResponse["lock_state"]; active_job_id?: string | null }>(
        `/databases/${dbId}/jobs`,
      ),
      definitionList(dbId),
    ]);
    return {
      jobs: (raw.jobs ?? []).map((row) => normalizeJob(row, migrations, [])),
      lock_state: raw.lock_state ?? null,
      active_job_id: raw.active_job_id ? String(raw.active_job_id) : null,
    };
  },
  detail: async (dbId: string, jobId: string): Promise<JobDetail> => {
    const [rawResponse, history, migrations] = await Promise.all([
      api<Any>(`/databases/${dbId}/jobs/${jobId}`),
      jobApi.history(dbId, false),
      definitionList(dbId),
    ]);
    const raw = rawResponse.job ?? rawResponse;
    const attempts = Array.isArray(rawResponse.attempts) && rawResponse.attempts.length > 0
      ? rawResponse.attempts.map((row: Any) => normalizeExecution(row, migrations))
      : history.executions.filter(
          (execution) => execution.root_execution_id === String(raw.root_execution_id),
        );
    const job = normalizeJob(raw, migrations, attempts);
    return {
      job,
      lock_state: rawResponse.lock_state ?? raw.lock_state ?? null,
      attempts,
      progress: {
        completed_batches: job.completed_batches,
        total_batches: job.total_batches,
        committed_rows: job.committed_rows,
        percent: job.total_batches ? Math.round((job.completed_batches / job.total_batches) * 100) : 0,
      },
    };
  },
  history: async (dbId: string, hideRollback: boolean): Promise<HistoryResponse> => {
    const [raw, migrations] = await Promise.all([
      api<{ executions: Any[] }>(`/databases/${dbId}/history${queryString({ hide_rollback: hideRollback })}`),
      definitionList(dbId),
    ]);
    return { executions: (raw.executions ?? []).map((row) => normalizeExecution(row, migrations)) };
  },
};

export const workerApi = {
  status: () => api<WorkersResponse>("/workers/status"),
};
