import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Plus, RefreshCcw, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { migrationApi } from "../../api/endpoints";
import type { MigrationDefinition, Proposal } from "../../api/types";
import {
  ActionBadge,
  Button,
  Callout,
  ConfirmDialog,
  ErrorBanner,
  PageLoader,
  Pagination,
  SearchInput,
  SectionHeader,
  StatusBadge,
  TableEmpty,
  TableWrap,
} from "../../components/ui";
import { usePagination } from "../../hooks/usePagination";
import { useToast } from "../../hooks/useToast";
import { formatDateTime } from "../../lib/format";
import { useDatabaseContext } from "./context";

export default function MigrationsPage() {
  const { dbId = "" } = useParams();
  const { overview } = useDatabaseContext();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();

  const migrationsQuery = useQuery({
    queryKey: ["database", dbId, "migrations"],
    queryFn: () => migrationApi.list(dbId),
  });
  const proposalsQuery = useQuery({
    queryKey: ["database", dbId, "proposals"],
    queryFn: () => migrationApi.proposals(dbId),
  });

  const [filter, setFilter] = useState("");
  const [deleteDefTarget, setDeleteDefTarget] = useState<MigrationDefinition | null>(null);
  const [deletePropTarget, setDeletePropTarget] = useState<Proposal | null>(null);
  const [deleteError, setDeleteError] = useState<unknown>(null);
  const [deleting, setDeleting] = useState(false);

  useEffect(() => {
    setFilter("");
    setDeleteDefTarget(null);
    setDeletePropTarget(null);
    setDeleteError(null);
  }, [dbId]);

  const canEdit = overview.level === "edit";
  const migrations = migrationsQuery.data?.migrations ?? [];
  const proposals = proposalsQuery.data?.proposals ?? [];

  const filteredMigrations = useMemo(
    () =>
      migrations
        .filter(
          (migration) =>
            migration.title.toLowerCase().includes(filter.toLowerCase()) ||
            migration.description.toLowerCase().includes(filter.toLowerCase()),
        )
        .sort((a, b) => b.effective_seq - a.effective_seq || b.seq_version - a.seq_version),
    [migrations, filter],
  );

  const definitionsPage = usePagination(filteredMigrations, { resetKey: `${dbId}|${filter}` });

  const visibleProposals = useMemo(
    () =>
      proposals.filter(
        (proposal) => proposal.status !== "withdrawn" && proposal.status !== "draft",
      ),
    [proposals],
  );
  const proposalsPage = usePagination(visibleProposals, { resetKey: dbId });

  const activeProposals = proposals.filter((proposal) =>
    [
      "pending_review",
      "approved",
      "queued",
      "running",
      "partial",
      "partially_reverted",
      "uncertain",
    ].includes(proposal.status),
  );

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["database", dbId, "migrations"] }),
      queryClient.invalidateQueries({ queryKey: ["database", dbId, "proposals"] }),
      queryClient.invalidateQueries({ queryKey: ["database", dbId, "overview"] }),
    ]);
  };

  const deleteDefinition = async () => {
    if (!deleteDefTarget) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      await migrationApi.delete(dbId, deleteDefTarget.id);
      toast.success("Draft deleted", `"${deleteDefTarget.title}" was removed.`);
      setDeleteDefTarget(null);
      await invalidate();
    } catch (error) {
      setDeleteError(error);
    } finally {
      setDeleting(false);
    }
  };

  const deleteProposal = async () => {
    if (!deletePropTarget) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      await migrationApi.deleteProposal(dbId, deletePropTarget.id);
      toast.success("Proposal deleted", `Proposal for "${deletePropTarget.migration_title}" was removed.`);
      setDeletePropTarget(null);
      await invalidate();
    } catch (error) {
      setDeleteError(error);
    } finally {
      setDeleting(false);
    }
  };

  if (migrationsQuery.isLoading) return <PageLoader label="Loading migration definitions" />;

  return (
    <div className="space-y-8">
      {!canEdit ? (
        <Callout tone="info" title="View-only workspace">
          Your group grants View Only access. Editing definitions and submitting proposals requires
          View + Edit.
        </Callout>
      ) : null}

      {migrationsQuery.isError ? (
        <ErrorBanner
          error={migrationsQuery.error}
          onRetry={() => void migrationsQuery.refetch()}
        />
      ) : null}

      {activeProposals.length > 0 ? (
        <Callout tone="warning" title={`${activeProposals.length} proposal(s) in the workflow`}>
          Pending proposals are waiting for leaders; queued/running/hold proposals are visible on
          the queue. A migration with an active proposal cannot be edited.
          <div className="mt-2.5 flex flex-wrap gap-2">
            {activeProposals.slice(0, 4).map((proposal) => (
              <Link
                key={proposal.id}
                to={`/databases/${dbId}/proposals/${proposal.id}`}
                className="btn btn-sm max-w-full text-left"
              >
                {proposal.migration_title} <ArrowRight className="h-3.5 w-3.5" />
              </Link>
            ))}
          </div>
        </Callout>
      ) : null}

      <section aria-labelledby="defs-caption">
        <SectionHeader
          title="Definitions"
          description="effective_seq is the logical slot; seq_version is the raw creation order."
          actions={
            <div className="flex w-full flex-wrap items-center gap-2 sm:w-auto">
              <SearchInput
                value={filter}
                onChange={setFilter}
                className="w-full sm:w-56"
                placeholder="Filter definitions…"
              />
              <Button
                variant="secondary"
                size="sm"
                className="max-sm:flex-1"
                onClick={() => {
                  void migrationsQuery.refetch();
                  void proposalsQuery.refetch();
                }}
              >
                <RefreshCcw className="h-3.5 w-3.5" /> Refresh
              </Button>
              {canEdit ? (
                <Button size="sm" className="max-sm:flex-1" onClick={() => navigate(`/databases/${dbId}/migrations/new`)}>
                  <Plus className="h-3.5 w-3.5" /> New migration
                </Button>
              ) : null}
            </div>
          }
        />
        <TableWrap>
          <table className="table-base table-stack table-compact">
            <caption id="defs-caption">
              {filteredMigrations.length}{" "}
              {filteredMigrations.length === 1 ? "migration definition" : "migration definitions"}
            </caption>
            <thead>
              <tr>
                <th scope="col" className="th">Title</th>
                <th scope="col" className="th text-right">Slot</th>
                <th scope="col" className="th">Mode</th>
                <th scope="col" className="th text-right">Revision</th>
                <th scope="col" className="th">State</th>
                <th scope="col" className="th">Last updated</th>
                <th scope="col" className="th text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {definitionsPage.pageItems.map((migration) => {
                const active = migration.active_proposal;
                const locked = migration.is_immutable || Boolean(active);
                return (
                  <tr key={migration.id}>
                    <th scope="row" data-label="Title" className="td cell-title col-main max-w-[24rem]">
                      <div>
                        <Link
                          to={`/databases/${dbId}/migrations/${migration.id}/edit`}
                          className="font-semibold text-leaf-800 hover:text-leaf-900"
                        >
                          {migration.title}
                        </Link>
                        {migration.description ? (
                          <span className="mt-0.5 block truncate text-xs font-normal text-muted">
                            {migration.description}
                          </span>
                        ) : null}
                        <div className="mt-1.5 flex flex-wrap gap-1.5">
                          {migration.is_irreversible ? (
                            <span className="badge badge-warning">Irreversible</span>
                          ) : null}
                          {migration.is_superseded ? (
                            <span className="badge badge-warning whitespace-normal">
                              Superseded by {migration.superseded_by_title}
                            </span>
                          ) : null}
                          {migration.replaces_title ? (
                            <span className="badge badge-info whitespace-normal">
                              Replaces #{migration.replaces_effective_seq ?? migration.effective_seq}: &ldquo;{migration.replaces_title}&rdquo;
                            </span>
                          ) : null}
                          {migration.is_immutable ? (
                            <span className="badge badge-neutral">Immutable</span>
                          ) : null}
                        </div>
                      </div>
                    </th>
                    <td data-label="Slot" className="td whitespace-nowrap text-right font-mono tabular-nums">
                      <div>
                        <span className="font-semibold">eff #{migration.effective_seq}</span>
                        <span className="block text-xs text-muted">raw #{migration.seq_version}</span>
                      </div>
                    </td>
                    <td data-label="Mode" className="td">
                      <div>
                        <span
                          className={`badge ${
                            migration.execution_mode === "batch" ? "badge-info" : "badge-neutral"
                          }`}
                        >
                          {migration.execution_mode}
                        </span>
                        {migration.batch_config ? (
                          <span className="mt-1 block font-mono text-xs text-muted">
                            {migration.batch_config.table_name} / {migration.batch_config.cursor_col}
                          </span>
                        ) : null}
                      </div>
                    </td>
                    <td data-label="Revision" className="td text-right font-mono tabular-nums">r{migration.revision}</td>
                    <td data-label="State" className="td">
                      <div className="flex flex-col items-end gap-1 sm:items-start">
                        <StatusBadge status={migration.latest_status} kind="auto" />
                        {active ? (
                          <Link
                            to={`/databases/${dbId}/proposals/${active.id}`}
                            className="text-xs font-semibold text-leaf-800 hover:text-leaf-900"
                          >
                            Open proposal →
                          </Link>
                        ) : null}
                      </div>
                    </td>
                    <td data-label="Last updated" className="td col-date text-muted">
                      {formatDateTime(migration.updated_at)}
                    </td>
                    <td data-label="Actions" className="td cell-actions text-right whitespace-nowrap">
                      <div className="inline-flex justify-end gap-2">
                        <Button
                          variant="secondary"
                          size="sm"
                          onClick={() =>
                            navigate(`/databases/${dbId}/migrations/${migration.id}/edit`)
                          }
                        >
                          {locked ? "View" : "Edit"}
                        </Button>
                        {canEdit && !locked && !migration.is_superseded ? (
                          <Button
                            variant="danger"
                            size="sm"
                            onClick={() => {
                              setDeleteDefTarget(migration);
                              setDeleteError(null);
                            }}
                          >
                            <Trash2 className="h-3.5 w-3.5" /> Delete
                          </Button>
                        ) : null}
                      </div>
                    </td>
                  </tr>
                );
              })}
              {filteredMigrations.length === 0 ? (
                <TableEmpty colSpan={7}>
                  No migration definitions match. Create one to start the approval workflow.
                </TableEmpty>
              ) : null}
            </tbody>
          </table>
        </TableWrap>
        {migrations.length > 0 ? (
          <Pagination
            label="Definitions"
            {...definitionsPage.controls}
            filteredFrom={migrations.length}
          />
        ) : null}
      </section>

      <section aria-labelledby="proposals-caption">
        <SectionHeader
          title="Proposals"
          description="A proposal snapshots the required approvers at submission time."
          actions={
            proposalsQuery.isFetching ? (
              <span className="text-xs text-muted">Refreshing…</span>
            ) : null
          }
        />
        <TableWrap>
          <table className="table-base table-stack table-compact">
            <caption id="proposals-caption">
              {visibleProposals.length}{" "}
              {visibleProposals.length === 1 ? "proposal" : "proposals"}
            </caption>
            <thead>
              <tr>
                <th scope="col" className="th">Migration</th>
                <th scope="col" className="th">Action</th>
                <th scope="col" className="th">Status</th>
                <th scope="col" className="th">Approvals</th>
                <th scope="col" className="th">Proposed by</th>
                <th scope="col" className="th">Updated</th>
                <th scope="col" className="th text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {proposalsPage.pageItems.map((proposal) => (
                <tr key={proposal.id}>
                  <th scope="row" data-label="Migration" className="td cell-title col-main max-w-[22rem]">
                    <div>
                      <Link
                        to={`/databases/${dbId}/proposals/${proposal.id}`}
                        className="font-semibold text-leaf-800 hover:text-leaf-900"
                      >
                        {proposal.migration_title}
                      </Link>
                      <span className="mt-0.5 block font-mono text-xs font-normal text-muted">
                        eff #{proposal.effective_seq} · rev {proposal.migration_revision}
                      </span>
                    </div>
                  </th>
                  <td data-label="Action" className="td">
                    <ActionBadge action={proposal.action_type} />
                  </td>
                  <td data-label="Status" className="td">
                    <StatusBadge status={proposal.status} kind="proposal" />
                  </td>
                  <td data-label="Approvals" className="td text-sm">
                    <span>
                      <span className="font-mono font-semibold tabular-nums">
                        {proposal.approval_summary.approved}/{proposal.approval_summary.required}
                      </span>
                      {proposal.approval_summary.pending > 0 ? (
                        <span className="ml-1 text-xs text-muted">
                          · {proposal.approval_summary.pending} pending
                        </span>
                      ) : null}
                      {proposal.approval_summary.rejected > 0 ? (
                        <span className="ml-1 text-xs font-semibold text-crimson">· rejected</span>
                      ) : null}
                    </span>
                  </td>
                  <td data-label="Proposed by" className="td">{proposal.proposed_by_username}</td>
                  <td data-label="Updated" className="td col-date text-muted">
                    {formatDateTime(proposal.updated_at)}
                  </td>
                  <td data-label="Actions" className="td cell-actions text-right whitespace-nowrap">
                    <div className="inline-flex justify-end gap-2">
                      {proposal.status === "pending_review" ? (
                        <Link
                          to={`/databases/${dbId}/proposals/${proposal.id}`}
                          className="btn btn-sm"
                        >
                          Review <ArrowRight className="h-3.5 w-3.5" />
                        </Link>
                      ) : (
                        <Link
                          to={`/databases/${dbId}/proposals/${proposal.id}`}
                          className="btn btn-secondary btn-sm"
                        >
                          View
                        </Link>
                      )}
                      {canEdit &&
                      (proposal.status === "withdrawn" || proposal.status === "draft") ? (
                        <Button
                          variant="danger"
                          size="sm"
                          onClick={() => {
                            setDeletePropTarget(proposal);
                            setDeleteError(null);
                          }}
                        >
                          <Trash2 className="h-3.5 w-3.5" /> Delete
                        </Button>
                      ) : null}
                    </div>
                  </td>
                </tr>
              ))}
              {visibleProposals.length === 0 ? (
                <TableEmpty colSpan={7}>No proposals yet.</TableEmpty>
              ) : null}
            </tbody>
          </table>
        </TableWrap>
        {visibleProposals.length > 0 ? (
          <Pagination label="Proposals" {...proposalsPage.controls} />
        ) : null}
      </section>

      <ConfirmDialog
        open={deleteDefTarget !== null}
        onClose={() => {
          setDeleteDefTarget(null);
          setDeleteError(null);
        }}
        onConfirm={() => void deleteDefinition()}
        title="Delete migration draft?"
        description={
          <div className="space-y-2">
            <p className="m-0">
              Permanently delete <strong>{deleteDefTarget?.title}</strong>? Any unexecuted proposals
              for this draft will also be removed. This cannot be undone.
            </p>
            {deleteError ? <ErrorBanner error={deleteError} /> : null}
          </div>
        }
        confirmLabel="Delete draft"
        tone="danger"
        loading={deleting}
      />

      <ConfirmDialog
        open={deletePropTarget !== null}
        onClose={() => {
          setDeletePropTarget(null);
          setDeleteError(null);
        }}
        onConfirm={() => void deleteProposal()}
        title="Delete proposal?"
        description={
          <div className="space-y-2">
            <p className="m-0">
              Permanently delete the <strong>{deletePropTarget?.status.replaceAll("_", " ")}</strong>{" "}
              proposal for <strong>{deletePropTarget?.migration_title}</strong>? This cannot be
              undone.
            </p>
            {deleteError ? <ErrorBanner error={deleteError} /> : null}
          </div>
        }
        confirmLabel="Delete proposal"
        tone="danger"
        loading={deleting}
      />
    </div>
  );
}
