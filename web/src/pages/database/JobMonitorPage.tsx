import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  History,
  RefreshCcw,
  RotateCcw,
  ShieldQuestion,
  Undo2,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";

import { jobApi, migrationApi } from "../../api/endpoints";
import type { ActionType } from "../../api/types";
import {
  ActionBadge,
  Button,
  Callout,
  Card,
  CopyButton,
  ErrorBanner,
  Field,
  KeyValue,
  Modal,
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
import {
  formatDateTime,
  formatDuration,
  formatNumber,
  formatRelative,
  truncateMiddle,
} from "../../lib/format";
import { jobActions } from "../../lib/jobActions";
import { actionLabel, isActiveJob, JOB_STATUS_META } from "../../lib/status";
import { useDatabaseContext } from "./context";
import { ResolveUncertainModal } from "./ResolveUncertainModal";

type ProposalAction = Extract<ActionType, "batch_retry" | "batch_revert" | "rollback" | "forward">;

const proposalCopy: Record<ProposalAction, { title: string; description: string; confirm: string }> =
  {
    batch_retry: {
      title: "Retry from the last checkpoint",
      description:
        "The worker resumes forward from the highest committed batch. It never re-runs batches that already have a checkpoint.",
      confirm: "Request batch retry",
    },
    batch_revert: {
      title: "Revert committed batches",
      description:
        "The worker runs SQL Down from the highest committed batch downward. Unsafe reverts are rejected and require a corrective migration.",
      confirm: "Request batch revert",
    },
    rollback: {
      title: "Rollback this migration",
      description:
        "LIFO rollback is only allowed when this migration is the highest active effective_seq. The rollback still needs leader approval.",
      confirm: "Request rollback",
    },
    forward: {
      title: "Forward this migration again",
      description:
        "A forward proposal re-applies a migration whose latest successful attempt was a normal rollback. It must respect linear effective_seq order.",
      confirm: "Request forward",
    },
  };

export default function JobMonitorPage() {
  const { dbId = "", jobId = "" } = useParams();
  const { overview } = useDatabaseContext();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();

  const detailQuery = useQuery({
    queryKey: ["database", dbId, "job", jobId],
    queryFn: () => jobApi.detail(dbId, jobId),
    refetchInterval: (query) => {
      const status = query.state.data?.job.status;
      return status && isActiveJob(status) ? 1_500 : false;
    },
  });

  const [proposalAction, setProposalAction] = useState<ProposalAction | null>(null);
  const [reason, setReason] = useState("");
  const [proposalError, setProposalError] = useState<unknown>(null);
  const [proposing, setProposing] = useState(false);
  const [resolveOpen, setResolveOpen] = useState(false);

  const terminalStatus = useRef<string | null>(null);

  useEffect(() => {
    const status = detailQuery.data?.job.status;
    if (!status) return;
    if (["success", "failed"].includes(status) && terminalStatus.current !== status) {
      terminalStatus.current = status;
      void queryClient.invalidateQueries({ queryKey: ["database", dbId, "overview"] });
      void queryClient.invalidateQueries({ queryKey: ["database", dbId, "history"] });
      void queryClient.invalidateQueries({ queryKey: ["database", dbId, "migrations"] });
      void queryClient.invalidateQueries({ queryKey: ["database", dbId, "proposals"] });
      void queryClient.invalidateQueries({ queryKey: ["databases"] });
    }
  }, [detailQuery.data?.job.status, dbId, queryClient]);

  if (detailQuery.isLoading) return <PageLoader label="Loading job" />;
  if (detailQuery.isError || !detailQuery.data) {
    return (
      <div className="space-y-4">
        <Link to={`/databases/${dbId}/overview`} className="btn btn-ghost btn-sm w-fit">
          Overview
        </Link>
        <ErrorBanner error={detailQuery.error} onRetry={() => void detailQuery.refetch()} />
      </div>
    );
  }

  const { job, attempts, progress, lock_state } = detailQuery.data;
  const meta = JOB_STATUS_META[job.status];
  const canEdit = overview.level === "edit";
  const actions = jobActions(job.status, lock_state, job.action_type);
  const executionId = job.execution_id || attempts[0]?.execution_id;

  const propose = async () => {
    if (!proposalAction) return;
    setProposing(true);
    setProposalError(null);
    try {
      const proposal = await migrationApi.createProposal(dbId, {
        migration_id: job.migration_id,
        action_type: proposalAction,
        target_execution_id: executionId,
        reason,
      });
      toast.success("Proposal submitted", "Leaders must approve before the worker acts.");
      setProposalAction(null);
      setReason("");
      navigate(`/databases/${dbId}/proposals/${proposal.id}`);
    } catch (error) {
      setProposalError(error);
    } finally {
      setProposing(false);
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <nav aria-label="Job location">
          <ol className="m-0 flex list-none flex-wrap items-center gap-2 p-0 text-sm">
            <li>
              <Link to={`/databases/${dbId}/overview`} className="text-leaf-800 hover:text-leaf-900">
                Overview
              </Link>
            </li>
            <li className="flex items-center gap-2">
              <span aria-hidden="true" className="text-muted">
                /
              </span>
              <span aria-current="page" className="font-mono font-semibold text-ink-soft">
                {job.job_id}
              </span>
            </li>
          </ol>
        </nav>

        {isActiveJob(job.status) ? (
          <span className="badge badge-info">Live polling 1.5s</span>
        ) : null}
      </div>

      <PageHeader
        title={
          <span className="inline-flex flex-wrap items-center gap-3">
            <span>{job.migration_title}</span>
            <StatusBadge status={job.status} />
          </span>
        }
        description={
          <>
            {actionLabel(job.action_type)} ({job.execution_mode}) · {meta.description} Queued{" "}
            {formatRelative(job.queued_at)} · worker{" "}
            <span className="font-mono">{job.worker_id ?? "unclaimed"}</span>
          </>
        }
        actions={
          <>
            <Button variant="secondary" onClick={() => void detailQuery.refetch()}>
              <RefreshCcw className="h-4 w-4" /> Refresh
            </Button>
            {canEdit && actions.retry ? (
              <Button
                variant="secondary"
                onClick={() => {
                  setProposalAction("batch_retry");
                  setProposalError(null);
                }}
              >
                <RotateCcw className="h-4 w-4" /> Retry batches
              </Button>
            ) : null}
            {canEdit && actions.revert ? (
              <Button
                variant="secondary"
                onClick={() => {
                  setProposalAction("batch_revert");
                  setProposalError(null);
                }}
              >
                <Undo2 className="h-4 w-4" /> Revert batches
              </Button>
            ) : null}
            {canEdit && actions.rollback ? (
              <Button
                onClick={() => {
                  setProposalAction("rollback");
                  setProposalError(null);
                }}
              >
                <Undo2 className="h-4 w-4" /> Propose rollback
              </Button>
            ) : null}
            {canEdit && actions.forward ? (
              <Button
                onClick={() => {
                  setProposalAction("forward");
                  setProposalError(null);
                }}
              >
                <RotateCcw className="h-4 w-4" /> Propose forward
              </Button>
            ) : null}
            {canEdit && actions.resolve ? (
              <Button variant="danger" onClick={() => setResolveOpen(true)}>
                <ShieldQuestion className="h-4 w-4" /> Resolve uncertain
              </Button>
            ) : null}
          </>
        }
      />

      {lock_state === "uncertain_hold" ? (
        <Callout tone="warning" title="UNCERTAIN_MANUAL_INTERVENTION">
          The old target session could not be proven dead. The database remains locked. Automatic
          re-probe runs every 15s; manual resolution is the optional exit and evidence wins.
        </Callout>
      ) : lock_state === "partial_hold" ? (
        <Callout tone="warning" title="Partial hold">
          Committed batches remain in the target. Retry resumes at the checkpoint; revert walks
          downward. A new migration cannot queue for this database until the hold clears.
        </Callout>
      ) : lock_state === "partially_reverted_hold" ? (
        <Callout tone="warning" title="Partially reverted hold">
          Resume the reverse direction only. Do not run SQL Up while reverting.
        </Callout>
      ) : null}

      {job.last_error ? (
        <Callout
          tone="danger"
          title="Last error"
          action={<CopyButton value={job.last_error} label="Copy" />}
        >
          <span className="font-mono text-xs break-words">{job.last_error}</span>
        </Callout>
      ) : null}

      <div className="split-layout min-w-0">
        <div className="min-w-0 space-y-6">
          <div className="panel">
            <div className="panel-header">
              <h2 className="m-0 text-base font-semibold text-ink">Cumulative progress</h2>
              <span className="badge badge-neutral font-mono">
                {job.retry_count}/{job.max_retries} retries
              </span>
            </div>
            <div className="card-pad space-y-4">
              <ProgressBar
                value={progress.percent}
                tone={
                  job.status === "uncertain" || job.status === "partial"
                    ? "amber"
                    : job.status === "failed"
                      ? "danger"
                      : "leaf"
                }
                label={`${progress.completed_batches} / ${progress.total_batches} batches`}
                detail={`${formatNumber(progress.committed_rows)} rows · ${progress.percent}%`}
              />
              <dl className="m-0 grid grid-cols-2 gap-4 border-t border-line-soft pt-4 sm:grid-cols-4">
                <div>
                  <dt className="text-xs font-semibold tracking-wider text-ink-soft uppercase">
                    Last cursor
                  </dt>
                  <dd className="m-0 mt-1 font-mono text-sm font-semibold tabular-nums">
                    {formatNumber(job.last_cursor)}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs font-semibold tracking-wider text-ink-soft uppercase">
                    Scope
                  </dt>
                  <dd className="m-0 mt-1 font-mono text-xs tabular-nums">
                    {job.scope_min_cursor ?? "—"} … {job.scope_max_cursor ?? "—"}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs font-semibold tracking-wider text-ink-soft uppercase">
                    Queue seq
                  </dt>
                  <dd className="m-0 mt-1 font-mono text-sm font-semibold tabular-nums">
                    #{job.queue_seq}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs font-semibold tracking-wider text-ink-soft uppercase">
                    Batches
                  </dt>
                  <dd className="m-0 mt-1 font-mono text-sm font-semibold tabular-nums">
                    {job.total_batches}
                  </dd>
                </div>
              </dl>
            </div>
          </div>

          <section aria-labelledby="attempts-caption">
            <SectionHeader
              title="Attempts in this root execution"
              description="Each retry/revert is its own execution row; increments are per attempt."
            />
            <TableWrap>
              <table className="table-base table-stack table-compact">
                <caption id="attempts-caption">
                  {attempts.length} {attempts.length === 1 ? "attempt" : "attempts"}
                </caption>
                <thead>
                  <tr>
                    <th scope="col" className="th text-right">Attempt</th>
                    <th scope="col" className="th">Action</th>
                    <th scope="col" className="th">Status</th>
                    <th scope="col" className="th text-right">Batches</th>
                    <th scope="col" className="th text-right">Rows</th>
                    <th scope="col" className="th text-right">Duration</th>
                    <th scope="col" className="th">Executed by</th>
                    <th scope="col" className="th">Started</th>
                    <th scope="col" className="th">Error</th>
                  </tr>
                </thead>
                <tbody>
                  {attempts.map((attempt) => (
                    <tr key={attempt.execution_id}>
                      <th scope="row" data-label="Attempt" className="td text-right font-mono tabular-nums">
                        <div>
                          <span className="font-semibold">#{attempt.seq_order}</span>
                          <span
                            className="block text-xs font-normal text-muted"
                            title={attempt.execution_id}
                          >
                            {truncateMiddle(attempt.execution_id, 14)}
                          </span>
                        </div>
                      </th>
                      <td data-label="Action" className="td">
                        <ActionBadge action={attempt.action_type} />
                      </td>
                      <td data-label="Status" className="td">
                        <StatusBadge status={attempt.status} />
                      </td>
                      <td data-label="Batches" className="td text-right font-mono tabular-nums">
                        {attempt.committed_batches}/{attempt.total_batches}
                      </td>
                      <td data-label="Rows" className="td text-right font-mono tabular-nums">
                        {formatNumber(attempt.committed_rows)}
                      </td>
                      <td data-label="Duration" className="td text-right tabular-nums">
                        {formatDuration(attempt.duration_ms)}
                      </td>
                      <td data-label="Executed by" className="td">{attempt.executed_by_username}</td>
                      <td data-label="Started" className="td col-date text-muted">
                        {formatDateTime(attempt.started_at)}
                      </td>
                      <td
                        data-label="Error"
                        className="td cell-wide max-w-[18rem] truncate font-mono text-xs text-crimson"
                        title={attempt.error_message}
                      >
                        {attempt.error_message || <span className="text-muted">—</span>}
                      </td>
                    </tr>
                  ))}
                  {attempts.length === 0 ? (
                    <TableEmpty colSpan={9}>No attempt rows yet.</TableEmpty>
                  ) : null}
                </tbody>
              </table>
            </TableWrap>
          </section>

          {job.status === "failed" && canEdit ? (
            <Callout tone="warning" title="Create a corrective migration">
              A failed predecessor blocks its successor with{" "}
              <code className="border border-leaf-200 bg-leaf-100 px-1 font-mono text-xs">
                PREDECESSOR_FAILED
              </code>
              . Define a replacement, then submit it for approval.{" "}
              <Link
                to={`/databases/${dbId}/migrations`}
                className="font-semibold underline"
              >
                Open migrations
              </Link>
            </Callout>
          ) : null}
        </div>

        <aside className="aside-stack lg:sticky lg:top-20 lg:self-start">
          <Card className="p-4 sm:p-5">
            <h2 className="section-title">Job metadata</h2>
            <div className="mt-3">
              <KeyValue
                items={[
                  {
                    label: "Job",
                    value: <span className="font-mono text-xs">{job.job_id}</span>,
                  },
                  {
                    label: "Execution",
                    value: <span className="font-mono text-xs">{job.execution_id}</span>,
                  },
                  {
                    label: "Root",
                    value: (
                      <span className="font-mono text-xs">
                        {truncateMiddle(job.root_execution_id, 20)}
                      </span>
                    ),
                  },
                  {
                    label: "Queue seq",
                    value: <span className="font-mono">#{job.queue_seq}</span>,
                  },
                  {
                    label: "Worker",
                    value: <span className="font-mono text-xs">{job.worker_id ?? "unclaimed"}</span>,
                  },
                  {
                    label: "Lock",
                    value: lock_state ? (
                      <StatusBadge status={lock_state} kind="lock" />
                    ) : (
                      <span className="badge badge-success">none</span>
                    ),
                  },
                  { label: "Queued", value: formatDateTime(job.queued_at) },
                  { label: "Started", value: formatDateTime(job.started_at) },
                  { label: "Finished", value: formatDateTime(job.finished_at) },
                  { label: "Updated", value: formatRelative(job.updated_at) },
                ]}
              />
            </div>
          </Card>

          {job.trace_id ? (
            <Card className="p-4 sm:p-5">
              <h2 className="section-title">Trace correlation</h2>
              <p className="m-0 mt-2 font-mono text-xs break-all text-ink">{job.trace_id}</p>
              <div className="mt-3 flex flex-wrap items-center gap-2">
                <CopyButton value={job.trace_id} label="Copy trace id" />
                <span className="badge badge-muted">Tempo / Loki</span>
              </div>
              <p className="m-0 mt-2 text-xs leading-relaxed text-muted">
                Every worker span and SQL log carries this trace id. Paste it in Grafana Tempo to
                open the waterfall.
              </p>
            </Card>
          ) : null}

          <Card className="p-4 sm:p-5">
            <h2 className="section-title">Directions</h2>
            <ul className="m-0 mt-2 space-y-2 pl-5 text-sm leading-relaxed text-ink-soft">
              <li>
                <strong>Forward:</strong> SQL Up from the next batch.
              </li>
              <li>
                <strong>Reverse:</strong> SQL Down from the highest batch.
              </li>
              <li>
                <strong>Holds:</strong> Never expire by lease; they require an explicit resolution.
              </li>
            </ul>
          </Card>

          <Card className="p-4 sm:p-5">
            <h2 className="section-title mb-2">History link</h2>
            <Link to={`/databases/${dbId}/history`} className="btn btn-sm">
              <History className="h-3.5 w-3.5" /> View full migration history
            </Link>
          </Card>
        </aside>
      </div>

      <Modal
        open={proposalAction !== null}
        onClose={() => {
          setProposalAction(null);
          setProposalError(null);
        }}
        title={proposalAction ? proposalCopy[proposalAction].title : "Request action"}
        description={proposalAction ? proposalCopy[proposalAction].description : undefined}
        footer={
          <>
            <Button
              variant="secondary"
              onClick={() => setProposalAction(null)}
              disabled={proposing}
            >
              Cancel
            </Button>
            <Button onClick={() => void propose()} loading={proposing}>
              {proposalAction ? proposalCopy[proposalAction].confirm : "Submit"}
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          {proposalError ? <ErrorBanner error={proposalError} /> : null}
          <Callout tone="info" title="Approval still required">
            Retry, revert, and rollback are proposals. Every required group leader must approve
            before a worker executes them.
          </Callout>
          <Field label="Reason / note for approvers" htmlFor="job-proposal-reason">
            <Textarea
              id="job-proposal-reason"
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              placeholder="Why is this the safe next step?"
            />
          </Field>
          <p className="m-0 font-mono text-xs text-muted">target_execution_id: {executionId}</p>
        </div>
      </Modal>

      <ResolveUncertainModal
        open={resolveOpen}
        onClose={() => setResolveOpen(false)}
        dbId={dbId}
        migrationId={job.migration_id}
        executionId={executionId ?? ""}
      />
    </div>
  );
}
