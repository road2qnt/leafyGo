import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, ArrowRight, Database as DatabaseIcon, HardDrive, Plus, Rows3, Table2 } from "lucide-react";
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
    <div className="space-y-6">
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Tables" value={formatNumber(overview.stats.table_count)} icon={Table2} />
        <StatCard
          label="Estimated rows"
          value={overview.stats.row_estimate > 0 ? formatNumber(overview.stats.row_estimate) : "—"}
          hint="Estimator only when the target exposes row counts."
          icon={Rows3}
        />
        <StatCard
          label="On-disk size"
          value={overview.stats.total_size_bytes > 0 ? formatBytes(overview.stats.total_size_bytes) : "—"}
          icon={HardDrive}
        />
        <StatCard
          label="Access level"
          value={overview.level === "edit" ? "View + Edit" : "View only"}
          hint={overview.level === "edit" ? "Can submit migrations and run console." : "Read-only tools."}
          icon={DatabaseIcon}
        />
      </div>

      <div className="grid min-w-0 gap-5 lg:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)]">
        <Card className="p-5">
          <SectionHeader
            title="Active execution"
            description="Cumulative progress for the root execution, including retries."
            actions={
              activeJob ? (
                <Link
                  to={`/databases/${dbId}/jobs/${activeJob.job_id}`}
                  className="text-xs font-medium text-leaf-700 hover:underline"
                >
                  Open monitor
                </Link>
              ) : null
            }
          />
          {activeJob ? (
            <div className="space-y-4">
              <div className="flex flex-wrap items-center gap-2">
                <StatusBadge status={activeJob.status} />
                <ActionBadge action={activeJob.action_type} />
                <span className="badge badge-neutral font-mono">{activeJob.execution_mode}</span>
                {activeJob.lock_state ? <StatusBadge status={activeJob.lock_state} kind="lock" /> : null}
              </div>
              <div>
                <p className="text-sm font-semibold text-ink">{activeJob.migration_title}</p>
                <p className="mt-0.5 font-mono text-[10px] text-muted">{activeJob.job_id}</p>
              </div>
              <ProgressBar
                value={jobProgress}
                tone={activeJob.status === "uncertain" ? "plum" : activeJob.status === "partial" ? "amber" : "leaf"}
                label={`${activeJob.completed_batches} / ${activeJob.total_batches} batches`}
                detail={`${formatNumber(activeJob.committed_rows)} rows`}
              />
              {activeJob.last_error ? (
                <p className="rounded-lg border border-[#e9c3b9] bg-[#fbebe6] px-3 py-2 font-mono text-[11px] text-[#722c1f]">
                  {activeJob.last_error}
                </p>
              ) : null}
              <p className="text-[11px] text-muted">
                Started {formatDateTime(activeJob.started_at)} · worker{" "}
                <span className="font-mono">{activeJob.worker_id ?? "unclaimed"}</span>
              </p>
            </div>
          ) : (
            <div className="flex flex-col items-start gap-2 rounded-lg border border-dashed border-line px-4 py-5">
              <span className="badge badge-success">No active migration</span>
              <p className="text-xs leading-relaxed text-muted">
                The queue is clear for this database. Submit a migration or open history to review past
                attempts.
              </p>
              <Link to={`/databases/${dbId}/migrations`} className="btn btn-secondary btn-sm mt-1">
                <Plus className="h-3.5 w-3.5" /> New migration
              </Link>
            </div>
          )}
        </Card>

        <Card className="p-5">
          <SectionHeader title="Database details" />
          <dl className="space-y-3 text-sm">
            <div className="flex justify-between gap-3">
              <dt className="text-muted">Lock state</dt>
              <dd>{overview.lock_state ? <StatusBadge status={overview.lock_state} kind="lock" /> : <span className="badge badge-success">Free</span>}</dd>
            </div>
            <div className="flex justify-between gap-3">
              <dt className="text-muted">Host</dt>
              <dd className="font-mono text-xs">{database.host}:{database.port}</dd>
            </div>
            <div className="flex justify-between gap-3">
              <dt className="text-muted">Database</dt>
              <dd className="font-mono text-xs">{database.db_name}</dd>
            </div>
            <div className="flex justify-between gap-3">
              <dt className="text-muted">User</dt>
              <dd className="font-mono text-xs">{database.username}</dd>
            </div>
            <div className="flex justify-between gap-3">
              <dt className="text-muted">SSL mode</dt>
              <dd className="font-mono text-xs">{database.ssl_mode}</dd>
            </div>
            <div className="flex justify-between gap-3">
              <dt className="text-muted">Last updated</dt>
              <dd className="text-xs">{formatDateTime(database.updated_at)}</dd>
            </div>
          </dl>
          <div className="mt-4 grid grid-cols-2 gap-2">
            <Link to={`/databases/${dbId}/schema`} className="btn btn-secondary btn-sm">
              <Table2 className="h-3.5 w-3.5" /> Schema
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
      </div>

      <section>
        <SectionHeader
          title="Recent attempts"
          description="Every attempt is retained; supersede and rollback status never erases audit rows."
          actions={
            <Link to={`/databases/${dbId}/history`} className="text-xs font-medium text-leaf-700 hover:underline">
              Full history
            </Link>
          }
        />
        <TableWrap>
          <table className="table-base min-w-[760px]">
            <thead>
              <tr>
                <th className="th">Seq</th>
                <th className="th">Migration</th>
                <th className="th">Action</th>
                <th className="th">Status</th>
                <th className="th">Batches</th>
                <th className="th">Rows</th>
                <th className="th">By</th>
                <th className="th">Started</th>
                <th className="th text-right"> </th>
              </tr>
            </thead>
            <tbody>
              {recent.map((execution) => (
                <tr key={execution.execution_id} className="tr-hover">
                  <td className="td font-mono text-xs text-muted">#{execution.seq_order}</td>
                  <td className="td">
                    <p className="text-sm font-medium text-ink">{execution.migration_title_snapshot}</p>
                    <p className="font-mono text-[10px] text-muted">
                      effective_seq {execution.effective_seq} · rev {execution.migration_revision}
                    </p>
                  </td>
                  <td className="td"><ActionBadge action={execution.action_type} /></td>
                  <td className="td"><StatusBadge status={execution.status} /></td>
                  <td className="td font-mono text-xs">
                    {execution.committed_batches}/{execution.total_batches}
                  </td>
                  <td className="td font-mono text-xs">{formatNumber(execution.committed_rows)}</td>
                  <td className="td text-xs">{execution.executed_by_username}</td>
                  <td className="td text-xs text-muted">{formatDateTime(execution.started_at)}</td>
                  <td className="td text-right">
                    <Link
                      to={`/databases/${dbId}/history`}
                      className="inline-flex items-center gap-1 text-xs font-medium text-leaf-700 hover:underline"
                    >
                      <ArrowRight className="h-3.5 w-3.5" />
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
