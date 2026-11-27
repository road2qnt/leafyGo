import { describe, expect, it } from "vitest";

import type { Execution } from "../api/types";
import { applyHideRollback, findRollbackAndForwardTargets } from "./history";

function execution(overrides: Partial<Execution> & Pick<Execution, "execution_id" | "action_type" | "status" | "seq_order">): Execution {
  return {
    root_execution_id: "root-1",
    rollback_of_execution_id: null,
    db_id: "db-1",
    db_name_snapshot: "inventory",
    migration_id: "mig-1",
    migration_title_snapshot: "Migration",
    migration_revision: 1,
    effective_seq: 1,
    seq_version: 1,
    executed_by_username: "jibril",
    started_at: null,
    finished_at: null,
    duration_ms: null,
    error_message: "",
    committed_batches: 0,
    total_batches: 1,
    committed_rows: 0,
    is_superseded: false,
    ...overrides,
  };
}

describe("applyHideRollback", () => {
  const e1 = execution({ execution_id: "E1", action_type: "apply", status: "partial", seq_order: 1 });
  const e2 = execution({ execution_id: "E2", action_type: "batch_retry", status: "partial", seq_order: 2 });
  const e3 = execution({
    execution_id: "E3",
    action_type: "batch_revert",
    status: "success",
    seq_order: 3,
    rollback_of_execution_id: "E2",
  });
  const later = execution({ execution_id: "E4", action_type: "apply", status: "success", seq_order: 4 });

  it("returns the list untouched when disabled", () => {
    expect(applyHideRollback([e3, e2, e1, later], false)).toHaveLength(4);
  });

  it("hides the whole root group for a successful batch revert", () => {
    const visible = applyHideRollback([e3, e2, e1, later], true);
    expect(visible.map((e) => e.execution_id)).toEqual(["E4"]);
  });

  it("never hides failed attempts", () => {
    const failed = execution({ execution_id: "E5", action_type: "batch_retry", status: "failed", seq_order: 2 });
    const visible = applyHideRollback([e3, failed, e1], true);
    expect(visible.map((e) => e.execution_id)).toContain("E5");
  });

  it("never hides resolve_uncertain rows", () => {
    const resolve = execution({
      execution_id: "E6",
      action_type: "resolve_uncertain",
      status: "success",
      seq_order: 4,
      rollback_of_execution_id: "E1",
    });
    const visible = applyHideRollback([e3, resolve, e1], true);
    expect(visible.map((e) => e.execution_id)).toContain("E6");
  });

  it("hides a normal-mode rollback and its target", () => {
    const apply = execution({ execution_id: "N1", action_type: "apply", status: "success", seq_order: 1, root_execution_id: "normal-1" });
    const rollback = execution({
      execution_id: "N2",
      action_type: "rollback",
      status: "success",
      seq_order: 2,
      root_execution_id: "normal-2",
      rollback_of_execution_id: "N1",
    });
    expect(applyHideRollback([rollback, apply], true)).toEqual([]);
  });

  it("keeps an unsuccessful revert visible", () => {
    const failedRevert = execution({ execution_id: "F1", action_type: "batch_revert", status: "failed", seq_order: 3 });
    expect(applyHideRollback([failedRevert, e1], true).map((e) => e.execution_id)).toEqual(["F1", "E1"]);
  });
});

describe("findRollbackAndForwardTargets", () => {
  it("excludes superseded rolled-back migrations from forward targets", () => {
    const rolledBack = execution({
      execution_id: "R1",
      migration_id: "mig-1",
      action_type: "rollback",
      status: "success",
      seq_order: 2,
      effective_seq: 1,
      is_superseded: true,
    });
    const replacement = execution({
      execution_id: "A2",
      migration_id: "mig-2",
      action_type: "apply",
      status: "success",
      seq_order: 3,
      effective_seq: 1,
      is_superseded: false,
    });
    const targets = findRollbackAndForwardTargets([replacement, rolledBack]);
    expect(targets.topActiveExecutionId).toBe("A2");
    expect(targets.latestRollbackIds.has("R1")).toBe(false);
  });
});
