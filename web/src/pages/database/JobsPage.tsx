import { useQuery } from "@tanstack/react-query";
import { ArrowRight, RefreshCcw } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router";

import { jobApi } from "../../api/endpoints";
import type { Job, JobStatus } from "../../api/types";
import {
  ActionBadge,
  Button,
  ErrorBanner,
  PageLoader,
  Pagination,
  ProgressBar,
  SearchInput,
  SectionHeader,
  Select,
  StatusBadge,
  TableEmpty,
  TableWrap,
} from "../../components/ui";
import { usePagination } from "../../hooks/usePagination";
import {
  formatDateTime,
  formatDuration,
  formatNumber,
  formatRelative,
  percent,
} from "../../lib/format";
import { isLiveJob } from "../../lib/status";

function jobDurationMs(job: Job): number | null {
  if (!job.started_at || !job.finished_at) return null;
  const diff = new Date(job.finished_at).getTime() - new Date(job.started_at).getTime();
  return Number.isNaN(diff) || diff < 0 ? null : diff;
}

export default function JobsPage() {
  const { dbId = "" } = useParams();
  const [filter, setFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState<"all" | JobStatus>("all");

  useEffect(() => {
    setFilter("");
    setStatusFilter("all");
  }, [dbId]);

  const jobsQuery = useQuery({
    queryKey: ["database", dbId, "jobs"],
    queryFn: () => jobApi.list(dbId),
    enabled: Boolean(dbId),
    refetchInterval: (query) => {
      const jobs = query.state.data?.jobs ?? [];
      const activeId = query.state.data?.active_job_id ?? null;
      return jobs.some((job) =>
        isLiveJob(job.status, job.lock_state, activeId !== null && job.job_id === activeId),
      )
        ? 3_000
        : 10_000;
    },
  });

  const jobs = jobsQuery.data?.jobs ?? [];
  const activeJobId = jobsQuery.data?.active_job_id ?? null;

  // Queue & active jobs ordered FIFO (queue_seq ASC) so head-of-line order is clear.
  const queueJobs = useMemo(
    () =>
      jobs
        .filter((job) =>
          isLiveJob(job.status, job.lock_state, activeJobId !== null && job.job_id === activeJobId),
        )
        .sort((a, b) => a.queue_seq - b.queue_seq),
    [jobs, activeJobId],
  );

  const historyJobs = useMemo(
    () =>
      jobs
        .filter(
          (job) =>
            !isLiveJob(
              job.status,
              job.lock_state,
              activeJobId !== null && job.job_id === activeJobId,
            ),
        )
        .sort((a, b) => b.queue_seq - a.queue_seq),
    [jobs, activeJobId],
  );

  const filteredHistory = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return historyJobs.filter((job) => {
      if (statusFilter !== "all" && job.status !== statusFilter) return false;
      if (!q) return true;
      return (
        job.migration_title.toLowerCase().includes(q) ||
        job.job_id.toLowerCase().includes(q) ||
        (job.worker_id ?? "").toLowerCase().includes(q) ||
        job.action_type.toLowerCase().includes(q)
      );
    });
  }, [historyJobs, filter, statusFilter]);

  const historyPage = usePagination(filteredHistory, {
    resetKey: `${dbId}|${filter}|${statusFilter}`,
  });

  const hasActiveWork = queueJobs.length > 0;

  if (jobsQuery.isLoading) return <PageLoader label="Loading jobs and queue" />;

  return (
    <div className="space-y-8">
      {jobsQuery.isError ? (
        <ErrorBanner error={jobsQuery.error} onRetry={() => void jobsQuery.refetch()} />
      ) : null}

      <section aria-labelledby="queue-caption">
        <SectionHeader
          title="Queue & active jobs"
          description="Ordered FIFO by queue sequence. One database runs one job at a time; holds block later queued jobs until resolved."
          actions={
            <>
              {hasActiveWork ? <span className="badge badge-info">Live polling 3s</span> : null}
              <Button variant="secondary" size="sm" onClick={() => void jobsQuery.refetch()}>
                <RefreshCcw className="h-3.5 w-3.5" /> Refresh
              </Button>
            </>
          }
        />
        <TableWrap>
          <table className="table-base table-stack table-compact">
            <caption id="queue-caption">
              {queueJobs.length} {queueJobs.length === 1 ? "active or queued job" : "active or queued jobs"}
            </caption>
            <thead>
              <tr>
                <th scope="col" className="th text-right">Queue</th>
                <th scope="col" className="th">Migration</th>
                <th scope="col" className="th">Action</th>
                <th scope="col" className="th">Status</th>
                <th scope="col" className="th">Progress</th>
                <th scope="col" className="th text-right">Retries</th>
                <th scope="col" className="th">Worker</th>
                <th scope="col" className="th">Queued</th>
                <th scope="col" className="th text-right">Monitor</th>
              </tr>
            </thead>
            <tbody>
              {queueJobs.map((job, index) => {
                const pct = percent(job.completed_batches, job.total_batches);
                const holdsLock = activeJobId === job.job_id && Boolean(job.lock_state);
                return (
                  <tr key={job.job_id}>
                    <th scope="row" data-label="Queue" className="td text-right font-mono tabular-nums">
                      <div>
                        <span className="font-semibold text-ink">#{job.queue_seq}</span>
                        <span className="block text-xs font-normal text-muted">
                          pos {index + 1}
                        </span>
                      </div>
                    </th>
                    <td data-label="Migration" className="td cell-title col-main col-main-narrow max-w-[24rem]">
                      <div>
                        <Link
                          to={`/databases/${dbId}/jobs/${job.job_id}`}
                          className="font-semibold text-ink hover:text-leaf-800 hover:underline"
                        >
                          {job.migration_title || "Untitled migration"}
                        </Link>
                        <div className="mt-1 flex flex-wrap items-center gap-1.5">
                          <span className="badge badge-neutral font-mono">{job.execution_mode}</span>
                          {holdsLock && job.lock_state ? (
                            <StatusBadge status={job.lock_state} kind="lock" />
                          ) : null}
                        </div>
                      </div>
                    </td>
                    <td data-label="Action" className="td">
                      <ActionBadge action={job.action_type} />
                    </td>
                    <td data-label="Status" className="td">
                      <StatusBadge status={job.status} />
                    </td>
                    <td data-label="Progress" className="td min-w-[10rem]">
                      <ProgressBar
                        value={pct}
                        tone={
                          job.status === "uncertain" ||
                          job.status === "partial" ||
                          job.status === "partially_reverted"
                            ? "amber"
                            : "leaf"
                        }
                        label={`${job.completed_batches}/${job.total_batches} batches`}
                        detail={`${formatNumber(job.committed_rows)} rows`}
                      />
                    </td>
                    <td data-label="Retries" className="td text-right font-mono tabular-nums">
                      {job.retry_count}/{job.max_retries}
                    </td>
                    <td data-label="Worker" className="td font-mono text-xs text-ink-soft">
                      {job.worker_id ?? <span className="text-muted">unclaimed</span>}
                    </td>
                    <td data-label="Queued" className="td col-date text-muted" title={formatDateTime(job.queued_at)}>
                      {formatRelative(job.queued_at)}
                    </td>
                    <td data-label="Monitor" className="td cell-actions text-right whitespace-nowrap">
                      <Link
                        to={`/databases/${dbId}/jobs/${job.job_id}`}
                        className="btn btn-secondary btn-sm"
                      >
                        Monitor <ArrowRight className="h-3.5 w-3.5" />
                      </Link>
                    </td>
                  </tr>
                );
              })}
              {queueJobs.length === 0 ? (
                <TableEmpty colSpan={9}>
                  The queue is empty and no job is currently running or held on this database.
                </TableEmpty>
              ) : null}
            </tbody>
          </table>
        </TableWrap>
      </section>

      <section aria-labelledby="job-history-caption" className="space-y-4">
        <SectionHeader
          title="Job history"
          description="Completed and failed worker jobs for this database, newest first."
        />

        <div className="card flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between sm:p-5">
          <div className="flex flex-1 flex-col gap-3 sm:flex-row sm:items-center">
            <SearchInput
              value={filter}
              onChange={setFilter}
              placeholder="Filter by migration, job ID, or worker…"
              className="sm:w-72"
            />
            <Select
              aria-label="Filter job history by status"
              value={statusFilter}
              onChange={(event) => setStatusFilter(event.target.value as "all" | JobStatus)}
              className="sm:w-44"
            >
              <option value="all">All statuses</option>
              <option value="success">Success</option>
              <option value="failed">Failed</option>
              <option value="partial">Partial</option>
              <option value="partially_reverted">Partially reverted</option>
              <option value="uncertain">Uncertain</option>
            </Select>
          </div>
          <span className="text-sm text-muted">
            Showing <strong className="text-ink">{filteredHistory.length}</strong> of{" "}
            <strong className="text-ink">{historyJobs.length}</strong> finished jobs
          </span>
        </div>

        <TableWrap>
          <table className="table-base table-stack table-compact">
            <caption id="job-history-caption">
              {filteredHistory.length} {filteredHistory.length === 1 ? "finished job" : "finished jobs"}
            </caption>
            <thead>
              <tr>
                <th scope="col" className="th text-right">Seq</th>
                <th scope="col" className="th">Migration</th>
                <th scope="col" className="th">Action</th>
                <th scope="col" className="th">Status</th>
                <th scope="col" className="th text-right">Batches / Rows</th>
                <th scope="col" className="th text-right">Retries</th>
                <th scope="col" className="th">Worker</th>
                <th scope="col" className="th">Finished</th>
                <th scope="col" className="th text-right">Duration</th>
                <th scope="col" className="th">Error</th>
                <th scope="col" className="th text-right">Details</th>
              </tr>
            </thead>
            <tbody>
              {historyPage.pageItems.map((job) => (
                <tr key={job.job_id}>
                  <th scope="row" data-label="Seq" className="td text-right font-mono tabular-nums">
                    #{job.queue_seq}
                  </th>
                  <td data-label="Migration" className="td cell-title col-main col-main-narrow max-w-[22rem]">
                    <div>
                      <Link
                        to={`/databases/${dbId}/jobs/${job.job_id}`}
                        className="font-semibold text-ink hover:text-leaf-800 hover:underline"
                      >
                        {job.migration_title || "Untitled migration"}
                      </Link>
                      <div className="mt-1">
                        <span className="badge badge-neutral font-mono">{job.execution_mode}</span>
                      </div>
                    </div>
                  </td>
                  <td data-label="Action" className="td">
                    <ActionBadge action={job.action_type} />
                  </td>
                  <td data-label="Status" className="td">
                    <StatusBadge status={job.status} />
                  </td>
                  <td data-label="Batches / Rows" className="td text-right font-mono tabular-nums">
                    <div>
                      <span>
                        {job.completed_batches}/{job.total_batches}
                      </span>
                      <span className="block text-xs text-muted">
                        {formatNumber(job.committed_rows)} rows
                      </span>
                    </div>
                  </td>
                  <td data-label="Retries" className="td text-right font-mono tabular-nums">
                    {job.retry_count}/{job.max_retries}
                  </td>
                  <td data-label="Worker" className="td font-mono text-xs text-ink-soft">
                    {job.worker_id ?? <span className="text-muted">—</span>}
                  </td>
                  <td data-label="Finished" className="td col-date text-muted">
                    {formatDateTime(job.finished_at ?? job.updated_at)}
                  </td>
                  <td data-label="Duration" className="td text-right font-mono tabular-nums">
                    {formatDuration(jobDurationMs(job))}
                  </td>
                  <td
                    data-label="Error"
                    className="td cell-wide max-w-[16rem] truncate font-mono text-xs text-crimson"
                    title={job.last_error}
                  >
                    {job.last_error || <span className="text-muted">—</span>}
                  </td>
                  <td data-label="Details" className="td cell-actions text-right whitespace-nowrap">
                    <Link
                      to={`/databases/${dbId}/jobs/${job.job_id}`}
                      className="btn btn-secondary btn-sm"
                    >
                      Inspect
                    </Link>
                  </td>
                </tr>
              ))}
              {filteredHistory.length === 0 ? (
                <TableEmpty colSpan={11}>
                  {historyJobs.length === 0
                    ? "No finished jobs recorded for this database yet."
                    : "No finished jobs match the current filter."}
                </TableEmpty>
              ) : null}
            </tbody>
          </table>
        </TableWrap>

        {historyJobs.length > 0 ? (
          <Pagination
            label="Job history"
            {...historyPage.controls}
            filteredFrom={historyJobs.length}
          />
        ) : null}
      </section>
    </div>
  );
}
