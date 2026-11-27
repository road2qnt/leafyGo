import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Save, Send, Undo2, Wand2 } from "lucide-react";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router";

import { isApiError } from "../../api/client";
import { migrationApi, type MigrationInput } from "../../api/endpoints";
import type { DryRunResult, MigrationDefinition } from "../../api/types";
import {
  Button,
  Callout,
  CheckboxRow,
  ConfirmDialog,
  ErrorBanner,
  Field,
  PageHeader,
  PageLoader,
  Select,
  TextInput,
  Textarea,
} from "../../components/ui";
import { SqlEditor } from "../../components/SqlEditor";
import { useToast } from "../../hooks/useToast";
import { formatDateTime } from "../../lib/format";
import { actionLabel } from "../../lib/status";
import { useDatabaseContext } from "./context";

interface EditorForm {
  title: string;
  description: string;
  sqlUp: string;
  sqlDown: string;
  irreversible: boolean;
  irreversibleReason: string;
  mode: "normal" | "batch";
  tableName: string;
  cursorCol: string;
  batchSize: string;
  updatedAtCol: string;
  replacesId: string;
}

const emptyForm: EditorForm = {
  title: "",
  description: "",
  sqlUp: "",
  sqlDown: "",
  irreversible: false,
  irreversibleReason: "",
  mode: "normal",
  tableName: "",
  cursorCol: "",
  batchSize: "5000",
  updatedAtCol: "",
  replacesId: "",
};

function formFor(migration: MigrationDefinition): EditorForm {
  return {
    title: migration.title,
    description: migration.description,
    sqlUp: migration.sql_up,
    sqlDown: migration.sql_down,
    irreversible: migration.is_irreversible,
    irreversibleReason: migration.irreversible_reason,
    mode: migration.execution_mode,
    tableName: migration.batch_config?.table_name ?? "",
    cursorCol: migration.batch_config?.cursor_col ?? "",
    batchSize: String(migration.batch_config?.batch_size ?? 5000),
    updatedAtCol: migration.batch_config?.updated_at_col ?? "",
    replacesId: migration.replaces_migration_id ?? "",
  };
}

function toInput(form: EditorForm): MigrationInput {
  return {
    title: form.title.trim(),
    description: form.description,
    sql_up: form.sqlUp,
    sql_down: form.sqlDown,
    is_irreversible: form.irreversible,
    irreversible_reason: form.irreversible ? form.irreversibleReason : "",
    execution_mode: form.mode,
    batch_config:
      form.mode === "batch"
        ? {
            table_name: form.tableName.trim(),
            cursor_col: form.cursorCol.trim(),
            batch_size: Number(form.batchSize),
            updated_at_col: form.updatedAtCol.trim() || null,
          }
        : null,
    replaces_migration_id: form.replacesId || null,
  };
}

function validateForm(form: EditorForm): string[] {
  const errors: string[] = [];
  if (!form.title.trim()) errors.push("Title is required.");
  if (!form.sqlUp.trim()) errors.push("SQL Up is required.");
  if (form.irreversible) {
    if (!form.irreversibleReason.trim())
      errors.push("Explain why this migration is irreversible.");
  } else if (!form.sqlDown.trim()) {
    errors.push("SQL Down is required unless the migration is marked irreversible.");
  }
  if (form.mode === "batch") {
    if (!form.tableName.trim()) errors.push("Batch table name is required.");
    if (!form.cursorCol.trim()) errors.push("Batch cursor column is required.");
    const size = Number(form.batchSize);
    if (!Number.isFinite(size) || size <= 0)
      errors.push("Batch size must be a positive integer.");
  }
  return errors;
}

export default function MigrationEditorPage() {
  const { dbId = "", migrationId } = useParams();
  const isNew = !migrationId;
  const { overview } = useDatabaseContext();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();

  const migrationsQuery = useQuery({
    queryKey: ["database", dbId, "migrations"],
    queryFn: () => migrationApi.list(dbId),
  });

  const migration =
    migrationsQuery.data?.migrations.find((entry) => entry.id === migrationId) ?? null;
  const [form, setForm] = useState<EditorForm>(emptyForm);
  const [hydratedId, setHydratedId] = useState<string | null>(isNew ? "__new__" : null);

  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [validating, setValidating] = useState(false);
  const [validateError, setValidateError] = useState<unknown>(null);
  const [dryRun, setDryRun] = useState<DryRunResult | null>(null);
  const [submitOpen, setSubmitOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [actionError, setActionError] = useState<unknown>(null);
  const [withdrawing, setWithdrawing] = useState(false);

  useEffect(() => {
    if (isNew) {
      setForm(emptyForm);
      setHydratedId("__new__");
      return;
    }
    if (migration && hydratedId !== migration.id) {
      setForm(formFor(migration));
      setHydratedId(migration.id);
      setDryRun(null);
    }
  }, [isNew, migration, hydratedId]);

  const activeProposal = migration?.active_proposal ?? null;
  const locked = Boolean(
    migration?.is_immutable || (activeProposal && activeProposal.status !== "withdrawn"),
  );
  const canEdit = overview.level === "edit";
  const readOnly = !canEdit || locked;

  const replaceOptions = useMemo(
    () =>
      (migrationsQuery.data?.migrations ?? []).filter(
        (candidate) =>
          candidate.id !== migrationId &&
          (candidate.is_superseded ||
            candidate.latest_status === "failed" ||
            candidate.latest_status === "rejected"),
      ),
    [migrationsQuery.data, migrationId],
  );

  const update = <K extends keyof EditorForm>(key: K, value: EditorForm[K]) => {
    setForm((current) => ({ ...current, [key]: value }));
  };

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["database", dbId, "migrations"] }),
      queryClient.invalidateQueries({ queryKey: ["database", dbId, "overview"] }),
    ]);
  };

  const save = async (event?: FormEvent) => {
    event?.preventDefault();
    const errors = validateForm(form);
    if (errors.length > 0) {
      setSaveError(new Error(errors.join(" ")));
      return;
    }
    setSaving(true);
    setSaveError(null);
    try {
      const input = toInput(form);
      if (isNew) {
        const created = await migrationApi.create(dbId, input);
        toast.success("Definition created", "Review it, validate, then submit for approval.");
        await invalidate();
        navigate(`/databases/${dbId}/migrations/${created.id}/edit`, { replace: true });
      } else if (migration) {
        const updated = await migrationApi.update(dbId, migration.id, {
          ...input,
          revision: migration.revision,
        });
        setForm(formFor(updated));
        toast.success("Definition saved", `Revision ${updated.revision} is ready for validation.`);
        await invalidate();
      }
    } catch (error) {
      setSaveError(error);
    } finally {
      setSaving(false);
    }
  };

  const validate = async () => {
    if (!migration) return;
    setValidating(true);
    setValidateError(null);
    setDryRun(null);
    try {
      const result = await migrationApi.validate(dbId, migration.id);
      setDryRun(result);
      if (result.dry_run_status === "inconclusive") {
        toast.info("Dry-run inconclusive", "Timeout or lock contention; review may continue.");
      } else {
        toast.success("Dry-run passed", "No target changes were committed.");
      }
    } catch (error) {
      setValidateError(error);
      if (isApiError(error)) {
        setDryRun({ dry_run_status: "failed", message: error.message });
      }
    } finally {
      setValidating(false);
    }
  };

  const submit = async () => {
    if (!migration) return;
    setSubmitting(true);
    setActionError(null);
    try {
      const proposal = await migrationApi.createProposal(dbId, {
        migration_id: migration.id,
        action_type: "apply",
      });
      toast.success(
        "Proposal submitted",
        "Approval requests are now visible to every required leader.",
      );
      setSubmitOpen(false);
      navigate(`/databases/${dbId}/proposals/${proposal.id}`);
    } catch (error) {
      setActionError(error);
    } finally {
      setSubmitting(false);
    }
  };

  const withdraw = async () => {
    if (!activeProposal) return;
    setWithdrawing(true);
    setActionError(null);
    try {
      await migrationApi.withdraw(dbId, activeProposal.id);
      toast.success(
        "Proposal withdrawn",
        "Approvals are cleared and the definition is editable again.",
      );
      await invalidate();
    } catch (error) {
      setActionError(error);
    } finally {
      setWithdrawing(false);
    }
  };

  if (migrationsQuery.isLoading) return <PageLoader label="Loading definition" />;
  if (!isNew && migrationsQuery.isSuccess && !migration) {
    return (
      <div className="space-y-4">
        <Link to={`/databases/${dbId}/migrations`} className="btn btn-ghost btn-sm w-fit">
          <ArrowLeft className="h-3.5 w-3.5" /> Migrations
        </Link>
        <ErrorBanner error={new Error("Migration definition not found in this database.")} />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={isNew ? "New migration draft" : form.title || "Untitled draft"}
        description={
          isNew
            ? "All statements run inside a single transaction (or per batch). Do not write BEGIN, COMMIT, or ROLLBACK yourself."
            : `Revision ${migration?.revision ?? 1} · effective_seq ${migration?.effective_seq} · raw seq ${migration?.seq_version} · updated ${formatDateTime(migration?.updated_at)}`
        }
        actions={
          <>
            <Link to={`/databases/${dbId}/migrations`} className="btn btn-secondary">
              <ArrowLeft className="h-4 w-4" /> Back
            </Link>
            {!readOnly ? (
              <Button variant="secondary" onClick={() => void save()} loading={saving}>
                <Save className="h-4 w-4" /> Save
              </Button>
            ) : null}
            {!isNew && !readOnly ? (
              <Button
                onClick={() => setSubmitOpen(true)}
                disabled={Boolean(activeProposal)}
                title={activeProposal ? "Withdraw the active proposal first" : "Submit for approval"}
              >
                <Send className="h-4 w-4" /> Submit for review
              </Button>
            ) : null}
          </>
        }
      />

      {!canEdit ? (
        <Callout tone="info" title="View-only definition">
          Your access grant is View Only. Editing requires View + Edit.
        </Callout>
      ) : locked && !activeProposal ? (
        <Callout tone="warning" title="Definition is locked">
          Execution has started, so SQL columns are permanently immutable.
        </Callout>
      ) : null}

      {activeProposal ? (
        <Callout
          tone="info"
          title="Active proposal"
          action={
            activeProposal.status === "pending_review" ? (
              <Button
                variant="secondary"
                size="sm"
                onClick={() => void withdraw()}
                loading={withdrawing}
              >
                <Undo2 className="h-3.5 w-3.5" /> Withdraw
              </Button>
            ) : null
          }
        >
          <Link
            to={`/databases/${dbId}/proposals/${activeProposal.id}`}
            className="font-semibold underline"
          >
            {activeProposal.status.replaceAll("_", " ")} · {actionLabel(activeProposal.action_type)}
          </Link>{" "}
          — open it to review approvals or execution.
          {locked ? (
            <span className="mt-1 block">
              {migration?.is_immutable
                ? "The definition is locked: execution has started, so SQL columns are permanently immutable."
                : "The definition is locked until the proposal is withdrawn or rejected."}
            </span>
          ) : null}
        </Callout>
      ) : null}

      {saveError ? <ErrorBanner error={saveError} /> : null}
      {actionError ? <ErrorBanner error={actionError} /> : null}

      <form
        className="split-layout min-w-0"
        onSubmit={(event) => void save(event)}
      >
        <div className="min-w-0 space-y-6">
          <div className="card card-pad space-y-4">
            <div className="grid gap-4 sm:grid-cols-[1fr_auto]">
              <Field
                label="Title"
                htmlFor="migration-title"
                required
                hint="Up to 200 characters."
              >
                <TextInput
                  id="migration-title"
                  value={form.title}
                  onChange={(event) => update("title", event.target.value)}
                  placeholder="Add supplier lead-time column"
                  disabled={readOnly}
                  required
                />
              </Field>
              <div>
                <span className="label">Execution mode</span>
                <div className="mt-1 inline-flex border-2 border-ink bg-canvas">
                  {(["normal", "batch"] as const).map((mode) => (
                    <button
                      key={mode}
                      type="button"
                      disabled={readOnly}
                      onClick={() => update("mode", mode)}
                      className={`min-h-[2.75rem] cursor-pointer px-4 py-2 text-sm font-semibold ${
                        form.mode === mode
                          ? "bg-leaf-700 text-paper"
                          : "bg-paper text-ink hover:bg-leaf-50"
                      }`}
                    >
                      {mode === "normal" ? "Normal" : "Per batch"}
                    </button>
                  ))}
                </div>
              </div>
            </div>

            <Field label="Description" htmlFor="migration-description">
              <Textarea
                id="migration-description"
                value={form.description}
                onChange={(event) => update("description", event.target.value)}
                placeholder="Why is this change needed? What is the rollback strategy?"
                disabled={readOnly}
              />
            </Field>
          </div>

          <div className="grid grid-cols-[minmax(0,1fr)] gap-6 xl:grid-cols-2">
            <div className="space-y-1.5">
              <label className="label mb-0">SQL Up</label>
              <p className="hint m-0">
                Forward statements executed inside a transaction or cursor-scoped batch.
              </p>
              <SqlEditor
                value={form.sqlUp}
                onChange={(value) => update("sqlUp", value)}
                height="260px"
                readOnly={readOnly}
                ariaLabel="SQL Up"
              />
            </div>
            <div className="space-y-1.5">
              <label className="label mb-0">SQL Down</label>
              <p className="hint m-0">
                Reverse statements used for LIFO rollback or batch compensation.
              </p>
              <SqlEditor
                value={form.sqlDown}
                onChange={(value) => update("sqlDown", value)}
                height="260px"
                readOnly={readOnly || form.irreversible}
                ariaLabel="SQL Down"
              />
            </div>
          </div>

          <div className="card card-pad space-y-4">
            <CheckboxRow
              label="Mark as irreversible"
              description="Irreversible migrations cannot be reverted per batch or rolled back. A reason is mandatory."
              checked={form.irreversible}
              onChange={(checked) => update("irreversible", checked)}
              disabled={readOnly}
            />
            {form.irreversible ? (
              <Field label="Irreversible reason" htmlFor="irreversible-reason" required>
                <Textarea
                  id="irreversible-reason"
                  value={form.irreversibleReason}
                  onChange={(event) => update("irreversibleReason", event.target.value)}
                  placeholder="Lossy overwrite with no source column to restore."
                  disabled={readOnly}
                />
              </Field>
            ) : null}
          </div>

          {form.mode === "batch" ? (
            <div className="card card-pad space-y-4">
              <div>
                <h2 className="m-0 text-lg font-semibold text-ink">Batch cursor configuration</h2>
                <p className="m-0 mt-1 text-sm text-muted">
                  Rows are processed with <code className="font-mono">LIMIT</code> over an integer
                  cursor. The rollback must be expressible with the same cursor range.
                </p>
              </div>
              <div className="grid gap-4 sm:grid-cols-3">
                <Field label="Target table" htmlFor="batch-table" required>
                  <TextInput
                    id="batch-table"
                    value={form.tableName}
                    onChange={(event) => update("tableName", event.target.value)}
                    placeholder="order_items"
                    className="font-mono"
                    disabled={readOnly}
                  />
                </Field>
                <Field
                  label="Cursor column"
                  htmlFor="batch-cursor"
                  required
                  hint="smallint, integer, or bigint."
                >
                  <TextInput
                    id="batch-cursor"
                    value={form.cursorCol}
                    onChange={(event) => update("cursorCol", event.target.value)}
                    placeholder="id"
                    className="font-mono"
                    disabled={readOnly}
                  />
                </Field>
                <Field label="Batch size" htmlFor="batch-size" required>
                  <TextInput
                    id="batch-size"
                    type="number"
                    min={1}
                    value={form.batchSize}
                    onChange={(event) => update("batchSize", event.target.value)}
                    className="font-mono"
                    disabled={readOnly}
                  />
                </Field>
              </div>
              <div className="border-2 border-dashed border-ink-soft bg-canvas p-4">
                <div className="flex flex-wrap items-center gap-2">
                  <p className="m-0 text-sm font-semibold text-ink">Drift detection guard</p>
                  <span className="tag-optional">Optional</span>
                </div>
                <p className="mt-1 mb-3 text-xs leading-relaxed text-ink-soft">
                  If set, revert refuses batches whose rows changed after{" "}
                  <code className="font-mono">committed_at</code>. Fallback without this field:
                  reject irreversible or LIFO-breaking reverts only.
                </p>
                <Field label="updated_at column" htmlFor="batch-updated">
                  <TextInput
                    id="batch-updated"
                    value={form.updatedAtCol}
                    onChange={(event) => update("updatedAtCol", event.target.value)}
                    placeholder="updated_at"
                    className="font-mono"
                    disabled={readOnly}
                  />
                </Field>
              </div>
            </div>
          ) : null}

          <div className="card card-pad space-y-3">
            <Field
              label="Replaces failed migration"
              htmlFor="replaces"
              hint="The replacement inherits the target's effective_seq. Only failed/pending chains can be superseded."
            >
              <Select
                id="replaces"
                value={form.replacesId}
                onChange={(event) => update("replacesId", event.target.value)}
                disabled={readOnly}
              >
                <option value="">None</option>
                {replaceOptions.map((candidate) => (
                  <option key={candidate.id} value={candidate.id}>
                    eff #{candidate.effective_seq} · raw #{candidate.seq_version} ·{" "}
                    {candidate.title}
                  </option>
                ))}
              </Select>
            </Field>
            {form.replacesId ? (
              <p className="m-0 text-xs font-semibold text-leaf-800">
                The replacement will hold the same effective slot and supersede the previous chain.
              </p>
            ) : null}
          </div>
        </div>

        <aside className="aside-stack lg:sticky lg:top-20 lg:self-start">
          <div className="card p-4 sm:p-5">
            <h2 className="section-title">Review readiness</h2>
            {dryRun ? (
              <div className="mt-3">
                {dryRun.dry_run_status === "passed" ? (
                  <Callout tone="success" title="Dry-run passed">
                    BEGIN … ROLLBACK completed. No target change was committed.
                  </Callout>
                ) : dryRun.dry_run_status === "inconclusive" ? (
                  <Callout tone="warning" title="Inconclusive — review may continue">
                    {dryRun.message ?? "Statement timeout or lock contention."}
                    {dryRun.sqlstate ? (
                      <span className="mt-1 block font-mono text-xs">
                        SQLSTATE {dryRun.sqlstate}
                      </span>
                    ) : null}
                  </Callout>
                ) : (
                  <Callout tone="danger" title="Dry-run failed">
                    {dryRun.message ?? "Fix the SQL before review."}
                  </Callout>
                )}
              </div>
            ) : (
              <p className="mt-2 mb-0 text-sm leading-relaxed text-muted">
                Save the definition, then run validation. Validation uses a short-lived dedicated
                connection with lock and statement timeouts.
              </p>
            )}
            {validateError && !isApiError(validateError) ? (
              <div className="mt-3">
                <ErrorBanner error={validateError} />
              </div>
            ) : null}
            <div className="mt-4 flex flex-col gap-2">
              <Button
                variant="secondary"
                type="button"
                onClick={() => void validate()}
                loading={validating}
                disabled={isNew || !migration || readOnly}
                title={isNew ? "Save the definition first" : "Run BEGIN … ROLLBACK dry-run"}
              >
                <Wand2 className="h-4 w-4" /> Validate / dry-run
              </Button>
              {isNew ? (
                <p className="m-0 text-xs text-muted">Save first to enable validation.</p>
              ) : null}
            </div>
          </div>

          <div className="card p-4 sm:p-5">
            <h2 className="section-title">Definition facts</h2>
            <dl className="m-0 mt-3 grid gap-2 text-sm">
              <div className="flex justify-between gap-2">
                <dt className="font-semibold text-ink-soft">Definition ID</dt>
                <dd className="m-0 font-mono text-xs">{migration?.id ?? "not saved"}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="font-semibold text-ink-soft">Revision</dt>
                <dd className="m-0 font-mono">{migration?.revision ?? 1}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="font-semibold text-ink-soft">Effective slot</dt>
                <dd className="m-0 font-mono">#{migration?.effective_seq ?? "—"}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="font-semibold text-ink-soft">Raw sequence</dt>
                <dd className="m-0 font-mono">#{migration?.seq_version ?? "—"}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="font-semibold text-ink-soft">Immutable</dt>
                <dd className="m-0">
                  {migration?.is_immutable ? (
                    <span className="badge badge-warning">yes</span>
                  ) : (
                    <span className="badge badge-muted">no</span>
                  )}
                </dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="font-semibold text-ink-soft">Reversible</dt>
                <dd className="m-0">
                  {form.irreversible ? (
                    <span className="badge badge-danger">no</span>
                  ) : (
                    <span className="badge badge-success">yes</span>
                  )}
                </dd>
              </div>
            </dl>
          </div>
        </aside>
      </form>

      <ConfirmDialog
        open={submitOpen}
        onClose={() => setSubmitOpen(false)}
        onConfirm={() => void submit()}
        title="Submit this migration for approval?"
        description={
          <div className="space-y-2">
            <p className="m-0">
              Every group leader with access to this database must approve. The definition becomes
              immutable once an execution starts.
            </p>
            {actionError ? <ErrorBanner error={actionError} /> : null}
          </div>
        }
        confirmLabel="Submit proposal"
        tone="primary"
        loading={submitting}
      />
    </div>
  );
}
