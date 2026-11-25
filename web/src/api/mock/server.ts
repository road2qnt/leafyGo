import { applyHideRollback } from "../../lib/history";
import type {
  AccessLevel,
  ApprovalRow,
  ApprovalSummary,
  DataResponse,
  Database,
  DatabaseOverview,
  Execution,
  Group,
  Job,
  JobDetail,
  MigrationDefinition,
  Proposal,
  ProposalDetail,
  QueryResponse,
  SchemaTable,
  User,
} from "../types";
import { createSeedState, type MockState } from "./seed";

const STORAGE_KEY = "leafygo-mock-state-v1";
const ACTIVE_PROPOSAL_STATUSES = new Set(["pending_review", "approved", "queued", "running"]);
const HOLD_PROPOSAL_STATUSES = new Set(["queued", "running", "partial", "partially_reverted", "uncertain"]);

declare global {
  interface Window {
    __leafygoResetMock?: () => void;
  }
}

class MockError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

let state: MockState = loadState();
let installed = false;

function loadState(): MockState {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as MockState;
      if (parsed.version === 1) return parsed;
    }
  } catch {
    // fall through to a fresh seed
  }
  return createSeedState();
}

function persist() {
  try {
    sessionStorage.setItem(STORAGE_KEY, JSON.stringify(state));
  } catch {
    // demo only: storage may be unavailable in private mode
  }
}

function id(prefix: string): string {
  state.sequence += 1;
  return `${prefix}-${state.sequence}`;
}

const nowIso = () => new Date().toISOString();

function randomHex(length: number): string {
  let out = "";
  while (out.length < length) {
    out += Math.floor(Math.random() * 16).toString(16);
  }
  return out.slice(0, length);
}

function mockError(status: number, code: string, message: string): never {
  throw new MockError(status, code, message);
}

function currentUser(): User {
  const user = state.users.find((entry) => entry.id === state.currentUserId);
  if (!user || !user.is_active) {
    throw new MockError(401, "UNAUTHENTICATED", "Your session has ended. Sign in again.");
  }
  return user;
}

function requireAdmin(): User {
  const user = currentUser();
  if (!user.is_platform_admin) {
    mockError(403, "FORBIDDEN", "This action is for Platform Admins only.");
  }
  return user;
}

function leaderGroups(userId: string): Group[] {
  return state.groups.filter((group) => group.leader_id === userId);
}

function authPayload(user: User) {
  return {
    user,
    csrf_token: `mock-csrf-${user.id}`,
    leader_of_groups: leaderGroups(user.id).map((group) => ({ id: group.id, name: group.name })),
  };
}

function levelFor(userId: string, dbId: string): AccessLevel | null {
  const user = state.users.find((entry) => entry.id === userId);
  if (!user || user.is_platform_admin) return null;
  let level: AccessLevel | null = null;
  for (const group of state.groups) {
    if (!group.members.some((member) => member.id === userId)) continue;
    const grant = group.access.find((entry) => entry.db_id === dbId);
    if (!grant) continue;
    if (grant.level === "edit") return "edit";
    level = "view";
  }
  return level;
}

function requireDatabase(dbId: string) {
  const user = currentUser();
  const database = state.databases.find((entry) => entry.id === dbId);
  if (!database) mockError(404, "DATABASE_NOT_FOUND", "Database not found.");
  const level = levelFor(user.id, dbId);
  if (!level) mockError(403, "FORBIDDEN", "You do not have access to this database.");
  return { database, level, user };
}

function withLevel(database: Database): Database {
  const user = state.users.find((entry) => entry.id === state.currentUserId);
  return { ...database, level: user ? levelFor(user.id, database.id) ?? undefined : undefined };
}

function approvalSummary(rows: ApprovalRow[]): ApprovalSummary {
  const approved = rows.filter((row) => row.decision === "approved").length;
  const rejected = rows.filter((row) => row.decision === "rejected").length;
  return { required: rows.length, approved, rejected, pending: rows.length - approved - rejected };
}

function refreshProposal(proposal: Proposal): Proposal {
  const rows = state.approvals[proposal.id] ?? [];
  proposal.approval_summary = approvalSummary(rows);
  return proposal;
}

function proposalFor(proposalId: string): Proposal {
  const proposal = state.proposals.find((entry) => entry.id === proposalId);
  if (!proposal) mockError(404, "PROPOSAL_NOT_FOUND", "Proposal not found.");
  return proposal;
}

function migrationFor(migrationId: string): MigrationDefinition {
  const migration = state.migrations.find((entry) => entry.id === migrationId);
  if (!migration) mockError(404, "MIGRATION_NOT_FOUND", "Migration definition not found.");
  return migration;
}

function isPredecessorReady(dbId: string, migration: MigrationDefinition): boolean {
  const previous = state.migrations
    .filter(
      (candidate) =>
        candidate.db_id === dbId &&
        !candidate.is_superseded &&
        candidate.effective_seq < migration.effective_seq,
    )
    .sort((a, b) => b.effective_seq - a.effective_seq)[0];
  if (!previous) return true;

  const executions = state.executions
    .filter((execution) => execution.db_id === dbId && execution.migration_id === previous.id)
    .sort((a, b) => b.seq_order - a.seq_order);
  const latest = executions[0];
  if (latest) {
    if (latest.status === "success" && !["rollback", "batch_revert"].includes(latest.action_type)) {
      return true;
    }
    if (["rollback", "batch_revert"].includes(latest.action_type) && latest.status === "success") {
      return false;
    }
  }

  const proposal = state.proposals
    .filter((entry) => entry.migration_id === previous.id)
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at))[0];
  return proposal ? HOLD_PROPOSAL_STATUSES.has(proposal.status) : false;
}

function executionFor(proposal: Proposal, actionType: Proposal["action_type"]): Execution {
  const migration = migrationFor(proposal.migration_id);
  const totalBatches = migration.execution_mode === "batch" ? 6 : 1;
  return {
    execution_id: id("e"),
    root_execution_id: "",
    rollback_of_execution_id: proposal.target_execution_id,
    seq_order: 0,
    db_id: proposal.db_id,
    db_name_snapshot: state.databases.find((database) => database.id === proposal.db_id)?.name ?? "database",
    migration_id: migration.id,
    migration_title_snapshot: migration.title,
    migration_revision: migration.revision,
    effective_seq: migration.effective_seq,
    seq_version: migration.seq_version,
    action_type: actionType,
    status: "queued",
    executed_by_username: currentUser().username,
    started_at: null,
    finished_at: null,
    duration_ms: null,
    error_message: "",
    committed_batches: 0,
    total_batches: totalBatches,
    committed_rows: 0,
    is_superseded: false,
    superseded_by_title: null,
  };
}

function enqueue(proposal: Proposal) {
  const execution = executionFor(proposal, proposal.action_type);
  execution.root_execution_id = proposal.action_type === "apply" || proposal.action_type === "forward" ? execution.execution_id : proposal.target_execution_id ?? execution.execution_id;
  execution.seq_order = Math.max(0, ...state.executions.map((entry) => entry.seq_order)) + 1;
  state.executions.push(execution);

  const migration = migrationFor(proposal.migration_id);
  const totalBatches = migration.execution_mode === "batch" ? 6 : 1;
  const job: Job = {
    job_id: id("job"),
    queue_seq: Math.max(0, ...state.jobs.map((entry) => entry.queue_seq)) + 1,
    db_id: proposal.db_id,
    proposal_id: proposal.id,
    execution_id: execution.execution_id,
    root_execution_id: execution.root_execution_id,
    migration_id: migration.id,
    migration_title: migration.title,
    action_type: proposal.action_type,
    execution_mode: migration.execution_mode,
    status: "queued",
    retry_count: 0,
    max_retries: 3,
    last_cursor: null,
    scope_min_cursor: migration.execution_mode === "batch" ? 1 : null,
    scope_max_cursor: migration.execution_mode === "batch" ? 120_000 : null,
    total_batches: totalBatches,
    completed_batches: 0,
    committed_rows: 0,
    last_error: "",
    queued_at: nowIso(),
    started_at: null,
    finished_at: null,
    updated_at: nowIso(),
    worker_id: null,
    lock_state: null,
    trace_id: randomHex(32),
  };
  state.jobs.push(job);

  proposal.status = "queued";
  proposal.updated_at = nowIso();
  proposal.job_id = job.job_id;
  migration.latest_status = "queued";
  migration.active_proposal = { id: proposal.id, status: "queued", action_type: proposal.action_type };
  const database = state.databases.find((entry) => entry.id === proposal.db_id);
  if (database && !database.lock_state) database.active_job_id = job.job_id;
  return job;
}

/* ------------------------------------------------------------------ schemas */

function schemaFor(dbId: string, tableName: string): SchemaTable {
  const schema = state.schemas[dbId]?.find((table) => table.name === tableName);
  if (!schema) {
    mockError(422, "INVALID_BATCH_IDENTIFIER", `Table "${tableName}" was not found in the target catalog.`);
  }
  return schema;
}

const FIRST_NAMES = ["Ayu", "Bima", "Citra", "Dimas", "Eka", "Fajar", "Gita", "Hana", "Indra", "Joko"];
const CITIES = ["Jakarta", "Bandung", "Surabaya", "Yogyakarta", "Medan", "Makassar"];
const SEGMENTS = ["retail", "wholesale", "vip", "new"];
const REASONS = ["restock", "sale", "adjustment", "return"];
const STATUSES = ["active", "paused", "review"];

function valueFor(columnName: string, dataType: string, index: number): unknown {
  const n = index + 1;
  switch (dataType) {
    case "bigint":
    case "integer":
    case "smallint":
      if (columnName.endsWith("_id")) return (n % 18) + 1;
      if (columnName === "capacity") return 500 + (n % 20) * 100;
      if (columnName === "quantity" || columnName === "delta" || columnName === "orders") return (n % 12) - 4;
      if (columnName.includes("cents") || columnName.includes("price")) return (n % 9 + 1) * 12_500;
      if (columnName === "mismatches") return n % 4;
      return n;
    case "numeric":
      return Number(((n % 50) + 1) * 1.25).toFixed(2);
    case "boolean":
      return n % 3 !== 0;
    case "date":
      return new Date(Date.now() - n * 86_400_000).toISOString().slice(0, 10);
    case "timestamptz":
      return new Date(Date.now() - n * 3_600_000).toISOString();
    case "uuid":
      return `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
    case "tsvector":
      return null;
    case "jsonb":
      return { source: "mock", n };
    default:
      break;
  }
  switch (columnName) {
    case "email":
    case "contact_email":
      return `user${n}@example.com`;
    case "name":
    case "full_name":
      return `${FIRST_NAMES[n % FIRST_NAMES.length]} ${["Pratama", "Saputra", "Wijaya", "Lestari"][n % 4]}`;
    case "sku":
      return `SKU-${String(n).padStart(5, "0")}`;
    case "code":
      return `WH-${String(n).padStart(3, "0")}`;
    case "city":
      return CITIES[n % CITIES.length];
    case "segment":
      return SEGMENTS[n % SEGMENTS.length];
    case "reason":
      return REASONS[n % REASONS.length];
    case "status":
      return STATUSES[n % STATUSES.length];
    case "description":
      return `Demo product ${n} used by the mock data viewer.`;
    case "cost_center":
      return `CC-${100 + (n % 20)}`;
    case "dept":
    case "department":
      return ["engineering", "finance", "operations", "people"][n % 4];
    default:
      return `${columnName}_${n}`;
  }
}

function rowsForTable(dbId: string, tableName: string): { columns: SchemaTable["columns"]; rows: Array<Record<string, unknown>> } {
  const schema = schemaFor(dbId, tableName);
  const count = Math.min(60, Math.max(0, schema.row_estimate));
  const rows = Array.from({ length: count }, (_, index) => {
    const row: Record<string, unknown> = {};
    for (const column of schema.columns) {
      row[column.name] = valueFor(column.name, column.data_type, index);
    }
    return row;
  });
  return { columns: schema.columns, rows };
}

/* -------------------------------------------------------------------- jobs */

function tickJob(job: Job) {
  if (job.status !== "queued" && job.status !== "running") return job;
  const migration = state.migrations.find((entry) => entry.id === job.migration_id);
  if (job.status === "queued") {
    job.status = "running";
    job.started_at = nowIso();
    job.worker_id = "worker-1";
    job.lock_state = "running";
    const database = state.databases.find((entry) => entry.id === job.db_id);
    if (database) database.lock_state = "running";
  } else {
    const batchRows = migration?.batch_config?.batch_size ?? 4_200;
    job.completed_batches = Math.min(job.total_batches, job.completed_batches + 1);
    job.committed_rows += batchRows;
    job.last_cursor = Math.round(((job.scope_min_cursor ?? 1) + ((job.scope_max_cursor ?? job.total_batches * batchRows) * job.completed_batches) / Math.max(1, job.total_batches)));
    if (job.completed_batches >= job.total_batches) {
      job.status = "success";
      job.finished_at = nowIso();
      job.lock_state = null;
      job.last_error = "";
      const execution = state.executions.find((entry) => entry.execution_id === job.execution_id);
      if (execution) {
        execution.status = "success";
        execution.finished_at = job.finished_at;
        execution.started_at = job.started_at;
        execution.committed_batches = job.completed_batches;
        execution.committed_rows = job.committed_rows;
        execution.duration_ms = 42_000;
      }
      const proposal = state.proposals.find((entry) => entry.id === job.proposal_id);
      if (proposal) {
        proposal.status = "completed";
        proposal.updated_at = nowIso();
      }
      if (migration) {
        migration.latest_status = "success";
        migration.active_proposal = null;
      }
      const database = state.databases.find((entry) => entry.id === job.db_id);
      if (database) {
        database.lock_state = null;
        database.active_job_id = null;
      }
    }
  }
  job.updated_at = nowIso();
  return job;
}

/* ------------------------------------------------------------------ helpers */

function databaseOverview(dbId: string): DatabaseOverview {
  const { database, level } = requireDatabase(dbId);
  const tables = state.schemas[dbId] ?? [];
  const activeJob = state.jobs.find((job) => job.job_id === database.active_job_id) ?? null;
  const recent = state.executions
    .filter((execution) => execution.db_id === dbId)
    .sort((a, b) => b.seq_order - a.seq_order)
    .slice(0, 6);
  return {
    database: { ...database, level },
    level,
    lock_state: database.lock_state ?? null,
    active_job: activeJob,
    stats: {
      table_count: tables.length,
      row_estimate: tables.reduce((sum, table) => sum + table.row_estimate, 0),
      total_size_bytes: tables.reduce((sum, table) => sum + table.size_bytes, 0),
    },
    recent_history: recent,
  };
}

function proposalDetail(proposalId: string): ProposalDetail {
  const proposal = proposalFor(proposalId);
  requireDatabase(proposal.db_id);
  const migration = migrationFor(proposal.migration_id);
  const rows = state.approvals[proposal.id] ?? [];
  const user = currentUser();
  return {
    proposal: refreshProposal(proposal),
    migration,
    approvers: rows,
    can_approve: rows.some((row) => row.leader_id === user.id && row.decision === null),
    can_withdraw: proposal.status === "pending_review" && proposal.proposed_by_username === user.username,
  };
}

function buildQueryResponse(dbId: string, sql: string): QueryResponse {
  const text = sql.trim().replace(/;$/, "");
  if (!text) mockError(422, "EMPTY_QUERY", "Enter a SQL statement first.");
  if (/^(insert|update|delete|create|alter|drop|truncate|grant|revoke)\b/i.test(text)) {
    mockError(422, "READ_ONLY_CONSOLE", "The query console is read-only.");
  }
  if (text.includes("-- error")) {
    mockError(422, "QUERY_FAILED", 'relation "missing_table" does not exist (SQLSTATE 42P01)');
  }
  const select = text.match(/^select\s+\*\s+from\s+([a-zA-Z0-9_."]+)(?:\s+limit\s+(\d+))?/i);
  if (select) {
    const tableName = select[1].replace(/"/g, "").split(".").pop() ?? "";
    const limit = Math.min(Number(select[2] ?? 100), 500);
    const { columns, rows } = rowsForTable(dbId, tableName);
    return {
      columns: columns.map((column) => ({ name: column.name, data_type: column.data_type })),
      rows: rows.slice(0, limit),
      row_count: Math.min(rows.length, limit),
      duration_ms: 38,
      truncated: rows.length > limit,
      notices: [],
    };
  }
  const count = text.match(/^select\s+count\(\*\)\s+from\s+([a-zA-Z0-9_."]+)/i);
  if (count) {
    const tableName = count[1].replace(/"/g, "").split(".").pop() ?? "";
    const schema = schemaFor(dbId, tableName);
    return {
      columns: [{ name: "count", data_type: "bigint" }],
      rows: [{ count: schema.row_estimate }],
      row_count: 1,
      duration_ms: 12,
      truncated: false,
      notices: [],
    };
  }
  return {
    columns: [{ name: "result", data_type: "text" }],
    rows: [{ result: "Mock console accepted the statement. No rows were returned." }],
    row_count: 1,
    duration_ms: 24,
    truncated: false,
    notices: ["Mock mode does not execute real SQL against the target database."],
  };
}

/* ------------------------------------------------------------------ routing */

interface MockResult {
  status: number;
  body?: unknown;
}

function ok(body?: unknown, status = 200): MockResult {
  return { status, body };
}

function noContent(): MockResult {
  return { status: 204 };
}

async function handle(method: string, path: string, search: URLSearchParams, body: Record<string, any>): Promise<MockResult> {
  const parts = path.split("/").filter(Boolean);
  const head = parts[0] ?? "";

  if (head === "auth") {
    if (parts[1] === "login" && method === "POST") {
      const username = String(body.username ?? "").trim();
      const password = String(body.password ?? "");
      const user = state.users.find((entry) => entry.username === username);
      if (!user || password !== (user.is_platform_admin ? "admin12345" : "password123")) {
        mockError(401, "INVALID_CREDENTIALS", "Incorrect username or password.");
      }
      if (!user.is_active) mockError(403, "ACCOUNT_INACTIVE", "This account is deactivated.");
      state.currentUserId = user.id;
      return ok(authPayload(user));
    }
    if (parts[1] === "logout" && method === "POST") {
      state.currentUserId = null;
      return noContent();
    }
    if (parts[1] === "me" && method === "GET") {
      return ok(authPayload(currentUser()));
    }
    mockError(404, "NOT_FOUND", "Auth route not found.");
  }

  if (head === "admin") {
    requireAdmin();
    const section = parts[1];
    if (section === "users") {
      if (parts.length === 2 && method === "GET") return ok({ users: state.users });
      if (parts.length === 2 && method === "POST") {
        const username = String(body.username ?? "").trim();
        const password = String(body.password ?? "");
        if (!/^[a-zA-Z0-9._-]{3,32}$/.test(username)) {
          mockError(422, "INVALID_USERNAME", "Use 3–32 letters, digits, dots, underscores, or dashes.");
        }
        if (password.length < 8) mockError(422, "WEAK_PASSWORD", "Password must be at least 8 characters.");
        if (state.users.some((entry) => entry.username.toLowerCase() === username.toLowerCase())) {
          mockError(409, "USERNAME_TAKEN", "That username already exists.");
        }
        const user: User = {
          id: id("u"),
          username,
          is_platform_admin: Boolean(body.is_platform_admin),
          is_active: true,
          created_at: nowIso(),
        };
        state.users.push(user);
        return ok(user, 201);
      }
      const userId = parts[2];
      if (parts[3] === "password" && method === "POST") {
        if (!state.users.some((entry) => entry.id === userId)) mockError(404, "USER_NOT_FOUND", "User not found.");
        if (String(body.password ?? "").length < 8) mockError(422, "WEAK_PASSWORD", "Password must be at least 8 characters.");
        return noContent();
      }
      if (parts[3] === "active" && method === "POST") {
        const user = state.users.find((entry) => entry.id === userId);
        if (!user) mockError(404, "USER_NOT_FOUND", "User not found.");
        if (user.id === state.currentUserId && !body.is_active) {
          mockError(422, "CANNOT_DEACTIVATE_SELF", "You cannot deactivate your own account.");
        }
        user.is_active = Boolean(body.is_active);
        return ok(user);
      }
      if (parts.length === 3 && method === "DELETE") {
        const user = state.users.find((entry) => entry.id === userId);
        if (!user) mockError(404, "USER_NOT_FOUND", "User not found.");
        const hasHistory =
          state.executions.some((execution) => execution.executed_by_username === user.username) ||
          state.proposals.some((proposal) => proposal.proposed_by_username === user.username) ||
          Object.values(state.approvals).some((rows) => rows.some((row) => row.leader_id === user.id));
        if (hasHistory) mockError(409, "USER_HAS_HISTORY", "This user appears in approval or execution history.");
        state.users = state.users.filter((entry) => entry.id !== user.id);
        for (const group of state.groups) {
          group.members = group.members.filter((member) => member.id !== user.id);
          if (group.leader_id === user.id) group.leader_id = null;
        }
        return noContent();
      }
      mockError(404, "NOT_FOUND", "User route not found.");
    }

    if (section === "groups") {
      if (parts.length === 2 && method === "GET") return ok({ groups: state.groups });
      if (parts.length === 2 && method === "POST") {
        const name = String(body.name ?? "").trim();
        if (name.length < 2) mockError(422, "INVALID_GROUP", "Group name is required.");
        if (state.groups.some((entry) => entry.name.toLowerCase() === name.toLowerCase())) {
          mockError(409, "GROUP_NAME_TAKEN", "A group with that name already exists.");
        }
        const group: Group = {
          id: id("g"),
          name,
          description: String(body.description ?? ""),
          leader_id: null,
          leader_username: null,
          members: [],
          access: [],
          created_at: nowIso(),
        };
        state.groups.push(group);
        return ok(group, 201);
      }
      const group = state.groups.find((entry) => entry.id === parts[2]);
      if (!group) mockError(404, "GROUP_NOT_FOUND", "Group not found.");
      if (parts.length === 3 && method === "PUT") {
        if (String(body.name ?? "").trim()) group.name = String(body.name).trim();
        group.description = String(body.description ?? "");
        return ok(group);
      }
      if (parts.length === 3 && method === "DELETE") {
        const hasHistory = Object.values(state.approvals).some((rows) => rows.some((row) => row.group_id === group.id));
        if (hasHistory) mockError(409, "GROUP_HAS_HISTORY", "This group appears in approval history.");
        state.groups = state.groups.filter((entry) => entry.id !== group.id);
        return noContent();
      }
      if (parts[3] === "leader" && method === "PUT") {
        const leaderId = body.leader_id as string | null;
        if (leaderId) {
          const leader = state.users.find((entry) => entry.id === leaderId && entry.is_active);
          if (!leader) mockError(422, "INVALID_LEADER", "Choose an active user as the group leader.");
          if (!group.members.some((member) => member.id === leaderId)) {
            mockError(422, "LEADER_NOT_MEMBER", "The leader must be a member of the group.");
          }
          group.leader_id = leader.id;
          group.leader_username = leader.username;
        } else {
          group.leader_id = null;
          group.leader_username = null;
        }
        return ok(group);
      }
      if (parts[3] === "members" && method === "PUT") {
        const ids = (body.user_ids as string[]) ?? [];
        const members = ids
          .map((userId) => state.users.find((entry) => entry.id === userId))
          .filter((entry): entry is User => Boolean(entry))
          .map((entry) => ({ id: entry.id, username: entry.username }));
        group.members = members;
        if (group.leader_id && !members.some((member) => member.id === group.leader_id)) {
          group.leader_id = null;
          group.leader_username = null;
        }
        return ok(group);
      }
      if (parts[3] === "access" && method === "PUT") {
        const grants = (body.grants as Array<{ db_id: string; level: AccessLevel }>) ?? [];
        group.access = grants
          .map((grant) => {
            const database = state.databases.find((entry) => entry.id === grant.db_id);
            if (!database) return null;
            return { db_id: database.id, db_name: database.name, level: grant.level === "edit" ? ("edit" as const) : ("view" as const) };
          })
          .filter((grant): grant is NonNullable<typeof grant> => Boolean(grant));
        return ok(group);
      }
      if (parts[3] === "deactivate" && method === "POST") {
        group.access = [];
        group.leader_id = null;
        group.leader_username = null;
        group.description = `${group.description} (deactivated)`.trim();
        return noContent();
      }
      mockError(404, "NOT_FOUND", "Group route not found.");
    }

    if (section === "databases") {
      if (parts.length === 2 && method === "GET") return ok({ databases: state.databases });
      if (parts.length === 2 && method === "POST") {
        const name = String(body.name ?? "").trim();
        if (!name) mockError(422, "INVALID_DATABASE", "Database display name is required.");
        if (state.databases.some((entry) => entry.name.toLowerCase() === name.toLowerCase())) {
          mockError(409, "DATABASE_NAME_TAKEN", "A database with that name already exists.");
        }
        const database: Database = {
          id: id("db"),
          name,
          host: String(body.host ?? "localhost"),
          port: Number(body.port ?? 5432),
          db_name: String(body.db_name ?? ""),
          username: String(body.username ?? ""),
          ssl_mode: String(body.ssl_mode ?? "disable"),
          created_at: nowIso(),
          updated_at: nowIso(),
          lock_state: null,
          active_job_id: null,
        };
        state.databases.push(database);
        state.schemas[database.id] = [];
        return ok(database, 201);
      }
      const database = state.databases.find((entry) => entry.id === parts[2]);
      if (!database) mockError(404, "DATABASE_NOT_FOUND", "Database not found.");
      if (parts.length === 3 && method === "PUT") {
        database.name = String(body.name ?? database.name).trim() || database.name;
        database.host = String(body.host ?? database.host);
        database.port = Number(body.port ?? database.port);
        database.db_name = String(body.db_name ?? database.db_name);
        database.username = String(body.username ?? database.username);
        database.ssl_mode = String(body.ssl_mode ?? database.ssl_mode);
        database.updated_at = nowIso();
        return ok(database);
      }
      if (parts.length === 3 && method === "DELETE") {
        const activeWork =
          Boolean(database.lock_state) ||
          state.jobs.some(
            (job) =>
              job.db_id === database.id &&
              ["queued", "running", "partial", "partially_reverted", "uncertain"].includes(job.status),
          );
        if (activeWork) {
          mockError(409, "DB_HAS_ACTIVE_WORK", "This database still has a lock or active job.");
        }
        // Soft delete for databases with history (the real backend keeps the row
        // with deleted_at); the mock simply removes it from active lists.
        state.databases = state.databases.filter((entry) => entry.id !== database.id);
        delete state.schemas[database.id];
        for (const group of state.groups) {
          group.access = group.access.filter((grant) => grant.db_id !== database.id);
        }
        return noContent();
      }
      if (parts[3] === "test" && method === "POST") {
        if (database.host.includes("bad") || database.db_name.includes("missing")) {
          return ok({ ok: false, error: "dial tcp: connection refused", latency_ms: 12 });
        }
        return ok({ ok: true, latency_ms: 23 });
      }
      mockError(404, "NOT_FOUND", "Database route not found.");
    }

    mockError(404, "NOT_FOUND", "Admin route not found.");
  }

  if (head === "workers" && parts[1] === "status" && method === "GET") {
    currentUser();
    return ok({ workers: state.workers });
  }

  if (head === "databases") {
    if (parts.length === 1 && method === "GET") {
      const user = currentUser();
      if (user.is_platform_admin) return ok({ databases: [] });
      const visible = state.databases
        .filter((database) => levelFor(user.id, database.id))
        .map(withLevel);
      return ok({ databases: visible });
    }

    const dbId = parts[1];
    const section = parts[2] ?? "";
    if (section === "overview" && method === "GET") return ok(databaseOverview(dbId));
    if (section === "schema" && method === "GET") {
      requireDatabase(dbId);
      return ok({ tables: state.schemas[dbId] ?? [] });
    }
    if (section === "data" && method === "GET") {
      requireDatabase(dbId);
      const tableName = search.get("table") ?? state.schemas[dbId]?.[0]?.name ?? "";
      const page = Math.max(1, Number(search.get("page") ?? 1));
      const pageSize = Math.min(100, Math.max(5, Number(search.get("page_size") ?? 20)));
      const query = (search.get("q") ?? "").toLowerCase();
      const { columns, rows } = rowsForTable(dbId, tableName);
      const filtered = query ? rows.filter((row) => JSON.stringify(row).toLowerCase().includes(query)) : rows;
      const totalPages = Math.max(1, Math.ceil(filtered.length / pageSize));
      const safePage = Math.min(page, totalPages);
      const body: DataResponse = {
        table: tableName,
        columns: columns.map((column) => ({ name: column.name, data_type: column.data_type })),
        rows: filtered.slice((safePage - 1) * pageSize, safePage * pageSize),
        page: safePage,
        page_size: pageSize,
        total_rows: filtered.length,
        total_pages: totalPages,
        truncated: false,
      };
      return ok(body);
    }
    if (section === "query" && method === "POST") {
      requireDatabase(dbId);
      return ok(buildQueryResponse(dbId, String(body.sql ?? "")));
    }
    if (section === "query" && parts[3] === "explain" && method === "POST") {
      requireDatabase(dbId);
      const sql = String(body.sql ?? "");
      if (!sql.trim()) mockError(422, "EMPTY_QUERY", "Enter a SQL statement first.");
      if (sql.includes("-- error")) mockError(422, "QUERY_FAILED", "relation \"missing_table\" does not exist (SQLSTATE 42P01)");
      return ok({
        plan: `Limit  (cost=0.42..12.80 rows=50 width=64)\n  ->  Index Scan using ${search.get("table") ?? "products"}_pkey on products  (cost=0.42..12.80 rows=50 width=64)\n        Filter: (is_active = true)\nPlanning Time: 0.214 ms\nExecution Time: 0.038 ms`,
        duration_ms: 9,
      });
    }
    if (section === "migrations") {
      const database = requireDatabase(dbId);
      if (parts.length === 3 && method === "GET") {
        return ok({ migrations: state.migrations.filter((migration) => migration.db_id === dbId) });
      }
      if (parts.length === 3 && method === "POST") {
        if (database.level !== "edit") mockError(403, "FORBIDDEN", "Edit access is required.");
        const title = String(body.title ?? "").trim();
        if (!title) mockError(422, "INVALID_MIGRATION", "Title is required.");
        const seqVersion = Math.max(0, ...state.migrations.filter((entry) => entry.db_id === dbId).map((entry) => entry.seq_version)) + 1;
        const replacesId = (body.replaces_migration_id as string | null) ?? null;
        const replaced = replacesId ? migrationFor(replacesId) : null;
        const migration: MigrationDefinition = {
          id: id("mig"),
          db_id: dbId,
          seq_version: seqVersion,
          effective_seq: replaced?.effective_seq ?? seqVersion,
          revision: 1,
          title,
          description: String(body.description ?? ""),
          sql_up: String(body.sql_up ?? ""),
          sql_down: String(body.sql_down ?? ""),
          is_irreversible: Boolean(body.is_irreversible),
          irreversible_reason: String(body.irreversible_reason ?? ""),
          execution_mode: body.execution_mode === "batch" ? "batch" : "normal",
          batch_config: body.batch_config ?? null,
          is_immutable: false,
          replaces_migration_id: replacesId,
          replaces_title: replaced?.title ?? null,
          is_superseded: false,
          superseded_by_migration_id: null,
          superseded_by_title: null,
          created_by_username: database.user.username,
          updated_at: nowIso(),
          active_proposal: null,
          latest_status: null,
        };
        state.migrations.push(migration);
        return ok(migration, 201);
      }
      const migrationId = parts[3];
      const migration = migrationFor(migrationId);
      if (migration.db_id !== dbId) mockError(404, "MIGRATION_NOT_FOUND", "Migration not found in this database.");
      if (parts[4] === "validate" && method === "POST") {
        requireDatabase(dbId);
        const combined = `${migration.sql_up}\n${migration.sql_down}`;
        if (combined.includes("FAIL")) {
          mockError(422, "INVALID_MIGRATION_SQL", 'syntax error at or near "FAIL" (SQLSTATE 42601)');
        }
        if (combined.includes("SLOW")) {
          return ok({
            dry_run_status: "inconclusive",
            message: "Dry-run hit statement_timeout=3s; treat as inconclusive and continue review.",
            sqlstate: "57014",
            duration_ms: 3000,
          });
        }
        return ok({ dry_run_status: "passed", duration_ms: 41 });
      }
      if (parts.length === 4 && method === "PUT") {
        if (migration.is_immutable) mockError(409, "MIGRATION_IMMUTABLE", "This migration is permanently immutable.");
        if (migration.active_proposal && ACTIVE_PROPOSAL_STATUSES.has(migration.active_proposal.status)) {
          mockError(409, "DUPLICATE_ACTIVE_PROPOSAL", "An active proposal already locks this definition.");
        }
        migration.title = String(body.title ?? migration.title);
        migration.description = String(body.description ?? migration.description);
        migration.sql_up = String(body.sql_up ?? migration.sql_up);
        migration.sql_down = String(body.sql_down ?? migration.sql_down);
        migration.is_irreversible = Boolean(body.is_irreversible);
        migration.irreversible_reason = String(body.irreversible_reason ?? "");
        migration.execution_mode = body.execution_mode === "batch" ? "batch" : "normal";
        migration.batch_config = body.batch_config ?? null;
        migration.revision += 1;
        migration.updated_at = nowIso();
        return ok(migration);
      }
      mockError(404, "NOT_FOUND", "Migration route not found.");
    }

    if (section === "proposals") {
      const database = requireDatabase(dbId);
      if (parts.length === 3 && method === "GET") {
        return ok({
          proposals: state.proposals
            .filter((proposal) => proposal.db_id === dbId)
            .map(refreshProposal)
            .sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
        });
      }
      if (parts.length === 3 && method === "POST") {
        if (database.level !== "edit") mockError(403, "FORBIDDEN", "Edit access is required.");
        const migration = migrationFor(String(body.migration_id ?? ""));
        if (migration.db_id !== dbId) mockError(404, "MIGRATION_NOT_FOUND", "Migration not found.");
        if (state.proposals.some((proposal) => proposal.migration_id === migration.id && ACTIVE_PROPOSAL_STATUSES.has(proposal.status))) {
          mockError(409, "DUPLICATE_ACTIVE_PROPOSAL", "This migration already has an active proposal.");
        }
        const action = String(body.action_type ?? "apply") as Proposal["action_type"];
        if (action === "resolve_uncertain") {
          if (!body.target_execution_id || !["committed", "not_committed"].includes(String(body.resolution)) || String(body.reason ?? "").trim().length === 0) {
            mockError(422, "RESOLVE_UNCERTAIN_INVALID", "Choose a resolution and write a reason.");
          }
        }
        const dryRun = `${migration.sql_up}\n${migration.sql_down}`.includes("SLOW") ? "inconclusive" : "passed";
        const groupsWithAccess = state.groups.filter((group) => group.access.some((grant) => grant.db_id === dbId));
        if (groupsWithAccess.length === 0) {
          mockError(422, "NO_ACCESS_GROUPS", "No active group has access to this database.");
        }
        const proposal: Proposal = {
          id: id("p"),
          db_id: dbId,
          migration_id: migration.id,
          migration_title: migration.title,
          migration_revision: migration.revision,
          effective_seq: migration.effective_seq,
          action_type: action,
          target_execution_id: (body.target_execution_id as string | null) ?? null,
          resolution: (body.resolution as Proposal["resolution"]) ?? null,
          reason: String(body.reason ?? ""),
          status: "pending_review",
          proposed_by_username: database.user.username,
          created_at: nowIso(),
          updated_at: nowIso(),
          approval_summary: { required: groupsWithAccess.length, approved: 0, rejected: 0, pending: groupsWithAccess.length },
          replaces_title: migration.replaces_title ?? null,
          dry_run_status: dryRun,
        };
        state.proposals.push(proposal);
        state.approvals[proposal.id] = groupsWithAccess.map((group) => {
          const leader = state.users.find((entry) => entry.id === group.leader_id);
          const grant = group.access.find((entry) => entry.db_id === dbId);
          return {
            group_id: group.id,
            group_name: group.name,
            leader_id: leader?.id ?? "",
            leader_username: leader?.username ?? "unassigned",
            access_level: grant?.level ?? "view",
            decision: null,
            comment: "",
            decided_at: null,
          };
        });
        migration.active_proposal = { id: proposal.id, status: "pending_review", action_type: action };
        migration.latest_status = "pending_review";
        return ok(proposal, 201);
      }
      const proposal = proposalFor(parts[3]);
      if (proposal.db_id !== dbId) mockError(404, "PROPOSAL_NOT_FOUND", "Proposal not found.");
      if (parts.length === 4 && method === "GET") {
        return ok(proposalDetail(proposal.id));
      }
      if (parts[4] === "approve" && method === "POST") {
        const rows = state.approvals[proposal.id] ?? [];
        const row = rows.find((entry) => entry.leader_id === database.user.id);
        if (!row) mockError(403, "FORBIDDEN_NOT_GROUP_LEADER", "You are not the leader of a required group.");
        if (row.decision) mockError(409, "ALREADY_DECIDED", "Your group already decided on this proposal.");
        if (proposal.status !== "pending_review") mockError(409, "INVALID_PROPOSAL_STATE", "This proposal is not awaiting review.");
        row.decision = "approved";
        row.comment = String(body.comment ?? "");
        row.decided_at = nowIso();
        refreshProposal(proposal);
        if (proposal.approval_summary.pending === 0 && proposal.approval_summary.rejected === 0) {
          const migration = migrationFor(proposal.migration_id);
          if (!isPredecessorReady(dbId, migration)) {
            row.decision = null;
            row.comment = "";
            row.decided_at = null;
            refreshProposal(proposal);
            mockError(409, "PREDECESSOR_NOT_READY", "The predecessor slot must be Active or queued before this proposal can queue.");
          }
          enqueue(proposal);
        }
        proposal.updated_at = nowIso();
        return ok(refreshProposal(proposal));
      }
      if (parts[4] === "reject" && method === "POST") {
        const rows = state.approvals[proposal.id] ?? [];
        const row = rows.find((entry) => entry.leader_id === database.user.id);
        if (!row) mockError(403, "FORBIDDEN_NOT_GROUP_LEADER", "You are not the leader of a required group.");
        if (row.decision) mockError(409, "ALREADY_DECIDED", "Your group already decided on this proposal.");
        row.decision = "rejected";
        row.comment = String(body.comment ?? "");
        row.decided_at = nowIso();
        proposal.status = "rejected";
        proposal.updated_at = nowIso();
        refreshProposal(proposal);
        const migration = migrationFor(proposal.migration_id);
        migration.latest_status = "rejected";
        migration.active_proposal = null;
        return ok(proposal);
      }
      if (parts[4] === "withdraw" && method === "POST") {
        if (proposal.status !== "pending_review") mockError(409, "INVALID_PROPOSAL_STATE", "Only pending proposals can be withdrawn.");
        proposal.status = "withdrawn";
        proposal.updated_at = nowIso();
        for (const row of state.approvals[proposal.id] ?? []) {
          row.decision = null;
          row.comment = "";
          row.decided_at = null;
        }
        refreshProposal(proposal);
        const migration = migrationFor(proposal.migration_id);
        migration.latest_status = "withdrawn";
        migration.active_proposal = null;
        return ok(proposal);
      }
      mockError(404, "NOT_FOUND", "Proposal route not found.");
    }

    if (section === "jobs") {
      requireDatabase(dbId);
      const job = state.jobs.find((entry) => entry.job_id === parts[3] && entry.db_id === dbId);
      if (!job) mockError(404, "JOB_NOT_FOUND", "Job not found.");
      if (method === "GET") {
        tickJob(job);
        const attempts = state.executions
          .filter((execution) => execution.root_execution_id === job.root_execution_id)
          .sort((a, b) => b.seq_order - a.seq_order);
        const detail: JobDetail = {
          job,
          lock_state: job.lock_state ?? (job.status === "partial" ? "partial_hold" : job.status === "uncertain" ? "uncertain_hold" : null),
          attempts,
          progress: {
            completed_batches: job.completed_batches,
            total_batches: job.total_batches,
            committed_rows: job.committed_rows,
            percent: job.total_batches ? Math.round((job.completed_batches / job.total_batches) * 100) : 0,
          },
        };
        return ok(detail);
      }
      mockError(404, "NOT_FOUND", "Job route not found.");
    }

    if (section === "history" && method === "GET") {
      requireDatabase(dbId);
      let executions = state.executions
        .filter((execution) => execution.db_id === dbId)
        .sort((a, b) => b.seq_order - a.seq_order);
      if (search.get("hide_rollback") === "true") {
        executions = applyHideRollback(executions, true);
      }
      return ok({ executions });
    }
  }

  mockError(404, "NOT_FOUND", `No mock route for ${method} ${path}.`);
}

/* ------------------------------------------------------------------ install */

export function installMockApi() {
  if (installed) return;
  installed = true;
  const originalFetch = window.fetch.bind(window);

  window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const rawUrl =
      typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    const url = new URL(rawUrl, window.location.origin);

    if (!url.pathname.startsWith("/api/v1/")) {
      return originalFetch(input, init);
    }

    const method = (init?.method ?? (input instanceof Request ? input.method : "GET")).toUpperCase();
    const path = url.pathname.replace(/^\/api\/v1/, "");
    let body: Record<string, any> = {};
    if (typeof init?.body === "string" && init.body.length > 0) {
      try {
        body = JSON.parse(init.body) as Record<string, any>;
      } catch {
        body = {};
      }
    }

    await new Promise((resolve) => window.setTimeout(resolve, 70 + Math.random() * 160));

    try {
      const result = await handle(method, path, url.searchParams, body);
      persist();
      if (result.status === 204) {
        return new Response(null, { status: 204 });
      }
      return new Response(JSON.stringify(result.body ?? null), {
        status: result.status,
        headers: { "Content-Type": "application/json" },
      });
    } catch (error) {
      persist();
      if (error instanceof MockError) {
        return new Response(
          JSON.stringify({
            error: {
              code: error.code,
              message: error.message,
              correlation_id: `req-${randomHex(12)}`,
              trace_id: randomHex(32),
            },
          }),
          { status: error.status, headers: { "Content-Type": "application/json" } },
        );
      }
      return new Response(
        JSON.stringify({
          error: {
            code: "MOCK_INTERNAL",
            message: error instanceof Error ? error.message : "Mock server error.",
            correlation_id: `req-${randomHex(12)}`,
            trace_id: randomHex(32),
          },
        }),
        { status: 500, headers: { "Content-Type": "application/json" } },
      );
    }
  };

  window.__leafygoResetMock = () => {
    sessionStorage.removeItem(STORAGE_KEY);
    state = createSeedState();
    window.location.reload();
  };
}
