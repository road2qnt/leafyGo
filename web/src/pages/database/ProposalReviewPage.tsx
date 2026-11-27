import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, ArrowRight, ThumbsDown, ThumbsUp, Trash2, Undo2 } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";

import { migrationApi } from "../../api/endpoints";
import type { ApprovalRow } from "../../api/types";
import { useAuth } from "../../auth/AuthContext";
import {
  ActionBadge,
  Button,
  Callout,
  Card,
  CodeBlock,
  ConfirmDialog,
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
import { formatDateTime, formatRelative, percent } from "../../lib/format";
import { useDatabaseContext } from "./context";

function DecisionBadge({ row }: { row: ApprovalRow }) {
  if (row.decision === "approved") {
    return <span className="badge badge-success">Approved</span>;
  }
  if (row.decision === "rejected") {
    return <span className="badge badge-danger">Rejected</span>;
  }
  return <span className="badge badge-warning">Pending</span>;
}

export default function ProposalReviewPage() {
  const { dbId = "", proposalId = "" } = useParams();
  const { overview } = useDatabaseContext();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();
  const { user } = useAuth();

  const detailQuery = useQuery({
    queryKey: ["database", dbId, "proposal", proposalId],
    queryFn: () => migrationApi.proposal(dbId, proposalId),
    refetchInterval: (query) => {
      const status = query.state.data?.proposal.status;
      return status &&
        ["queued", "running", "partial", "partially_reverted", "uncertain"].includes(status)
        ? 4_000
        : false;
    },
  });

  const [comment, setComment] = useState("");
  const [deciding, setDeciding] = useState<"approve" | "reject" | null>(null);
  const [decisionError, setDecisionError] = useState<unknown>(null);
  const [withdrawing, setWithdrawing] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [queuedJobID, setQueuedJobID] = useState<string | null>(null);

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
      const result =
        kind === "approve"
          ? await migrationApi.approve(dbId, proposalId, comment)
          : await migrationApi.reject(dbId, proposalId, comment);
      setComment("");
      await invalidate();
      if (result.job_id) setQueuedJobID(result.job_id);
      if (result.status === "queued") {
        toast.success("Quorum reached", "The job is queued in the per-database FIFO.");
      } else if (result.status === "rejected") {
        toast.info(
          "Proposal rejected",
          "The definition stays editable because execution never started.",
        );
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
      await migrationApi.deleteProposal(dbId, proposalId);
      queryClient.removeQueries({ queryKey: ["database", dbId, "proposal", proposalId] });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["database", dbId, "proposals"] }),
        queryClient.invalidateQueries({ queryKey: ["database", dbId, "migrations"] }),
        queryClient.invalidateQueries({ queryKey: ["database", dbId, "overview"] }),
      ]);
      toast.success("Proposal withdrawn", "The proposal was removed and all approvals were discarded.");
      navigate(`/databases/${dbId}/migrations`, { replace: true });
    } catch (error) {
      setDecisionError(error);
    } finally {
      setWithdrawing(false);
    }
  };

  const deleteProposal = async () => {
    setDeleting(true);
    setDecisionError(null);
    try {
      await migrationApi.deleteProposal(dbId, proposalId);
      setDeleteOpen(false);
      await invalidate();
      toast.success("Proposal deleted", "The proposal was removed.");
      navigate(`/databases/${dbId}/migrations`, { replace: true });
    } catch (error) {
      setDecisionError(error);
    } finally {
      setDeleting(false);
    }
  };

  if (detailQuery.isLoading) return <PageLoader label="Loading proposal" />;
  if (detailQuery.isError || !detailQuery.data) {
    return (
      <div className="space-y-4">
        <Link to={`/databases/${dbId}/migrations`} className="btn btn-ghost btn-sm w-fit">
          Migrations
        </Link>
        <ErrorBanner error={detailQuery.error} onRetry={() => void detailQuery.refetch()} />
      </div>
    );
  }

  const { proposal, migration, approvers, can_approve, can_withdraw } = detailQuery.data;
  const canApprove =
    can_approve ||
    approvers.some((row) => row.leader_id === user?.id && row.decision === null);
  const canWithdraw =
    can_withdraw ||
    (proposal.status === "pending_review" && proposal.proposed_by_username === user?.username);
  const canDelete =
    canEdit &&
    (proposal.status === "rejected" ||
      proposal.status === "withdrawn" ||
      proposal.status === "draft");
  const summary = proposal.approval_summary;
  const reviewOpen = proposal.status === "pending_review";
  const activeJobId = proposal.job_id ?? queuedJobID;

  return (
    <div className="space-y-6">
      <nav aria-label="Proposal location">
        <ol className="m-0 flex list-none flex-wrap items-center gap-2 p-0 text-sm">
          <li>
            <Link to={`/databases/${dbId}/migrations`} className="text-leaf-800 hover:text-leaf-900">
              Migrations & proposals
            </Link>
          </li>
          <li className="flex items-center gap-2">
            <span aria-hidden="true" className="text-muted">
              /
            </span>
            <span aria-current="page" className="font-mono font-semibold text-ink-soft">
              {proposal.id}
            </span>
          </li>
        </ol>
      </nav>

      <PageHeader
        title={
          <span className="inline-flex flex-wrap items-center gap-3">
            <span>{proposal.migration_title}</span>
            <StatusBadge status={proposal.status} kind="proposal" />
          </span>
        }
        description={
          <>
            Proposed by <strong>{proposal.proposed_by_username}</strong> · revision{" "}
            <span className="font-mono">{proposal.migration_revision}</span> · effective slot{" "}
            <span className="font-mono">#{proposal.effective_seq}</span> ·{" "}
            {formatRelative(proposal.created_at)}
          </>
        }
        actions={
          <>
            <Link to={`/databases/${dbId}/migrations`} className="btn btn-secondary">
              <ArrowLeft className="h-4 w-4" /> Back
            </Link>
            {activeJobId ? (
              <Link to={`/databases/${dbId}/jobs/${activeJobId}`} className="btn btn-primary">
                Open job monitor <ArrowRight className="h-4 w-4" />
              </Link>
            ) : canWithdraw ? (
              <Button variant="secondary" onClick={() => void withdraw()} loading={withdrawing}>
                <Undo2 className="h-4 w-4" /> Withdraw
              </Button>
            ) : canDelete ? (
              <Button
                variant="danger"
                onClick={() => {
                  setDecisionError(null);
                  setDeleteOpen(true);
                }}
              >
                <Trash2 className="h-4 w-4" /> Delete
              </Button>
            ) : null}
          </>
        }
      />

      {proposal.dry_run_status === "inconclusive" ? (
        <Callout tone="warning" title="Dry-run was inconclusive">
          The pre-review dry-run hit{" "}
          <code className="border border-leaf-200 bg-leaf-100 px-1 font-mono text-xs">
            statement_timeout
          </code>{" "}
          (57014) or{" "}
          <code className="border border-leaf-200 bg-leaf-100 px-1 font-mono text-xs">
            lock_not_available
          </code>{" "}
          (55P03). That is not a rejection; approvers may continue, but should inspect the SQL
          carefully.
        </Callout>
      ) : proposal.dry_run_status === "failed" ? (
        <Callout tone="danger" title="Dry-run failed — approval blocked">
          The saved revision failed validation. Fix the SQL and run the dry-run again before asking
          leaders to approve. The API must not queue a failed revision.
        </Callout>
      ) : null}

      {proposal.status === "partial" ||
        proposal.status === "partially_reverted" ||
        proposal.status === "uncertain" ? (
        <Callout tone="warning" title="Database is held by a non-terminal execution">
          A new migration on this database must wait. Retry/revert from the job monitor, or let the
          automatic evidence re-probe resolve uncertain commits.
        </Callout>
      ) : null}

      {decisionError ? (
        <ErrorBanner error={decisionError} onDismiss={() => setDecisionError(null)} />
      ) : null}

      <div className="split-layout min-w-0">
        <div className="min-w-0 space-y-6">
          <div className="panel">
            <div className="panel-header">
              <h2 className="m-0 text-base font-semibold text-ink">Change summary</h2>
              <ActionBadge action={proposal.action_type} />
            </div>
            <div className="card-pad space-y-4">
              <KeyValue
                items={[
                  { label: "Action", value: <ActionBadge action={proposal.action_type} /> },
                  {
                    label: "Mode",
                    value: <span className="badge badge-neutral">{migration.execution_mode}</span>,
                  },
                  {
                    label: "Effective slot",
                    value: <span className="font-mono">#{proposal.effective_seq}</span>,
                  },
                  {
                    label: "Raw sequence",
                    value: <span className="font-mono">#{migration.seq_version}</span>,
                  },
                  {
                    label: "Replaces",
                    value: proposal.replaces_title ?? migration.replaces_title ?? "—",
                  },
                  {
                    label: "Irreversible",
                    value: migration.is_irreversible ? (
                      <span className="badge badge-warning">yes</span>
                    ) : (
                      <span className="badge badge-success">no</span>
                    ),
                  },
                ]}
              />
              {proposal.reason ? (
                <Callout tone="neutral" title="Proposer note">
                  {proposal.reason}
                </Callout>
              ) : null}
              {migration.description ? (
                <p className="m-0 text-sm leading-relaxed text-ink-soft">{migration.description}</p>
              ) : null}
            </div>
          </div>

          <div className="grid gap-6 xl:grid-cols-2">
            <CodeBlock
              code={migration.sql_up}
              label="SQL Up (approved version)"
              maxHeight="22rem"
            />
            <CodeBlock
              code={migration.sql_down || "-- irreversible: no down SQL"}
              label="SQL Down"
              maxHeight="22rem"
            />
          </div>

          <section aria-labelledby="approvals-caption">
            <SectionHeader
              title="Approval matrix"
              description="Snapshot of groups with access when the proposal was submitted."
            />
            <TableWrap>
              <table className="table-base table-stack">
                <caption id="approvals-caption">
                  {summary.approved} of {summary.required} required leader approvals
                </caption>
                <thead>
                  <tr>
                    <th scope="col" className="th">Group</th>
                    <th scope="col" className="th">Leader</th>
                    <th scope="col" className="th">Access</th>
                    <th scope="col" className="th">Decision</th>
                    <th scope="col" className="th">Comment</th>
                    <th scope="col" className="th">Decided</th>
                  </tr>
                </thead>
                <tbody>
                  {approvers.map((row) => (
                    <tr key={row.group_id}>
                      <th scope="row" data-label="Group" className="td cell-title font-semibold">
                        {row.group_name}
                      </th>
                      <td data-label="Leader" className="td font-semibold text-ink">{row.leader_username}</td>
                      <td data-label="Access" className="td">
                        <span
                          className={`badge ${row.access_level === "edit" ? "badge-success" : "badge-info"
                            }`}
                        >
                          {row.access_level === "edit" ? "View + Edit" : "View Only"}
                        </span>
                      </td>
                      <td data-label="Decision" className="td">
                        <DecisionBadge row={row} />
                      </td>
                      <td data-label="Comment" className="td cell-wide max-w-[18rem] text-sm text-ink-soft">
                        {row.comment || "—"}
                      </td>
                      <td data-label="Decided" className="td text-muted">
                        {formatDateTime(row.decided_at)}
                      </td>
                    </tr>
                  ))}
                  {approvers.length === 0 ? (
                    <TableEmpty colSpan={6}>No approver snapshot recorded.</TableEmpty>
                  ) : null}
                </tbody>
              </table>
            </TableWrap>
          </section>
        </div>

        <aside className="aside-stack lg:sticky lg:top-20 lg:self-start">
          <Card className="p-4 sm:p-5">
            <h2 className="section-title">Quorum</h2>
            <p className="m-0 mt-2 text-2xl font-bold text-ink">
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
            <div className="mt-3 flex flex-wrap gap-2">
              <StatusBadge status={proposal.status} kind="proposal" />
            </div>
          </Card>

          {reviewOpen ? (
            <Card className="p-4 sm:p-5">
              <h2 className="section-title mb-2">Your decision</h2>
              {canApprove ? (
                <>
                  <p className="m-0 text-sm leading-relaxed text-ink-soft">
                    You lead a required group. Comments are written to the immutable approval row.
                  </p>
                  <Field label="Comment" htmlFor="decision-comment" className="mt-3">
                    <Textarea
                      id="decision-comment"
                      value={comment}
                      onChange={(event) => setComment(event.target.value)}
                      placeholder="Why approve or reject?"
                      className="min-h-20"
                    />
                  </Field>
                  <div className="mt-4 grid grid-cols-2 gap-2">
                    <Button
                      variant="danger"
                      onClick={() => void decide("reject")}
                      loading={deciding === "reject"}
                    >
                      <ThumbsDown className="h-4 w-4" /> Reject
                    </Button>
                    <Button
                      onClick={() => void decide("approve")}
                      loading={deciding === "approve"}
                    >
                      <ThumbsUp className="h-4 w-4" /> Approve
                    </Button>
                  </div>
                </>
              ) : (
                <Callout tone="neutral" title="Not your action">
                  {canEdit
                    ? "Only the leader of a required group can approve or reject. "
                    : "You have View Only access. Approval requires being the leader of a required group."}
                </Callout>
              )}
            </Card>
          ) : (
            <Card className="p-4 sm:p-5">
              <h2 className="section-title">Workflow state</h2>
              <p className="m-0 mt-2 text-sm leading-relaxed text-ink-soft">
                This proposal is <strong>{proposal.status.replaceAll("_", " ")}</strong>.{" "}
                {activeJobId ? (
                  <Link
                    to={`/databases/${dbId}/jobs/${activeJobId}`}
                    className="font-semibold text-leaf-800 underline"
                  >
                    Open the job monitor
                  </Link>
                ) : proposal.status === "rejected" || proposal.status === "withdrawn" ? (
                  "The definition remains editable; submit a new proposal when ready."
                ) : null}
              </p>
            </Card>
          )}

          <Card className="p-4 sm:p-5">
            <h2 className="section-title">Audit</h2>
            <dl className="m-0 mt-3 grid gap-2 text-sm">
              <div className="flex justify-between gap-2">
                <dt className="font-semibold text-ink-soft">Proposal</dt>
                <dd className="m-0 min-w-0 font-mono text-xs break-all">{proposal.id}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="font-semibold text-ink-soft">Migration</dt>
                <dd className="m-0 min-w-0 font-mono text-xs break-all">{proposal.migration_id}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="font-semibold text-ink-soft">Created</dt>
                <dd className="m-0">{formatDateTime(proposal.created_at)}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="font-semibold text-ink-soft">Updated</dt>
                <dd className="m-0">{formatDateTime(proposal.updated_at)}</dd>
              </div>
              {migration.replaces_migration_id ? (
                <div className="flex justify-between gap-2">
                  <dt className="font-semibold text-ink-soft">Replaces ID</dt>
                  <dd className="m-0 min-w-0 font-mono text-xs break-all">{migration.replaces_migration_id}</dd>
                </div>
              ) : null}
            </dl>
          </Card>
        </aside>
      </div>

      <ConfirmDialog
        open={deleteOpen}
        onClose={() => {
          setDeleteOpen(false);
          setDecisionError(null);
        }}
        onConfirm={() => void deleteProposal()}
        title="Delete proposal?"
        description={
          <div className="space-y-2">
            <p className="m-0">
              Permanently delete this <strong>{proposal.status.replaceAll("_", " ")}</strong>{" "}
              proposal for <strong>{proposal.migration_title}</strong>? This cannot be undone.
            </p>
            {decisionError ? <ErrorBanner error={decisionError} /> : null}
          </div>
        }
        confirmLabel="Delete proposal"
        tone="danger"
        loading={deleting}
      />
    </div>
  );
}
