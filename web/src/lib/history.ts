import type { Execution } from "../api/types";

const REVERT_ACTIONS = new Set(["rollback", "batch_revert"]);
const FORWARD_ACTIONS = new Set(["apply", "forward", "batch_retry"]);

/**
 * Mirrors the backend hide_rollback rule. Kept client-side so the table updates
 * immediately while the server filter is authoritative on refresh.
 *
 * A successful rollback/batch_revert hides itself, its precise target, and every
 * apply/forward/batch_retry attempt in the same root group that precedes it.
 * Failed rows and resolve_uncertain rows are never hidden.
 */
export function applyHideRollback(executions: Execution[], hide: boolean): Execution[] {
  if (!hide) return executions;

  const byRoot = new Map<string, Execution[]>();
  for (const execution of executions) {
    const group = byRoot.get(execution.root_execution_id);
    if (group) {
      group.push(execution);
    } else {
      byRoot.set(execution.root_execution_id, [execution]);
    }
  }

  const hidden = new Set<string>();
  for (const revert of executions) {
    if (!REVERT_ACTIONS.has(revert.action_type) || revert.status !== "success") {
      continue;
    }
    hidden.add(revert.execution_id);
    if (revert.rollback_of_execution_id) {
      hidden.add(revert.rollback_of_execution_id);
    }
    for (const candidate of byRoot.get(revert.root_execution_id) ?? []) {
      if (
        FORWARD_ACTIONS.has(candidate.action_type) &&
        candidate.seq_order < revert.seq_order &&
        candidate.status !== "failed"
      ) {
        hidden.add(candidate.execution_id);
      }
    }
  }

  return executions.filter((execution) => {
    if (execution.status === "failed") return true;
    if (execution.action_type === "resolve_uncertain") return true;
    return !hidden.has(execution.execution_id);
  });
}

/**
 * Computes which execution is currently eligible for LIFO rollback (highest active
 * effective_seq) and which rollback executions are the latest state for their migration
 * (eligible for forward).
 */
export function findRollbackAndForwardTargets(executions: Execution[]): {
  topActiveExecutionId: string | null;
  latestRollbackIds: Set<string>;
} {
  const latestByMigration = new Map<string, Execution>();
  for (const execution of executions) {
    if (
      execution.status !== "success" ||
      (!FORWARD_ACTIONS.has(execution.action_type) && !REVERT_ACTIONS.has(execution.action_type))
    ) {
      continue;
    }
    const prev = latestByMigration.get(execution.migration_id);
    if (!prev || execution.seq_order > prev.seq_order) {
      latestByMigration.set(execution.migration_id, execution);
    }
  }
  let topActive: Execution | null = null;
  const latestRollbackIds = new Set<string>();
  for (const execution of latestByMigration.values()) {
    if (!execution.is_superseded && FORWARD_ACTIONS.has(execution.action_type)) {
      if (!topActive || execution.effective_seq > topActive.effective_seq) {
        topActive = execution;
      }
    } else if (execution.action_type === "rollback") {
      latestRollbackIds.add(execution.execution_id);
    }
  }
  return {
    topActiveExecutionId: topActive?.execution_id ?? null,
    latestRollbackIds,
  };
}
