import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  ArrowLeft,
  Database as DatabaseIcon,
  History,
  LayoutDashboard,
  ListTree,
  Play,
  Send,
  Table2,
} from "lucide-react";
import { Link, Outlet, useParams } from "react-router";

import { databaseApi } from "../../api/endpoints";
import {
  Callout,
  ErrorBanner,
  LevelBadge,
  PageHeader,
  PageLoader,
  StatusBadge,
  TabNav,
} from "../../components/ui";
import { DatabaseContext } from "./context";

export default function DatabaseLayout() {
  const { dbId = "" } = useParams();
  const overviewQuery = useQuery({
    queryKey: ["database", dbId, "overview"],
    queryFn: () => databaseApi.overview(dbId),
    enabled: Boolean(dbId),
    refetchInterval: 8_000,
  });

  if (overviewQuery.isLoading) return <PageLoader label="Opening database" />;
  if (overviewQuery.isError || !overviewQuery.data) {
    return (
      <div className="space-y-4">
        <Link to="/" className="btn btn-ghost btn-sm w-fit">
          <ArrowLeft className="h-3.5 w-3.5" /> Dashboard
        </Link>
        <ErrorBanner error={overviewQuery.error} onRetry={() => void overviewQuery.refetch()} />
      </div>
    );
  }

  const overview = overviewQuery.data;
  const database = overview.database;
  const activeJob = overview.active_job;

  return (
    <DatabaseContext.Provider value={{ overview, refetch: () => void overviewQuery.refetch() }}>
      <div className="space-y-6">
        <div className="flex flex-wrap items-center gap-2 text-xs text-muted">
          <Link to="/" className="inline-flex items-center gap-1 font-medium text-leaf-700 hover:underline">
            <ArrowLeft className="h-3.5 w-3.5" /> Databases
          </Link>
          <span className="text-line">/</span>
          <span className="font-mono text-[11px]">{database.db_name}</span>
        </div>

        <PageHeader
          eyebrow="Database workspace"
          title={
            <span className="inline-flex flex-wrap items-center gap-2">
              {database.name}
              <LevelBadge level={overview.level} />
              {database.lock_state ? <StatusBadge status={database.lock_state} kind="lock" /> : null}
            </span>
          }
          description={
            <span className="font-mono text-xs">
              {database.host}:{database.port}/{database.db_name} · user {database.username} · SSL{" "}
              {database.ssl_mode}
            </span>
          }
          actions={
            activeJob ? (
              <Link to={`/databases/${dbId}/jobs/${activeJob.job_id}`} className="btn btn-secondary">
                <Activity className="h-4 w-4" /> Active job
              </Link>
            ) : (
              <>
                <Link to={`/databases/${dbId}/query`} className="btn btn-secondary">
                  <Play className="h-4 w-4" /> Query console
                </Link>
                <Link to={`/databases/${dbId}/history`} className="btn btn-secondary">
                  <History className="h-4 w-4" /> History
                </Link>
              </>
            )
          }
        />

        {database.lock_state === "uncertain_hold" ? (
          <Callout tone="plum" title="Uncertain commit — database locked">
            The previous session could not be proven dead. Automatic re-probe runs every 15s. Only an
            approved <code className="font-mono text-xs">resolve_uncertain</code> proposal can override
            evidence, and evidence wins when it is readable.{" "}
            {activeJob ? (
              <Link to={`/databases/${dbId}/jobs/${activeJob.job_id}`} className="font-semibold underline">
                Open the job monitor
              </Link>
            ) : null}
          </Callout>
        ) : database.lock_state === "partial_hold" ? (
          <Callout tone="warning" title="Partial migration holds the database lock">
            Committed batches remain, later batches did not. Queue another migration would be unsafe
            until this is retried or reverted.{" "}
            {activeJob ? (
              <Link to={`/databases/${dbId}/jobs/${activeJob.job_id}`} className="font-semibold underline">
                Resume from checkpoint
              </Link>
            ) : null}
          </Callout>
        ) : database.lock_state === "partially_reverted_hold" ? (
          <Callout tone="warning" title="Partial revert holds the database lock">
            Reverse compensation stopped part-way. Resume the revert downward; never run SQL up.
            {activeJob ? (
              <>
                {" "}
                <Link to={`/databases/${dbId}/jobs/${activeJob.job_id}`} className="font-semibold underline">
                  Open revert monitor
                </Link>
              </>
            ) : null}
          </Callout>
        ) : database.lock_state === "running" ? (
          <Callout tone="info" title="A migration is running on this database">
            One database runs one migration at a time; other databases continue in parallel.
          </Callout>
        ) : null}

        <TabNav
          items={[
            { to: `/databases/${dbId}/overview`, label: "Overview", icon: LayoutDashboard, end: true },
            { to: `/databases/${dbId}/schema`, label: "Schema", icon: ListTree },
            { to: `/databases/${dbId}/data`, label: "Data", icon: Table2 },
            { to: `/databases/${dbId}/query`, label: "Query console", icon: Play },
            { to: `/databases/${dbId}/migrations`, label: "Migrations", icon: Send },
            { to: `/databases/${dbId}/history`, label: "History", icon: History },
          ]}
        />

        <Outlet />
      </div>
    </DatabaseContext.Provider>
  );
}
