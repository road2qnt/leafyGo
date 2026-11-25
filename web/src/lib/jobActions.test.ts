import { describe, expect, it } from "vitest";

import { jobActions } from "./jobActions";

describe("jobActions", () => {
  it("offers both directions for a partial batch", () => {
    const actions = jobActions("partial", "partial_hold", "apply");
    expect(actions.retry).toBe(true);
    expect(actions.revert).toBe(true);
    expect(actions.rollback).toBe(false);
    expect(actions.forward).toBe(false);
  });

  it("never offers forward retry for a partially reverted job", () => {
    const actions = jobActions("partially_reverted", "partially_reverted_hold", "batch_revert");
    expect(actions.retry).toBe(false);
    expect(actions.revert).toBe(true);
  });

  it("offers rollback for a successful forward execution", () => {
    const actions = jobActions("success", null, "apply");
    expect(actions.rollback).toBe(true);
    expect(actions.forward).toBe(false);
  });

  it("offers forward after a successful rollback", () => {
    const actions = jobActions("success", null, "rollback");
    expect(actions.rollback).toBe(false);
    expect(actions.forward).toBe(true);
  });

  it("does not offer rollback/forward after a successful revert or resolve", () => {
    expect(jobActions("success", null, "batch_revert").rollback).toBe(false);
    expect(jobActions("success", null, "batch_revert").forward).toBe(false);
    expect(jobActions("success", null, "resolve_uncertain").rollback).toBe(false);
  });

  it("exposes only resolve for uncertain", () => {
    const actions = jobActions("uncertain", "uncertain_hold", "apply");
    expect(actions.resolve).toBe(true);
    expect(actions.retry).toBe(false);
    expect(actions.revert).toBe(false);
    expect(actions.rollback).toBe(false);
  });

  it("offers nothing while running or queued", () => {
    expect(jobActions("running", "running", "apply")).toEqual({
      retry: false,
      revert: false,
      rollback: false,
      forward: false,
      resolve: false,
    });
  });
});
