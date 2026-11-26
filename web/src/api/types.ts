export type AccessLevel = "view" | "edit";

export type LockState =
  | "running"
  | "partial_hold"
  | "partially_reverted_hold"
  | "uncertain_hold";

export type ExecutionMode = "normal" | "batch";

export type ActionType =
  | "apply"
  | "rollback"
  | "forward"
  | "batch_retry"
  | "batch_revert"
  | "resolve_uncertain";

export type JobStatus =
  | "queued"
  | "running"
  | "partial"
  | "partially_reverted"
  | "success"
  | "failed"
  | "uncertain";

export type ProposalStatus =
  | "draft"
  | "pending_review"
  | "withdrawn"
  | "approved"
  | "rejected"
  | "queued"
  | "running"
  | "partial"
  | "partially_reverted"
  | "uncertain"
  | "completed"
  | "failed";

export interface UserRef {
  id: string;
  username: string;
}

export interface User {
  id: string;
  username: string;
  is_platform_admin: boolean;
  is_active: boolean;
  created_at: string;
}

export interface GroupAccessGrant {
  db_id: string;
  db_name: string;
  level: AccessLevel;
}

export interface Group {
  id: string;
  name: string;
  description: string;
  leader_id: string | null;
  leader_username: string | null;
  members: UserRef[];
  access: GroupAccessGrant[];
  created_at: string;
}

export interface Database {
  id: string;
  name: string;
  host: string;
  port: number;
  db_name: string;
  username: string;
  ssl_mode: string;
  created_at: string;
  updated_at: string;
  level?: AccessLevel;
  lock_state?: LockState | null;
  active_job_id?: string | null;
}

export interface AuthResponse {
  user: User;
  csrf_token: string;
  leader_of_groups: Array<{ id: string; name: string }>;
}

export interface DatabaseOverview {
  database: Database;
  level: AccessLevel;
  lock_state: LockState | null;
  active_job: Job | null;
  active_job_id?: string | null;
  stats: {
    table_count: number;
    row_estimate: number;
    total_size_bytes: number;
  };
  recent_history: Execution[];
}

export interface SchemaColumn {
  name: string;
  data_type: string;
  nullable: boolean;
  default_value: string | null;
  is_primary_key: boolean;
}

export interface SchemaTable {
  schema: string;
  name: string;
  row_estimate: number;
  size_bytes: number;
  columns: SchemaColumn[];
}

export interface SchemaResponse {
  tables: SchemaTable[];
}

export interface ResultColumn {
  name: string;
  data_type?: string;
}

export interface DataResponse {
  table: string;
  columns: ResultColumn[];
  rows: Array<Record<string, unknown>>;
  page: number;
  page_size: number;
  total_rows: number;
  total_pages: number;
  truncated: boolean;
}

export interface QueryResponse {
  columns: ResultColumn[];
  rows: Array<Record<string, unknown>>;
  row_count: number;
  duration_ms: number;
  truncated: boolean;
  notices: string[];
}

export interface ExplainResponse {
  plan: string;
  duration_ms: number;
}

export interface BatchConfig {
  table_name: string;
  cursor_col: string;
  batch_size: number;
  updated_at_col?: string | null;
}

export interface ProposalSummary {
  id: string;
  status: ProposalStatus;
  action_type: ActionType;
}

export interface MigrationDefinition {
  id: string;
  db_id: string;
  seq_version: number;
  effective_seq: number;
  revision: number;
  title: string;
  description: string;
  sql_up: string;
  sql_down: string;
  is_irreversible: boolean;
  irreversible_reason: string;
  execution_mode: ExecutionMode;
  batch_config: BatchConfig | null;
  is_immutable: boolean;
  replaces_migration_id: string | null;
  replaces_title?: string | null;
  is_superseded: boolean;
  superseded_by_migration_id?: string | null;
  superseded_by_title?: string | null;
  created_by_username?: string | null;
  updated_at: string;
  active_proposal?: ProposalSummary | null;
  latest_status?: JobStatus | ProposalStatus | null;
}

export interface DryRunResult {
  dry_run_status: "passed" | "inconclusive" | "failed";
  message?: string;
  sqlstate?: string;
  duration_ms?: number;
}

export interface ApprovalRow {
  group_id: string;
  group_name: string;
  leader_id: string;
  leader_username: string;
  access_level: AccessLevel;
  decision: "approved" | "rejected" | null;
  comment: string;
  decided_at: string | null;
}

export interface ApprovalSummary {
  required: number;
  approved: number;
  rejected: number;
  pending: number;
}

export interface Proposal {
  id: string;
  db_id: string;
  migration_id: string;
  migration_title: string;
  migration_revision: number;
  effective_seq: number;
  action_type: ActionType;
  target_execution_id: string | null;
  resolution: "committed" | "not_committed" | null;
  reason: string;
  status: ProposalStatus;
  proposed_by_username: string;
  created_at: string;
  updated_at: string;
  approval_summary: ApprovalSummary;
  replaces_title?: string | null;
  dry_run_status?: "passed" | "inconclusive" | "failed" | null;
  job_id?: string | null;
}

export interface ProposalDetail {
  proposal: Proposal;
  migration: MigrationDefinition;
  approvers: ApprovalRow[];
  can_approve: boolean;
  can_withdraw: boolean;
}

export interface Execution {
  execution_id: string;
  root_execution_id: string;
  rollback_of_execution_id: string | null;
  seq_order: number;
  db_id: string;
  db_name_snapshot: string;
  migration_id: string;
  migration_title_snapshot: string;
  migration_revision: number;
  effective_seq: number;
  seq_version: number;
  action_type: ActionType;
  status: JobStatus;
  executed_by_username: string;
  executed_sql?: string;
  started_at: string | null;
  finished_at: string | null;
  duration_ms: number | null;
  error_message: string;
  committed_batches: number;
  total_batches: number;
  committed_rows: number;
  is_superseded: boolean;
  superseded_by_title?: string | null;
}

export interface Job {
  job_id: string;
  queue_seq: number;
  db_id: string;
  proposal_id: string;
  execution_id: string;
  root_execution_id: string;
  migration_id: string;
  migration_title: string;
  action_type: ActionType;
  execution_mode: ExecutionMode;
  status: JobStatus;
  retry_count: number;
  max_retries: number;
  last_cursor: number | null;
  scope_min_cursor: number | null;
  scope_max_cursor: number | null;
  total_batches: number;
  completed_batches: number;
  committed_rows: number;
  last_error: string;
  queued_at: string;
  started_at: string | null;
  finished_at: string | null;
  updated_at: string;
  worker_id: string | null;
  lock_state?: LockState | null;
  trace_id?: string | null;
}

export interface JobDetail {
  job: Job;
  lock_state: LockState | null;
  attempts: Execution[];
  progress: {
    completed_batches: number;
    total_batches: number;
    committed_rows: number;
    percent: number;
  };
}

export interface HistoryResponse {
  executions: Execution[];
}

export interface WorkerStatus {
  worker_id: string;
  hostname: string;
  active_jobs: number;
  last_seen_at: string;
  online: boolean;
}

export interface WorkersResponse {
  workers: WorkerStatus[];
}

export interface ApiErrorBody {
  error: {
    code: string;
    message: string;
    correlation_id?: string;
    trace_id?: string;
  };
}

export interface BatchTestResult {
  ok: boolean;
  error?: string;
  latency_ms?: number;
}
