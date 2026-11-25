import { describe, expect, it } from "vitest";

import { actionLabel, errorGuidance, isActiveJob, jobStatusMeta, proposalStatusMeta } from "./status";

describe("status metadata", () => {
  it("marks every non-terminal job status as active", () => {
    for (const status of ["queued", "running", "partial", "partially_reverted", "uncertain"] as const) {
      expect(isActiveJob(status)).toBe(true);
      expect(jobStatusMeta(status).label).not.toBe("");
    }
    expect(isActiveJob("success")).toBe(false);
    expect(isActiveJob("failed")).toBe(false);
    expect(isActiveJob(null)).toBe(false);
  });

  it("maps actions and proposal states", () => {
    expect(actionLabel("batch_revert")).toBe("Revert batches");
    expect(actionLabel("resolve_uncertain")).toBe("Resolve uncertain");
    expect(proposalStatusMeta("pending_review").tone).toBe("warning");
    expect(proposalStatusMeta("uncertain").tone).toBe("plum");
  });

  it("exposes guidance for the M2 error catalog", () => {
    expect(errorGuidance("PREDECESSOR_NOT_READY")).toContain("effective_seq");
    expect(errorGuidance("UNSAFE_BATCH_REVERT")).toContain("irreversible");
    expect(errorGuidance("NOT_A_REAL_CODE")).toBeUndefined();
  });
});
