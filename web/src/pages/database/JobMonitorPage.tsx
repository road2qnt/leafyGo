import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  AlertTriangle,
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  Clock,
  GitBranch,
  Hammer,
  History,
  Lock,
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
  CodeBlock,
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
import { formatDateTime, formatDuration, formatNumber, formatRelative, truncateMiddle } from "../../lib/format";
import { actionLabel, isActiveJob, JOB_STATUS_META } from "../../lib/status";
import { useDatabaseContext } from "./context";
import { ResolveUncertainModal } from "./ResolveUncertainModal";

type ProposalAction = Extract<ActionType, "batch_retry" | "batch_revert" | "rollback">;

const proposalCopy: Record<ProposalAction, { title: string; description: string; confirm: string }> = {
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
          <ArrowLeft className="h-3.5 w-3.5" /> Overview
        </Link>
        <ErrorBanner error={detailQuery.error} onRetry={() => void detailQuery.refetch()} />
      </div>
    );
  }

  const { job, attempts, progress, lock_state } = detailQuery.data;
  const meta = JOB_STATUS_META[job.status];
  const canEdit = overview.level === "edit";
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
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted">
        <Link to={`/databases/${dbId}/overview`} className="inline-flex items-center gap-1 font-medium text-leaf-700 hover:underline">
          <ArrowLeft className="h-3.5 w-3.5" /> Overview
        </Link>
        <span>/</span>
        <span className="font-mono text-[10px]">{job.job_id}</span>
        {isActiveJob(job.status) ? (
          <span className="ml-auto inline-flex items-center gap-1.5 text-[10px] text-leaf-600">
            <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-leaf-500" /> live polling 1.5s
          </span>
        ) : null}
      </div>

      <PageHeader
        eyebrow={`Job · ${actionLabel(job.action_type)} · ${job.execution_mode}`}
        title={
          <span className="inline-flex flex-wrap items-center gap-2">
            {job.migration_title}
            <StatusBadge status={job.status} />
          </span>
        }
        description={
          <>
            {meta.description} Queued {formatRelative(job.queued_at)} · worker{" "}
            <span className="font-mono">{job.worker_id ?? "unclaimed"}</span>
          </>
        }
        actions={
          <>
            <Button variant="secondary" onClick={() => void detailQuery.refetch()}>
              <RefreshCcw className="h-4 w-4" /> Refresh
            </Button>
            {canEdit && ["partial", "partially_reverted"].includes(job.status) ? (
              <Button variant="secondary" onClick={() => { setProposalAction("batch_retry"); setProposalError(null); }}>
                <RotateCcw className="h-4 w-4" /> Retry batches
              </Button>
            ) : null}
            {canEdit && ["partial", "partially_reverted"].includes(job.status) ? (
              <Button variant="secondary" onClick={() => { setProposalAction("batch_revert"); setProposalError(null); }}>
                <Undo2 className="h-4 w-4" /> Revert batches
              </Button>
            ) : null}
            {canEdit && job.status === "success" ? (
              <Button onClick={() => { setProposalAction("rollback"); setProposalError(null); }}>
                <Undo2 className="h-4 w-4" /> Propose rollback
              </Button>
            ) : null}
            {canEdit && job.status === "uncertain" ? (
              <Button variant="danger" onClick={() => setResolveOpen(true)}>
                <ShieldQuestion className="h-4 w-4" /> Resolve uncertain
              </Button>
            ) : null}
          </>
        }
      />

      {lock_state === "uncertain_hold" ? (
        <Callout tone="plum" title="UNCERTAIN_MANUAL_INTERVENTION">
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
        <Callout tone="danger" title="Last error" action={<CopyButton value={job.last_error} label="Copy" />}>
          <span className="break-words font-mono text-xs">{job.last_error}</span>
        </Callout>
      ) : null}

      <div className="grid min-w-0 gap-5 xl:grid-cols-[minmax(0,1fr)_380px]">
        <div className="min-w-0 space-y-5">
          <Card className="p-5">
            <SectionHeader
              title="Cumulative progress"
              description="Counts span retries for the root execution, so the bar never jumps backwards."
              actions={
                <span className="badge badge-neutral">
                  {job.retry_count}/{job.max_retries} retries
                </span>
              }
            />
            <ProgressBar
              value={progress.percent}
              tone={job.status === "uncertain" ? "plum" : job.status === "partial" ? "amber" : job.status === "failed" ? "danger" : "leaf"}
              label={`${progress.completed_batches} / ${progress.total_batches} batches`}
              detail={`${formatNumber(progress.committed_rows)} rows · ${progress.percent}%`}
            />
            <div className="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
              <div className="rounded-lg bg-[#f7f6f0] p-3">
                <p className="text-[10px] tracking-wider text-muted uppercase">Last cursor</p>
                <p className="mt-1 font-mono text-sm">{formatNumber(job.last_cursor)}</p>
              </div>
              <div className="rounded-lg bg-[#f7f6f0] p-3">
                <p className="text-[10px] tracking-wider text-muted uppercase">Scope</p>
                <p className="mt-1 font-mono text-xs">
                  {job.scope_min_cursor ?? "—"} … {job.scope_max_cursor ?? "—"}
                </p>
              </div>
              <div className="rounded-lg bg-[#f7f6f0] p-3">
                <p className="text-[10px] tracking-wider text-muted uppercase">Queue seq</p>
                <p className="mt-1 font-mono text-sm">#{job.queue_seq}</p>
              </div>
              <div className="rounded-lg bg-[#f7f6f0] p-3">
                <p className="text-[10px] tracking-wider text-muted uppercase">Batches</p>
                <p className="mt-1 font-mono text-sm">{job.total_batches}</p>
              </div>
            </div>
          </Card>

          <section>
            <SectionHeader
              title="Attempts in this root execution"
              description="Each retry/revert is its own execution row; increments are per attempt."
            />
            <TableWrap>
              <table className="table-base min-w-[900px]">
                <thead>
                  <tr>
                    <th className="th">Attempt</th>
                    <th className="th">Action</th>
                    <th className="th">Status</th>
                    <th className="th">Batches</th>
                    <th className="th">Rows</th>
                    <th className="th">Duration</th>
                    <th className="th">By</th>
                    <th className="th">Started</th>
                    <th className="th">Error</th>
                  </tr>
                </thead>
                <tbody>
                  {attempts.map((attempt) => (
                    <tr key={attempt.execution_id} className="tr-hover">
                      <td className="td">
                        <p className="font-mono text-[11px]">#{attempt.seq_order}</p>
                        <p className="font-mono text-[9px] text-muted" title={attempt.execution_id}>
                          {truncateMiddle(attempt.execution_id, 14)}
                        </p>
                      </td>
                      <td className="td"><ActionBadge action={attempt.action_type} /></td>
                      <td className="td"><StatusBadge status={attempt.status} /></td>
                      <td className="td font-mono text-xs">
                        {attempt.committed_batches}/{attempt.total_batches}
                      </td>
                      <td className="td font-mono text-xs">{formatNumber(attempt.committed_rows)}</td>
                      <td className="td text-xs">{formatDuration(attempt.duration_ms)}</td>
                      <td className="td text-xs">{attempt.executed_by_username}</td>
                      <td className="td text-xs text-muted">{formatDateTime(attempt.started_at)}</td>
                      <td className="td max-w-[18rem] truncate text-[11px] text-[#8b3323]" title={attempt.error_message}>
                        {attempt.error_message || "—"}
                      </td>
                    </tr>
                  ))}
                  {attempts.length === 0 ? <TableEmpty colSpan={9}>No attempt rows yet.</TableEmpty> : null}
                </tbody>
              </table>
            </TableWrap>
          </section>

          {job.status === "failed" && canEdit ? (
            <Callout tone="warning" title="Create a corrective migration">
              A failed predecessor blocks its successor with{" "}
              <code className="font-mono text-xs">PREDECESSOR_FAILED</code>. Define a replacement, then
              submit it for approval.{" "}
              <Link to={`/databases/${dbId}/migrations`} className="font-semibold underline">
                Open migrations
              </Link>
            </Callout>
          ) : null}
        </div>

        <aside className="min-w-0 space-y-4 xl:sticky xl:top-6 xl:self-start">
          <Card className="p-4">
            <p className="section-title">Job metadata</p>
            <div className="mt-3">
              <KeyValue
                items={[
                  { label: "Job", value: <span className="font-mono text-[10px]">{job.job_id}</span> },
                  { label: "Execution", value: <span className="font-mono text-[10px]">{job.execution_id}</span> },
                  { label: "Root", value: <span className="font-mono text-[10px]">{truncateMiddle(job.root_execution_id, 20)}</span> },
                  { label: "Queue seq", value: <span className="font-mono">#{job.queue_seq}</span> },
                  { label: "Worker", value: job.worker_id ?? "unclaimed" },
                  { label: "Lock", value: lock_state ? <StatusBadge status={lock_state} kind="lock" /> : <span className="badge badge-success">none</span> },
                  { label: "Queued", value: formatDateTime(job.queued_at) },
                  { label: "Started", value: formatDateTime(job.started_at) },
                  { label: "Finished", value: formatDateTime(job.finished_at) },
                  { label: "Updated", value: formatRelative(job.updated_at) },
                ]}
              />
            </div>
          </Card>

          {job.trace_id ? (
            <Card className="p-4">
              <p className="section-title">Trace correlation</p>
              <p className="mt-2 break-all font-mono text-[10px] text-muted">{job.trace_id}</p>
              <div className="mt-2 flex gap-2">
                <CopyButton value={job.trace_id} label="Copy trace id" />
                <span className="inline-flex items-center gap-1 text-[10px] text-muted">
                  <GitBranch className="h-3 w-3" /> Tempo / Loki
                </span>
              </div>
              <p className="mt-2 text-[10px] leading-relaxed text-muted">
                Every worker span and SQL log carries this trace id. Paste it in Grafana Tempo to open
                the waterfall.
              </p>
            </Card>
          ) : null}

          <Card className="p-4">
            <p className="section-title">Directions</p>
            <ul className="mt-2 space-y-2 text-[11px] leading-relaxed text-muted">
              <li className="flex gap-2"><Hammer className="mt-0.5 h-3.5 w-3.5 shrink-0 text-leaf-600" /> Forward: SQL Up from the next batch.</li>
              <li className="flex gap-2"><Undo2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-[#b38327]" /> Reverse: SQL Down from the highest batch.</li>
              <li className="flex gap-2"><Lock className="mt-0.5 h-3.5 w-3.5 shrink-0 text-[#6d4b8f]" /> Holds never expire by lease; they need an explicit resolution.</li>
            </ul>
          </Card>

          <Card className="p-4">
            <p className="section-title">History link</p>
            <Link to={`/databases/${dbId}/history`} className="mt-2 inline-flex items-center gap-1 text-xs font-medium text-leaf-700 hover:underline">
              <History className="h-3.5 w-3.5" /> See every attempt for this root
            </Link>
          </Card>
        </aside>
      </div>

      <Modal
        open={proposalAction !== null}
        onClose={() => { setProposalAction(null); setProposalError(null); }}
        title={proposalAction ? proposalCopy[proposalAction].title : "Request action"}
        description={proposalAction ? proposalCopy[proposalAction].description : undefined}
        footer={
          <>
            <Button variant="secondary" onClick={() => setProposalAction(null)} disabled={proposing}>
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
            Retry, revert, and rollback are proposals. Every required group leader must approve before
            a worker executes them.
          </Callout>
          <Field label="Reason / note for approvers" htmlFor="job-proposal-reason">
            <Textarea
              id="job-proposal-reason"
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              placeholder="Why is this the safe next step?"
            />
          </Field>
          <p className="font-mono text-[10px] text-muted">target_execution_id: {executionId}</p>
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
