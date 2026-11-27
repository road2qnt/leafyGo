import { describe, expect, it } from "vitest";

import {
  actionLabel,
  errorGuidance,
  isActiveJob,
  isActiveProposal,
  isLiveJob,
  jobStatusMeta,
  proposalStatusMeta,
} from "./status";

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

  it("only treats hold jobs as live while they still hold the database lock", () => {
    expect(isLiveJob("queued", null, false)).toBe(true);
    expect(isLiveJob("running", "running", true)).toBe(true);
    expect(isLiveJob("partial", "partial_hold", true)).toBe(true);
    expect(isLiveJob("partially_reverted", "partially_reverted_hold", true)).toBe(true);
    expect(isLiveJob("uncertain", "uncertain_hold", true)).toBe(true);

    expect(isLiveJob("partial", null, false)).toBe(false);
    expect(isLiveJob("partially_reverted", null, false)).toBe(false);
    expect(isLiveJob("uncertain", null, false)).toBe(false);
    expect(isLiveJob("partial", null)).toBe(false);
    expect(isLiveJob("success", null, false)).toBe(false);
    expect(isLiveJob("failed", null, false)).toBe(false);
  });

  it("only treats actionable workflow proposal statuses as active", () => {
    for (const status of ["pending_review", "approved", "queued", "running"] as const) {
      expect(isActiveProposal(status)).toBe(true);
    }
    for (const status of [
      "draft",
      "withdrawn",
      "rejected",
      "partial",
      "partially_reverted",
      "uncertain",
      "completed",
      "failed",
    ] as const) {
      expect(isActiveProposal(status)).toBe(false);
    }
    expect(isActiveProposal(null)).toBe(false);
    expect(isActiveProposal(undefined)).toBe(false);
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
