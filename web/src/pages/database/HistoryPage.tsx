import { useQuery } from "@tanstack/react-query";
import { RefreshCcw, RotateCcw, X } from "lucide-react";
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
  PageLoader,
  Pagination,
  SearchInput,
  StatusBadge,
  TableEmpty,
  TableWrap,
} from "../../components/ui";
import { reveal } from "../../components/reveal";
import { usePagination } from "../../hooks/usePagination";
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

  const executions = historyQuery.data?.executions ?? [];
  const filtered = useMemo(
    () =>
      executions.filter(
        (execution) =>
          execution.migration_title_snapshot.toLowerCase().includes(filter.toLowerCase()) ||
          execution.executed_by_username.toLowerCase().includes(filter.toLowerCase()),
      ),
    [executions, filter],
  );

  const historyPage = usePagination(filtered, {
    resetKey: `${dbId}|${hideRollback}|${filter}`,
  });

  if (historyQuery.isLoading) return <PageLoader label="Loading history" />;

  return (
    <div className="space-y-6">
      {historyQuery.isError ? (
        <ErrorBanner error={historyQuery.error} onRetry={() => void historyQuery.refetch()} />
      ) : null}

      <div className="card flex flex-col gap-4 p-4 sm:p-5 lg:flex-row lg:items-center lg:justify-between">
        <div className="flex flex-1 flex-col gap-3 sm:flex-row sm:items-center">
          <SearchInput
            value={filter}
            onChange={setFilter}
            placeholder="Filter by migration or actor…"
            className="sm:w-72"
          />
          <span className="text-sm text-muted">
            Showing <strong className="text-ink">{filtered.length}</strong> of{" "}
            <strong className="text-ink">{executions.length}</strong> attempts
          </span>
        </div>
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="min-w-0 flex-1 basis-56 lg:max-w-xs">
            <CheckboxRow
              label="Hide rollback"
              description="Hides successful rollback/revert pairs and root batch groups. Failed rows and resolve_uncertain remain visible."
              checked={hideRollback}
              onChange={setHideRollback}
            />
          </div>
          <Button variant="secondary" size="sm" onClick={() => void historyQuery.refetch()}>
            <RefreshCcw className="h-3.5 w-3.5" /> Refresh
          </Button>
        </div>
      </div>

      {selected ? (
        <section id="execution-detail" className="space-y-3" aria-labelledby="detail-title">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <h2 id="detail-title" className="m-0 flex flex-wrap items-center gap-2 text-xl font-semibold text-ink">
              <span className="font-mono">#{selected.seq_order}</span>
              <span>{selected.migration_title_snapshot}</span>
            </h2>
            <div className="flex flex-wrap items-center gap-2">
              <ActionBadge action={selected.action_type} />
              <StatusBadge status={selected.status} />
              <Button variant="ghost" size="sm" onClick={() => setSelected(null)}>
                <X className="h-4 w-4" /> Close detail
              </Button>
            </div>
          </div>

          <div className="panel">
            <div className="card-pad space-y-5">
              {selected.status === "failed" ? (
                <Callout tone="danger" title="Migration failed">
                  <span className="font-mono text-xs break-words">{selected.error_message}</span>
                </Callout>
              ) : selected.status === "success" ? (
                <Callout tone="success">The transaction was committed.</Callout>
              ) : (
                <Callout tone="warning">
                  This execution is in state <strong>{selected.status}</strong>.
                </Callout>
              )}

              <dl className="m-0 grid grid-cols-2 gap-x-4 gap-y-3 sm:gap-x-6 lg:grid-cols-4 [&>div]:min-w-0 [&_dd]:break-words">
                <div>
                  <dt className="text-xs font-semibold tracking-wider text-ink-soft uppercase">
                    Executed by
                  </dt>
                  <dd className="m-0 mt-1 text-sm text-ink">{selected.executed_by_username}</dd>
                </div>
                <div>
                  <dt className="text-xs font-semibold tracking-wider text-ink-soft uppercase">
                    Started
                  </dt>
                  <dd className="m-0 mt-1 text-sm text-ink">
                    {formatDateTime(selected.started_at)}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs font-semibold tracking-wider text-ink-soft uppercase">
                    Finished
                  </dt>
                  <dd className="m-0 mt-1 text-sm text-ink">
                    {formatDateTime(selected.finished_at)}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs font-semibold tracking-wider text-ink-soft uppercase">
                    Duration
                  </dt>
                  <dd className="m-0 mt-1 font-mono text-sm tabular-nums">
                    {formatDuration(selected.duration_ms)}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs font-semibold tracking-wider text-ink-soft uppercase">
                    Effective / Raw seq
                  </dt>
                  <dd className="m-0 mt-1 font-mono text-sm">
                    eff #{selected.effective_seq} · raw #{selected.seq_version} · rev{" "}
                    {selected.migration_revision}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs font-semibold tracking-wider text-ink-soft uppercase">
                    Batches & rows
                  </dt>
                  <dd className="m-0 mt-1 font-mono text-sm tabular-nums">
                    {selected.committed_batches} / {selected.total_batches} batches ·{" "}
                    {formatNumber(selected.committed_rows)} rows
                  </dd>
                </div>
                <div>
                  <dt className="text-xs font-semibold tracking-wider text-ink-soft uppercase">
                    Root execution
                  </dt>
                  <dd className="m-0 mt-1 font-mono text-xs break-all text-ink-soft">
                    {selected.root_execution_id}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs font-semibold tracking-wider text-ink-soft uppercase">
                    Rollback of
                  </dt>
                  <dd className="m-0 mt-1 font-mono text-xs break-all text-ink-soft">
                    {selected.rollback_of_execution_id ?? "—"}
                  </dd>
                </div>
              </dl>

              <CodeBlock
                code={
                  selected.executed_sql ??
                  "-- SQL snapshot is not available in this response.\n-- The full text is stored in migration_executions.executed_sql."
                }
                label="Executed SQL (immutable snapshot)"
                maxHeight="24rem"
              />
            </div>
          </div>
        </section>
      ) : null}

      <section aria-labelledby="history-caption">
        <TableWrap>
          <table className="table-base table-stack table-compact">
            <caption id="history-caption">Migration history</caption>
            <thead>
              <tr>
                <th scope="col" className="th text-right">Seq</th>
                <th scope="col" className="th">Migration</th>
                <th scope="col" className="th">Action</th>
                <th scope="col" className="th">Status</th>
                <th scope="col" className="th text-right">Batches / Rows</th>
                <th scope="col" className="th">Executed by</th>
                <th scope="col" className="th">Started</th>
                <th scope="col" className="th text-right">Duration</th>
                <th scope="col" className="th">Root</th>
                <th scope="col" className="th text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {historyPage.pageItems.map((execution) => {
                const isSelected = selected?.execution_id === execution.execution_id;
                return (
                  <tr
                    key={execution.execution_id}
                    className={isSelected ? "is-selected" : undefined}
                    aria-current={isSelected ? "true" : undefined}
                  >
                    <th scope="row" data-label="Seq" className="td text-right font-mono tabular-nums">
                      <button
                        type="button"
                        onClick={() => {
                          setSelected(execution);
                          reveal("execution-detail");
                        }}
                        className="cursor-pointer border-0 bg-transparent p-0 font-mono font-semibold text-leaf-800 underline hover:text-leaf-900 pointer-coarse:min-h-11 pointer-coarse:min-w-11 pointer-coarse:text-left"
                      >
                        #{execution.seq_order}
                        <span className="sr-only">, view details</span>
                      </button>
                    </th>
                    <td data-label="Migration" className="td cell-title col-main col-main-narrow max-w-[24rem]">
                      <div>
                        <span className="font-semibold text-ink">
                          {execution.migration_title_snapshot}
                        </span>
                        <div className="mt-1 flex flex-wrap items-center gap-1.5">
                          <span className="font-mono text-xs text-muted">
                            eff #{execution.effective_seq} · raw #{execution.seq_version} · rev{" "}
                            {execution.migration_revision}
                          </span>
                          {execution.is_superseded ? (
                            <span className="badge badge-warning whitespace-normal">
                              Superseded by {execution.superseded_by_title}
                            </span>
                          ) : null}
                        </div>
                      </div>
                    </td>
                    <td data-label="Action" className="td">
                      <ActionBadge action={execution.action_type} />
                    </td>
                    <td data-label="Status" className="td">
                      <StatusBadge status={execution.status} />
                    </td>
                    <td data-label="Batches / Rows" className="td text-right font-mono tabular-nums">
                      <div>
                        <span>
                          {execution.committed_batches}/{execution.total_batches}
                        </span>
                        <span className="block text-xs text-muted">
                          {formatNumber(execution.committed_rows)} rows
                        </span>
                      </div>
                    </td>
                    <td data-label="Executed by" className="td">{execution.executed_by_username}</td>
                    <td data-label="Started" className="td col-date text-muted">
                      {formatDateTime(execution.started_at)}
                    </td>
                    <td data-label="Duration" className="td text-right font-mono tabular-nums">
                      {formatDuration(execution.duration_ms)}
                    </td>
                    <td data-label="Root" className="td">
                      <span
                        className="font-mono text-xs whitespace-nowrap text-muted"
                        title={execution.root_execution_id}
                      >
                        {truncateMiddle(execution.root_execution_id, 14)}
                      </span>
                    </td>
                    <td data-label="Actions" className="td cell-actions text-right whitespace-nowrap">
                      <div className="inline-flex flex-wrap justify-end gap-1.5">
                        {canEdit &&
                        execution.action_type === "rollback" &&
                        execution.status === "success" ? (
                          <Button
                            variant="secondary"
                            size="sm"
                            className="whitespace-normal"
                            onClick={() => {
                              setForwardTarget(execution);
                              setForwardError(null);
                            }}
                          >
                            <RotateCcw className="h-3.5 w-3.5" /> Propose forward
                          </Button>
                        ) : null}
                        <Button
                          variant="secondary"
                          size="sm"
                          onClick={() => {
                          setSelected(execution);
                          reveal("execution-detail");
                        }}
                        >
                          View SQL
                        </Button>
                      </div>
                    </td>
                  </tr>
                );
              })}
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
        {executions.length > 0 ? (
          <Pagination
            label="History"
            {...historyPage.controls}
            filteredFrom={executions.length}
          />
        ) : null}
      </section>

      <ConfirmDialog
        open={forwardTarget !== null}
        onClose={() => {
          setForwardTarget(null);
          setForwardError(null);
        }}
        onConfirm={() => void proposeForward()}
        title="Propose forward after rollback?"
        description={
          <div className="space-y-3">
            <p className="m-0">
              Forward re-applies <strong>{forwardTarget?.migration_title_snapshot}</strong> after
              its successful rollback. It still needs every required group leader to approve, and
              the backend must confirm linear effective_seq order.
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
