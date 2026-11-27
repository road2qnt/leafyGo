import type { ActionType, JobStatus, LockState } from "../api/types";

export interface JobActionAvailability {
  retry: boolean;
  revert: boolean;
  rollback: boolean;
  forward: boolean;
  resolve: boolean;
}

/**
 * Mirrors the direction rules from ADR-04:
 * - `partial` may resume forward (retry) or reverse (revert).
 * - `partially_reverted` may only resume the reverse direction; never SQL up.
 * - a successful normal rollback may be forwarded again.
 * - uncertain exposes only the optional resolve_uncertain path.
 */
export function jobActions(
  status: JobStatus,
  lockState: LockState | null | undefined,
  actionType: ActionType,
): JobActionAvailability {
  const partialHeld = status === "partial" && lockState === "partial_hold";
  const partiallyRevertedHeld =
    status === "partially_reverted" && lockState === "partially_reverted_hold";
  const uncertainHeld = status === "uncertain" && lockState === "uncertain_hold";
  return {
    retry: partialHeld,
    revert: partialHeld || partiallyRevertedHeld,
    rollback:
      status === "success" &&
      actionType !== "rollback" &&
      actionType !== "batch_revert" &&
      actionType !== "resolve_uncertain",
    forward: status === "success" && actionType === "rollback",
    resolve: uncertainHeld,
  };
}
