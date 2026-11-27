import { useQuery } from "@tanstack/react-query";
import { ArrowRight, Database as DatabaseIcon } from "lucide-react";
import { Link } from "react-router";

import { adminApi, databaseApi } from "../api/endpoints";
import { isMockMode } from "../api/mock/mode";
import { useAuth } from "../auth/AuthContext";
import {
  Callout,
  EmptyState,
  ErrorBanner,
  LevelBadge,
  PageHeader,
  PageLoader,
  SectionHeader,
  StatusBadge,
} from "../components/ui";

const isMock = isMockMode();

export default function DashboardPage() {
  const { user, leaderOfGroups } = useAuth();
  const isAdmin = Boolean(user?.is_platform_admin);

  const databasesQuery = useQuery({
    queryKey: ["databases"],
    queryFn: databaseApi.list,
    enabled: !isAdmin,
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
  const activeDatabases = databases.filter((database) => database.active_job_id);

  if (databasesQuery.isLoading && !isAdmin) {
    return <PageLoader label="Loading your databases" />;
  }

  return (
    <div className="space-y-8">
      <PageHeader
        title={isAdmin ? "Manage platform" : "Databases"}
        description={
          isAdmin
            ? "Create accounts, group users, and register PostgreSQL connections. Platform Admins manage the platform and do not open target database tools."
            : leaderOfGroups.length > 0
              ? `Signed in as ${user?.username}. You lead ${leaderOfGroups.map((group) => group.name).join(", ")}. Pending proposals wait for your decision.`
              : "Browse schemas, run read-only SQL Console queries, submit migrations, and monitor the execution queue."
        }
      />

      {isMock ? (
        <Callout tone="info" title="Demo mode is on">
          The SPA is running against an in-browser mock API. Try the pending Inventory proposal as{" "}
          <strong>putra</strong>, watch a batch job progress live, then tick{" "}
          <strong>Hide rollback</strong> on Inventory history to see the root-group filter. Data
          resets from the top header.
        </Callout>
      ) : null}

      {isAdmin ? (
        <div className="space-y-8">
          <section aria-labelledby="admin-title">
            <h2 id="admin-title" className="sr-only">
              Admin menu
            </h2>
            <ul className="grid-cards m-0 list-none p-0">
              <li className="card card-hover card-pad group relative flex flex-col gap-3">
                <h3 className="m-0 flex items-center justify-between gap-3 text-xl font-semibold text-ink">
                  <Link
                    to="/admin/users"
                    className="text-ink no-underline group-hover:underline after:absolute after:inset-0"
                  >
                    Users
                  </Link>
                  <span
                    aria-hidden="true"
                    className="mr-1 h-2.5 w-2.5 shrink-0 rotate-45 border-t-[2.5px] border-r-[2.5px] border-ink-soft group-hover:border-ink"
                  />
                </h3>
                <p className="m-0 text-sm leading-relaxed text-ink-soft">
                  Create accounts, reset passwords, and activate or deactivate users.
                </p>
                <div className="mt-auto border-t border-line-soft pt-3 font-mono text-xs text-muted">
                  {usersQuery.data ? `${usersQuery.data.users.length} accounts` : "Loading…"}
                </div>
              </li>

              <li className="card card-hover card-pad group relative flex flex-col gap-3">
                <h3 className="m-0 flex items-center justify-between gap-3 text-xl font-semibold text-ink">
                  <Link
                    to="/admin/groups"
                    className="text-ink no-underline group-hover:underline after:absolute after:inset-0"
                  >
                    Groups and access
                  </Link>
                  <span
                    aria-hidden="true"
                    className="mr-1 h-2.5 w-2.5 shrink-0 rotate-45 border-t-[2.5px] border-r-[2.5px] border-ink-soft group-hover:border-ink"
                  />
                </h3>
                <p className="m-0 text-sm leading-relaxed text-ink-soft">
                  Assign a group leader, add members, then grant each group View Only or View + Edit per database.
                </p>
                <div className="mt-auto border-t border-line-soft pt-3 font-mono text-xs text-muted">
                  {groupsQuery.data ? `${groupsQuery.data.groups.length} groups` : "Loading…"}
                </div>
              </li>

              <li className="card card-hover card-pad group relative flex flex-col gap-3">
                <h3 className="m-0 flex items-center justify-between gap-3 text-xl font-semibold text-ink">
                  <Link
                    to="/admin/databases"
                    className="text-ink no-underline group-hover:underline after:absolute after:inset-0"
                  >
                    Connections
                  </Link>
                  <span
                    aria-hidden="true"
                    className="mr-1 h-2.5 w-2.5 shrink-0 rotate-45 border-t-[2.5px] border-r-[2.5px] border-ink-soft group-hover:border-ink"
                  />
                </h3>
                <p className="m-0 text-sm leading-relaxed text-ink-soft">
                  Register the PostgreSQL databases the platform manages and test their connections.
                </p>
                <div className="mt-auto border-t border-line-soft pt-3 font-mono text-xs text-muted">
                  {adminDatabasesQuery.data
                    ? `${adminDatabasesQuery.data.databases.length} connections`
                    : "Loading…"}
                </div>
              </li>
            </ul>
          </section>
        </div>
      ) : (
        <>
          {databases.length > 0 ? (
            <section aria-labelledby="db-list-title">
              <h2 id="db-list-title" className="sr-only">
                Database list
              </h2>
              <ul className="grid-cards m-0 list-none p-0">
                {databases.map((database) => (
                  <li key={database.id} className="card card-hover card-pad flex flex-col gap-3">
                    <div className="flex items-start justify-between gap-3">
                      <h3 className="m-0 text-xl font-semibold text-ink">
                        <Link
                          to={`/databases/${database.id}/overview`}
                          className="text-ink no-underline hover:underline"
                        >
                          {database.name}
                        </Link>
                      </h3>
                      <LevelBadge level={database.level} />
                    </div>

                    <p
                      className="m-0 font-mono text-sm text-muted max-sm:break-all sm:truncate"
                      title={`${database.host}:${database.port}/${database.db_name}`}
                    >
                      {database.host}:{database.port}/{database.db_name}
                    </p>

                    <div className="flex flex-wrap items-center gap-2 pt-1">
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
                          className="badge badge-info no-underline hover:underline"
                        >
                          Active job <ArrowRight className="h-3.5 w-3.5" />
                        </Link>
                      ) : null}
                    </div>

                    <div className="mt-auto flex items-center justify-between border-t border-line-soft pt-3 text-xs text-muted">
                      <span>
                        {database.lock_state
                          ? database.lock_state.replaceAll("_", " ")
                          : database.active_job_id
                            ? "Queued job waiting"
                            : "No migration lock"}
                      </span>
                      <Link
                        to={`/databases/${database.id}/overview`}
                        className="btn btn-sm"
                      >
                        Open
                      </Link>
                    </div>
                  </li>
                ))}
              </ul>
            </section>
          ) : (
            <EmptyState
              icon={DatabaseIcon}
              title="No databases yet"
              description="You are not in any group with database access. Ask a Platform Admin to add you to the right group."
            />
          )}

          {activeDatabases.length > 0 ? (
            <section>
              <SectionHeader
                title="Active work"
                description="Locks stay on the database until the job, revert, or evidence probe finishes."
              />
              <div className="space-y-3">
                {activeDatabases.map((database) => (
                  <Link
                    key={database.id}
                    to={`/databases/${database.id}/jobs/${database.active_job_id}`}
                    className="card card-hover flex flex-wrap items-center justify-between gap-3 px-5 py-4 no-underline"
                  >
                    <div className="min-w-0">
                      <p className="m-0 truncate text-base font-semibold text-ink">{database.name}</p>
                      <p className="m-0 mt-0.5 font-mono text-xs text-muted">{database.active_job_id}</p>
                    </div>
                    <div className="flex items-center gap-2">
                      {database.lock_state ? (
                        <StatusBadge status={database.lock_state} kind="lock" />
                      ) : (
                        <span className="badge badge-neutral">Queued</span>
                      )}
                      <span className="btn btn-sm">Monitor</span>
                    </div>
                  </Link>
                ))}
              </div>
            </section>
          ) : null}
        </>
      )}

      {databasesQuery.isError ? (
        <ErrorBanner error={databasesQuery.error} onRetry={() => void databasesQuery.refetch()} />
      ) : null}
    </div>
  );
}
