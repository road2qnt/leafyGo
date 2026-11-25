import { describe, expect, it } from "vitest";

import { dryRunMeta } from "./dryRun";

describe("dryRunMeta", () => {
  it("distinguishes passed, inconclusive, failed, and not run", () => {
    expect(dryRunMeta("passed")).toMatchObject({ tone: "success", canReview: true });
    expect(dryRunMeta("inconclusive")).toMatchObject({ tone: "warning", canReview: true });
    expect(dryRunMeta("failed")).toMatchObject({ tone: "danger", canReview: false });
    expect(dryRunMeta(null)).toMatchObject({ tone: "neutral", label: "Not validated", canReview: true });
    expect(dryRunMeta(undefined)).toMatchObject({ tone: "neutral" });
  });
});
