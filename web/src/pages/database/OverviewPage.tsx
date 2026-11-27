import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Plus } from "lucide-react";
import { Link, useParams } from "react-router";

import { jobApi } from "../../api/endpoints";
import {
  ActionBadge,
  Button,
  Card,
  ProgressBar,
  SectionHeader,
  StatCard,
  StatusBadge,
  TableEmpty,
  TableWrap,
} from "../../components/ui";
import { formatBytes, formatDateTime, formatNumber, percent } from "../../lib/format";
import { useDatabaseContext } from "./context";

export default function OverviewPage() {
  const { dbId = "" } = useParams();
  const queryClient = useQueryClient();
  const { overview, refetch } = useDatabaseContext();
  const activeJobID = overview.active_job_id ?? overview.database.active_job_id ?? null;

  const jobQuery = useQuery({
    queryKey: ["database", dbId, "overview-job", activeJobID],
    queryFn: () => jobApi.detail(dbId, activeJobID as string),
    enabled: Boolean(activeJobID),
  });
  const historyQuery = useQuery({
    queryKey: ["database", dbId, "overview-history"],
    queryFn: () => jobApi.history(dbId, false),
  });

  const activeJob = jobQuery.data?.job ?? overview.active_job;
  const recent = (historyQuery.data?.executions ?? overview.recent_history).slice(0, 6);
  const database = overview.database;

  const jobProgress = activeJob ? percent(activeJob.completed_batches, activeJob.total_batches) : 0;

  return (
    <div className="space-y-8">
      <div className="grid-stats">
        <StatCard
          label="Target status"
          value={
            overview.lock_state ? (
              <span className="text-amber">{overview.lock_state.replaceAll("_", " ")}</span>
            ) : (
              <span className="text-leaf-700">Connected</span>
            )
          }
        />
        <StatCard
          label="Tables and views"
          value={formatNumber(overview.stats.table_count)}
        />
        <StatCard
          label="Estimated rows"
          value={overview.stats.row_estimate > 0 ? formatNumber(overview.stats.row_estimate) : "—"}
        />
        <StatCard
          label="On-disk size"
          value={
            overview.stats.total_size_bytes > 0 ? formatBytes(overview.stats.total_size_bytes) : "—"
          }
        />
      </div>

      <div className="split-layout min-w-0">
        <Card className="card-pad flex flex-col gap-3">
          <h2 className="m-0 text-xl font-semibold text-ink">Database details</h2>
          <dl className="m-0 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm [overflow-wrap:anywhere]">
            <dt className="font-semibold text-ink-soft">Name</dt>
            <dd className="m-0 text-ink">{database.name}</dd>
            <dt className="font-semibold text-ink-soft">Database</dt>
            <dd className="m-0 font-mono text-ink">{database.db_name}</dd>
            <dt className="font-semibold text-ink-soft">Host</dt>
            <dd className="m-0 font-mono text-ink">
              {database.host}:{database.port}
            </dd>
            <dt className="font-semibold text-ink-soft">User</dt>
            <dd className="m-0 font-mono text-ink">{database.username}</dd>
            <dt className="font-semibold text-ink-soft">SSL mode</dt>
            <dd className="m-0 font-mono text-ink">{database.ssl_mode}</dd>
            <dt className="font-semibold text-ink-soft">Access level</dt>
            <dd className="m-0 text-ink">
              {overview.level === "edit" ? "View + Edit" : "View Only"}
            </dd>
            <dt className="font-semibold text-ink-soft">Lock state</dt>
            <dd className="m-0">
              {overview.lock_state ? (
                <StatusBadge status={overview.lock_state} kind="lock" />
              ) : (
                <span className="badge badge-success">Free</span>
              )}
            </dd>
          </dl>
          <div className="mt-auto flex flex-wrap gap-2 border-t border-line-soft pt-3">
            <Link to={`/databases/${dbId}/schema`} className="btn btn-sm">
              View schema
            </Link>
            <Link to={`/databases/${dbId}/data`} className="btn btn-sm">
              Browse data
            </Link>
            <Link to={`/databases/${dbId}/query`} className="btn btn-sm">
              SQL Console
            </Link>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => {
                void queryClient.invalidateQueries({ queryKey: ["database", dbId] });
                refetch();
              }}
            >
              <Activity className="h-3.5 w-3.5" /> Refresh
            </Button>
          </div>
        </Card>

        <Card className="card-pad flex flex-col gap-3">
          <div className="flex items-center justify-between gap-2">
            <h2 className="m-0 text-xl font-semibold text-ink">Active execution</h2>
            {activeJob ? (
              <Link
                to={`/databases/${dbId}/jobs/${activeJob.job_id}`}
                className="btn btn-sm"
              >
                Open monitor
              </Link>
            ) : null}
          </div>

          {activeJob ? (
            <div className="space-y-3">
              <div className="flex flex-wrap items-center gap-2">
                <StatusBadge status={activeJob.status} />
                <ActionBadge action={activeJob.action_type} />
                <span className="badge badge-neutral font-mono">{activeJob.execution_mode}</span>
                {activeJob.lock_state ? (
                  <StatusBadge status={activeJob.lock_state} kind="lock" />
                ) : null}
              </div>
              <div>
                <p className="m-0 text-base font-semibold text-ink">{activeJob.migration_title}</p>
                <p className="m-0 mt-0.5 font-mono text-xs text-muted">{activeJob.job_id}</p>
              </div>
              <ProgressBar
                value={jobProgress}
                tone={
                  activeJob.status === "uncertain" || activeJob.status === "partial"
                    ? "amber"
                    : "leaf"
                }
                label={`${activeJob.completed_batches} / ${activeJob.total_batches} batches`}
                detail={`${formatNumber(activeJob.committed_rows)} rows`}
              />
              {activeJob.last_error ? (
                <div className="alert alert-danger font-mono text-xs">{activeJob.last_error}</div>
              ) : null}
              <p className="m-0 text-xs text-muted">
                Started {formatDateTime(activeJob.started_at)} · worker{" "}
                <span className="font-mono">{activeJob.worker_id ?? "unclaimed"}</span>
              </p>
            </div>
          ) : (
            <div className="space-y-2">
              <p className="m-0">
                <span className="badge badge-success">No active migration</span>
              </p>
              <p className="m-0 text-sm leading-relaxed text-muted">
                The queue is clear for this database. Submit a migration or open history to review
                past attempts.
              </p>
            </div>
          )}

          <div className="mt-auto flex flex-wrap gap-2 border-t border-line-soft pt-3">
            <Link to={`/databases/${dbId}/migrations`} className="btn btn-sm">
              <Plus className="h-3.5 w-3.5" /> Migrations
            </Link>
            <Link to={`/databases/${dbId}/history`} className="btn btn-sm">
              View history
            </Link>
          </div>
        </Card>
      </div>

      <section aria-labelledby="recent-caption">
        <SectionHeader
          title="Recent attempts"
          description="Every attempt is retained; supersede and rollback status never erases audit rows."
          actions={
            <Link to={`/databases/${dbId}/history`} className="btn btn-sm">
              Full history
            </Link>
          }
        />
        <TableWrap>
          <table className="table-base table-stack table-compact">
            <caption id="recent-caption">
              Latest {recent.length} {recent.length === 1 ? "attempt" : "attempts"}
            </caption>
            <thead>
              <tr>
                <th scope="col" className="th text-right">Seq</th>
                <th scope="col" className="th">Migration</th>
                <th scope="col" className="th">Action</th>
                <th scope="col" className="th">Status</th>
                <th scope="col" className="th text-right">Batches</th>
                <th scope="col" className="th text-right">Rows</th>
                <th scope="col" className="th">Executed by</th>
                <th scope="col" className="th">Started</th>
                <th scope="col" className="th text-right">Details</th>
              </tr>
            </thead>
            <tbody>
              {recent.map((execution) => (
                <tr key={execution.execution_id}>
                  <th scope="row" data-label="Seq" className="td text-right font-mono tabular-nums">
                    #{execution.seq_order}
                  </th>
                  <td data-label="Migration" className="td cell-title col-main">
                    <div>
                      <span className="font-semibold text-ink">
                        {execution.migration_title_snapshot}
                      </span>
                      <span className="mt-0.5 block font-mono text-xs text-muted">
                        eff #{execution.effective_seq} · rev {execution.migration_revision}
                      </span>
                    </div>
                  </td>
                  <td data-label="Action" className="td">
                    <ActionBadge action={execution.action_type} />
                  </td>
                  <td data-label="Status" className="td">
                    <StatusBadge status={execution.status} />
                  </td>
                  <td data-label="Batches" className="td text-right font-mono tabular-nums">
                    {execution.committed_batches}/{execution.total_batches}
                  </td>
                  <td data-label="Rows" className="td text-right font-mono tabular-nums">
                    {formatNumber(execution.committed_rows)}
                  </td>
                  <td data-label="Executed by" className="td">{execution.executed_by_username}</td>
                  <td data-label="Started" className="td col-date text-muted">
                    {formatDateTime(execution.started_at)}
                  </td>
                  <td data-label="Details" className="td cell-actions text-right whitespace-nowrap">
                    <Link to={`/databases/${dbId}/history`} className="btn btn-sm">
                      View
                    </Link>
                  </td>
                </tr>
              ))}
              {recent.length === 0 ? (
                <TableEmpty colSpan={9}>No executions yet for this database.</TableEmpty>
              ) : null}
            </tbody>
          </table>
        </TableWrap>
      </section>
    </div>
  );
}
