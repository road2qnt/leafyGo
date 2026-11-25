import type {
  ActionType,
  JobStatus,
  LockState,
  ProposalStatus,
} from "../api/types";

export type Tone = "neutral" | "success" | "warning" | "danger" | "info" | "plum";

export interface StatusMeta {
  label: string;
  tone: Tone;
  description: string;
  active: boolean;
}

export const JOB_STATUS_META: Record<JobStatus, StatusMeta> = {
  queued: {
    label: "Queued",
    tone: "neutral",
    description: "Waiting for a worker and the per-database lock.",
    active: true,
  },
  running: {
    label: "Running",
    tone: "info",
    description: "A worker holds the database lock and is executing.",
    active: true,
  },
  partial: {
    label: "Partial",
    tone: "warning",
    description: "Some batches committed, then execution stopped. Lock is held.",
    active: true,
  },
  partially_reverted: {
    label: "Partially reverted",
    tone: "warning",
    description: "Reverse-batch compensation stopped part-way. Resume revert downward.",
    active: true,
  },
  uncertain: {
    label: "Uncertain",
    tone: "plum",
    description: "Commit outcome is unknown. The database stays locked until probing resolves it.",
    active: true,
  },
  success: {
    label: "Success",
    tone: "success",
    description: "All work committed successfully.",
    active: false,
  },
  failed: {
    label: "Failed",
    tone: "danger",
    description: "Execution failed. Review the error and the attempts below.",
    active: false,
  },
};

export const PROPOSAL_STATUS_META: Record<ProposalStatus, StatusMeta> = {
  draft: { label: "Draft", tone: "neutral", description: "Not submitted for review yet.", active: false },
  pending_review: {
    label: "Pending review",
    tone: "warning",
    description: "Waiting for every required group leader.",
    active: false,
  },
  withdrawn: { label: "Withdrawn", tone: "neutral", description: "Withdrawn by the proposer.", active: false },
  approved: { label: "Approved", tone: "success", description: "Quorum reached.", active: false },
  rejected: { label: "Rejected", tone: "danger", description: "A group leader rejected this proposal.", active: false },
  queued: { label: "Queued", tone: "neutral", description: "Approved and waiting in the FIFO queue.", active: true },
  running: { label: "Running", tone: "info", description: "Worker is executing this proposal.", active: true },
  partial: { label: "Partial", tone: "warning", description: "Execution stopped part-way.", active: true },
  partially_reverted: {
    label: "Partially reverted",
    tone: "warning",
    description: "Reverse compensation stopped part-way.",
    active: true,
  },
  uncertain: {
    label: "Uncertain",
    tone: "plum",
    description: "Commit outcome is unknown; the lock is held.",
    active: true,
  },
  completed: { label: "Completed", tone: "success", description: "Finished successfully.", active: false },
  failed: { label: "Failed", tone: "danger", description: "Finished with an error.", active: false },
};

export const ACTION_LABELS: Record<ActionType, string> = {
  apply: "Apply",
  rollback: "Rollback",
  forward: "Forward",
  batch_retry: "Retry batches",
  batch_revert: "Revert batches",
  resolve_uncertain: "Resolve uncertain",
};

export const LOCK_STATE_META: Record<LockState, StatusMeta> = {
  running: {
    label: "Lock: running",
    tone: "info",
    description: "A worker currently holds this database's migration lock.",
    active: true,
  },
  partial_hold: {
    label: "Lock: partial hold",
    tone: "warning",
    description: "A partial migration holds the lock. Only retry/revert may proceed.",
    active: true,
  },
  partially_reverted_hold: {
    label: "Lock: reverted hold",
    tone: "warning",
    description: "A partially reverted migration holds the lock.",
    active: true,
  },
  uncertain_hold: {
    label: "Lock: uncertain hold",
    tone: "plum",
    description:
      "Commit evidence is unresolved. The lock stays until a probe confirms the outcome.",
    active: true,
  },
};

export function jobStatusMeta(status: JobStatus | null | undefined): StatusMeta {
  return status ? JOB_STATUS_META[status] : JOB_STATUS_META.failed;
}

export function proposalStatusMeta(status: ProposalStatus | null | undefined): StatusMeta {
  return status ? PROPOSAL_STATUS_META[status] : PROPOSAL_STATUS_META.draft;
}

export function actionLabel(action: ActionType | null | undefined): string {
  return action ? ACTION_LABELS[action] : "—";
}

export function lockLabel(lock: LockState | null | undefined): string {
  return lock ? LOCK_STATE_META[lock].label : "No lock";
}

export function isActiveJob(status: JobStatus | null | undefined): boolean {
  return status ? JOB_STATUS_META[status].active : false;
}

export function isUncertain(status: string | null | undefined): boolean {
  return status === "uncertain";
}

const ERROR_GUIDANCE: Record<string, string> = {
  INVALID_MIGRATION_SQL:
    "Fix the SQL and run Validate again. Transaction-control statements (BEGIN/COMMIT/ROLLBACK) are rejected.",
  NON_TRANSACTIONAL_STATEMENT:
    "Remove statements PostgreSQL cannot run inside a transaction (VACUUM, CLUSTER, CREATE/DROP DATABASE, ALTER SYSTEM, CREATE INDEX CONCURRENTLY).",
  INVALID_BATCH_IDENTIFIER:
    "Check the batch table, cursor column, and optional updated_at column against the target schema.",
  INVALID_BATCH_CURSOR_TYPE:
    "The cursor column must be an integer type (smallint, integer, or bigint).",
  DRY_RUN_BUSY:
    "Another dry-run is already running on this database. Wait a moment and validate again.",
  DUPLICATE_ACTIVE_PROPOSAL:
    "This migration already has an active proposal. Withdraw it or wait for approval before submitting another.",
  PREDECESSOR_NOT_READY:
    "A predecessor at a lower effective_seq must be Active or queued first. Approve it before this slot.",
  PREDECESSOR_FAILED:
    "The predecessor failed terminally. Fix or supersede it, then resubmit this migration.",
  APPROVER_SET_CHANGED:
    "Database access groups changed after submission. Resubmit so the new leaders can review.",
  NO_ACCESS_GROUPS:
    "No active group has access to this database. Ask a Platform Admin to grant access.",
  MIGRATION_NOT_SUPERSEDABLE:
    "The target chain is currently Active or has in-flight work. Only a failed/pending chain at the lowest effective_seq can be replaced.",
  USER_HAS_HISTORY:
    "This user appears in approval or execution history. Deactivate the account instead of deleting it.",
  GROUP_HAS_HISTORY:
    "This group appears in approval history. Deactivation (soft delete) is the safe option.",
  DB_HAS_HISTORY:
    "This object has migration history and cannot be hard-deleted.",
  DB_HAS_ACTIVE_WORK:
    "A lock or active job still exists for this database. Finish or resolve it before deleting.",
  UNCERTAIN_MANUAL_INTERVENTION:
    "The old target session could not be proven dead. The lock stays in uncertain_hold and is re-probed automatically every 15s.",
  RESOLVE_UNCERTAIN_INVALID:
    "Evidence in the target database contradicts the chosen resolution. Evidence wins; the lock stays held.",
  UNSAFE_BATCH_REVERT:
    "The revert is unsafe: the migration is irreversible, violates LIFO order, or rows changed after the batch was committed. Create a corrective migration instead.",
  FORBIDDEN_NOT_GROUP_LEADER:
    "Only the leader of a required group can approve or reject this proposal.",
  FORBIDDEN: "You do not have sufficient access for this action.",
};

export function errorGuidance(code: string | undefined | null): string | undefined {
  return code ? ERROR_GUIDANCE[code] : undefined;
}
