import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowRight,
  FileCode2,
  Lock,
  Pencil,
  Plus,
  RefreshCcw,
  Send,
  ShieldAlert,
  Sparkles,
} from "lucide-react";
import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";

import { migrationApi } from "../../api/endpoints";
import type { MigrationDefinition } from "../../api/types";
import {
  ActionBadge,
  Button,
  Callout,
  ErrorBanner,
  Field,
  Modal,
  PageHeader,
  PageLoader,
  SearchInput,
  SectionHeader,
  StatusBadge,
  TableEmpty,
  TableWrap,
  Textarea,
} from "../../components/ui";
import { useToast } from "../../hooks/useToast";
import { formatDateTime } from "../../lib/format";
import { useDatabaseContext } from "./context";

export default function MigrationsPage() {
  const { dbId = "" } = useParams();
  const { overview } = useDatabaseContext();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();

  const migrationsQuery = useQuery({
    queryKey: ["database", dbId, "migrations"],
    queryFn: () => migrationApi.list(dbId),
  });
  const proposalsQuery = useQuery({
    queryKey: ["database", dbId, "proposals"],
    queryFn: () => migrationApi.proposals(dbId),
  });

  const [filter, setFilter] = useState("");
  const [submitTarget, setSubmitTarget] = useState<MigrationDefinition | null>(null);
  const [submitComment, setSubmitComment] = useState("");
  const [submitError, setSubmitError] = useState<unknown>(null);
  const [submitting, setSubmitting] = useState(false);

  const canEdit = overview.level === "edit";
  const migrations = migrationsQuery.data?.migrations ?? [];
  const proposals = proposalsQuery.data?.proposals ?? [];

  const filteredMigrations = useMemo(
    () =>
      migrations
        .filter(
          (migration) =>
            migration.title.toLowerCase().includes(filter.toLowerCase()) ||
            migration.description.toLowerCase().includes(filter.toLowerCase()),
        )
        .sort((a, b) => a.effective_seq - b.effective_seq || a.seq_version - b.seq_version),
    [migrations, filter],
  );

  const activeProposals = proposals.filter((proposal) =>
    ["pending_review", "approved", "queued", "running", "partial", "partially_reverted", "uncertain"].includes(
      proposal.status,
    ),
  );

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["database", dbId, "migrations"] }),
      queryClient.invalidateQueries({ queryKey: ["database", dbId, "proposals"] }),
      queryClient.invalidateQueries({ queryKey: ["database", dbId, "overview"] }),
    ]);
  };

  const submitProposal = async () => {
    if (!submitTarget) return;
    setSubmitting(true);
    setSubmitError(null);
    try {
      const proposal = await migrationApi.createProposal(dbId, {
        migration_id: submitTarget.id,
        action_type: "apply",
        reason: submitComment,
      });
      toast.success("Proposal submitted", "Every required group leader must approve it.");
      setSubmitTarget(null);
      setSubmitComment("");
      await invalidate();
      navigate(`/databases/${dbId}/proposals/${proposal.id}`);
    } catch (error) {
      setSubmitError(error);
    } finally {
      setSubmitting(false);
    }
  };

  if (migrationsQuery.isLoading) return <PageLoader label="Loading migration definitions" />;

  return (
    <div className="space-y-6">
      <PageHeader
        eyebrow="Change workflow"
        title="Migrations & proposals"
        description="Definitions are immutable once a proposal is active or execution has started. Every action goes through the approval workflow."
        actions={
          <>
            <Button variant="secondary" onClick={() => { void migrationsQuery.refetch(); void proposalsQuery.refetch(); }}>
              <RefreshCcw className="h-4 w-4" /> Refresh
            </Button>
            {canEdit ? (
              <Button onClick={() => navigate(`/databases/${dbId}/migrations/new`)}>
                <Plus className="h-4 w-4" /> New migration
              </Button>
            ) : null}
          </>
        }
      />

      {!canEdit ? (
        <Callout tone="info" title="View-only workspace">
          Your group grants View access. Editing definitions and submitting proposals requires View +
          Edit.
        </Callout>
      ) : null}

      {migrationsQuery.isError ? (
        <ErrorBanner error={migrationsQuery.error} onRetry={() => void migrationsQuery.refetch()} />
      ) : null}

      {activeProposals.length > 0 ? (
        <Callout tone="warning" title={`${activeProposals.length} proposal(s) in the workflow`}>
          Pending proposals are waiting for leaders; queued/running/hold proposals are visible on the
          queue. A migration with an active proposal cannot be edited.
          <div className="mt-2 flex flex-wrap gap-2">
            {activeProposals.slice(0, 4).map((proposal) => (
              <Link
                key={proposal.id}
                to={`/databases/${dbId}/proposals/${proposal.id}`}
                className="inline-flex items-center gap-1 rounded-md border border-current/20 bg-white/40 px-2 py-1 text-xs font-medium hover:bg-white/70"
              >
                {proposal.migration_title} <ArrowRight className="h-3 w-3" />
              </Link>
            ))}
          </div>
        </Callout>
      ) : null}

      <section>
        <SectionHeader
          title="Definitions"
          description="effective_seq is the logical slot; seq_version is the raw creation order."
          actions={<SearchInput value={filter} onChange={setFilter} className="w-56" placeholder="Filter definitions…" />}
        />
        <TableWrap>
          <table className="table-base min-w-[980px]">
            <thead>
              <tr>
                <th className="th">Title</th>
                <th className="th">Slot</th>
                <th className="th">Mode</th>
                <th className="th">Revision</th>
                <th className="th">State</th>
                <th className="th">Updated</th>
                <th className="th text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {filteredMigrations.map((migration) => {
                const active = migration.active_proposal;
                const locked = migration.is_immutable || Boolean(active);
                return (
                  <tr key={migration.id} className="tr-hover">
                    <td className="td max-w-[24rem]">
                      <div className="flex items-start gap-2">
                        <FileCode2 className="mt-0.5 h-4 w-4 shrink-0 text-leaf-600" />
                        <div className="min-w-0">
                          <p className="truncate text-sm font-medium text-ink">{migration.title}</p>
                          <p className="truncate text-[11px] text-muted">
                            {migration.description || "No description."}
                          </p>
                          <div className="mt-1 flex flex-wrap gap-1">
                            {migration.is_irreversible ? (
                              <span className="badge badge-warning">Irreversible</span>
                            ) : null}
                            {migration.is_superseded ? (
                              <span className="badge badge-plum">
                                <Sparkles className="h-3 w-3" /> Superseded by {migration.superseded_by_title}
                              </span>
                            ) : null}
                            {migration.replaces_title ? (
                              <span className="badge badge-info">Replaces {migration.replaces_title}</span>
                            ) : null}
                            {migration.is_immutable ? (
                              <span className="badge badge-neutral">
                                <Lock className="h-3 w-3" /> Immutable
                              </span>
                            ) : null}
                          </div>
                        </div>
                      </div>
                    </td>
                    <td className="td">
                      <p className="font-mono text-xs font-semibold">eff #{migration.effective_seq}</p>
                      <p className="font-mono text-[10px] text-muted">raw #{migration.seq_version}</p>
                    </td>
                    <td className="td">
                      <span className={`badge ${migration.execution_mode === "batch" ? "badge-info" : "badge-neutral"}`}>
                        {migration.execution_mode}
                      </span>
                      {migration.batch_config ? (
                        <p className="mt-1 font-mono text-[10px] text-muted">
                          {migration.batch_config.table_name} / {migration.batch_config.cursor_col}
                        </p>
                      ) : null}
                    </td>
                    <td className="td font-mono text-xs">r{migration.revision}</td>
                    <td className="td">
                      <div className="flex flex-col items-start gap-1">
                        <StatusBadge status={migration.latest_status} kind="auto" />
                        {active ? (
                          <Link
                            to={`/databases/${dbId}/proposals/${active.id}`}
                            className="text-[10px] font-medium text-leaf-700 hover:underline"
                          >
                            Open proposal →
                          </Link>
                        ) : null}
                      </div>
                    </td>
                    <td className="td text-xs text-muted">{formatDateTime(migration.updated_at)}</td>
                    <td className="td">
                      <div className="flex justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => navigate(`/databases/${dbId}/migrations/${migration.id}/edit`)}
                          disabled={migration.is_superseded && !migration.is_immutable ? false : false}
                        >
                          <Pencil className="h-3.5 w-3.5" /> {locked ? "View" : "Edit"}
                        </Button>
                        <Button
                          variant="secondary"
                          size="sm"
                          disabled={!canEdit || Boolean(active) || migration.is_superseded}
                          title={
                            active
                              ? "An active proposal already locks this definition"
                              : migration.is_superseded
                                ? "Superseded definitions cannot be submitted"
                                : "Submit for multi-group leader approval"
                          }
                          onClick={() => { setSubmitTarget(migration); setSubmitComment(""); setSubmitError(null); }}
                        >
                          <Send className="h-3.5 w-3.5" /> Submit
                        </Button>
                      </div>
                    </td>
                  </tr>
                );
              })}
              {filteredMigrations.length === 0 ? (
                <TableEmpty colSpan={7}>
                  No migration definitions match. Create one to start the approval workflow.
                </TableEmpty>
              ) : null}
            </tbody>
          </table>
        </TableWrap>
      </section>

      <section>
        <SectionHeader
          title="Proposals"
          description="A proposal snapshots the required approvers and the dry-run result."
          actions={
            proposalsQuery.isFetching ? <span className="text-[11px] text-muted">Refreshing…</span> : null
          }
        />
        <TableWrap>
          <table className="table-base min-w-[900px]">
            <thead>
              <tr>
                <th className="th">Migration</th>
                <th className="th">Action</th>
                <th className="th">Status</th>
                <th className="th">Approvals</th>
                <th className="th">Dry-run</th>
                <th className="th">Proposed by</th>
                <th className="th">Updated</th>
                <th className="th text-right"> </th>
              </tr>
            </thead>
            <tbody>
              {proposals.map((proposal) => (
                <tr key={proposal.id} className="tr-hover">
                  <td className="td max-w-[22rem]">
                    <p className="truncate text-sm font-medium text-ink">{proposal.migration_title}</p>
                    <p className="font-mono text-[10px] text-muted">
                      eff #{proposal.effective_seq} · rev {proposal.migration_revision}
                    </p>
                  </td>
                  <td className="td"><ActionBadge action={proposal.action_type} /></td>
                  <td className="td"><StatusBadge status={proposal.status} kind="proposal" /></td>
                  <td className="td text-xs">
                    <span className="font-mono">
                      {proposal.approval_summary.approved}/{proposal.approval_summary.required}
                    </span>
                    {proposal.approval_summary.pending > 0 ? (
                      <span className="ml-1 text-muted">· {proposal.approval_summary.pending} pending</span>
                    ) : null}
                    {proposal.approval_summary.rejected > 0 ? (
                      <span className="ml-1 text-[#8b3323]">· rejected</span>
                    ) : null}
                  </td>
                  <td className="td">
                    {proposal.dry_run_status === "inconclusive" ? (
                      <span className="badge badge-warning">inconclusive</span>
                    ) : proposal.dry_run_status === "failed" ? (
                      <span className="badge badge-danger">failed</span>
                    ) : (
                      <span className="badge badge-success">passed</span>
                    )}
                  </td>
                  <td className="td text-xs">{proposal.proposed_by_username}</td>
                  <td className="td text-xs text-muted">{formatDateTime(proposal.updated_at)}</td>
                  <td className="td text-right">
                    <Link
                      to={`/databases/${dbId}/proposals/${proposal.id}`}
                      className="inline-flex items-center gap-1 text-xs font-medium text-leaf-700 hover:underline"
                    >
                      Review <ArrowRight className="h-3.5 w-3.5" />
                    </Link>
                  </td>
                </tr>
              ))}
              {proposals.length === 0 ? (
                <TableEmpty colSpan={8}>No proposals yet.</TableEmpty>
              ) : null}
            </tbody>
          </table>
        </TableWrap>
      </section>

      <Modal
        open={submitTarget !== null}
        onClose={() => setSubmitTarget(null)}
        title={`Submit for review · ${submitTarget?.title ?? ""}`}
        description="The approver snapshot is frozen at submission. Every required group leader must approve before the job is queued."
        footer={
          <>
            <Button variant="secondary" onClick={() => setSubmitTarget(null)} disabled={submitting}>
              Cancel
            </Button>
            <Button onClick={() => void submitProposal()} loading={submitting}>
              <Send className="h-4 w-4" /> Submit proposal
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          {submitError ? <ErrorBanner error={submitError} /> : null}
          <Callout tone="info" title="Pre-review validation runs automatically">
            The API runs a <code className="font-mono text-xs">BEGIN … ROLLBACK</code> dry-run. Timeout
            or lock-not-available results are recorded as <strong>inconclusive</strong>, not rejected.
          </Callout>
          <Field label="Note for approvers (optional)" htmlFor="proposal-comment">
            <Textarea
              id="proposal-comment"
              value={submitComment}
              onChange={(event) => setSubmitComment(event.target.value)}
              placeholder="Context, risk, or rollout window…"
            />
          </Field>
          {submitTarget ? (
            <div className="rounded-lg border border-line bg-[#f7f6f0] p-3 text-xs">
              <p className="font-semibold text-ink">{submitTarget.title}</p>
              <p className="mt-1 font-mono text-[10px] text-muted">
                effective_seq {submitTarget.effective_seq} · {submitTarget.execution_mode} ·{" "}
                {submitTarget.is_irreversible ? "irreversible" : "reversible"}
              </p>
            </div>
          ) : null}
        </div>
      </Modal>
    </div>
  );
}
