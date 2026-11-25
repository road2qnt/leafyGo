import { useQuery } from "@tanstack/react-query";
import { Activity, Radio } from "lucide-react";

import { workerApi } from "../api/endpoints";
import {
  Callout,
  Card,
  ErrorBanner,
  OptionalTag,
  PageHeader,
  PageLoader,
  StatCard,
  TableEmpty,
  TableWrap,
} from "../components/ui";
import { formatRelative } from "../lib/format";

export default function WorkersPage() {
  const workersQuery = useQuery({
    queryKey: ["workers"],
    queryFn: workerApi.status,
    refetchInterval: 5_000,
    retry: false,
  });

  const workers = workersQuery.data?.workers ?? [];
  const online = workers.filter((worker) => worker.online);
  const activeJobs = workers.reduce((sum, worker) => sum + worker.active_jobs, 0);

  if (workersQuery.isLoading) {
    return <PageLoader label="Loading worker heartbeats" />;
  }

  return (
    <div className="space-y-6">
      <PageHeader
        eyebrow="Operations"
        title={
          <span className="inline-flex flex-wrap items-center gap-2">
            Worker liveness <OptionalTag label="Optional" />
          </span>
        }
        description="Sourced from worker_heartbeats. If every worker is offline, queued jobs simply wait; the fallback is heartbeat logging via log/slog."
      />

      {workersQuery.isError ? (
        <>
          <ErrorBanner error={workersQuery.error} onRetry={() => void workersQuery.refetch()} />
          <Callout tone="warning" title="Optional endpoint not enabled">
            The worker liveness table/UI is tagged optional in the M2 plan. The queue, locks, and
            migration progress do not depend on it. Fallback: inspect worker containers and
            structured heartbeat logs.
          </Callout>
        </>
      ) : null}

      {!workersQuery.isError ? (
        <>
          <div className="grid gap-4 sm:grid-cols-3">
            <StatCard label="Workers online" value={`${online.length} / ${workers.length}`} icon={Radio} />
            <StatCard label="Active jobs" value={activeJobs} icon={Activity} />
            <StatCard
              label="Heartbeat freshness"
              value={
                online.length > 0
                  ? formatRelative(
                      online
                        .map((worker) => worker.last_seen_at)
                        .sort()
                        .at(-1),
                    )
                  : "No workers"
              }
            />
          </div>

          {workers.length === 0 ? (
            <Callout tone="warning" title="No worker online">
              Queued jobs will wait. Start <code className="font-mono text-xs">cmd/worker</code> and
              check the shared <code className="font-mono text-xs">ENCRYPTION_KEY</code>.
            </Callout>
          ) : null}

          <TableWrap>
            <table className="table-base min-w-[720px]">
              <thead>
                <tr>
                  <th className="th">Worker</th>
                  <th className="th">Status</th>
                  <th className="th">Active jobs</th>
                  <th className="th">Last heartbeat</th>
                  <th className="th">Hostname</th>
                </tr>
              </thead>
              <tbody>
                {workers.map((worker) => (
                  <tr key={worker.worker_id} className="tr-hover">
                    <td className="td font-mono text-xs">{worker.worker_id}</td>
                    <td className="td">
                      <span
                        className={`badge ${worker.online ? "badge-success" : "badge-neutral"}`}
                      >
                        <span
                          className={`h-1.5 w-1.5 rounded-full ${worker.online ? "bg-leaf-500" : "bg-[#8a948d]"}`}
                        />
                        {worker.online ? "Online" : "Offline"}
                      </span>
                    </td>
                    <td className="td tabular-nums">{worker.active_jobs}</td>
                    <td className="td text-muted">{formatRelative(worker.last_seen_at)}</td>
                    <td className="td font-mono text-xs text-muted">{worker.hostname}</td>
                  </tr>
                ))}
                {workers.length === 0 ? (
                  <TableEmpty colSpan={5}>No heartbeats recorded.</TableEmpty>
                ) : null}
              </tbody>
            </table>
          </TableWrap>

          <Card className="p-4 text-xs leading-relaxed text-muted">
            A worker is considered online when its heartbeat is within 3× the heartbeat interval.
            Active jobs count leases currently held by that process.
          </Card>
        </>
      ) : null}
    </div>
  );
}
