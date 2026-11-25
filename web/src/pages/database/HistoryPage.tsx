import { useQuery } from "@tanstack/react-query";
import { Eye, Filter, History, RefreshCcw, RotateCcw } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router";

import { migrationApi, jobApi } from "../../api/endpoints";
import type { Execution } from "../../api/types";
import {
  ActionBadge,
  Button,
  Callout,
  CheckboxRow,
  CodeBlock,
  ConfirmDialog,
  ErrorBanner,
  KeyValue,
  Modal,
  PageHeader,
  PageLoader,
  SearchInput,
  StatusBadge,
  TableEmpty,
  TableWrap,
} from "../../components/ui";
import { useToast } from "../../hooks/useToast";
import {
  formatDateTime,
  formatDuration,
  formatNumber,
  truncateMiddle,
} from "../../lib/format";
import { useDatabaseContext } from "./context";

export default function HistoryPage() {
  const { dbId = "" } = useParams();
  const { overview } = useDatabaseContext();
  const navigate = useNavigate();
  const toast = useToast();
  const [hideRollback, setHideRollback] = useState(false);
  const [filter, setFilter] = useState("");
  const [selected, setSelected] = useState<Execution | null>(null);
  const [forwardTarget, setForwardTarget] = useState<Execution | null>(null);
  const [forwardError, setForwardError] = useState<unknown>(null);
  const [forwarding, setForwarding] = useState(false);

  useEffect(() => {
    setSelected(null);
    setFilter("");
    setForwardTarget(null);
  }, [dbId]);

  const canEdit = overview.level === "edit";

  const proposeForward = async () => {
    if (!forwardTarget) return;
    setForwarding(true);
    setForwardError(null);
    try {
      const proposal = await migrationApi.createProposal(dbId, {
        migration_id: forwardTarget.migration_id,
        action_type: "forward",
        target_execution_id: forwardTarget.execution_id,
        reason: "Forward after successful rollback from history.",
      });
      toast.success("Forward proposal submitted", "Leaders must approve before re-applying.");
      setForwardTarget(null);
      navigate(`/databases/${dbId}/proposals/${proposal.id}`);
    } catch (error) {
      setForwardError(error);
    } finally {
      setForwarding(false);
    }
  };

  const historyQuery = useQuery({
    queryKey: ["database", dbId, "history", hideRollback],
    queryFn: () => jobApi.history(dbId, hideRollback),
    refetchInterval: 10_000,
  });

  const raw = historyQuery.data?.executions ?? [];
  const executions = raw;
  const filtered = useMemo(
    () =>
      executions.filter(
        (execution) =>
          execution.migration_title_snapshot.toLowerCase().includes(filter.toLowerCase()) ||
          execution.executed_by_username.toLowerCase().includes(filter.toLowerCase()),
      ),
    [executions, filter],
  );

  if (historyQuery.isLoading) return <PageLoader label="Loading history" />;

  return (
    <div className="space-y-4">
      <PageHeader
        eyebrow="Audit trail"
        title="Migration history"
        description="Every apply, forward, retry, revert, rollback, and resolve attempt is retained. Rows are never deleted."
        actions={
          <Button variant="secondary" onClick={() => void historyQuery.refetch()}>
            <RefreshCcw className="h-4 w-4" /> Refresh
          </Button>
        }
      />

      {historyQuery.isError ? (
        <ErrorBanner error={historyQuery.error} onRetry={() => void historyQuery.refetch()} />
      ) : null}

      <div className="card flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex flex-1 flex-col gap-3 sm:flex-row sm:items-center">
          <SearchInput value={filter} onChange={setFilter} placeholder="Filter by migration or actor…" className="sm:w-72" />
          <span className="inline-flex items-center gap-1 text-[11px] text-muted">
            <Filter className="h-3.5 w-3.5" /> {filtered.length} of {executions.length} attempts
          </span>
        </div>
        <div className="sm:w-80">
          <CheckboxRow
            label="Hide rollback"
            description="Successful rollback/revert hides its precise target and the root batch group. Failed rows and resolve_uncertain are never hidden."
            checked={hideRollback}
            onChange={setHideRollback}
          />
        </div>
      </div>

      <Callout tone="neutral" title="Why attempts can repeat the same migration">
        A retry or revert is a new execution row with its own <code className="font-mono text-xs">execution_id</code>{" "}
        but the same <code className="font-mono text-xs">root_execution_id</code>. The checkpoint table is
        keyed by the root so batch numbers continue across attempts.
      </Callout>

      <TableWrap>
        <table className="table-base min-w-[1100px]">
          <thead>
            <tr>
              <th className="th">Seq</th>
              <th className="th">Migration</th>
              <th className="th">Action</th>
              <th className="th">Status</th>
              <th className="th">Attempt increment</th>
              <th className="th">Actor</th>
              <th className="th">Started</th>
              <th className="th">Duration</th>
              <th className="th">Root</th>
              <th className="th text-right">SQL</th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((execution) => (
              <tr key={execution.execution_id} className="tr-hover">
                <td className="td font-mono text-xs text-muted">#{execution.seq_order}</td>
                <td className="td max-w-[24rem]">
                  <p className="truncate text-sm font-medium text-ink">{execution.migration_title_snapshot}</p>
                  <div className="mt-1 flex flex-wrap items-center gap-1">
                    <span className="font-mono text-[10px] text-muted">
                      eff #{execution.effective_seq} · raw #{execution.seq_version} · rev {execution.migration_revision}
                    </span>
                    {execution.is_superseded ? (
                      <span className="badge badge-plum">Superseded by {execution.superseded_by_title}</span>
                    ) : null}
                  </div>
                </td>
                <td className="td"><ActionBadge action={execution.action_type} /></td>
                <td className="td"><StatusBadge status={execution.status} /></td>
                <td className="td">
                  <p className="font-mono text-xs">
                    {execution.committed_batches}/{execution.total_batches} batches
                  </p>
                  <p className="font-mono text-[10px] text-muted">
                    {formatNumber(execution.committed_rows)} rows
                  </p>
                </td>
                <td className="td text-xs">{execution.executed_by_username}</td>
                <td className="td text-xs text-muted">{formatDateTime(execution.started_at)}</td>
                <td className="td text-xs">{formatDuration(execution.duration_ms)}</td>
                <td className="td">
                  <span className="font-mono text-[10px] text-muted" title={execution.root_execution_id}>
                    {truncateMiddle(execution.root_execution_id, 14)}
                  </span>
                </td>
                <td className="td text-right">
                  <div className="flex justify-end gap-1">
                    {canEdit && execution.action_type === "rollback" && execution.status === "success" ? (
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => { setForwardTarget(execution); setForwardError(null); }}
                      >
                        <RotateCcw className="h-3.5 w-3.5" /> Forward
                      </Button>
                    ) : null}
                    <Button variant="ghost" size="sm" onClick={() => setSelected(execution)}>
                      <Eye className="h-3.5 w-3.5" /> View
                    </Button>
                  </div>
                </td>
              </tr>
            ))}
            {filtered.length === 0 ? (
              <TableEmpty colSpan={10}>
                {hideRollback
                  ? "Everything is hidden by the rollback filter. Untick it to inspect the full audit trail."
                  : "No executions recorded for this database yet."}
              </TableEmpty>
            ) : null}
          </tbody>
        </table>
      </TableWrap>

      <p className="text-[11px] text-muted">
        <History className="mr-1 inline h-3 w-3" />
        The server applies <code className="font-mono">?hide_rollback=true</code>; the SPA refetches
        with the filter rather than filtering a second time.
      </p>

      <Modal
        open={selected !== null}
        onClose={() => setSelected(null)}
        title={selected?.migration_title_snapshot ?? "Execution"}
        description={
          selected ? (
            <span className="inline-flex flex-wrap items-center gap-2">
              <StatusBadge status={selected.status} />
              <ActionBadge action={selected.action_type} />
              <span className="font-mono text-[10px]">{selected.execution_id}</span>
            </span>
          ) : undefined
        }
        size="lg"
      >
        {selected ? (
          <div className="space-y-4">
            <KeyValue
              items={[
                { label: "Executed by", value: <span className="font-mono text-[10px]">{selected.executed_by_username}</span> },
                { label: "Started", value: formatDateTime(selected.started_at) },
                { label: "Finished", value: formatDateTime(selected.finished_at) },
                { label: "Duration", value: formatDuration(selected.duration_ms) },
                { label: "Effective seq", value: <span className="font-mono">#{selected.effective_seq}</span> },
                { label: "Raw seq", value: <span className="font-mono">#{selected.seq_version}</span> },
                { label: "Revision", value: <span className="font-mono">{selected.migration_revision}</span> },
                { label: "Root execution", value: <span className="break-all font-mono text-[10px]">{selected.root_execution_id}</span> },
                {
                  label: "Rollback of",
                  value: selected.rollback_of_execution_id ? (
                    <span className="break-all font-mono text-[10px]">{selected.rollback_of_execution_id}</span>
                  ) : (
                    "—"
                  ),
                },
                { label: "Batches", value: `${selected.committed_batches} / ${selected.total_batches}` },
                { label: "Rows", value: formatNumber(selected.committed_rows) },
              ]}
            />

            {selected.error_message ? (
              <Callout tone="danger" title="Error">
                <span className="break-words font-mono text-xs">{selected.error_message}</span>
              </Callout>
            ) : null}

            <CodeBlock
              code={selected.executed_sql ?? "-- SQL snapshot is not available in this response.\n-- The full text is stored in migration_executions.executed_sql."}
              label="Executed SQL (immutable snapshot)"
              maxHeight="26rem"
            />
          </div>
        ) : null}
      </Modal>

      <ConfirmDialog
        open={forwardTarget !== null}
        onClose={() => { setForwardTarget(null); setForwardError(null); }}
        onConfirm={() => void proposeForward()}
        title="Propose forward after rollback?"
        description={
          <div className="space-y-3">
            <p>
              Forward re-applies <strong>{forwardTarget?.migration_title_snapshot}</strong> after its
              successful rollback. It still needs every required group leader to approve, and the
              backend must confirm linear effective_seq order.
            </p>
            {forwardError ? <ErrorBanner error={forwardError} /> : null}
          </div>
        }
        confirmLabel="Submit forward proposal"
        tone="primary"
        loading={forwarding}
      />
    </div>
  );
}
