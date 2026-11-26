import { describe, expect, it } from "vitest";

import {
  normalizeAuth,
  normalizeData,
  normalizeDatabase,
  normalizeMigration,
  normalizeProposalDetail,
  normalizeQuery,
} from "./normalize";
import type { MigrationDefinition } from "./types";

describe("real API normalizers", () => {
  it("accepts the flat P1 auth response", () => {
    const auth = normalizeAuth({
      id: "u-1",
      username: "jibril",
      is_platform_admin: false,
      is_admin: false,
      is_active: true,
      csrf_token: "csrf",
      leader_of_groups: ["g-1"],
    });
    expect(auth.user.username).toBe("jibril");
    expect(auth.csrf_token).toBe("csrf");
    expect(auth.leader_of_groups[0]).toEqual({ id: "g-1", name: "Group g-1" });
  });

  it("maps dbname/sslmode and lock metadata", () => {
    const db = normalizeDatabase({
      id: "db-1", name: "Inventory", host: "db", port: 5432,
      dbname: "inventory", username: "app", sslmode: "disable",
      access_level: "edit", lock_state: "running", lock: { job_id: "job-1", lock_state: "running" },
      created_at: "2026-01-01T00:00:00Z",
    });
    expect(db.db_name).toBe("inventory");
    expect(db.ssl_mode).toBe("disable");
    expect(db.level).toBe("edit");
    expect(db.active_job_id).toBe("job-1");
  });

  it("maps introspected pages (array rows + Cell objects) to row objects", () => {
    const page = normalizeData({
      page: 2, page_size: 10, row_count: 42,
      columns: [{ name: "id", numeric: true }, { name: "email", numeric: false }],
      rows: [[{ text: "1", is_null: false }, { text: "", is_null: true }]],
    });
    expect(page.rows[0]).toEqual({ id: "1", email: null });
    expect(page.total_rows).toBe(42);
    expect(page.total_pages).toBe(5);
  });

  it("maps queryconsole Result cells and duration", () => {
    const result = normalizeQuery({
      columns: ["count"],
      rows: [[{ text: "7", is_null: false, truncated: false }]],
      command_tag: "SELECT 1",
      row_count: 1,
      truncated: false,
      duration_ns: 12_000_000,
    });
    expect(result.rows[0]).toEqual({ count: "7" });
    expect(result.duration_ms).toBe(12);
    expect(result.notices).toEqual(["SELECT 1"]);
  });

  it("derives definition status from latest proposal", () => {
    const raw = {
      id: "m-1", db_id: "db-1", title: "Add column", effective_seq: 3, seq_version: 4,
      latest_proposal: { id: "p-1", status: "pending_review", action_type: "apply" },
    };
    const def = normalizeMigration(raw, [] as MigrationDefinition[]);
    expect(def.active_proposal?.id).toBe("p-1");
    expect(def.latest_status).toBe("pending_review");
  });

  it("joins approvers and approvals into an approval matrix", () => {
    const migration = normalizeMigration(
      { id: "m-1", db_id: "db-1", title: "Add column", effective_seq: 1 },
      [],
    );
    const detail = normalizeProposalDetail(
      {
        id: "p-1", db_id: "db-1", migration_id: "m-1", action_type: "apply",
        status: "pending_review", migration_revision: 1, proposed_by_username: "ega",
        approvers: [
          { group_id: "g-1", group_name: "Core", leader_id: "u-1", leader_username: "jibril", access_level: "edit" },
          { group_id: "g-2", group_name: "Ops", leader_id: "u-2", leader_username: "putra", access_level: "view" },
        ],
        approvals: [{ group_id: "g-1", decision: "approved", comment: "ok", decided_at: "2026-01-01T00:00:00Z" }],
      },
      [migration],
    );
    expect(detail.approvers).toHaveLength(2);
    expect(detail.proposal.approval_summary).toEqual({ required: 2, approved: 1, rejected: 0, pending: 1 });
    expect(detail.proposal.migration_title).toBe("Add column");
  });
});
