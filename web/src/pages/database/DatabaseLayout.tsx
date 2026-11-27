import { useQuery } from "@tanstack/react-query";
import { Activity, ArrowLeft, Plus } from "lucide-react";
import { Link, Outlet, useParams } from "react-router";

import { databaseApi } from "../../api/endpoints";
import {
  Callout,
  ErrorBanner,
  LevelBadge,
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
          <ArrowLeft className="h-3.5 w-3.5" /> Databases
        </Link>
        <ErrorBanner error={overviewQuery.error} onRetry={() => void overviewQuery.refetch()} />
      </div>
    );
  }

  const overview = overviewQuery.data;
  const database = overview.database;
  const activeJob = overview.active_job;
  const lockState = overview.lock_state;
  const canEdit = overview.level === "edit";

  return (
    <DatabaseContext.Provider value={{ overview, refetch: () => void overviewQuery.refetch() }}>
      <div className="space-y-4 sm:space-y-6">
        <nav aria-label="Breadcrumb">
          <ol className="m-0 flex list-none flex-wrap items-center gap-2 p-0 text-sm">
            <li>
              <Link to="/" className="text-leaf-800 hover:text-leaf-900">
                Databases
              </Link>
            </li>
            <li className="flex items-center gap-2">
              <span aria-hidden="true" className="text-muted">
                /
              </span>
              <span aria-current="page" className="font-semibold text-ink-soft">
                {database.name}
              </span>
            </li>
          </ol>
        </nav>

        <div className="flex flex-wrap items-end justify-between gap-4">
          <div className="min-w-0 flex-1 basis-80">
            <h1 className="m-0 break-words text-[clamp(1.625rem,1.3rem+1.4vw,2.25rem)] leading-[1.25] font-semibold text-ink">
              {database.name}
            </h1>
            <div className="mt-2 flex flex-wrap items-center gap-2">
              <LevelBadge level={overview.level} />
              {lockState ? <StatusBadge status={lockState} kind="lock" /> : null}
              <span className="min-w-0 font-mono text-xs break-all text-muted">
                {database.host}:{database.port}/{database.db_name}
              </span>
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-2 empty:hidden max-sm:w-full max-sm:[&>*]:flex-1 max-[22.5rem]:[&>*]:px-2 max-[22.5rem]:[&>*]:whitespace-nowrap">
            {activeJob ? (
              <Link to={`/databases/${dbId}/jobs/${activeJob.job_id}`} className="btn btn-secondary">
                <Activity className="h-4 w-4" /> Active job
              </Link>
            ) : null}
            {canEdit ? (
              <Link to={`/databases/${dbId}/migrations/new`} className="btn btn-primary">
                <Plus className="h-4 w-4" /> New migration
              </Link>
            ) : null}
          </div>
        </div>

        {lockState === "uncertain_hold" ? (
          <Callout tone="warning" title="Uncertain commit — database locked">
            The previous session could not be proven dead. Automatic re-probe runs every 15s. Only
            an approved <code className="border border-leaf-200 bg-leaf-100 px-1 font-mono text-xs">resolve_uncertain</code> proposal can override
            evidence, and evidence wins when it is readable.{" "}
            {activeJob ? (
              <Link
                to={`/databases/${dbId}/jobs/${activeJob.job_id}`}
                className="font-semibold underline"
              >
                Open the job monitor
              </Link>
            ) : null}
          </Callout>
        ) : lockState === "partial_hold" ? (
          <Callout tone="warning" title="Partial migration holds the database lock">
            Committed batches remain, later batches did not. Queueing another migration is blocked
            until this is retried or reverted.{" "}
            {activeJob ? (
              <Link
                to={`/databases/${dbId}/jobs/${activeJob.job_id}`}
                className="font-semibold underline"
              >
                Resume from checkpoint
              </Link>
            ) : null}
          </Callout>
        ) : lockState === "partially_reverted_hold" ? (
          <Callout tone="warning" title="Partial revert holds the database lock">
            Reverse compensation stopped part-way. Resume the revert downward; never run SQL Up.
            {activeJob ? (
              <>
                {" "}
                <Link
                  to={`/databases/${dbId}/jobs/${activeJob.job_id}`}
                  className="font-semibold underline"
                >
                  Open revert monitor
                </Link>
              </>
            ) : null}
          </Callout>
        ) : lockState === "running" ? (
          <Callout tone="info" title="A migration is running on this database">
            One database runs one migration at a time; other databases continue in parallel.
          </Callout>
        ) : null}

        <TabNav
          items={[
            { to: `/databases/${dbId}/overview`, label: "Overview", end: true },
            { to: `/databases/${dbId}/schema`, label: "Schema" },
            { to: `/databases/${dbId}/data`, label: "Data" },
            { to: `/databases/${dbId}/query`, label: "SQL Console" },
            {
              to: `/databases/${dbId}/migrations`,
              label: (
                <span>
                  Migrations<span className="max-md:sr-only"> & proposals</span>
                </span>
              ),
            },
            {
              to: `/databases/${dbId}/jobs`,
              label: (
                <span>
                  Jobs<span className="max-md:sr-only"> & queue</span>
                </span>
              ),
            },
            {
              to: `/databases/${dbId}/history`,
              label: (
                <span>
                  <span className="max-md:sr-only">Migration </span>
                  <span className="max-md:inline-block max-md:capitalize">history</span>
                </span>
              ),
            },
          ]}
        />

        <Outlet />
      </div>
    </DatabaseContext.Provider>
  );
}
