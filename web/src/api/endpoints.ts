import { api, queryString } from "./client";
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
  MigrationDefinition,
  Proposal,
  ProposalDetail,
  QueryResponse,
  SchemaResponse,
  User,
  WorkersResponse,
} from "./types";

export const authApi = {
  login: (username: string, password: string) =>
    api<AuthResponse>("/auth/login", { json: { username, password } }),
  logout: () => api<void>("/auth/logout", { method: "POST" }),
  me: () => api<AuthResponse>("/auth/me"),
};

export const adminApi = {
  users: () => api<{ users: User[] }>("/admin/users"),
  createUser: (input: { username: string; password: string; is_platform_admin: boolean }) =>
    api<User>("/admin/users", { json: input }),
  resetPassword: (userId: string, password: string) =>
    api<void>(`/admin/users/${userId}/password`, { json: { password } }),
  setActive: (userId: string, isActive: boolean) =>
    api<User>(`/admin/users/${userId}/active`, { json: { is_active: isActive } }),
  deleteUser: (userId: string) => api<void>(`/admin/users/${userId}`, { method: "DELETE" }),

  groups: () => api<{ groups: Group[] }>("/admin/groups"),
  createGroup: (input: { name: string; description: string }) =>
    api<Group>("/admin/groups", { json: input }),
  updateGroup: (groupId: string, input: { name: string; description: string }) =>
    api<Group>(`/admin/groups/${groupId}`, { method: "PUT", json: input }),
  setLeader: (groupId: string, leaderId: string | null) =>
    api<Group>(`/admin/groups/${groupId}/leader`, { method: "PUT", json: { leader_id: leaderId } }),
  setMembers: (groupId: string, userIds: string[]) =>
    api<Group>(`/admin/groups/${groupId}/members`, { method: "PUT", json: { user_ids: userIds } }),
  setAccess: (groupId: string, grants: Array<{ db_id: string; level: "view" | "edit" }>) =>
    api<Group>(`/admin/groups/${groupId}/access`, { method: "PUT", json: { grants } }),
  deleteGroup: (groupId: string) => api<void>(`/admin/groups/${groupId}`, { method: "DELETE" }),
  deactivateGroup: (groupId: string) =>
    api<void>(`/admin/groups/${groupId}/deactivate`, { method: "POST" }),

  databases: () => api<{ databases: Database[] }>("/admin/databases"),
  createDatabase: (input: {
    name: string;
    host: string;
    port: number;
    db_name: string;
    username: string;
    password: string;
    ssl_mode: string;
  }) => api<Database>("/admin/databases", { json: input }),
  updateDatabase: (
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
  ) => api<Database>(`/admin/databases/${dbId}`, { method: "PUT", json: input }),
  testDatabase: (dbId: string) =>
    api<BatchTestResult>(`/admin/databases/${dbId}/test`, { method: "POST" }),
  deleteDatabase: (dbId: string) =>
    api<void>(`/admin/databases/${dbId}`, { method: "DELETE" }),
};

export const databaseApi = {
  list: () => api<{ databases: Database[] }>("/databases"),
  overview: (dbId: string) => api<DatabaseOverview>(`/databases/${dbId}/overview`),
  schema: (dbId: string) => api<SchemaResponse>(`/databases/${dbId}/schema`),
  data: (dbId: string, params: { table: string; page: number; page_size: number; q?: string }) =>
    api<DataResponse>(`/databases/${dbId}/data${queryString(params)}`),
  query: (dbId: string, sql: string) =>
    api<QueryResponse>(`/databases/${dbId}/query`, { json: { sql } }),
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
}

export const migrationApi = {
  list: (dbId: string) =>
    api<{ migrations: MigrationDefinition[] }>(`/databases/${dbId}/migrations`),
  create: (dbId: string, input: MigrationInput) =>
    api<MigrationDefinition>(`/databases/${dbId}/migrations`, { json: input }),
  update: (dbId: string, migrationId: string, input: MigrationInput) =>
    api<MigrationDefinition>(`/databases/${dbId}/migrations/${migrationId}`, {
      method: "PUT",
      json: input,
    }),
  validate: (dbId: string, migrationId: string) =>
    api<DryRunResult>(`/databases/${dbId}/migrations/${migrationId}/validate`, { method: "POST" }),

  proposals: (dbId: string) =>
    api<{ proposals: Proposal[] }>(`/databases/${dbId}/proposals`),
  proposal: (dbId: string, proposalId: string) =>
    api<ProposalDetail>(`/databases/${dbId}/proposals/${proposalId}`),
  createProposal: (
    dbId: string,
    input: {
      migration_id: string;
      action_type: "apply" | "rollback" | "forward" | "batch_retry" | "batch_revert" | "resolve_uncertain";
      target_execution_id?: string | null;
      resolution?: "committed" | "not_committed" | null;
      reason?: string;
    },
  ) => api<Proposal>(`/databases/${dbId}/proposals`, { json: input }),
  approve: (dbId: string, proposalId: string, comment: string) =>
    api<Proposal>(`/databases/${dbId}/proposals/${proposalId}/approve`, { json: { comment } }),
  reject: (dbId: string, proposalId: string, comment: string) =>
    api<Proposal>(`/databases/${dbId}/proposals/${proposalId}/reject`, { json: { comment } }),
  withdraw: (dbId: string, proposalId: string) =>
    api<Proposal>(`/databases/${dbId}/proposals/${proposalId}/withdraw`, { method: "POST" }),
};

export const jobApi = {
  detail: (dbId: string, jobId: string) => api<JobDetail>(`/databases/${dbId}/jobs/${jobId}`),
  history: (dbId: string, hideRollback: boolean) =>
    api<HistoryResponse>(`/databases/${dbId}/history${queryString({ hide_rollback: hideRollback })}`),
};

export const workerApi = {
  status: () => api<WorkersResponse>("/workers/status"),
};
