import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  Clock,
  GitPullRequest,
  ShieldCheck,
  ThumbsDown,
  ThumbsUp,
  Undo2,
  XCircle,
} from "lucide-react";
import { useState } from "react";
import { Link, useParams } from "react-router";

import { migrationApi } from "../../api/endpoints";
import type { ApprovalRow } from "../../api/types";
import {
  ActionBadge,
  Button,
  Callout,
  Card,
  CodeBlock,
  EmptyState,
  ErrorBanner,
  Field,
  KeyValue,
  PageHeader,
  PageLoader,
  ProgressBar,
  SectionHeader,
  StatusBadge,
  TableEmpty,
  TableWrap,
  Textarea,
} from "../../components/ui";
import { useToast } from "../../hooks/useToast";
import { formatDateTime, formatRelative } from "../../lib/format";
import { percent } from "../../lib/format";
import { dryRunMeta } from "../../lib/dryRun";
import { useDatabaseContext } from "./context";

function DecisionBadge({ row }: { row: ApprovalRow }) {
  if (row.decision === "approved") {
    return (
      <span className="badge badge-success">
        <CheckCircle2 className="h-3 w-3" /> Approved
      </span>
    );
  }
  if (row.decision === "rejected") {
    return (
      <span className="badge badge-danger">
        <XCircle className="h-3 w-3" /> Rejected
      </span>
    );
  }
  return (
    <span className="badge badge-warning">
      <Clock className="h-3 w-3" /> Pending
    </span>
  );
}

export default function ProposalReviewPage() {
  const { dbId = "", proposalId = "" } = useParams();
  const { overview } = useDatabaseContext();
  const queryClient = useQueryClient();
  const toast = useToast();

  const detailQuery = useQuery({
    queryKey: ["database", dbId, "proposal", proposalId],
    queryFn: () => migrationApi.proposal(dbId, proposalId),
    refetchInterval: (query) => {
      const status = query.state.data?.proposal.status;
      return status && ["queued", "running", "partial", "partially_reverted", "uncertain"].includes(status)
        ? 4_000
        : false;
    },
  });

  const [comment, setComment] = useState("");
  const [deciding, setDeciding] = useState<"approve" | "reject" | null>(null);
  const [decisionError, setDecisionError] = useState<unknown>(null);
  const [withdrawing, setWithdrawing] = useState(false);

  const canEdit = overview.level === "edit";

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["database", dbId, "proposal", proposalId] }),
      queryClient.invalidateQueries({ queryKey: ["database", dbId, "proposals"] }),
      queryClient.invalidateQueries({ queryKey: ["database", dbId, "migrations"] }),
      queryClient.invalidateQueries({ queryKey: ["database", dbId, "overview"] }),
    ]);
  };

  const decide = async (kind: "approve" | "reject") => {
    setDeciding(kind);
    setDecisionError(null);
    try {
      const result = kind === "approve"
        ? await migrationApi.approve(dbId, proposalId, comment)
        : await migrationApi.reject(dbId, proposalId, comment);
      setComment("");
      await invalidate();
      if (result.status === "queued") {
        toast.success("Quorum reached", "The job is queued in the per-database FIFO.");
      } else if (result.status === "rejected") {
        toast.info("Proposal rejected", "The definition stays editable because execution never started.");
      } else {
        toast.success("Decision recorded", "Waiting for the remaining group leaders.");
      }
    } catch (error) {
      setDecisionError(error);
    } finally {
      setDeciding(null);
    }
  };

  const withdraw = async () => {
    setWithdrawing(true);
    setDecisionError(null);
    try {
      await migrationApi.withdraw(dbId, proposalId);
      await invalidate();
      toast.success("Proposal withdrawn", "All approvals were discarded.");
    } catch (error) {
      setDecisionError(error);
    } finally {
      setWithdrawing(false);
    }
  };

  if (detailQuery.isLoading) return <PageLoader label="Loading proposal" />;
  if (detailQuery.isError || !detailQuery.data) {
    return (
      <div className="space-y-4">
        <Link to={`/databases/${dbId}/migrations`} className="btn btn-ghost btn-sm w-fit">
          <ArrowLeft className="h-3.5 w-3.5" /> Migrations
        </Link>
        <ErrorBanner error={detailQuery.error} onRetry={() => void detailQuery.refetch()} />
      </div>
    );
  }

  const { proposal, migration, approvers, can_approve, can_withdraw } = detailQuery.data;
  const summary = proposal.approval_summary;
  const reviewOpen = proposal.status === "pending_review";
  const activeJobId = proposal.job_id;
  const dryRun = dryRunMeta(proposal.dry_run_status);
  const dryRunBadge = {
    success: "badge-success",
    warning: "badge-warning",
    danger: "badge-danger",
    neutral: "badge-neutral",
  }[dryRun.tone];

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted">
        <Link to={`/databases/${dbId}/migrations`} className="inline-flex items-center gap-1 font-medium text-leaf-700 hover:underline">
          <ArrowLeft className="h-3.5 w-3.5" /> Migrations
        </Link>
        <span>/</span>
        <span className="font-mono text-[10px]">{proposal.id}</span>
      </div>

      <PageHeader
        eyebrow={`Proposal · ${proposal.action_type.replaceAll("_", " ")}`}
        title={
          <span className="inline-flex flex-wrap items-center gap-2">
            {proposal.migration_title}
            <StatusBadge status={proposal.status} kind="proposal" />
          </span>
        }
        description={
          <>
            Proposed by <strong>{proposal.proposed_by_username}</strong> · revision{" "}
            {proposal.migration_revision} · effective slot #{proposal.effective_seq} ·{" "}
            {formatRelative(proposal.created_at)}
          </>
        }
        actions={
          activeJobId ? (
            <Link to={`/databases/${dbId}/jobs/${activeJobId}`} className="btn btn-primary">
              Open job monitor <ArrowRight className="h-4 w-4" />
            </Link>
          ) : can_withdraw ? (
            <Button variant="secondary" onClick={() => void withdraw()} loading={withdrawing}>
              <Undo2 className="h-4 w-4" /> Withdraw
            </Button>
          ) : null
        }
      />

      {proposal.dry_run_status === "inconclusive" ? (
        <Callout tone="warning" title="Dry-run was inconclusive">
          The pre-review dry-run hit <code className="font-mono text-xs">statement_timeout</code> (
          57014) or <code className="font-mono text-xs">lock_not_available</code> (55P03). That is not
          a rejection; approvers may continue, but should read the SQL carefully.
        </Callout>
      ) : proposal.dry_run_status === "failed" ? (
        <Callout tone="danger" title="Dry-run failed — approval blocked">
          The saved revision failed validation. Fix the SQL and run the dry-run again before asking
          leaders to approve. The API must not queue a failed revision.
        </Callout>
      ) : null}

      {proposal.status === "partial" || proposal.status === "partially_reverted" || proposal.status === "uncertain" ? (
        <Callout tone="plum" title="Database is held by a non-terminal execution">
          A new migration on this database must wait. Retry/revert from the job monitor, or let the
          automatic evidence re-probe resolve uncertain commits.
        </Callout>
      ) : null}

      {decisionError ? <ErrorBanner error={decisionError} onDismiss={() => setDecisionError(null)} /> : null}

      <div className="grid min-w-0 gap-5 xl:grid-cols-[minmax(0,1fr)_360px]">
        <div className="min-w-0 space-y-5">
          <Card className="p-5">
            <SectionHeader title="Change summary" />
            <div className="grid gap-4 sm:grid-cols-2">
              <KeyValue
                items={[
                  { label: "Action", value: <ActionBadge action={proposal.action_type} /> },
                  { label: "Mode", value: <span className="badge badge-neutral">{migration.execution_mode}</span> },
                  { label: "Effective slot", value: <span className="font-mono">#{proposal.effective_seq}</span> },
                  { label: "Raw sequence", value: <span className="font-mono">#{migration.seq_version}</span> },
                  { label: "Replaces", value: proposal.replaces_title ?? migration.replaces_title ?? "—" },
                  { label: "Irreversible", value: migration.is_irreversible ? <span className="badge badge-warning">yes</span> : <span className="badge badge-success">no</span> },
                ]}
              />
            </div>
            {proposal.reason ? (
              <Callout tone="neutral" title="Proposer note">
                {proposal.reason}
              </Callout>
            ) : null}
            {migration.description ? (
              <p className="mt-3 text-sm leading-relaxed text-muted">{migration.description}</p>
            ) : null}
          </Card>

          <div className="grid gap-4 2xl:grid-cols-2">
            <CodeBlock code={migration.sql_up} label="SQL Up (approved version)" maxHeight="22rem" />
            <CodeBlock
              code={migration.sql_down || "-- irreversible: no down SQL"}
              label="SQL Down"
              maxHeight="22rem"
            />
          </div>

          <section>
            <SectionHeader
              title="Approval matrix"
              description="Snapshot of groups with access when the proposal was submitted."
            />
            <TableWrap>
              <table className="table-base min-w-[760px]">
                <thead>
                  <tr>
                    <th className="th">Group</th>
                    <th className="th">Leader</th>
                    <th className="th">Access</th>
                    <th className="th">Decision</th>
                    <th className="th">Comment</th>
                    <th className="th">Decided</th>
                  </tr>
                </thead>
                <tbody>
                  {approvers.map((row) => (
                    <tr key={row.group_id} className="tr-hover">
                      <td className="td text-sm font-medium">{row.group_name}</td>
                      <td className="td">
                        <span className="inline-flex items-center gap-1.5 text-sm">
                          <ShieldCheck className="h-3.5 w-3.5 text-[#6d4b8f]" />
                          {row.leader_username}
                        </span>
                      </td>
                      <td className="td">
                        <span className={`badge ${row.access_level === "edit" ? "badge-success" : "badge-info"}`}>
                          {row.access_level}
                        </span>
                      </td>
                      <td className="td"><DecisionBadge row={row} /></td>
                      <td className="td max-w-[18rem] text-xs text-muted">{row.comment || "—"}</td>
                      <td className="td text-xs text-muted">{formatDateTime(row.decided_at)}</td>
                    </tr>
                  ))}
                  {approvers.length === 0 ? <TableEmpty colSpan={6}>No approver snapshot recorded.</TableEmpty> : null}
                </tbody>
              </table>
            </TableWrap>
          </section>
        </div>

        <aside className="min-w-0 space-y-4 xl:sticky xl:top-6 xl:self-start">
          <Card className="p-4">
            <p className="section-title">Quorum</p>
            <p className="mt-2 text-2xl font-semibold">
              {summary.approved}
              <span className="text-sm font-normal text-muted"> / {summary.required} approved</span>
            </p>
            <div className="mt-2">
              <ProgressBar
                value={percent(summary.approved, summary.required)}
                tone={summary.rejected > 0 ? "danger" : summary.pending === 0 ? "leaf" : "amber"}
                detail={`${summary.pending} pending`}
              />
            </div>
            <div className="mt-3 flex flex-wrap gap-1.5">
              <StatusBadge status={proposal.status} kind="proposal" />
              <span className={`badge ${dryRunBadge}`}>{dryRun.label}</span>
            </div>
          </Card>

          {reviewOpen ? (
            <Card className="p-4">
              <div className="flex items-center gap-2">
                <GitPullRequest className="h-4 w-4 text-leaf-600" />
                <p className="text-sm font-semibold">Your decision</p>
              </div>
              {can_approve ? (
                <>
                  <p className="mt-1 text-xs leading-relaxed text-muted">
                    You lead a required group. Comments are written to the immutable approval row.
                  </p>
                  <Field label="Comment (optional)" htmlFor="decision-comment" className="mt-3">
                    <Textarea
                      id="decision-comment"
                      value={comment}
                      onChange={(event) => setComment(event.target.value)}
                      placeholder="Why approve or reject?"
                      className="min-h-20"
                    />
                  </Field>
                  <div className="mt-3 grid grid-cols-2 gap-2">
                    <Button
                      variant="danger"
                      onClick={() => void decide("reject")}
                      loading={deciding === "reject"}
                      disabled={!dryRun.canReview}
                    >
                      <ThumbsDown className="h-4 w-4" /> Reject
                    </Button>
                    <Button
                      onClick={() => void decide("approve")}
                      loading={deciding === "approve"}
                      disabled={!dryRun.canReview}
                      title={dryRun.canReview ? undefined : "Dry-run failed; approval is blocked"}
                    >
                      <ThumbsUp className="h-4 w-4" /> Approve
                    </Button>
                  </div>
                  {deciding === "approve" ? (
                    <p className="mt-2 text-[10px] leading-relaxed text-muted">
                      The last approval checks the predecessor gate. If the predecessor is not ready,
                      the approval is rolled back and the proposal stays pending.
                    </p>
                  ) : null}
                </>
              ) : (
                <Callout tone="neutral" title="Not your action">
                  {canEdit
                    ? "Only the leader of a required group can approve or reject. Membership in the group is not enough."
                    : "You have View access. Approval requires being the leader of a required group."}
                </Callout>
              )}
            </Card>
          ) : (
            <Card className="p-4">
              <p className="section-title">Workflow state</p>
              <p className="mt-1 text-sm leading-relaxed text-muted">
                This proposal is <strong>{proposal.status.replaceAll("_", " ")}</strong>.{" "}
                {activeJobId ? (
                  <Link to={`/databases/${dbId}/jobs/${activeJobId}`} className="font-medium text-leaf-700 underline">
                    Open the job monitor
                  </Link>
                ) : proposal.status === "rejected" || proposal.status === "withdrawn" ? (
                  "The definition remains editable; submit a new proposal when ready."
                ) : null}
              </p>
            </Card>
          )}

          <Card className="p-4">
            <p className="section-title">Audit</p>
            <dl className="mt-3 space-y-2 text-xs">
              <div className="flex justify-between gap-2">
                <dt className="text-muted">Proposal</dt>
                <dd className="font-mono text-[10px]">{proposal.id}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-muted">Migration</dt>
                <dd className="font-mono text-[10px]">{proposal.migration_id}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-muted">Created</dt>
                <dd>{formatDateTime(proposal.created_at)}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-muted">Updated</dt>
                <dd>{formatDateTime(proposal.updated_at)}</dd>
              </div>
              {migration.replaces_migration_id ? (
                <div className="flex justify-between gap-2">
                  <dt className="text-muted">Replaces ID</dt>
                  <dd className="font-mono text-[10px]">{migration.replaces_migration_id}</dd>
                </div>
              ) : null}
            </dl>
          </Card>

          <Card className="p-4">
            <p className="section-title">Reviewer checklist</p>
            <ul className="mt-2 space-y-2 text-[11px] leading-relaxed text-muted">
              <li className="flex gap-2"><CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-leaf-600" /> SQL Up and Down use the same cursor scope in batch mode.</li>
              <li className="flex gap-2"><CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-leaf-600" /> The change is additive, or the rollback restores the previous value.</li>
              <li className="flex gap-2"><CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-leaf-600" /> effective_seq respects predecessor order.</li>
              <li className="flex gap-2"><CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-leaf-600" /> Approver snapshot matches the currently active groups.</li>
            </ul>
          </Card>
        </aside>
      </div>

      {!canEdit ? (
        <EmptyState
          icon={ShieldCheck}
          title="Read-only review"
          description="You can inspect SQL and approvals. Submitting decisions requires being the leader of a required group."
        />
      ) : null}
    </div>
  );
}
