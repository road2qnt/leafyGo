import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  ArrowRight,
  Database as DatabaseIcon,
  GitBranch,
  Layers,
  ShieldCheck,
  Users,
  UsersRound,
} from "lucide-react";
import { Link } from "react-router";

import { adminApi, databaseApi, workerApi } from "../api/endpoints";
import { isMockMode } from "../api/mock/mode";
import { useAuth } from "../auth/AuthContext";
import {
  Callout,
  Card,
  EmptyState,
  ErrorBanner,
  LevelBadge,
  OptionalTag,
  PageHeader,
  PageLoader,
  ProgressBar,
  SectionHeader,
  StatusBadge,
} from "../components/ui";
import { formatNumber, formatRelative } from "../lib/format";
import { lockLabel } from "../lib/status";

const isMock = isMockMode();

export default function DashboardPage() {
  const { user, leaderOfGroups } = useAuth();
  const isAdmin = Boolean(user?.is_platform_admin);

  const databasesQuery = useQuery({
    queryKey: ["databases"],
    queryFn: databaseApi.list,
    enabled: !isAdmin,
  });

  const workersQuery = useQuery({
    queryKey: ["workers"],
    queryFn: workerApi.status,
    refetchInterval: 8_000,
    retry: false,
  });

  const usersQuery = useQuery({
    queryKey: ["admin", "users"],
    queryFn: adminApi.users,
    enabled: isAdmin,
  });
  const groupsQuery = useQuery({
    queryKey: ["admin", "groups"],
    queryFn: adminApi.groups,
    enabled: isAdmin,
  });
  const adminDatabasesQuery = useQuery({
    queryKey: ["admin", "databases"],
    queryFn: adminApi.databases,
    enabled: isAdmin,
  });

  const databases = databasesQuery.data?.databases ?? [];
  const workers = workersQuery.data?.workers ?? [];
  const onlineWorkers = workers.filter((worker) => worker.online);
  const activeDatabases = databases.filter((database) => database.active_job_id);

  if (databasesQuery.isLoading && !isAdmin) {
    return <PageLoader label="Loading your databases" />;
  }

  return (
    <div className="space-y-7">
      <PageHeader
        eyebrow={isAdmin ? "Platform administration" : "Migration workspace"}
        title={
          <>
            {isAdmin ? "Manage the platform." : "Your database control room."}
            <span className="ml-2 hidden text-base font-normal text-muted sm:inline">
              {user?.username}
            </span>
          </>
        }
        description={
          isAdmin
            ? "Accounts, group access, and target connections. Admins do not open database tools directly."
            : leaderOfGroups.length > 0
              ? `You lead ${leaderOfGroups.map((group) => group.name).join(", ")}. Pending proposals wait for your decision.`
              : "Submit migrations, follow the queue, and keep an eye on locks holding a database."
        }
        actions={
          isAdmin ? null : (
            <Link to="/workers" className="btn btn-secondary">
              <Activity className="h-4 w-4" /> Worker status
            </Link>
          )
        }
      />

      {isMock ? (
        <Callout tone="plum" title="Demo mode is on">
          The SPA is running against an in-browser mock API. Try the pending Inventory proposal as{" "}
          <strong>putra</strong>, watch a batch job progress live, then tick{" "}
          <strong>Hide rollback</strong> on Inventory history to see the root-group filter. Data
          resets from the sidebar.
        </Callout>
      ) : null}

      {workersQuery.isError ? (
        <Callout tone="warning" title="Worker liveness endpoint unavailable">
          The optional <code className="font-mono text-xs">/api/v1/workers/status</code> endpoint is
          not enabled. Queue execution is unaffected.
        </Callout>
      ) : onlineWorkers.length === 0 && workers.length > 0 ? (
        <Callout tone="warning" title="No worker online">
          Queued jobs will wait until a worker heartbeats. Check the worker container and{" "}
          <code className="font-mono text-xs">WORKER_MAX_CONCURRENT_JOBS</code>.
        </Callout>
      ) : null}

      {isAdmin ? (
        <div className="space-y-6">
          <div className="grid gap-4 sm:grid-cols-3">
            <Link to="/admin/users" className="card card-hover block p-5">
              <div className="flex items-start justify-between">
                <span className="grid h-10 w-10 place-items-center rounded-lg bg-leaf-50 text-leaf-700">
                  <Users className="h-5 w-5" />
                </span>
                <ArrowRight className="h-4 w-4 text-muted" />
              </div>
              <p className="mt-4 text-sm font-semibold text-ink">Users</p>
              <p className="mt-1 text-xs leading-relaxed text-muted">
                Create accounts, reset passwords, and deactivate users with history.
              </p>
              <p className="mt-3 font-mono text-[11px] text-leaf-600">
                {usersQuery.data ? `${usersQuery.data.users.length} accounts` : "…"}
              </p>
            </Link>

            <Link to="/admin/groups" className="card card-hover block p-5">
              <div className="flex items-start justify-between">
                <span className="grid h-10 w-10 place-items-center rounded-lg bg-leaf-50 text-leaf-700">
                  <UsersRound className="h-5 w-5" />
                </span>
                <ArrowRight className="h-4 w-4 text-muted" />
              </div>
              <p className="mt-4 text-sm font-semibold text-ink">Groups & access</p>
              <p className="mt-1 text-xs leading-relaxed text-muted">
                Assign a leader, add members, then grant View or View + Edit per database.
              </p>
              <p className="mt-3 font-mono text-[11px] text-leaf-600">
                {groupsQuery.data ? `${groupsQuery.data.groups.length} groups` : "…"}
              </p>
            </Link>

            <Link to="/admin/databases" className="card card-hover block p-5">
              <div className="flex items-start justify-between">
                <span className="grid h-10 w-10 place-items-center rounded-lg bg-leaf-50 text-leaf-700">
                  <DatabaseIcon className="h-5 w-5" />
                </span>
                <ArrowRight className="h-4 w-4 text-muted" />
              </div>
              <p className="mt-4 text-sm font-semibold text-ink">Connections</p>
              <p className="mt-1 text-xs leading-relaxed text-muted">
                Register target PostgreSQL servers and test their credentials.
              </p>
              <p className="mt-3 font-mono text-[11px] text-leaf-600">
                {adminDatabasesQuery.data
                  ? `${adminDatabasesQuery.data.databases.length} connections`
                  : "…"}
              </p>
            </Link>
          </div>

          <Card className="p-5">
            <SectionHeader
              title="Why admins cannot open database tools"
              description="Separation of duties from Milestone 1 still applies."
            />
            <div className="grid gap-4 sm:grid-cols-3">
              <div className="rounded-lg bg-[#f7f6f0] p-3">
                <ShieldCheck className="h-4 w-4 text-leaf-600" />
                <p className="mt-2 text-xs font-semibold">Platform vs data access</p>
                <p className="mt-1 text-[11px] leading-relaxed text-muted">
                  Platform Admin manages accounts and connections. Database actions require group
                  membership.
                </p>
              </div>
              <div className="rounded-lg bg-[#f7f6f0] p-3">
                <GitBranch className="h-4 w-4 text-leaf-600" />
                <p className="mt-2 text-xs font-semibold">Approval audit trail</p>
                <p className="mt-1 text-[11px] leading-relaxed text-muted">
                  Histories survive user deletion through immutable snapshots.
                </p>
              </div>
              <div className="rounded-lg bg-[#f7f6f0] p-3">
                <Layers className="h-4 w-4 text-leaf-600" />
                <p className="mt-2 text-xs font-semibold">Soft-delete safety</p>
                <p className="mt-1 text-[11px] leading-relaxed text-muted">
                  Connections with history are hidden instead of erased.
                </p>
              </div>
            </div>
          </Card>
        </div>
      ) : (
        <>
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
            {databases.map((database) => {
              return (
                <div key={database.id} className="card card-hover flex flex-col p-5">
                  <div className="flex items-start justify-between gap-2">
                    <div className="flex min-w-0 items-center gap-2.5">
                      <span className="grid h-10 w-10 shrink-0 place-items-center rounded-lg bg-leaf-50 text-leaf-700">
                        <DatabaseIcon className="h-5 w-5" />
                      </span>
                      <div className="min-w-0">
                        <Link
                          to={`/databases/${database.id}/overview`}
                          className="block truncate text-sm font-semibold text-ink hover:text-leaf-700"
                        >
                          {database.name}
                        </Link>
                        <p className="truncate font-mono text-[10px] text-muted" title={database.db_name}>
                          {database.host}:{database.port}/{database.db_name}
                        </p>
                      </div>
                    </div>
                    <LevelBadge level={database.level} />
                  </div>

                  <div className="mt-4 flex flex-wrap items-center gap-1.5">
                    {database.lock_state ? (
                      <StatusBadge status={database.lock_state} kind="lock" />
                    ) : database.active_job_id ? (
                      <span className="badge badge-neutral">Queued</span>
                    ) : (
                      <span className="badge badge-success">Available</span>
                    )}
                    {database.active_job_id ? (
                      <Link
                        to={`/databases/${database.id}/jobs/${database.active_job_id}`}
                        className="badge badge-info hover:underline"
                      >
                        Active job <ArrowRight className="h-3 w-3" />
                      </Link>
                    ) : null}
                  </div>

                  <div className="mt-auto flex items-center justify-between border-t border-line/70 pt-3.5 text-[11px] text-muted">
                    <span title={lockLabel(database.lock_state)}>
                      {database.lock_state
                        ? database.lock_state.replaceAll("_", " ")
                        : database.active_job_id
                          ? "Queued job waiting"
                          : "No migration lock"}
                    </span>
                    <Link
                      to={`/databases/${database.id}/overview`}
                      className="inline-flex items-center gap-1 font-medium text-leaf-700 hover:text-leaf-900"
                    >
                      Open <ArrowRight className="h-3 w-3" />
                    </Link>
                  </div>
                </div>
              );
            })}
          </div>

          {databases.length === 0 ? (
            <EmptyState
              icon={DatabaseIcon}
              title="No databases yet"
              description="You are not in a group with database access. Ask a Platform Admin to add you to the right group and grant View or View + Edit."
            />
          ) : null}

          {activeDatabases.length > 0 ? (
            <section>
              <SectionHeader
                title="Active work"
                description="Locks stay on the database until the job, revert, or evidence probe finishes."
              />
              <div className="space-y-2.5">
                {activeDatabases.map((database) => (
                  <Link
                    key={database.id}
                    to={`/databases/${database.id}/jobs/${database.active_job_id}`}
                    className="card flex flex-wrap items-center justify-between gap-3 px-4 py-3 transition hover:border-leaf-300"
                  >
                    <div className="flex min-w-0 items-center gap-3">
                      <span className="grid h-8 w-8 place-items-center rounded-md bg-leaf-50 text-leaf-700">
                        <Activity className="h-4 w-4" />
                      </span>
                      <div className="min-w-0">
                        <p className="truncate text-sm font-medium text-ink">{database.name}</p>
                        <p className="font-mono text-[10px] text-muted">{database.active_job_id}</p>
                      </div>
                    </div>
                    <div className="flex items-center gap-2">
                      {database.lock_state ? (
                        <StatusBadge status={database.lock_state} kind="lock" />
                      ) : (
                        <span className="badge badge-neutral">Queued</span>
                      )}
                      <ArrowRight className="h-4 w-4 text-muted" />
                    </div>
                  </Link>
                ))}
              </div>
            </section>
          ) : null}
        </>
      )}

      <section>
        <SectionHeader
          title="Worker liveness"
          description="Optional M2 indicator sourced from worker_heartbeats."
          optional
          actions={
            <Link to="/workers" className="text-xs font-medium text-leaf-700 hover:underline">
              View all workers
            </Link>
          }
        />
        <Card className="p-4">
          {workersQuery.isLoading ? (
            <p className="text-sm text-muted">Checking workers…</p>
          ) : workers.length === 0 ? (
            <p className="text-sm text-muted">No heartbeat data available.</p>
          ) : (
            <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
              <div>
                <p className="text-2xl font-semibold text-ink">
                  {onlineWorkers.length}
                  <span className="text-sm font-normal text-muted"> / {workers.length} online</span>
                </p>
                <p className="text-[11px] text-muted">
                  {workers.reduce((sum, worker) => sum + worker.active_jobs, 0)} active jobs
                </p>
              </div>
              <div className="grid w-full flex-1 gap-2 sm:grid-cols-2 xl:grid-cols-3">
                {workers.map((worker) => (
                  <span
                    key={worker.worker_id}
                    className="inline-flex w-full items-center gap-1.5 rounded-lg border border-line bg-[#f7f6f0] px-2.5 py-1.5 font-mono text-[10px] whitespace-nowrap text-muted"
                  >
                    <span
                      className={`h-1.5 w-1.5 shrink-0 rounded-full ${worker.online ? "bg-leaf-500" : "bg-[#b3b3a8]"}`}
                    />
                    <span className="truncate">{worker.hostname}</span>
                    <span className="ml-auto text-muted/60">{worker.active_jobs} jobs</span>
                    <span className="text-muted/60">·</span>
                    <span className="shrink-0">{formatRelative(worker.last_seen_at)}</span>
                  </span>
                ))}
              </div>
              <OptionalTag />
            </div>
          )}
        </Card>
      </section>

      {!isAdmin ? (
        <div className="grid gap-4 sm:grid-cols-3">
          <Card className="p-4">
            <p className="stat-label">Queue rule</p>
            <p className="mt-1 text-sm font-medium">FIFO per database</p>
            <p className="mt-1 text-[11px] leading-relaxed text-muted">
              One migration at a time per database; different databases run in parallel.
            </p>
          </Card>
          <Card className="p-4">
            <p className="stat-label">Approval rule</p>
            <p className="mt-1 text-sm font-medium">Every required leader</p>
            <p className="mt-1 text-[11px] leading-relaxed text-muted">
              Quorum is the snapshot of groups with access when the proposal was submitted.
            </p>
          </Card>
          <Card className="p-4">
            <p className="stat-label">Rollback rule</p>
            <p className="mt-1 text-sm font-medium">LIFO, per batch</p>
            <p className="mt-1 text-[11px] leading-relaxed text-muted">
              Reverse order by effective sequence. Unsafe reverts are rejected, not forced.
            </p>
          </Card>
        </div>
      ) : null}

      {databasesQuery.isError ? (
        <ErrorBanner error={databasesQuery.error} onRetry={() => void databasesQuery.refetch()} />
      ) : null}
    </div>
  );
}
